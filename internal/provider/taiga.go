package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

type taigaProvider struct {
	name     string
	baseURL  string
	username string
	password string
	client   *http.Client

	mu        sync.Mutex
	authToken string
	userID    int
}

func NewTaiga(name, baseURL, username, password string) Provider {
	return &taigaProvider{
		name:     name,
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		client:   newHTTPClient(),
	}
}

func (t *taigaProvider) Name() string { return t.name }
func (t *taigaProvider) Type() string { return "taiga" }

func (t *taigaProvider) Ping(ctx context.Context) error {
	return t.ensureAuth(ctx)
}

func (t *taigaProvider) ListTasks(ctx context.Context, opts ListOpts) ([]Task, error) {
	if err := t.ensureAuth(ctx); err != nil {
		return nil, err
	}

	baseQS := "status__is_closed=false"
	if opts.Assigned {
		baseQS += fmt.Sprintf("&assigned_to=%d", t.userID)
	}
	if opts.Project != "" {
		if pid, err := t.resolveProjectID(ctx, opts.Project); err == nil {
			baseQS += fmt.Sprintf("&project=%d", pid)
		}
	}

	var allTasks []Task

	// Fetch user stories
	usURL := fmt.Sprintf("%s/api/v1/userstories?%s", t.baseURL, baseQS)
	var stories []taigaUserStory
	if err := t.getAll(ctx, usURL, &stories); err != nil {
		return nil, fmt.Errorf("listing user stories: %w", err)
	}
	for _, s := range stories {
		allTasks = append(allTasks, s.toTask(t.name))
	}

	// Fetch tasks
	taskURL := fmt.Sprintf("%s/api/v1/tasks?%s", t.baseURL, baseQS)
	var tasks []taigaTask
	if err := t.getAll(ctx, taskURL, &tasks); err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	for _, tk := range tasks {
		allTasks = append(allTasks, tk.toTask(t.name))
	}

	return allTasks, nil
}

func (t *taigaProvider) GetTask(ctx context.Context, identifier string) (*TaskDetail, error) {
	if err := t.ensureAuth(ctx); err != nil {
		return nil, err
	}

	itemType, id, err := parseTaigaID(identifier)
	if err != nil {
		return nil, err
	}

	switch itemType {
	case "us":
		url := fmt.Sprintf("%s/api/v1/userstories/%d", t.baseURL, id)
		var story taigaUserStoryDetail
		if err := t.get(ctx, url, &story); err != nil {
			return nil, err
		}
		detail := story.toTaskDetail(t.name)

		// Fetch child tasks belonging to this user story (open and closed)
		tasksURL := fmt.Sprintf("%s/api/v1/tasks?user_story=%d", t.baseURL, id)
		var childTasks []taigaTask
		if err := t.getAll(ctx, tasksURL, &childTasks); err == nil {
			for _, ct := range childTasks {
				detail.Subtasks = append(detail.Subtasks, ct.toTask(t.name))
			}
		}

		// Fetch comments
		commentsURL := fmt.Sprintf("%s/api/v1/history/userstory/%d", t.baseURL, id)
		comments, _ := t.fetchComments(ctx, commentsURL)
		detail.Comments = comments
		return detail, nil

	case "task":
		url := fmt.Sprintf("%s/api/v1/tasks/%d", t.baseURL, id)
		var task taigaTaskDetail
		if err := t.get(ctx, url, &task); err != nil {
			return nil, err
		}
		detail := task.toTaskDetail(t.name)

		// Fetch parent user story context if this task belongs to one
		if task.UserStory != nil && *task.UserStory > 0 {
			usURL := fmt.Sprintf("%s/api/v1/userstories/%d", t.baseURL, *task.UserStory)
			var parentUS struct {
				Ref             int    `json:"ref"`
				Subject         string `json:"subject"`
				Description     string `json:"description"`
				AssignedToExtra *struct {
					FullName string `json:"full_name_display"`
				} `json:"assigned_to_extra_info"`
			}
			if err := t.get(ctx, usURL, &parentUS); err == nil {
				assignee := "unassigned"
				if parentUS.AssignedToExtra != nil {
					assignee = parentUS.AssignedToExtra.FullName
				}
				detail.Milestone = fmt.Sprintf("US #%d: %s (assigned to: %s)", parentUS.Ref, parentUS.Subject, assignee)
				if detail.Description == "" && parentUS.Description != "" {
					detail.Description = fmt.Sprintf("[From parent US #%d]\n\n%s", parentUS.Ref, parentUS.Description)
				}
			}
		}

		commentsURL := fmt.Sprintf("%s/api/v1/history/task/%d", t.baseURL, id)
		comments, _ := t.fetchComments(ctx, commentsURL)
		detail.Comments = comments
		return detail, nil

	default:
		return nil, fmt.Errorf("unknown Taiga item type %q", itemType)
	}
}

