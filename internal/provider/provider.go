package provider

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Task is the unified task representation across all providers.
type Task struct {
	Source     string // provider name configured by the user (e.g. "work", "personal")
	SourceType string // "linear" or "taiga"
	Identifier string // Linear: issue key (e.g. "ABC-42"); Taiga: "<provider>:us:<id>" or "<provider>:task:<id>"
	Title      string
	Status     string
	StatusType string // backlog, unstarted, started, completed, canceled
	Priority   string // Urgent, High, Medium, Low, None
	Project    string
	Assignee   string // display name of the assignee, empty if unassigned
	DueDate    string // ISO 8601 or empty
	URL        string
	CreatedAt  string
	UpdatedAt  string
}

// TaskDetail extends Task with full information.
type TaskDetail struct {
	Task
	Description string
	Comments    []Comment
	Labels      []string
	Assignee    string
	Estimate    *float64
	Milestone   string // cycle for Linear, sprint for Taiga
	Subtasks    []Task // child tasks (Taiga: tasks belonging to a user story)
}

type Comment struct {
	Author    string
	Body      string
	CreatedAt string
}

type Project struct {
	Source     string
	SourceType string
	ID         string
	Name       string
	Key        string // team key for Linear, slug for Taiga
	Kind       string // "team" | "project" (Linear), "project" (Taiga)
	ParentTeam string // parent team name for Linear projects; empty otherwise

	// Rich fields (populated for Linear projects; empty for teams and Taiga).
	Description string  // short summary
	Status      string  // Linear project state: "planned" | "started" | "paused" | "completed" | "canceled" | "backlog"
	Health      string  // Linear project health: "onTrack" | "atRisk" | "offTrack" | ""
	Priority    string  // "Urgent" | "High" | "Medium" | "Low" | "None"
	Lead        string  // lead user's display name
	StartDate   string  // ISO 8601 or empty
	TargetDate  string  // ISO 8601 or empty
	Progress    float64 // 0.0 — 1.0
	URL         string  // Linear project URL
}

type State struct {
	ID      string
	Name    string
	Type    string // backlog, unstarted, started, completed, canceled
	Project string
}

// Member is an assignable user in a provider.
type Member struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

type ListOpts struct {
	Provider string
	Project  string
	State    string // "active", "backlog", "all"
	Assigned bool   // true = only tasks assigned to me; false (default) = all open tasks in workspace
}

type CreateInput struct {
	Provider    string
	Project     string
	Title       string
	Description string
	Priority    string
	StateID     string
	Type        string // taiga: "userstory" or "task"

	// Rich fields (Linear only; Taiga ignores these). Empty = not set.
	// Strings accept a human name OR a UUID.
	Assignee string   // user name/email/UUID, or "me"
	Labels   []string // label names
	DueDate  string   // ISO 8601 date (YYYY-MM-DD)
	Estimate *float64 // point estimate
	Cycle    string   // cycle name/number/UUID, or "active"
	Parent   string   // parent issue identifier (e.g. ABC-42)
}

type UpdateInput struct {
	Title       *string
	Description *string
	StateID     *string // back-compat: Linear workflow state UUID passed directly
	Priority    *string

	// Rich edits (Linear only; Taiga ignores these). A nil pointer means "leave
	// unchanged". String fields accept a human name OR a UUID; the literal "none"
	// (or "") clears/unassigns the field where that makes sense.
	State    *string   // state name or UUID (preferred over StateID)
	Assignee *string   // user name/email/UUID, "me", or "none" to unassign
	Project  *string   // project name or UUID, "none" to remove from project
	Labels   *[]string // label names; replaces the whole label set
	DueDate  *string   // ISO 8601 date (YYYY-MM-DD), "none" to clear
	Estimate *float64  // point estimate
	Cycle    *string   // cycle name/number/UUID, "active", or "none" to clear
	Parent   *string   // parent issue identifier (e.g. ABC-42), "none" to detach
}

// Document is the unified representation of a Linear document.
type Document struct {
	Source     string
	SourceType string // "linear"
	ID         string // UUID
	SlugID     string // short stable identifier used in URLs
	Title      string
	Icon       string
	Color      string
	Project    string // project name the doc belongs to
	Creator    string
	URL        string
	CreatedAt  string
	UpdatedAt  string
}

// DocumentDetail extends Document with the full markdown content.
type DocumentDetail struct {
	Document
	Content string // markdown body
}

