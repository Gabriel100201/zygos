package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeProvider implements Provider so Registry behaviour can be tested without
// touching a real tracker. Every method fails loudly unless the test wires up
// the matching hook, so a test can never accidentally pass on a stub default.
type fakeProvider struct {
	name     string
	kind     string
	calls    atomic.Int32
	delay    time.Duration
	listFn   func(ctx context.Context, opts ListOpts) ([]Task, error)
	getFn    func(ctx context.Context, id string) (*TaskDetail, error)
	projFn   func(ctx context.Context) ([]Project, error)
	searchFn func(ctx context.Context, q string) ([]Task, error)
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Type() string {
	if f.kind == "" {
		return "linear"
	}
	return f.kind
}
func (f *fakeProvider) Ping(ctx context.Context) error { return nil }

func (f *fakeProvider) ListTasks(ctx context.Context, opts ListOpts) ([]Task, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.listFn != nil {
		return f.listFn(ctx, opts)
	}
	return []Task{{Source: f.name, Identifier: f.name + "-1", Title: "task from " + f.name}}, nil
}

func (f *fakeProvider) GetTask(ctx context.Context, id string) (*TaskDetail, error) {
	f.calls.Add(1)
	if f.getFn != nil {
		return f.getFn(ctx, id)
	}
	return &TaskDetail{Task: Task{Source: f.name, Identifier: id}}, nil
}

func (f *fakeProvider) SearchTasks(ctx context.Context, q string) ([]Task, error) {
	f.calls.Add(1)
	if f.searchFn != nil {
		return f.searchFn(ctx, q)
	}
	return []Task{{Source: f.name, Identifier: f.name + "-1"}}, nil
}

func (f *fakeProvider) ListProjects(ctx context.Context) ([]Project, error) {
	f.calls.Add(1)
	if f.projFn != nil {
		return f.projFn(ctx)
	}
	return []Project{{Source: f.name, Name: f.name + " project", Kind: "project"}}, nil
}

func (f *fakeProvider) CreateTask(ctx context.Context, in CreateInput) (*Task, error) {
	f.calls.Add(1)
	return &Task{Source: f.name, Title: in.Title}, nil
}

func (f *fakeProvider) UpdateTask(ctx context.Context, id string, in UpdateInput) (*Task, error) {
	f.calls.Add(1)
	return &Task{Source: f.name, Identifier: id}, nil
}

func (f *fakeProvider) ListStates(ctx context.Context, projectKey string) ([]State, error) {
	return nil, ErrNotSupported
}
func (f *fakeProvider) AddComment(ctx context.Context, id, body string) (*Comment, error) {
	return nil, ErrNotSupported
}
func (f *fakeProvider) ListMembers(ctx context.Context, projectKey string) ([]Member, error) {
	return nil, ErrNotSupported
}
func (f *fakeProvider) ListDocuments(ctx context.Context, o DocumentListOpts) ([]Document, error) {
	return nil, ErrDocsNotSupported
}
func (f *fakeProvider) GetDocument(ctx context.Context, id string) (*DocumentDetail, error) {
	return nil, ErrDocsNotSupported
}
func (f *fakeProvider) CreateDocument(ctx context.Context, in DocumentCreateInput) (*Document, error) {
	return nil, ErrDocsNotSupported
}
func (f *fakeProvider) UpdateDocument(ctx context.Context, id string, in DocumentUpdateInput) (*Document, error) {
	return nil, ErrDocsNotSupported
}
func (f *fakeProvider) DeleteDocument(ctx context.Context, id string) error {
	return ErrDocsNotSupported
}
func (f *fakeProvider) SearchDocuments(ctx context.Context, q string) ([]Document, error) {
	return nil, ErrDocsNotSupported
}

func sources(tasks []Task) []string {
	var out []string
	for _, t := range tasks {
		out = append(out, t.Source)
	}
	sort.Strings(out)
	return out
}

// The headline promise of the project: one unreachable tracker degrades to a
// warning, it never takes the others down with it.
func TestAllTasksDegradesGracefully(t *testing.T) {
	broken := &fakeProvider{name: "vpn-down", listFn: func(context.Context, ListOpts) ([]Task, error) {
		return nil, errors.New("dial tcp: no route to host")
	}}
	healthy := &fakeProvider{name: "work"}

	reg := NewRegistry([]Provider{broken, healthy})
	tasks, warnings, err := reg.AllTasks(context.Background(), ListOpts{})
	if err != nil {
		t.Fatalf("one broken provider must not fail the call: %v", err)
	}
	if got := sources(tasks); !equalStrings(got, []string{"work"}) {
		t.Errorf("tasks from %v, want only the healthy provider", got)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "vpn-down") {
		t.Errorf("warnings = %v, want one naming the broken provider", warnings)
	}
}

func TestAllTasksFailsOnlyWhenEveryProviderFails(t *testing.T) {
	fail := func(name string) *fakeProvider {
		return &fakeProvider{name: name, listFn: func(context.Context, ListOpts) ([]Task, error) {
			return nil, errors.New("boom")
		}}
	}
	reg := NewRegistry([]Provider{fail("a"), fail("b")})

	_, warnings, err := reg.AllTasks(context.Background(), ListOpts{})
	if err == nil {
		t.Fatal("every provider failing must surface as an error")
	}
	if len(warnings) != 2 {
		t.Errorf("got %d warnings, want one per provider", len(warnings))
	}
}

// Filtering by a name nobody has used to report "all providers failed", which
// sent the agent hunting for an outage instead of fixing its argument.
func TestAllTasksRejectsUnknownProviderName(t *testing.T) {
	reg := NewRegistry([]Provider{&fakeProvider{name: "work"}})

	_, _, err := reg.AllTasks(context.Background(), ListOpts{Provider: "typo"})
	if err == nil {
		t.Fatal("an unknown provider filter must be an error")
	}
	if !strings.Contains(err.Error(), "unknown provider") || !strings.Contains(err.Error(), "typo") {
		t.Errorf("error = %q, want it to name the unknown provider", err)
	}
}

func TestAllTasksProviderFilterIsCaseInsensitive(t *testing.T) {
	work := &fakeProvider{name: "Work"}
	other := &fakeProvider{name: "personal"}
	reg := NewRegistry([]Provider{work, other})

	tasks, _, err := reg.AllTasks(context.Background(), ListOpts{Provider: "wOrK"})
	if err != nil {
		t.Fatalf("AllTasks: %v", err)
	}
	if got := sources(tasks); !equalStrings(got, []string{"Work"}) {
		t.Errorf("tasks from %v, want only Work", got)
	}
	if other.calls.Load() != 0 {
		t.Error("the filtered-out provider was queried anyway")
	}
}

func TestAllTasksQueriesProvidersConcurrently(t *testing.T) {
	const n = 4
	providers := make([]Provider, n)
	for i := range providers {
		providers[i] = &fakeProvider{name: fmt.Sprintf("p%d", i), delay: 150 * time.Millisecond}
	}

	start := time.Now()
	tasks, _, err := NewRegistry(providers).AllTasks(context.Background(), ListOpts{})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("AllTasks: %v", err)
	}
	if len(tasks) != n {
		t.Fatalf("got %d tasks, want %d", len(tasks), n)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("took %v for %d providers of 150ms each — they ran serially", elapsed, n)
	}
}