func (t *taigaProvider) CreateTask(ctx context.Context, input CreateInput) (*Task, error) {
	if err := t.ensureAuth(ctx); err != nil {
		return nil, err
	}

	projectID, err := t.resolveProjectID(ctx, input.Project)
	if err != nil {
		return nil, err
	}

	itemType := input.Type
	if itemType == "" {
		itemType = "userstory"
	}

	switch itemType {
	case "userstory":
		url := fmt.Sprintf("%s/api/v1/userstories", t.baseURL)
		body := map[string]any{
			"project":     projectID,
			"subject":     input.Title,
			"assigned_to": t.userID,
		}
		if input.Description != "" {
			body["description"] = input.Description
		}
		if input.StateID != "" {
			if sid, err := strconv.Atoi(input.StateID); err == nil {
				body["status"] = sid
			}
		}
		var story taigaUserStory
		if err := t.post(ctx, url, body, &story); err != nil {
			return nil, err
		}
		task := story.toTask(t.name)
		return &task, nil

	case "task":
		url := fmt.Sprintf("%s/api/v1/tasks", t.baseURL)
		body := map[string]any{
			"project":     projectID,
			"subject":     input.Title,
			"assigned_to": t.userID,
		}
		if input.Description != "" {
			body["description"] = input.Description
		}
		if input.StateID != "" {
			if sid, err := strconv.Atoi(input.StateID); err == nil {
				body["status"] = sid
			}
		}
		var tk taigaTask
		if err := t.post(ctx, url, body, &tk); err != nil {
			return nil, err
		}
		task := tk.toTask(t.name)
		return &task, nil

	default:
		return nil, fmt.Errorf("unknown Taiga item type %q (use 'userstory' or 'task')", itemType)
	}
}

func (t *taigaProvider) UpdateTask(ctx context.Context, identifier string, input UpdateInput) (*Task, error) {
	if err := t.ensureAuth(ctx); err != nil {
		return nil, err
	}

	itemType, id, err := parseTaigaID(identifier)
	if err != nil {
		return nil, err
	}

	// First get current version for optimistic concurrency
	var endpoint string
	switch itemType {
	case "us":
		endpoint = fmt.Sprintf("%s/api/v1/userstories/%d", t.baseURL, id)
	case "task":
		endpoint = fmt.Sprintf("%s/api/v1/tasks/%d", t.baseURL, id)
	default:
		return nil, fmt.Errorf("unknown type %q", itemType)
	}

	// Get current version
	var current struct {
		Version int `json:"version"`
	}
	if err := t.get(ctx, endpoint, &current); err != nil {
		return nil, fmt.Errorf("getting current version: %w", err)
	}

	body := map[string]any{"version": current.Version}
	if input.Title != nil {
		body["subject"] = *input.Title
	}
	if input.Description != nil {
		body["description"] = *input.Description
	}
	if input.StateID != nil {
		if sid, err := strconv.Atoi(*input.StateID); err == nil {
			body["status"] = sid
		}
	}

	switch itemType {
	case "us":
		var story taigaUserStory
		if err := t.patch(ctx, endpoint, body, &story); err != nil {
			return nil, err
		}
		task := story.toTask(t.name)
		return &task, nil
	case "task":
		var tk taigaTask
		if err := t.patch(ctx, endpoint, body, &tk); err != nil {
			return nil, err
		}
		task := tk.toTask(t.name)
		return &task, nil
	}
	return nil, fmt.Errorf("unreachable")
}

