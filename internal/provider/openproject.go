package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

// openProjectProvider talks to an OpenProject instance over its HAL+JSON REST API
// (<baseURL>/api/v3). Authentication uses an API token via HTTP Basic auth with the
// fixed username "apikey".
type openProjectProvider struct {
	name    string
	baseURL string
	authHdr string // precomputed "Basic <base64(apikey:token)>"
	client  *http.Client

	mu           sync.Mutex
	userID       int             // cached from /users/me
	statusClosed map[string]bool // status id → isClosed (lazily loaded)
}

func NewOpenProject(name, baseURL, apiKey string) Provider {
	cred := base64.StdEncoding.EncodeToString([]byte("apikey:" + apiKey))
	return &openProjectProvider{
		name:    name,
		baseURL: strings.TrimRight(baseURL, "/"),
		authHdr: "Basic " + cred,
		client:  newHTTPClient(),
	}
}

func (o *openProjectProvider) Name() string { return o.name }
func (o *openProjectProvider) Type() string { return "openproject" }

func (o *openProjectProvider) Ping(ctx context.Context) error {
	return o.ensureUser(ctx)
}

// ─── Tasks ──────────────────────────────────────────────────────────────────────

func (o *openProjectProvider) ListTasks(ctx context.Context, opts ListOpts) ([]Task, error) {
	var filters []map[string]any

	// Open work packages unless the caller explicitly asks for everything.
	if !strings.EqualFold(opts.State, "all") {
		filters = append(filters, map[string]any{
			"status": map[string]any{"operator": "o", "values": []string{}},
		})
	}
	if opts.Project != "" {
		pid, err := o.resolveProjectID(ctx, opts.Project)
		if err != nil {
			return nil, err
		}
		filters = append(filters, map[string]any{
			"project": map[string]any{"operator": "=", "values": []string{pid}},
		})
	}
	if opts.Assigned {
		if err := o.ensureUser(ctx); err != nil {
			return nil, err
		}
		filters = append(filters, map[string]any{
			"assignee": map[string]any{"operator": "=", "values": []string{strconv.Itoa(o.userID)}},
		})
	}

	const pageSize = 100
	const maxPages = 10 // hard cap: 1000 work packages per list call
	var tasks []Task
	offset := 1 // OpenProject pages are 1-based
	for page := 0; page < maxPages; page++ {
		u := fmt.Sprintf("%s/api/v3/work_packages?offset=%d&pageSize=%d",
			o.baseURL, offset, pageSize)
		// Only send `filters` when non-empty: a nil/empty slice marshals to
		// `null`, and OpenProject returns HTTP 500 on filters=null.
		if len(filters) > 0 {
			filtersJSON, _ := json.Marshal(filters)
			u += "&filters=" + url.QueryEscape(string(filtersJSON))
		}

		var coll struct {
			Total    int `json:"total"`
			Count    int `json:"count"`
			Embedded struct {
				Elements []opWorkPackage `json:"elements"`
			} `json:"_embedded"`
		}
		if err := o.get(ctx, u, &coll); err != nil {
			return nil, fmt.Errorf("listing work packages: %w", err)
		}
		for i := range coll.Embedded.Elements {
			tasks = append(tasks, o.toTask(ctx, &coll.Embedded.Elements[i]))
		}
		if len(tasks) >= coll.Total || coll.Count == 0 {
			break
		}
		offset++
	}
	return tasks, nil
}

