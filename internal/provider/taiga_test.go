package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// taigaStub is a minimal fake Taiga instance: it authenticates, then serves
// whatever the test registers per path.
type taigaStub struct {
	*httptest.Server
	auths atomic.Int32
}

func newTaigaStub(t *testing.T, routes map[string]http.HandlerFunc) *taigaStub {
	t.Helper()
	stub := &taigaStub{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth", func(w http.ResponseWriter, r *http.Request) {
		stub.auths.Add(1)
		fmt.Fprintf(w, `{"auth_token":"token-%d","id":7}`, stub.auths.Load())
	})
	for path, h := range routes {
		mux.HandleFunc(path, h)
	}
	stub.Server = httptest.NewServer(mux)
	t.Cleanup(stub.Close)
	return stub
}

func newTestTaiga(t *testing.T, stub *taigaStub) *taigaProvider {
	t.Helper()
	p := NewTaiga("work", stub.URL, "your-username", "your-password").(*taigaProvider)
	p.client = testClient(2)
	return p
}

// storyPage renders one page of user stories with the given refs.
func storyPage(refs ...int) string {
	items := make([]string, 0, len(refs))
	for _, ref := range refs {
		items = append(items, fmt.Sprintf(
			`{"id":%d,"ref":%d,"subject":"story %d","project_extra_info":{"name":"Web","slug":"web","id":1},"status_extra_info":{"name":"New","is_closed":false}}`,
			ref*10, ref, ref))
	}
	return "[" + strings.Join(items, ",") + "]"
}

// Taiga paginates list endpoints at 30 items per page. Reading only the first
// response makes the agent report a partial list as if it were the whole
// backlog — a wrong answer delivered confidently, which is worse than an error.
func TestTaigaListTasksFollowsPagination(t *testing.T) {
	var pagesServed atomic.Int32
	var stub *taigaStub

	paginated := func(total int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			pagesServed.Add(1)
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			if page == 0 {
				page = 1
			}
			if page < total {
				next := *r.URL
				q := next.Query()
				q.Set("page", strconv.Itoa(page+1))
				next.RawQuery = q.Encode()
				w.Header().Set("x-pagination-next", stub.URL+next.RequestURI())
			}
			io.WriteString(w, storyPage(page*100+1, page*100+2))
		}
	}

	stub = newTaigaStub(t, map[string]http.HandlerFunc{
		"/api/v1/userstories": paginated(3),
		"/api/v1/tasks":       paginated(1),
	})

	tasks, err := newTestTaiga(t, stub).ListTasks(context.Background(), ListOpts{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}

	// 3 pages of stories × 2 + 1 page of tasks × 2.
	if len(tasks) != 8 {
		t.Fatalf("got %d tasks, want 8 — pagination was not followed", len(tasks))
	}
	if got := pagesServed.Load(); got != 4 {
		t.Errorf("server served %d pages, want 4", got)
	}

	seen := map[string]bool{}
	for _, task := range tasks {
		if seen[task.Identifier] {
			t.Errorf("duplicate identifier %q — pages overlapped", task.Identifier)
		}
		seen[task.Identifier] = true
	}
}

func TestTaigaGetAllRequestsALargerPageSize(t *testing.T) {
	got := make(chan string, 1)
	stub := newTaigaStub(t, map[string]http.HandlerFunc{
		"/api/v1/projects": func(w http.ResponseWriter, r *http.Request) {
			select {
			case got <- r.URL.Query().Get("page_size"):
			default:
			}
			io.WriteString(w, "[]")
		},
	})

	if _, err := newTestTaiga(t, stub).ListProjects(context.Background()); err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if size := <-got; size != strconv.Itoa(taigaPageSize) {
		t.Errorf("page_size = %q, want %d", size, taigaPageSize)
	}
}

// The 401 replay used to reuse an io.Reader the first attempt had already
// drained, so an expired session silently turned every write into an empty body.
func TestTaigaReplaysBodyAfterTokenExpiry(t *testing.T) {
	var attempts atomic.Int32
	bodies := make(chan string, 4)

	stub := newTaigaStub(t, map[string]http.HandlerFunc{
		"/api/v1/projects": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `[{"id":1,"name":"Web","slug":"web"}]`)
		},
		"/api/v1/userstories": func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			if r.Method == http.MethodGet {
				io.WriteString(w, "[]")
				return
			}
			bodies <- string(b)
			if attempts.Add(1) == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				io.WriteString(w, `{"detail":"Invalid token"}`)
				return
			}
			io.WriteString(w, `{"id":10,"ref":1,"subject":"story","project_extra_info":{"name":"Web","slug":"web","id":1},"status_extra_info":{"name":"New","is_closed":false}}`)
		},
	})

	p := newTestTaiga(t, stub)
	task, err := p.CreateTask(context.Background(), CreateInput{
		Provider: "work", Project: "web", Title: "story", Type: "userstory",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.Title != "story" {
		t.Errorf("Title = %q, want %q", task.Title, "story")
	}
	close(bodies)

	n := 0
	for body := range bodies {
		n++
		var payload map[string]any
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("attempt %d sent unparsable body %q", n, body)
		}
		if payload["subject"] != "story" {
			t.Errorf("attempt %d lost the payload: %q", n, body)
		}
	}
	if n != 2 {
		t.Fatalf("server saw %d attempts, want 2 (401 then replay)", n)
	}
	if stub.auths.Load() < 2 {
		t.Errorf("re-authentication did not happen: %d auth calls", stub.auths.Load())
	}
}

func TestTaigaSurfacesAPIErrors(t *testing.T) {
	stub := newTaigaStub(t, map[string]http.HandlerFunc{
		"/api/v1/userstories": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"_error_message":"nope"}`)
		},
	})

	_, err := newTestTaiga(t, stub).ListTasks(context.Background(), ListOpts{})
	if err == nil {
		t.Fatal("a 400 from Taiga must surface as an error")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error should carry the API message, got: %v", err)
	}
}

func TestWithPageSize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://t.example/api/v1/tasks", "https://t.example/api/v1/tasks?page_size=100"},
		{"https://t.example/api/v1/tasks?project=3", "https://t.example/api/v1/tasks?project=3&page_size=100"},
		{"https://t.example/api/v1/tasks?page_size=5", "https://t.example/api/v1/tasks?page_size=5"},
	}
	for _, tc := range cases {
		if got := withPageSize(tc.in, 100); got != tc.want {
			t.Errorf("withPageSize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseTaigaID(t *testing.T) {
	cases := []struct {
		in       string
		wantKind string
		wantID   int
		wantErr  bool
	}{
		{"work:us:234", "us", 234, false},
		{"work:task:56", "task", 56, false},
		{"ABC-42", "", 0, true},
		{"work:us:notanumber", "", 0, true},
		{"work:us", "", 0, true},
		{"", "", 0, true},
	}
	for _, tc := range cases {
		kind, id, err := parseTaigaID(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseTaigaID(%q) should fail", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseTaigaID(%q): %v", tc.in, err)
			continue
		}
		if kind != tc.wantKind || id != tc.wantID {
			t.Errorf("parseTaigaID(%q) = (%q, %d), want (%q, %d)", tc.in, kind, id, tc.wantKind, tc.wantID)
		}
	}
}