func (t *taigaProvider) SearchTasks(ctx context.Context, query string) ([]Task, error) {
	// Taiga doesn't have a great search API, so we list and filter client-side
	all, err := t.ListTasks(ctx, ListOpts{})
	if err != nil {
		return nil, err
	}

	q := strings.ToLower(query)
	var matched []Task
	for _, task := range all {
		if strings.Contains(strings.ToLower(task.Title), q) {
			matched = append(matched, task)
		}
	}
	return matched, nil
}

func (t *taigaProvider) ListProjects(ctx context.Context) ([]Project, error) {
	if err := t.ensureAuth(ctx); err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/api/v1/projects?member=%d&order_by=-total_activity", t.baseURL, t.userID)
	var projects []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := t.getAll(ctx, url, &projects); err != nil {
		return nil, err
	}

	var result []Project
	for _, p := range projects {
		result = append(result, Project{
			Source:     t.name,
			SourceType: "taiga",
			ID:         strconv.Itoa(p.ID),
			Name:       p.Name,
			Key:        p.Slug,
			Kind:       "project",
		})
	}
	return result, nil
}

// ─── Documents (unsupported) ──────────────────────────────────────────────────

func (t *taigaProvider) ListDocuments(ctx context.Context, opts DocumentListOpts) ([]Document, error) {
	return nil, ErrDocsNotSupported
}

func (t *taigaProvider) GetDocument(ctx context.Context, identifier string) (*DocumentDetail, error) {
	return nil, ErrDocsNotSupported
}

func (t *taigaProvider) CreateDocument(ctx context.Context, input DocumentCreateInput) (*Document, error) {
	return nil, ErrDocsNotSupported
}

func (t *taigaProvider) UpdateDocument(ctx context.Context, identifier string, input DocumentUpdateInput) (*Document, error) {
	return nil, ErrDocsNotSupported
}

func (t *taigaProvider) DeleteDocument(ctx context.Context, identifier string) error {
	return ErrDocsNotSupported
}

func (t *taigaProvider) SearchDocuments(ctx context.Context, query string) ([]Document, error) {
	return nil, ErrDocsNotSupported
}

// AddComment is not supported by the Taiga provider in the current version.
func (t *taigaProvider) AddComment(ctx context.Context, identifier, body string) (*Comment, error) {
	return nil, ErrNotSupported
}

// ListMembers is not supported by the Taiga provider in the current version.
func (t *taigaProvider) ListMembers(ctx context.Context, projectKey string) ([]Member, error) {
	return nil, ErrNotSupported
}

func (t *taigaProvider) ListStates(ctx context.Context, projectKey string) ([]State, error) {
	if err := t.ensureAuth(ctx); err != nil {
		return nil, err
	}

	projectID, err := t.resolveProjectID(ctx, projectKey)
	if err != nil {
		return nil, err
	}

	var states []State

	// User story statuses
	usURL := fmt.Sprintf("%s/api/v1/userstory-statuses?project=%d", t.baseURL, projectID)
	var usStatuses []struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		IsClosed bool   `json:"is_closed"`
	}
	if err := t.getAll(ctx, usURL, &usStatuses); err == nil {
		for _, s := range usStatuses {
			stype := "started"
			if s.IsClosed {
				stype = "completed"
			}
			states = append(states, State{
				ID:      fmt.Sprintf("us:%d", s.ID),
				Name:    fmt.Sprintf("[US] %s", s.Name),
				Type:    stype,
				Project: projectKey,
			})
		}
	}

	// Task statuses
	taskURL := fmt.Sprintf("%s/api/v1/task-statuses?project=%d", t.baseURL, projectID)
	var taskStatuses []struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		IsClosed bool   `json:"is_closed"`
	}
	if err := t.getAll(ctx, taskURL, &taskStatuses); err == nil {
		for _, s := range taskStatuses {
			stype := "started"
			if s.IsClosed {
				stype = "completed"
			}
			states = append(states, State{
				ID:      fmt.Sprintf("task:%d", s.ID),
				Name:    fmt.Sprintf("[Task] %s", s.Name),
				Type:    stype,
				Project: projectKey,
			})
		}
	}

	return states, nil
}