func (o *openProjectProvider) GetTask(ctx context.Context, identifier string) (*TaskDetail, error) {
	id, err := parseOpenProjectID(identifier)
	if err != nil {
		return nil, err
	}

	u := fmt.Sprintf("%s/api/v3/work_packages/%d", o.baseURL, id)
	var wp opWorkPackage
	if err := o.get(ctx, u, &wp); err != nil {
		return nil, err
	}

	task := o.toTask(ctx, &wp)
	detail := &TaskDetail{
		Task:        task,
		Description: wp.Description.Raw,
		Assignee:    task.Assignee,
	}

	// Comments live in the activities collection (entries with a non-empty comment).
	actURL := fmt.Sprintf("%s/api/v3/work_packages/%d/activities", o.baseURL, id)
	var acts struct {
		Embedded struct {
			Elements []opActivity `json:"elements"`
		} `json:"_embedded"`
	}
	if err := o.get(ctx, actURL, &acts); err == nil {
		for _, a := range acts.Embedded.Elements {
			if strings.TrimSpace(a.Comment.Raw) == "" {
				continue
			}
			detail.Comments = append(detail.Comments, Comment{
				Author:    a.Links.User.Title,
				Body:      a.Comment.Raw,
				CreatedAt: a.CreatedAt,
			})
		}
	}
	return detail, nil
}

func (o *openProjectProvider) CreateTask(ctx context.Context, input CreateInput) (*Task, error) {
	pid, err := o.resolveProjectID(ctx, input.Project)
	if err != nil {
		return nil, err
	}

	links := map[string]any{}

	// OpenProject requires a type. Pick "Task" if available, else the project's first type.
	typeHref, err := o.defaultTypeHref(ctx, pid)
	if err != nil {
		return nil, err
	}
	links["type"] = map[string]string{"href": typeHref}

	if input.StateID != "" {
		links["status"] = map[string]string{"href": fmt.Sprintf("/api/v3/statuses/%s", input.StateID)}
	}
	if input.Priority != "" {
		if href, err := o.resolvePriorityHref(ctx, input.Priority); err == nil && href != "" {
			links["priority"] = map[string]string{"href": href}
		}
	}

	body := map[string]any{
		"subject": input.Title,
		"_links":  links,
	}
	if input.Description != "" {
		body["description"] = map[string]any{"raw": input.Description}
	}

	u := fmt.Sprintf("%s/api/v3/projects/%s/work_packages", o.baseURL, pid)
	var wp opWorkPackage
	if err := o.post(ctx, u, body, &wp); err != nil {
		return nil, err
	}
	task := o.toTask(ctx, &wp)
	return &task, nil
}

func (o *openProjectProvider) UpdateTask(ctx context.Context, identifier string, input UpdateInput) (*Task, error) {
	id, err := parseOpenProjectID(identifier)
	if err != nil {
		return nil, err
	}

	// Fetch the current lockVersion — OpenProject rejects PATCH without it.
	u := fmt.Sprintf("%s/api/v3/work_packages/%d", o.baseURL, id)
	var current opWorkPackage
	if err := o.get(ctx, u, &current); err != nil {
		return nil, fmt.Errorf("getting current work package: %w", err)
	}

	body := map[string]any{"lockVersion": current.LockVersion}
	links := map[string]any{}
	if input.Title != nil {
		body["subject"] = *input.Title
	}
	if input.Description != nil {
		body["description"] = map[string]any{"raw": *input.Description}
	}
	if input.StateID != nil {
		links["status"] = map[string]string{"href": fmt.Sprintf("/api/v3/statuses/%s", *input.StateID)}
	}
	if input.Priority != nil {
		if href, err := o.resolvePriorityHref(ctx, *input.Priority); err == nil && href != "" {
			links["priority"] = map[string]string{"href": href}
		}
	}
	if len(links) > 0 {
		body["_links"] = links
	}

	var wp opWorkPackage
	if err := o.patch(ctx, u, body, &wp); err != nil {
		return nil, err
	}
	task := o.toTask(ctx, &wp)
	return &task, nil
}