type DocumentListOpts struct {
	Provider string
	Project  string // project name or team name/key (expands to all projects in the team)
	Query    string // optional title substring filter (client-side)
}

type DocumentCreateInput struct {
	Provider string
	Project  string // project name (required for Linear — docs belong to a project)
	Title    string
	Content  string // markdown
	Icon     string
	Color    string
}

type DocumentUpdateInput struct {
	Title   *string
	Content *string
	Icon    *string
	Color   *string
}

// Provider is the interface each integration must implement.
type Provider interface {
	Name() string
	Type() string
	// Ping verifies the connection with a minimal API call (no heavy queries).
	Ping(ctx context.Context) error
	ListTasks(ctx context.Context, opts ListOpts) ([]Task, error)
	GetTask(ctx context.Context, identifier string) (*TaskDetail, error)
	CreateTask(ctx context.Context, input CreateInput) (*Task, error)
	UpdateTask(ctx context.Context, identifier string, input UpdateInput) (*Task, error)
	SearchTasks(ctx context.Context, query string) ([]Task, error)
	ListProjects(ctx context.Context) ([]Project, error)
	ListStates(ctx context.Context, projectKey string) ([]State, error)

	// AddComment posts a comment on a task. Providers that don't support it
	// return ErrNotSupported.
	AddComment(ctx context.Context, identifier, body string) (*Comment, error)
	// ListMembers lists assignable users. projectKey is an optional scope hint.
	// Providers that don't support it return ErrNotSupported.
	ListMembers(ctx context.Context, projectKey string) ([]Member, error)

	// Documents (Linear only; Taiga returns ErrDocsNotSupported).
	ListDocuments(ctx context.Context, opts DocumentListOpts) ([]Document, error)
	GetDocument(ctx context.Context, identifier string) (*DocumentDetail, error)
	CreateDocument(ctx context.Context, input DocumentCreateInput) (*Document, error)
	UpdateDocument(ctx context.Context, identifier string, input DocumentUpdateInput) (*Document, error)
	DeleteDocument(ctx context.Context, identifier string) error
	SearchDocuments(ctx context.Context, query string) ([]Document, error)
}

// ErrDocsNotSupported is returned by providers that do not expose document APIs (e.g. Taiga).
var ErrDocsNotSupported = fmt.Errorf("documents are not supported by this provider")

// ErrNotSupported is returned by providers that do not implement an optional
// capability (e.g. Taiga for comments/members in the current version).
var ErrNotSupported = fmt.Errorf("not supported by this provider")

const (
	// opTimeout bounds one provider operation. List calls paginate, so a single
	// operation can be a dozen round trips against a large workspace.
	opTimeout = 45 * time.Second

	// discoveryTimeout bounds the lazy team-key lookup: routing must not stall
	// on one slow workspace.
	discoveryTimeout = 20 * time.Second
)

// Registry holds all providers and provides aggregate methods.
type Registry struct {
	providers   []Provider
	teamKeyMap  map[string]Provider // Linear team key → owning provider (populated lazily)
	teamKeyOnce sync.Once
}

func NewRegistry(providers []Provider) *Registry {
	return &Registry{providers: providers}
}

// buildTeamKeyMap lazily builds a mapping from Linear team keys to providers.
func (r *Registry) buildTeamKeyMap(ctx context.Context) {
	r.teamKeyOnce.Do(func() {
		r.teamKeyMap = make(map[string]Provider)
		for _, p := range r.providers {
			if p.Type() != "linear" {
				continue
			}
			pctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
			projects, err := p.ListProjects(pctx)
			cancel()
			if err != nil {
				continue
			}
			for _, proj := range projects {
				if proj.Key != "" {
					r.teamKeyMap[strings.ToUpper(proj.Key)] = p
				}
			}
		}
	})
}

func (r *Registry) Providers() []Provider { return r.providers }

// AllTasks queries all providers concurrently and merges results.
// Failed providers are reported as warnings, not errors.
func (r *Registry) AllTasks(ctx context.Context, opts ListOpts) ([]Task, []string, error) {
	matching := r.matchingProviders(opts.Provider)
	if len(matching) == 0 {
		return nil, nil, fmt.Errorf("unknown provider %q", opts.Provider)
	}

	var (
		mu       sync.Mutex
		allTasks []Task
		warnings []string
		wg       sync.WaitGroup
	)

	for _, p := range matching {
		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, opTimeout)
			defer cancel()

			tasks, err := p.ListTasks(pctx, opts)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("[%s] %v", p.Name(), err))
				return
			}
			allTasks = append(allTasks, tasks...)
		}(p)
	}
	wg.Wait()

	if len(allTasks) == 0 && len(warnings) == len(matching) {
		return nil, warnings, fmt.Errorf("all providers failed")
	}
	return allTasks, warnings, nil
}