// ─── Auth ───────────────��────────────────────────��────────────────────────────

func (t *taigaProvider) ensureAuth(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.authToken != "" {
		return nil
	}
	return t.authenticate(ctx)
}

func (t *taigaProvider) authenticate(ctx context.Context) error {
	url := fmt.Sprintf("%s/api/v1/auth", t.baseURL)
	body, _ := json.Marshal(map[string]string{
		"type":     "normal",
		"username": t.username,
		"password": t.password,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("Taiga auth failed (is VPN connected?): %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("Taiga auth returned %d: %s", resp.StatusCode, string(respBody))
	}

	var authResp struct {
		AuthToken string `json:"auth_token"`
		ID        int    `json:"id"`
	}
	if err := json.Unmarshal(respBody, &authResp); err != nil {
		return fmt.Errorf("parsing auth response: %w", err)
	}

	t.authToken = authResp.AuthToken
	t.userID = authResp.ID
	return nil
}

func (t *taigaProvider) reauth(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.authToken = ""
	return t.authenticate(ctx)
}

// ─── HTTP helpers ────────────────────────────────────────────────────────────

// taigaPageSize is the page size requested from paginated Taiga endpoints.
// Taiga's own default is 30; asking for more cuts the number of round-trips.
const taigaPageSize = 100

// taigaMaxPages bounds getAll so a misbehaving instance can never spin forever.
// At taigaPageSize items per page that covers 10k items per collection.
const taigaMaxPages = 100

func (t *taigaProvider) doRequest(ctx context.Context, method, url string, body any, target any) error {
	_, err := t.doRequestH(ctx, method, url, body, target)
	return err
}

// doRequestH performs a request and returns the response headers, which the
// paginated helpers need in order to follow Taiga's x-pagination-next links.
//
// The body is buffered rather than streamed so the 401 retry below can replay
// it: an io.Reader is drained by the first attempt and replays as empty.
func (t *taigaProvider) doRequestH(ctx context.Context, method, url string, body any, target any) (http.Header, error) {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding Taiga request body: %w", err)
		}
	}

	newRequest := func(token string) (*http.Request, error) {
		var r io.Reader
		if payload != nil {
			r = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, r)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		return req, nil
	}

	t.mu.Lock()
	token := t.authToken
	t.mu.Unlock()

	req, err := newRequest(token)
	if err != nil {
		return nil, err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Taiga request failed (is VPN connected?): %w", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// On 401 the session token expired: re-authenticate and replay once.
	if resp.StatusCode == http.StatusUnauthorized {
		if err := t.reauth(ctx); err != nil {
			return nil, fmt.Errorf("re-auth failed: %w", err)
		}
		t.mu.Lock()
		token = t.authToken
		t.mu.Unlock()

		req, err = newRequest(token)
		if err != nil {
			return nil, err
		}
		resp, err = t.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("Taiga request failed after re-auth: %w", err)
		}
		respBody, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
	}

	if resp.StatusCode >= 400 {
		return resp.Header, fmt.Errorf("Taiga %s %s returned %d: %s", method, url, resp.StatusCode, string(respBody))
	}

	if target != nil {
		if err := json.Unmarshal(respBody, target); err != nil {
			return resp.Header, fmt.Errorf("decoding Taiga response: %w", err)
		}
	}
	return resp.Header, nil
}

func (t *taigaProvider) get(ctx context.Context, url string, target any) error {
	return t.doRequest(ctx, "GET", url, nil, target)
}