func (o *openProjectProvider) SearchTasks(ctx context.Context, query string) ([]Task, error) {
	filters := []map[string]any{
		{"subject": map[string]any{"operator": "~", "values": []string{query}}},
	}
	filtersJSON, _ := json.Marshal(filters)
	u := fmt.Sprintf("%s/api/v3/work_packages?pageSize=100&filters=%s",
		o.baseURL, url.QueryEscape(string(filtersJSON)))

	var coll struct {
		Embedded struct {
			Elements []opWorkPackage `json:"elements"`
		} `json:"_embedded"`
	}
	if err := o.get(ctx, u, &coll); err != nil {
		return nil, err
	}
	var tasks []Task
	for i := range coll.Embedded.Elements {
		tasks = append(tasks, o.toTask(ctx, &coll.Embedded.Elements[i]))
	}
	return tasks, nil
}

func (o *openProjectProvider) ListProjects(ctx context.Context) ([]Project, error) {
	const pageSize = 100
	const maxPages = 10 // hard cap: 1000 projects
	var result []Project
	offset := 1 // OpenProject pages are 1-based
	for page := 0; page < maxPages; page++ {
		u := fmt.Sprintf("%s/api/v3/projects?offset=%d&pageSize=%d", o.baseURL, offset, pageSize)
		var coll struct {
			Total    int `json:"total"`
			Count    int `json:"count"`
			Embedded struct {
				Elements []opProject `json:"elements"`
			} `json:"_embedded"`
		}
		if err := o.get(ctx, u, &coll); err != nil {
			return nil, err
		}
		for _, p := range coll.Embedded.Elements {
			result = append(result, Project{
				Source:     o.name,
				SourceType: "openproject",
				ID:         strconv.Itoa(p.ID),
				Name:       p.Name,
				Key:        p.Identifier,
				Kind:       "project",
				URL:        fmt.Sprintf("%s/projects/%s", o.baseURL, p.Identifier),
			})
		}
		if len(result) >= coll.Total || coll.Count == 0 {
			break
		}
		offset++
	}
	return result, nil
}

// ListStates returns the instance-wide statuses. OpenProject defines statuses globally
// (not per project), so projectKey is accepted for interface compatibility but ignored.
func (o *openProjectProvider) ListStates(ctx context.Context, projectKey string) ([]State, error) {
	statuses, err := o.fetchStatuses(ctx)
	if err != nil {
		return nil, err
	}
	var states []State
	for _, s := range statuses {
		stype := "started"
		if s.IsClosed {
			stype = "completed"
		}
		states = append(states, State{
			ID:      strconv.Itoa(s.ID),
			Name:    s.Name,
			Type:    stype,
			Project: projectKey,
		})
	}
	return states, nil
}

// AddComment is not supported by the OpenProject provider in the current version.
func (o *openProjectProvider) AddComment(ctx context.Context, identifier, body string) (*Comment, error) {
	return nil, ErrNotSupported
}

// ListMembers is not supported by the OpenProject provider in the current version.
func (o *openProjectProvider) ListMembers(ctx context.Context, projectKey string) ([]Member, error) {
	return nil, ErrNotSupported
}

// ─── Documents (unsupported) ──────────────────────────────────────────────────

func (o *openProjectProvider) ListDocuments(ctx context.Context, opts DocumentListOpts) ([]Document, error) {
	return nil, ErrDocsNotSupported
}

func (o *openProjectProvider) GetDocument(ctx context.Context, identifier string) (*DocumentDetail, error) {
	return nil, ErrDocsNotSupported
}

func (o *openProjectProvider) CreateDocument(ctx context.Context, input DocumentCreateInput) (*Document, error) {
	return nil, ErrDocsNotSupported
}

func (o *openProjectProvider) UpdateDocument(ctx context.Context, identifier string, input DocumentUpdateInput) (*Document, error) {
	return nil, ErrDocsNotSupported
}

func (o *openProjectProvider) DeleteDocument(ctx context.Context, identifier string) error {
	return ErrDocsNotSupported
}

func (o *openProjectProvider) SearchDocuments(ctx context.Context, query string) ([]Document, error) {
	return nil, ErrDocsNotSupported
}

// ─── HTTP helpers ───────────────────────────────────────────────────────────────