// GetTask routes to the correct provider based on identifier format.
func (r *Registry) GetTask(ctx context.Context, identifier string) (*TaskDetail, error) {
	p, localID := r.routeIdentifier(identifier)
	if p == nil {
		return nil, fmt.Errorf("cannot route identifier %q to any provider", identifier)
	}
	pctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	return p.GetTask(pctx, localID)
}

// CreateTask routes to the named provider.
func (r *Registry) CreateTask(ctx context.Context, input CreateInput) (*Task, error) {
	p := r.findByName(input.Provider)
	if p == nil {
		return nil, fmt.Errorf("unknown provider %q", input.Provider)
	}
	pctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	return p.CreateTask(pctx, input)
}

// UpdateTask routes based on identifier.
func (r *Registry) UpdateTask(ctx context.Context, identifier string, input UpdateInput) (*Task, error) {
	p, localID := r.routeIdentifier(identifier)
	if p == nil {
		return nil, fmt.Errorf("cannot route identifier %q to any provider", identifier)
	}
	pctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	return p.UpdateTask(pctx, localID, input)
}

// AddComment routes to the correct provider based on identifier and posts a comment.
func (r *Registry) AddComment(ctx context.Context, identifier, body string) (*Comment, error) {
	p, localID := r.routeIdentifier(identifier)
	if p == nil {
		return nil, fmt.Errorf("cannot route identifier %q to any provider", identifier)
	}
	pctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	return p.AddComment(pctx, localID, body)
}

// ListMembers lists assignable users for a named provider.
func (r *Registry) ListMembers(ctx context.Context, providerName, projectKey string) ([]Member, error) {
	p := r.findByName(providerName)
	if p == nil {
		return nil, fmt.Errorf("unknown provider %q", providerName)
	}
	pctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	return p.ListMembers(pctx, projectKey)
}

// SearchTasks searches across all providers concurrently.
func (r *Registry) SearchTasks(ctx context.Context, query string, providerFilter string) ([]Task, []string, error) {
	var (
		mu       sync.Mutex
		allTasks []Task
		warnings []string
		wg       sync.WaitGroup
	)

	for _, p := range r.providers {
		if providerFilter != "" && !strings.EqualFold(p.Name(), providerFilter) {
			continue
		}
		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, opTimeout)
			defer cancel()

			tasks, err := p.SearchTasks(pctx, query)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("[%s] %v", p.Name(), err))
				return
			}
			allTasks = append(allTasks, tasks...)
		}(p)
	}
	wg.Wait()
	return allTasks, warnings, nil
}

// AllProjects lists projects from all providers.
func (r *Registry) AllProjects(ctx context.Context, providerFilter string) ([]Project, []string, error) {
	var (
		mu       sync.Mutex
		all      []Project
		warnings []string
		wg       sync.WaitGroup
	)

	for _, p := range r.providers {
		if providerFilter != "" && !strings.EqualFold(p.Name(), providerFilter) {
			continue
		}
		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, opTimeout)
			defer cancel()

			projects, err := p.ListProjects(pctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("[%s] %v", p.Name(), err))
				return
			}
			all = append(all, projects...)
		}(p)
	}
	wg.Wait()
	return all, warnings, nil
}

// AllDocuments queries documents across all providers concurrently.
// Providers that don't support docs (Taiga) return nothing (not an error).
func (r *Registry) AllDocuments(ctx context.Context, opts DocumentListOpts) ([]Document, []string, error) {
	var (
		mu       sync.Mutex
		all      []Document
		warnings []string
		wg       sync.WaitGroup
	)

	for _, p := range r.providers {
		if opts.Provider != "" && !strings.EqualFold(p.Name(), opts.Provider) {
			continue
		}
		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()

			docs, err := p.ListDocuments(pctx, opts)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if err == ErrDocsNotSupported {
					return
				}
				warnings = append(warnings, fmt.Sprintf("[%s] %v", p.Name(), err))
				return
			}
			all = append(all, docs...)
		}(p)
	}
	wg.Wait()
	return all, warnings, nil
}