// getAll reads a whole paginated collection into target (a pointer to a slice).
//
// Taiga paginates list endpoints at 30 items per page by default and points at
// the next page with an x-pagination-next header. Reading only the first
// response — what a plain get does — silently truncates the collection, which
// is worse than an error: the agent reports a partial list as if it were
// complete.
func (t *taigaProvider) getAll(ctx context.Context, url string, target any) error {
	var all []json.RawMessage

	next := withPageSize(url, taigaPageSize)
	for page := 0; next != "" && page < taigaMaxPages; page++ {
		var chunk []json.RawMessage
		hdr, err := t.doRequestH(ctx, "GET", next, nil, &chunk)
		if err != nil {
			return err
		}
		all = append(all, chunk...)
		next = hdr.Get("x-pagination-next")
	}

	data, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

// withPageSize appends Taiga's page_size parameter unless the caller set one.
func withPageSize(rawURL string, size int) string {
	if strings.Contains(rawURL, "page_size=") {
		return rawURL
	}
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	return rawURL + sep + "page_size=" + strconv.Itoa(size)
}

func (t *taigaProvider) post(ctx context.Context, url string, body any, target any) error {
	return t.doRequest(ctx, "POST", url, body, target)
}

func (t *taigaProvider) patch(ctx context.Context, url string, body any, target any) error {
	return t.doRequest(ctx, "PATCH", url, body, target)
}

func (t *taigaProvider) resolveProjectID(ctx context.Context, nameOrSlug string) (int, error) {
	projects, err := t.ListProjects(ctx)
	if err != nil {
		return 0, err
	}
	for _, p := range projects {
		if strings.EqualFold(p.Name, nameOrSlug) || strings.EqualFold(p.Key, nameOrSlug) {
			id, _ := strconv.Atoi(p.ID)
			return id, nil
		}
	}
	return 0, fmt.Errorf("project %q not found in %s", nameOrSlug, t.name)
}

func (t *taigaProvider) fetchComments(ctx context.Context, url string) ([]Comment, error) {
	var history []struct {
		Comment     string `json:"comment"`
		CommentHTML string `json:"comment_html"`
		CreatedAt   string `json:"created_at"`
		User        struct {
			Name string `json:"name"`
		} `json:"user"`
	}
	if err := t.getAll(ctx, url, &history); err != nil {
		return nil, err
	}

	var comments []Comment
	for _, h := range history {
		if h.Comment == "" {
			continue
		}
		comments = append(comments, Comment{
			Author:    h.User.Name,
			Body:      h.Comment,
			CreatedAt: h.CreatedAt,
		})
	}
	return comments, nil
}

// ─── Types ───────────────────��─────────────────────────────���──────────────────

type taigaUserStory struct {
	ID           int `json:"id"`
	Ref          int `json:"ref"`
	ProjectExtra struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
		ID   int    `json:"id"`
	} `json:"project_extra_info"`
	StatusExtra struct {
		Name     string `json:"name"`
		Color    string `json:"color"`
		IsClosed bool   `json:"is_closed"`
	} `json:"status_extra_info"`
	AssignedToExtra *struct {
		FullName string `json:"full_name_display"`
	} `json:"assigned_to_extra_info"`
	Subject       string  `json:"subject"`
	DueDate       *string `json:"due_date"`
	Tags          [][]any `json:"tags"`
	CreatedDate   string  `json:"created_date"`
	ModifiedDate  string  `json:"modified_date"`
	MilestoneName *string `json:"milestone_name"`
}

type taigaUserStoryDetail struct {
	taigaUserStory
	Description string `json:"description"`
	Version     int    `json:"version"`
}

type taigaTask struct {
	ID           int `json:"id"`
	Ref          int `json:"ref"`
	ProjectExtra struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
		ID   int    `json:"id"`
	} `json:"project_extra_info"`
	StatusExtra struct {
		Name     string `json:"name"`
		Color    string `json:"color"`
		IsClosed bool   `json:"is_closed"`
	} `json:"status_extra_info"`
	AssignedToExtra *struct {
		FullName string `json:"full_name_display"`
	} `json:"assigned_to_extra_info"`
	Subject       string  `json:"subject"`
	DueDate       *string `json:"due_date"`
	Tags          [][]any `json:"tags"`
	CreatedDate   string  `json:"created_date"`
	ModifiedDate  string  `json:"modified_date"`
	MilestoneSlug *string `json:"milestone_slug"`
	UserStory     *int    `json:"user_story"`
}