func (o *openProjectProvider) doRequest(ctx context.Context, method, u string, body any, target any) error {
	var bodyReader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", o.authHdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("OpenProject request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("OpenProject %s %s returned %d: %s", method, u, resp.StatusCode, string(respBody))
	}
	if target != nil {
		return json.Unmarshal(respBody, target)
	}
	return nil
}

func (o *openProjectProvider) get(ctx context.Context, u string, target any) error {
	return o.doRequest(ctx, "GET", u, nil, target)
}

func (o *openProjectProvider) post(ctx context.Context, u string, body, target any) error {
	return o.doRequest(ctx, "POST", u, body, target)
}

func (o *openProjectProvider) patch(ctx context.Context, u string, body, target any) error {
	return o.doRequest(ctx, "PATCH", u, body, target)
}

// ─── Lookups & caching ──────────────────────────────────────────────────────────

func (o *openProjectProvider) ensureUser(ctx context.Context) error {
	o.mu.Lock()
	cached := o.userID
	o.mu.Unlock()
	if cached != 0 {
		return nil
	}
	var me struct {
		ID int `json:"id"`
	}
	if err := o.get(ctx, o.baseURL+"/api/v3/users/me", &me); err != nil {
		return fmt.Errorf("OpenProject auth check failed: %w", err)
	}
	o.mu.Lock()
	o.userID = me.ID
	o.mu.Unlock()
	return nil
}

func (o *openProjectProvider) fetchStatuses(ctx context.Context) ([]opStatus, error) {
	var coll struct {
		Embedded struct {
			Elements []opStatus `json:"elements"`
		} `json:"_embedded"`
	}
	if err := o.get(ctx, o.baseURL+"/api/v3/statuses", &coll); err != nil {
		return nil, err
	}
	return coll.Embedded.Elements, nil
}

// statusClosedMap lazily builds and caches a status id → isClosed lookup.
func (o *openProjectProvider) statusClosedMap(ctx context.Context) map[string]bool {
	o.mu.Lock()
	if o.statusClosed != nil {
		m := o.statusClosed
		o.mu.Unlock()
		return m
	}
	o.mu.Unlock()

	statuses, err := o.fetchStatuses(ctx)
	if err != nil {
		return map[string]bool{}
	}
	m := make(map[string]bool, len(statuses))
	for _, s := range statuses {
		m[strconv.Itoa(s.ID)] = s.IsClosed
	}
	o.mu.Lock()
	o.statusClosed = m
	o.mu.Unlock()
	return m
}

func (o *openProjectProvider) resolveProjectID(ctx context.Context, nameOrKey string) (string, error) {
	projects, err := o.ListProjects(ctx)
	if err != nil {
		return "", err
	}
	for _, p := range projects {
		if strings.EqualFold(p.Name, nameOrKey) || strings.EqualFold(p.Key, nameOrKey) || p.ID == nameOrKey {
			return p.ID, nil
		}
	}
	return "", fmt.Errorf("project %q not found in %s", nameOrKey, o.name)
}

