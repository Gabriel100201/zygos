package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// linearStub answers GraphQL POSTs with a canned response per query, chosen by
// looking for a marker substring in the query text.
type linearStub struct {
	*httptest.Server

	mu       sync.Mutex
	requests []map[string]any
}

func newLinearStub(t *testing.T, reply func(query string, vars map[string]any) string) *linearStub {
	t.Helper()
	stub := &linearStub{}
	stub.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("stub received unparsable request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		stub.mu.Lock()
		stub.requests = append(stub.requests, map[string]any{"query": body.Query, "variables": body.Variables})
		stub.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, reply(body.Query, body.Variables))
	}))
	t.Cleanup(stub.Close)
	return stub
}

func (s *linearStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func newTestLinear(t *testing.T, stub *linearStub) *linearProvider {
	t.Helper()
	p := NewLinear("work", "lin_api_example").(*linearProvider)
	p.client = testClient(1)
	p.endpoint = stub.URL
	return p
}

// cursor returns the value of the $after variable, or "" on the first page.
func cursor(vars map[string]any) string {
	if v, ok := vars["after"].(string); ok {
		return v
	}
	return ""
}

// Both connections used to be a bare first: 100 with no cursor, so a workspace
// with more than 100 teams or projects was silently truncated.
func TestLinearListProjectsPaginatesTeamsAndProjects(t *testing.T) {
	stub := newLinearStub(t, func(query string, vars map[string]any) string {
		switch {
		case strings.Contains(query, "teams(first: 100"):
			switch cursor(vars) {
			case "":
				return `{"data":{"teams":{"pageInfo":{"hasNextPage":true,"endCursor":"T1"},
					"nodes":[{"id":"t1","name":"Alpha","key":"ALP"}]}}}`
			case "T1":
				return `{"data":{"teams":{"pageInfo":{"hasNextPage":false,"endCursor":""},
					"nodes":[{"id":"t2","name":"Beta","key":"BET"}]}}}`
			}
		case strings.Contains(query, "projects(first: 100"):
			switch cursor(vars) {
			case "":
				return `{"data":{"projects":{"pageInfo":{"hasNextPage":true,"endCursor":"P1"},
					"nodes":[{"id":"p1","name":"Website","teams":{"nodes":[{"id":"t1","name":"Alpha","key":"ALP"}]}}]}}}`
			case "P1":
				return `{"data":{"projects":{"pageInfo":{"hasNextPage":false,"endCursor":""},
					"nodes":[{"id":"p2","name":"Mobile","teams":{"nodes":[{"id":"t2","name":"Beta","key":"BET"}]}}]}}}`
			}
		}
		t.Errorf("unexpected query: %s", query)
		return `{"data":{}}`
	})

	projects, err := newTestLinear(t, stub).ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}

	var teams, projs []string
	for _, p := range projects {
		switch p.Kind {
		case "team":
			teams = append(teams, p.Name)
		case "project":
			projs = append(projs, p.Name)
		}
	}
	if want := []string{"Alpha", "Beta"}; !equalStrings(teams, want) {
		t.Errorf("teams = %v, want %v — the second page was dropped", teams, want)
	}
	if want := []string{"Website", "Mobile"}; !equalStrings(projs, want) {
		t.Errorf("projects = %v, want %v — the second page was dropped", projs, want)
	}
	if got := stub.callCount(); got != 4 {
		t.Errorf("made %d GraphQL calls, want 4 (2 team pages + 2 project pages)", got)
	}
}

func TestLinearListProjectsAttachesParentTeam(t *testing.T) {
	stub := newLinearStub(t, func(query string, vars map[string]any) string {
		if strings.Contains(query, "teams(first: 100") {
			return `{"data":{"teams":{"pageInfo":{"hasNextPage":false},"nodes":[{"id":"t1","name":"Alpha","key":"ALP"}]}}}`
		}
		return `{"data":{"projects":{"pageInfo":{"hasNextPage":false},
			"nodes":[{"id":"p1","name":"Website","teams":{"nodes":[{"id":"t1","name":"Alpha","key":"ALP"}]}}]}}}`
	})

	projects, err := newTestLinear(t, stub).ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	for _, p := range projects {
		if p.Kind == "project" && p.ParentTeam != "Alpha" {
			t.Errorf("project %q has ParentTeam %q, want %q", p.Name, p.ParentTeam, "Alpha")
		}
	}
}