type taigaTaskDetail struct {
	taigaTask
	Description string `json:"description"`
	Version     int    `json:"version"`
}

func (s *taigaUserStory) toTask(source string) Task {
	due := ""
	if s.DueDate != nil {
		due = *s.DueDate
	}
	statusType := "started"
	if s.StatusExtra.IsClosed {
		statusType = "completed"
	}
	assignee := ""
	if s.AssignedToExtra != nil {
		assignee = s.AssignedToExtra.FullName
	}
	return Task{
		Source:     source,
		SourceType: "taiga",
		Identifier: fmt.Sprintf("%s:us:%d", source, s.ID),
		Title:      s.Subject,
		Status:     s.StatusExtra.Name,
		StatusType: statusType,
		Priority:   "", // Taiga US don't have priority by default
		Project:    s.ProjectExtra.Name,
		Assignee:   assignee,
		DueDate:    due,
		URL:        "", // Taiga URLs are project-dependent
		CreatedAt:  s.CreatedDate,
		UpdatedAt:  s.ModifiedDate,
	}
}

func (s *taigaUserStoryDetail) toTaskDetail(source string) *TaskDetail {
	t := s.taigaUserStory.toTask(source)
	assignee := ""
	if s.AssignedToExtra != nil {
		assignee = s.AssignedToExtra.FullName
	}
	milestone := ""
	if s.MilestoneName != nil {
		milestone = *s.MilestoneName
	}
	var tags []string
	for _, tag := range s.Tags {
		if len(tag) > 0 {
			if name, ok := tag[0].(string); ok {
				tags = append(tags, name)
			}
		}
	}
	return &TaskDetail{
		Task:        t,
		Description: s.Description,
		Labels:      tags,
		Assignee:    assignee,
		Milestone:   milestone,
	}
}

func (tk *taigaTask) toTask(source string) Task {
	due := ""
	if tk.DueDate != nil {
		due = *tk.DueDate
	}
	statusType := "started"
	if tk.StatusExtra.IsClosed {
		statusType = "completed"
	}
	assignee := ""
	if tk.AssignedToExtra != nil {
		assignee = tk.AssignedToExtra.FullName
	}
	return Task{
		Source:     source,
		SourceType: "taiga",
		Identifier: fmt.Sprintf("%s:task:%d", source, tk.ID),
		Title:      tk.Subject,
		Status:     tk.StatusExtra.Name,
		StatusType: statusType,
		Priority:   "",
		Project:    tk.ProjectExtra.Name,
		Assignee:   assignee,
		DueDate:    due,
		URL:        "",
		CreatedAt:  tk.CreatedDate,
		UpdatedAt:  tk.ModifiedDate,
	}
}

func (tk *taigaTaskDetail) toTaskDetail(source string) *TaskDetail {
	t := tk.taigaTask.toTask(source)
	assignee := ""
	if tk.AssignedToExtra != nil {
		assignee = tk.AssignedToExtra.FullName
	}
	milestone := ""
	if tk.MilestoneSlug != nil {
		milestone = *tk.MilestoneSlug
	}
	var tags []string
	for _, tag := range tk.Tags {
		if len(tag) > 0 {
			if name, ok := tag[0].(string); ok {
				tags = append(tags, name)
			}
		}
	}
	return &TaskDetail{
		Task:        t,
		Description: tk.Description,
		Labels:      tags,
		Assignee:    assignee,
		Milestone:   milestone,
	}
}

func parseTaigaID(identifier string) (string, int, error) {
	// Format: "providerName:type:id" e.g. "work:us:123" or "work:task:456"
	parts := strings.SplitN(identifier, ":", 3)
	if len(parts) != 3 {
		return "", 0, fmt.Errorf("invalid Taiga identifier %q (expected format: provider:us:123 or provider:task:456)", identifier)
	}
	id, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", 0, fmt.Errorf("invalid Taiga ID %q: %w", parts[2], err)
	}
	return parts[1], id, nil
}