func TestAllProjectsCollectsWarningsWithoutFailing(t *testing.T) {
	broken := &fakeProvider{name: "broken", projFn: func(context.Context) ([]Project, error) {
		return nil, errors.New("unreachable")
	}}
	reg := NewRegistry([]Provider{broken, &fakeProvider{name: "ok"}})

	projects, warnings, err := reg.AllProjects(context.Background(), "")
	if err != nil {
		t.Fatalf("AllProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Errorf("got %d projects, want 1 from the healthy provider", len(projects))
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %v, want one", warnings)
	}
}

func TestSearchTasksMergesAcrossProviders(t *testing.T) {
	reg := NewRegistry([]Provider{&fakeProvider{name: "a"}, &fakeProvider{name: "b"}})

	tasks, warnings, err := reg.SearchTasks(context.Background(), "login", "")
	if err != nil {
		t.Fatalf("SearchTasks: %v", err)
	}
	if got := sources(tasks); !equalStrings(got, []string{"a", "b"}) {
		t.Errorf("results from %v, want both providers", got)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestRouteIdentifierUsesTaigaPrefix(t *testing.T) {
	taiga := &fakeProvider{name: "selfhosted", kind: "taiga"}
	linear := &fakeProvider{name: "work", kind: "linear"}
	reg := NewRegistry([]Provider{linear, taiga})

	p, id := reg.routeIdentifier("selfhosted:us:234")
	if p == nil || p.Name() != "selfhosted" {
		t.Fatalf("routed to %v, want the Taiga provider named in the identifier", p)
	}
	if id != "selfhosted:us:234" {
		t.Errorf("identifier rewritten to %q", id)
	}
}

func TestRouteIdentifierFallsBackToLinear(t *testing.T) {
	taiga := &fakeProvider{name: "selfhosted", kind: "taiga", projFn: func(context.Context) ([]Project, error) {
		return nil, errors.New("should not be consulted")
	}}
	linear := &fakeProvider{name: "work", kind: "linear", projFn: func(context.Context) ([]Project, error) {
		return []Project{{Key: "ABC", Kind: "team"}}, nil
	}}
	reg := NewRegistry([]Provider{taiga, linear})

	p, _ := reg.routeIdentifier("ABC-42")
	if p == nil || p.Type() != "linear" {
		t.Fatalf("routed %q to %v, want a Linear provider", "ABC-42", p)
	}
}

func TestRouteIdentifierReturnsNilWhenNothingMatches(t *testing.T) {
	reg := NewRegistry([]Provider{&fakeProvider{name: "selfhosted", kind: "taiga"}})
	if p, _ := reg.routeIdentifier("ABC-42"); p != nil {
		t.Errorf("routed a Linear key to %q with no Linear provider configured", p.Name())
	}
}

func TestGetTaskRejectsUnroutableIdentifier(t *testing.T) {
	reg := NewRegistry([]Provider{&fakeProvider{name: "selfhosted", kind: "taiga"}})
	if _, err := reg.GetTask(context.Background(), "ABC-42"); err == nil {
		t.Fatal("an unroutable identifier must be an error")
	}
}

func TestCreateTaskRejectsUnknownProvider(t *testing.T) {
	reg := NewRegistry([]Provider{&fakeProvider{name: "work"}})
	_, err := reg.CreateTask(context.Background(), CreateInput{Provider: "nope", Title: "x"})
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("err = %v, want an unknown-provider error", err)
	}
}
