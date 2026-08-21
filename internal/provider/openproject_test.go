package provider

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func newOpenProjectStub(t *testing.T, routes map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/users/me", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":7,"name":"Gabriel"}`)
	})
	for path, h := range routes {
		mux.HandleFunc(path, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestOpenProject(t *testing.T, srv *httptest.Server) *openProjectProvider {
	t.Helper()
	p := NewOpenProject("work", srv.URL, "op_api_example").(*openProjectProvider)
	p.client = testClient(1)
	return p
}

// wpPage renders one page of an OpenProject work-package collection.
func wpPage(total int, ids ...int) string {
	items := make([]string, 0, len(ids))
	for _, id := range ids {
		items = append(items, fmt.Sprintf(
			`{"id":%d,"subject":"wp %d","_links":{"status":{"title":"New","href":"/api/v3/statuses/1"},"project":{"title":"Web"}}}`,
			id, id))
	}
	return fmt.Sprintf(`{"total":%d,"count":%d,"_embedded":{"elements":[%s]}}`,
		total, len(ids), strings.Join(items, ","))
}

func TestOpenProjectListTasksPaginates(t *testing.T) {
	var offsets []string
	srv := newOpenProjectStub(t, map[string]http.HandlerFunc{
		"/api/v3/work_packages": func(w http.ResponseWriter, r *http.Request) {
			offset := r.URL.Query().Get("offset")
			offsets = append(offsets, offset)
			switch offset {
			case "1":
				io.WriteString(w, wpPage(3, 101, 102))
			default:
				io.WriteString(w, wpPage(3, 103))
			}
		},
	})

	tasks, err := newTestOpenProject(t, srv).ListTasks(context.Background(), ListOpts{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("got %d tasks, want 3 — pagination stopped early", len(tasks))
	}
	if len(offsets) != 2 || offsets[0] != "1" || offsets[1] != "2" {
		t.Errorf("offsets requested = %v, want [1 2] (OpenProject pages are 1-based)", offsets)
	}
	for _, task := range tasks {
		if !strings.HasPrefix(task.Identifier, "work:wp:") {
			t.Errorf("identifier %q does not use the provider:wp:id form", task.Identifier)
		}
	}
}

// An empty filter slice marshals to `null`, which OpenProject answers with a
// 500. The parameter has to be omitted entirely when there is nothing to filter.
func TestOpenProjectOmitsEmptyFilters(t *testing.T) {
	var sawFilters atomic.Bool
	srv := newOpenProjectStub(t, map[string]http.HandlerFunc{
		"/api/v3/work_packages": func(w http.ResponseWriter, r *http.Request) {
			if _, ok := r.URL.Query()["filters"]; ok {
				sawFilters.Store(true)
			}
			io.WriteString(w, wpPage(0))
		},
	})

	if _, err := newTestOpenProject(t, srv).ListTasks(context.Background(), ListOpts{State: "all"}); err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if sawFilters.Load() {
		t.Error("an empty filter set must not be sent — OpenProject returns 500 on filters=null")
	}
}

func TestOpenProjectListProjectsPaginates(t *testing.T) {
	var pages atomic.Int32
	srv := newOpenProjectStub(t, map[string]http.HandlerFunc{
		"/api/v3/projects": func(w http.ResponseWriter, r *http.Request) {
			n := pages.Add(1)
			if n == 1 {
				io.WriteString(w, `{"total":2,"count":1,"_embedded":{"elements":[{"id":1,"name":"Web","identifier":"web"}]}}`)
				return
			}
			io.WriteString(w, `{"total":2,"count":1,"_embedded":{"elements":[{"id":2,"name":"Mobile","identifier":"mobile"}]}}`)
		},
	})

	projects, err := newTestOpenProject(t, srv).ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("got %d projects, want 2 — the second page was dropped", len(projects))
	}
}

func TestOpenProjectSendsAPIKeyBasicAuth(t *testing.T) {
	got := make(chan string, 1)
	srv := newOpenProjectStub(t, map[string]http.HandlerFunc{
		"/api/v3/work_packages": func(w http.ResponseWriter, r *http.Request) {
			select {
			case got <- r.Header.Get("Authorization"):
			default:
			}
			io.WriteString(w, wpPage(0))
		},
	})

	if _, err := newTestOpenProject(t, srv).ListTasks(context.Background(), ListOpts{}); err != nil {
		t.Fatalf("ListTasks: %v", err)
	}

	header := <-got
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("apikey:op_api_example"))
	if header != want {
		t.Errorf("Authorization = %q, want the fixed apikey user", header)
	}
}

func TestOpenProjectSurfacesAPIErrors(t *testing.T) {
	srv := newOpenProjectStub(t, map[string]http.HandlerFunc{
		"/api/v3/work_packages": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"message":"You are not authorized"}`)
		},
	})

	_, err := newTestOpenProject(t, srv).ListTasks(context.Background(), ListOpts{})
	if err == nil {
		t.Fatal("a 403 must surface as an error")
	}
	if !strings.Contains(err.Error(), "not authorized") {
		t.Errorf("error should carry the API message, got: %v", err)
	}
}

func TestOpenProjectDoesNotSupportDocuments(t *testing.T) {
	p := NewOpenProject("work", "https://op.example", "k")
	if _, err := p.ListDocuments(context.Background(), DocumentListOpts{}); err != ErrDocsNotSupported {
		t.Errorf("ListDocuments err = %v, want ErrDocsNotSupported", err)
	}
	if _, err := p.GetDocument(context.Background(), "x"); err != ErrDocsNotSupported {
		t.Errorf("GetDocument err = %v, want ErrDocsNotSupported", err)
	}
}

func TestParseOpenProjectID(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"work:wp:1234", 1234, false},
		{"other:wp:1", 1, false},
		{"work:us:1234", 0, true},
		{"ABC-42", 0, true},
		{"work:wp:abc", 0, true},
		{"work:wp:", 0, true},
		{"", 0, true},
	}
	for _, tc := range cases {
		got, err := parseOpenProjectID(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseOpenProjectID(%q) should fail", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseOpenProjectID(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseOpenProjectID(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestOpenProjectIdentifierRoundTrips(t *testing.T) {
	id := 4321
	identifier := "work:wp:" + strconv.Itoa(id)
	got, err := parseOpenProjectID(identifier)
	if err != nil {
		t.Fatalf("parseOpenProjectID: %v", err)
	}
	if got != id {
		t.Errorf("round trip lost the id: %d != %d", got, id)
	}
}