// workflowStates had no `first` argument at all, and Linear defaults
// connections to 50 nodes — states past the first page were invisible, which
// made tasks_update reject perfectly valid state names.
func TestLinearListStatesPaginates(t *testing.T) {
	stub := newLinearStub(t, func(query string, vars map[string]any) string {
		if !strings.Contains(query, "workflowStates(first:") {
			t.Errorf("workflowStates query must request an explicit page size: %s", query)
		}
		switch cursor(vars) {
		case "":
			return `{"data":{"workflowStates":{"pageInfo":{"hasNextPage":true,"endCursor":"S1"},
				"nodes":[{"id":"s1","name":"Todo","type":"unstarted","team":{"name":"Alpha","key":"ALP"}}]}}}`
		default:
			return `{"data":{"workflowStates":{"pageInfo":{"hasNextPage":false},
				"nodes":[{"id":"s2","name":"In Review","type":"started","team":{"name":"Alpha","key":"ALP"}}]}}}`
		}
	})

	states, err := newTestLinear(t, stub).ListStates(context.Background(), "")
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	var names []string
	for _, s := range states {
		names = append(names, s.Name)
	}
	if want := []string{"Todo", "In Review"}; !equalStrings(names, want) {
		t.Errorf("states = %v, want %v", names, want)
	}
}

func TestLinearListStatesFiltersByTeam(t *testing.T) {
	stub := newLinearStub(t, func(query string, vars map[string]any) string {
		return `{"data":{"workflowStates":{"pageInfo":{"hasNextPage":false},"nodes":[
			{"id":"s1","name":"Todo","type":"unstarted","team":{"name":"Alpha","key":"ALP"}},
			{"id":"s2","name":"Doing","type":"started","team":{"name":"Beta","key":"BET"}}]}}}`
	})

	p := newTestLinear(t, stub)
	for _, key := range []string{"ALP", "alp", "Alpha"} {
		states, err := p.ListStates(context.Background(), key)
		if err != nil {
			t.Fatalf("ListStates(%q): %v", key, err)
		}
		if len(states) != 1 || states[0].Name != "Todo" {
			t.Errorf("ListStates(%q) = %+v, want only Alpha's states", key, states)
		}
	}
}

func TestLinearSurfacesGraphQLErrors(t *testing.T) {
	stub := newLinearStub(t, func(query string, vars map[string]any) string {
		return `{"errors":[{"message":"Query complexity exceeded"}]}`
	})

	_, err := newTestLinear(t, stub).ListProjects(context.Background())
	if err == nil {
		t.Fatal("a GraphQL error payload must surface as an error")
	}
	if !strings.Contains(err.Error(), "complexity") {
		t.Errorf("error should carry the API message, got: %v", err)
	}
}

func TestLinearPaginationRespectsPageCap(t *testing.T) {
	var calls int
	stub := newLinearStub(t, func(query string, vars map[string]any) string {
		calls++
		if calls > 100 {
			t.Fatal("pagination never terminated — the page cap is not enforced")
		}
		if strings.Contains(query, "teams(first: 100") {
			// Always claims another page: without a cap this loops forever.
			return fmt.Sprintf(`{"data":{"teams":{"pageInfo":{"hasNextPage":true,"endCursor":"c%d"},
				"nodes":[{"id":"t%d","name":"Team %d","key":"T%d"}]}}}`, calls, calls, calls, calls)
		}
		return `{"data":{"projects":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}`
	})

	if _, err := newTestLinear(t, stub).ListProjects(context.Background()); err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
}

func TestBuildStateFilter(t *testing.T) {
	cases := map[string][]string{
		"active":  {"canceled", "completed", "backlog", "triage"},
		"backlog": {"canceled", "completed", "started"},
		"":        {"canceled", "completed"},
		"all":     {"canceled", "completed"},
	}
	for state, want := range cases {
		filter := buildStateFilter(state)
		typeFilter, ok := filter["type"].(map[string]any)
		if !ok {
			t.Fatalf("buildStateFilter(%q) has no type filter: %+v", state, filter)
		}
		got, ok := typeFilter["nin"].([]string)
		if !ok {
			t.Fatalf("buildStateFilter(%q) nin is not []string: %+v", state, typeFilter)
		}
		if !equalStrings(got, want) {
			t.Errorf("buildStateFilter(%q) excludes %v, want %v", state, got, want)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