// GetDocument routes to the first Linear-type provider that can resolve the identifier.
func (r *Registry) GetDocument(ctx context.Context, providerName, identifier string) (*DocumentDetail, error) {
	p := r.findDocProvider(providerName)
	if p == nil {
		return nil, fmt.Errorf("no document-capable provider found (provider=%q)", providerName)
	}
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return p.GetDocument(pctx, identifier)
}

// CreateDocument creates a doc in the named provider.
func (r *Registry) CreateDocument(ctx context.Context, input DocumentCreateInput) (*Document, error) {
	p := r.findByName(input.Provider)
	if p == nil {
		return nil, fmt.Errorf("unknown provider %q", input.Provider)
	}
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return p.CreateDocument(pctx, input)
}

// UpdateDocument updates a doc in the named provider.
func (r *Registry) UpdateDocument(ctx context.Context, providerName, identifier string, input DocumentUpdateInput) (*Document, error) {
	p := r.findDocProvider(providerName)
	if p == nil {
		return nil, fmt.Errorf("no document-capable provider found (provider=%q)", providerName)
	}
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return p.UpdateDocument(pctx, identifier, input)
}

// DeleteDocument removes a doc in the named provider.
func (r *Registry) DeleteDocument(ctx context.Context, providerName, identifier string) error {
	p := r.findDocProvider(providerName)
	if p == nil {
		return fmt.Errorf("no document-capable provider found (provider=%q)", providerName)
	}
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return p.DeleteDocument(pctx, identifier)
}

// SearchDocuments searches across all providers. Taiga returns nothing.
func (r *Registry) SearchDocuments(ctx context.Context, query, providerFilter string) ([]Document, []string, error) {
	var (
		mu       sync.Mutex
		all      []Document
		warnings []string
		wg       sync.WaitGroup
	)

	for _, p := range r.providers {
		if providerFilter != "" && !strings.EqualFold(p.Name(), providerFilter) {
			continue
		}
		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()

			docs, err := p.SearchDocuments(pctx, query)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if err == ErrDocsNotSupported {
					return
				}
				warnings = append(warnings, fmt.Sprintf("[%s] %v", p.Name(), err))
				return
			}
			all = append(all, docs...)
		}(p)
	}
	wg.Wait()
	return all, warnings, nil
}

// findDocProvider returns a provider that supports documents. If providerName is given
// it must match; otherwise the first Linear provider is returned.
func (r *Registry) findDocProvider(providerName string) Provider {
	if providerName != "" {
		p := r.findByName(providerName)
		if p == nil || p.Type() != "linear" {
			return nil
		}
		return p
	}
	for _, p := range r.providers {
		if p.Type() == "linear" {
			return p
		}
	}
	return nil
}

// States lists workflow states for a specific provider and project.
func (r *Registry) States(ctx context.Context, providerName, projectKey string) ([]State, error) {
	p := r.findByName(providerName)
	if p == nil {
		return nil, fmt.Errorf("unknown provider %q", providerName)
	}
	pctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	return p.ListStates(pctx, projectKey)
}

// routeIdentifier determines which provider handles an identifier.
// Linear identifiers: "<TEAMKEY>-<number>" (uppercase letters + dash + number)
// Taiga identifiers: "<providerName>:us:<id>" or "<providerName>:task:<id>"
func (r *Registry) routeIdentifier(identifier string) (Provider, string) {
	// Taiga format: providerName:type:id
	if parts := strings.SplitN(identifier, ":", 3); len(parts) == 3 {
		p := r.findByName(parts[0])
		return p, identifier
	}

	// Linear format: match team key to the correct provider
	if parts := strings.SplitN(identifier, "-", 2); len(parts) == 2 {
		teamKey := strings.ToUpper(parts[0])
		r.buildTeamKeyMap(context.Background())
		if p, ok := r.teamKeyMap[teamKey]; ok {
			return p, identifier
		}
	}

	// Fallback: first Linear provider
	for _, p := range r.providers {
		if p.Type() == "linear" {
			return p, identifier
		}
	}
	return nil, identifier
}

func (r *Registry) findByName(name string) Provider {
	for _, p := range r.providers {
		if strings.EqualFold(p.Name(), name) {
			return p
		}
	}
	return nil
}

func (r *Registry) matchingProviders(filter string) []Provider {
	if filter == "" {
		return r.providers
	}
	var out []Provider
	for _, p := range r.providers {
		if strings.EqualFold(p.Name(), filter) {
			out = append(out, p)
		}
	}
	return out
}