func (o *openProjectProvider) defaultTypeHref(ctx context.Context, projectID string) (string, error) {
	u := fmt.Sprintf("%s/api/v3/projects/%s/types", o.baseURL, projectID)
	var coll struct {
		Embedded struct {
			Elements []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"elements"`
		} `json:"_embedded"`
	}
	if err := o.get(ctx, u, &coll); err != nil {
		return "", fmt.Errorf("listing project types: %w", err)
	}
	if len(coll.Embedded.Elements) == 0 {
		return "", fmt.Errorf("project %s has no work package types", projectID)
	}
	for _, t := range coll.Embedded.Elements {
		if strings.EqualFold(t.Name, "Task") {
			return fmt.Sprintf("/api/v3/types/%d", t.ID), nil
		}
	}
	return fmt.Sprintf("/api/v3/types/%d", coll.Embedded.Elements[0].ID), nil
}

func (o *openProjectProvider) resolvePriorityHref(ctx context.Context, name string) (string, error) {
	var coll struct {
		Embedded struct {
			Elements []opPriority `json:"elements"`
		} `json:"_embedded"`
	}
	if err := o.get(ctx, o.baseURL+"/api/v3/priorities", &coll); err != nil {
		return "", err
	}
	for _, p := range coll.Embedded.Elements {
		if strings.EqualFold(p.Name, name) {
			return fmt.Sprintf("/api/v3/priorities/%d", p.ID), nil
		}
	}
	return "", nil
}

// ─── Types & mapping ──────────────────────────────────────────────────────────

type opWorkPackage struct {
	ID          int    `json:"id"`
	Subject     string `json:"subject"`
	Description struct {
		Raw string `json:"raw"`
	} `json:"description"`
	DueDate     *string `json:"dueDate"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
	LockVersion int     `json:"lockVersion"`
	Links       struct {
		Status   opLink `json:"status"`
		Priority opLink `json:"priority"`
		Project  opLink `json:"project"`
		Assignee opLink `json:"assignee"`
	} `json:"_links"`
}

type opLink struct {
	Href  string `json:"href"`
	Title string `json:"title"`
}

type opActivity struct {
	Comment struct {
		Raw string `json:"raw"`
	} `json:"comment"`
	CreatedAt string `json:"createdAt"`
	Links     struct {
		User opLink `json:"user"`
	} `json:"_links"`
}

type opProject struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Identifier string `json:"identifier"`
}

type opStatus struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	IsClosed bool   `json:"isClosed"`
}

type opPriority struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func (o *openProjectProvider) toTask(ctx context.Context, wp *opWorkPackage) Task {
	due := ""
	if wp.DueDate != nil {
		due = *wp.DueDate
	}

	statusType := "started"
	if statusID := idFromHref(wp.Links.Status.Href); statusID != "" {
		if o.statusClosedMap(ctx)[statusID] {
			statusType = "completed"
		}
	}

	return Task{
		Source:     o.name,
		SourceType: "openproject",
		Identifier: fmt.Sprintf("%s:wp:%d", o.name, wp.ID),
		Title:      wp.Subject,
		Status:     wp.Links.Status.Title,
		StatusType: statusType,
		Priority:   normalizeOpPriority(wp.Links.Priority.Title),
		Project:    wp.Links.Project.Title,
		Assignee:   wp.Links.Assignee.Title,
		DueDate:    due,
		URL:        fmt.Sprintf("%s/work_packages/%d", o.baseURL, wp.ID),
		CreatedAt:  wp.CreatedAt,
		UpdatedAt:  wp.UpdatedAt,
	}
}

// normalizeOpPriority maps OpenProject's default priority names onto the unified scale,
// falling back to the original label for custom priorities.
func normalizeOpPriority(name string) string {
	switch strings.ToLower(name) {
	case "immediate":
		return "Urgent"
	case "high":
		return "High"
	case "normal":
		return "Medium"
	case "low":
		return "Low"
	case "":
		return "None"
	default:
		return name
	}
}

// idFromHref extracts the trailing numeric id from a HAL href like "/api/v3/statuses/7".
func idFromHref(href string) string {
	if href == "" {
		return ""
	}
	parts := strings.Split(strings.TrimRight(href, "/"), "/")
	return parts[len(parts)-1]
}

func parseOpenProjectID(identifier string) (int, error) {
	// Format: "providerName:wp:id" e.g. "work:wp:1234"
	parts := strings.SplitN(identifier, ":", 3)
	if len(parts) != 3 || parts[1] != "wp" {
		return 0, fmt.Errorf("invalid OpenProject identifier %q (expected format: provider:wp:1234)", identifier)
	}
	id, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, fmt.Errorf("invalid OpenProject work package id %q: %w", parts[2], err)
	}
	return id, nil
}
