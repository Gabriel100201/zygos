package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const linearAPI = "https://api.linear.app/graphql"

type linearProvider struct {
	name   string
	apiKey string
	client *http.Client

	// endpoint is the GraphQL URL. It exists so tests can point the provider at
	// a local server; production always uses linearAPI.
	endpoint string
}

func NewLinear(name, apiKey string) Provider {
	return &linearProvider{
		name:     name,
		apiKey:   apiKey,
		client:   newHTTPClient(),
		endpoint: linearAPI,
	}
}

func (l *linearProvider) Name() string { return l.name }
func (l *linearProvider) Type() string { return "linear" }

func (l *linearProvider) Ping(ctx context.Context) error {
	var resp struct {
		Data struct {
			Viewer struct {
				ID string `json:"id"`
			} `json:"viewer"`
		} `json:"data"`
	}
	if err := l.graphql(ctx, `{ viewer { id } }`, nil, &resp); err != nil {
		return err
	}
	if resp.Data.Viewer.ID == "" {
		return fmt.Errorf("viewer id empty — token may be invalid")
	}
	return nil
}

func (l *linearProvider) ListTasks(ctx context.Context, opts ListOpts) ([]Task, error) {
	filter := map[string]any{"state": buildStateFilter(opts.State)}

	if opts.Assigned {
		filter["assignee"] = map[string]any{"isMe": map[string]any{"eq": true}}
	}

	if opts.Project != "" {
		filter["or"] = []any{
			map[string]any{"project": map[string]any{"name": map[string]any{"eqIgnoreCase": opts.Project}}},
			map[string]any{"team": map[string]any{"key": map[string]any{"eqIgnoreCase": opts.Project}}},
			map[string]any{"team": map[string]any{"name": map[string]any{"eqIgnoreCase": opts.Project}}},
		}
	}

	query := `query($filter: IssueFilter, $after: String) {
		issues(filter: $filter, first: 250, after: $after) {
			pageInfo { hasNextPage endCursor }
			nodes {
				id identifier title
				priority priorityLabel
				state { name type }
				team { name key }
				project { name }
				labels { nodes { name } }
				dueDate
				createdAt updatedAt
			}
		}
	}`

	var (
		tasks []Task
		after string
		pages int
	)
	const maxPages = 10 // hard cap: 2500 issues per list call
	for {
		vars := map[string]any{"filter": filter}
		if after != "" {
			vars["after"] = after
		}

		var resp struct {
			Data struct {
				Issues struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []linearIssue `json:"nodes"`
				} `json:"issues"`
			} `json:"data"`
		}

		if err := l.graphql(ctx, query, vars, &resp); err != nil {
			return nil, err
		}

		for _, n := range resp.Data.Issues.Nodes {
			tasks = append(tasks, n.toTask(l.name))
		}

		pages++
		if !resp.Data.Issues.PageInfo.HasNextPage || pages >= maxPages {
			break
		}
		after = resp.Data.Issues.PageInfo.EndCursor
	}

	return tasks, nil
}

// buildStateFilter returns a map matching Linear's WorkflowStateFilter.type.nin.
func buildStateFilter(state string) map[string]any {
	nin := []string{"canceled", "completed"}
	switch state {
	case "active":
		nin = []string{"canceled", "completed", "backlog", "triage"}
	case "backlog":
		nin = []string{"canceled", "completed", "started"}
	}
	return map[string]any{"type": map[string]any{"nin": nin}}
}

func (l *linearProvider) GetTask(ctx context.Context, identifier string) (*TaskDetail, error) {
	query := `query($filter: IssueFilter) {
		issues(filter: $filter, first: 1) {
			nodes {
				id identifier title description
				priority priorityLabel
				state { name type }
				team { name key }
				project { name }
				labels { nodes { name } }
				dueDate estimate
				cycle { name number }
				assignee { name }
				comments { nodes { body createdAt user { name } } }
				createdAt updatedAt
			}
		}
	}`

	// Parse identifier like "ABC-42" into team key and issue number
	parts := strings.SplitN(identifier, "-", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid Linear identifier %q (expected format: TEAMKEY-NUMBER, e.g. ABC-42)", identifier)
	}

	vars := map[string]any{
		"filter": map[string]any{
			"team": map[string]any{
				"key": map[string]any{"eq": parts[0]},
			},
			"number": map[string]any{"eq": jsonNumber(parts[1])},
		},
	}

	var resp struct {
		Data struct {
			Issues struct {
				Nodes []linearIssueDetail `json:"nodes"`
			} `json:"issues"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, query, vars, &resp); err != nil {
		return nil, err
	}

	if len(resp.Data.Issues.Nodes) == 0 {
		return nil, fmt.Errorf("issue %s not found in %s", identifier, l.name)
	}

	return resp.Data.Issues.Nodes[0].toTaskDetail(l.name), nil
}

func (l *linearProvider) CreateTask(ctx context.Context, input CreateInput) (*Task, error) {
	// First, resolve team ID from project name/key
	teamID, err := l.resolveTeamID(ctx, input.Project)
	if err != nil {
		return nil, fmt.Errorf("resolving team: %w", err)
	}

	mutation := `mutation($input: IssueCreateInput!) {
		issueCreate(input: $input) {
			success
			issue {
				id identifier title
				priority priorityLabel
				state { name type }
				team { name key }
				project { name }
				labels { nodes { name } }
				dueDate createdAt updatedAt
			}
		}
	}`

	issueInput := map[string]any{
		"teamId": teamID,
		"title":  input.Title,
	}
	if input.Description != "" {
		issueInput["description"] = input.Description
	}
	if input.Priority != "" {
		issueInput["priority"] = priorityToInt(input.Priority)
	}
	if input.StateID != "" {
		issueInput["stateId"] = input.StateID
	}
	if input.Assignee != "" {
		userID, err := l.resolveUserID(ctx, input.Assignee)
		if err != nil {
			return nil, err
		}
		if userID != "" {
			issueInput["assigneeId"] = userID
		}
	}
	if len(input.Labels) > 0 {
		labelIDs, err := l.resolveLabelIDs(ctx, input.Labels)
		if err != nil {
			return nil, err
		}
		issueInput["labelIds"] = labelIDs
	}
	if input.DueDate != "" && !isClear(input.DueDate) {
		issueInput["dueDate"] = input.DueDate
	}
	if input.Estimate != nil {
		issueInput["estimate"] = *input.Estimate
	}
	if input.Cycle != "" {
		cycleID, err := l.resolveCycleID(ctx, teamID, input.Cycle)
		if err != nil {
			return nil, err
		}
		if cycleID != "" {
			issueInput["cycleId"] = cycleID
		}
	}
	if input.Parent != "" {
		parentID, err := l.resolveParentID(ctx, input.Parent)
		if err != nil {
			return nil, err
		}
		if parentID != "" {
			issueInput["parentId"] = parentID
		}
	}

	vars := map[string]any{"input": issueInput}

	var resp struct {
		Data struct {
			IssueCreate struct {
				Success bool        `json:"success"`
				Issue   linearIssue `json:"issue"`
			} `json:"issueCreate"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, mutation, vars, &resp); err != nil {
		return nil, err
	}

	if !resp.Data.IssueCreate.Success {
		return nil, fmt.Errorf("Linear issueCreate returned success=false")
	}

	t := resp.Data.IssueCreate.Issue.toTask(l.name)
	return &t, nil
}

func (l *linearProvider) UpdateTask(ctx context.Context, identifier string, input UpdateInput) (*Task, error) {
	// Resolve the issue ID plus its team context (needed to resolve state,
	// labels and cycle names, which are scoped to a team).
	ref, err := l.resolveIssueRef(ctx, identifier)
	if err != nil {
		return nil, err
	}

	mutation := `mutation($id: String!, $input: IssueUpdateInput!) {
		issueUpdate(id: $id, input: $input) {
			success
			issue {
				id identifier title
				priority priorityLabel
				state { name type }
				team { name key }
				project { name }
				labels { nodes { name } }
				dueDate createdAt updatedAt
			}
		}
	}`

	updateInput := map[string]any{}
	if input.Title != nil {
		updateInput["title"] = *input.Title
	}
	if input.Description != nil {
		updateInput["description"] = *input.Description
	}
	if input.Priority != nil {
		updateInput["priority"] = priorityToInt(*input.Priority)
	}
	// State: prefer the resolved State field; fall back to the raw StateID.
	if input.State != nil {
		stateID, err := l.resolveStateID(ctx, ref.teamKey, *input.State)
		if err != nil {
			return nil, err
		}
		updateInput["stateId"] = stateID
	} else if input.StateID != nil {
		updateInput["stateId"] = *input.StateID
	}
	if input.Assignee != nil {
		userID, err := l.resolveUserID(ctx, *input.Assignee)
		if err != nil {
			return nil, err
		}
		updateInput["assigneeId"] = nilIfEmpty(userID)
	}
	if input.Project != nil {
		projectID, err := l.resolveProjectUUID(ctx, *input.Project)
		if err != nil {
			return nil, err
		}
		updateInput["projectId"] = nilIfEmpty(projectID)
	}
	if input.Labels != nil {
		labelIDs, err := l.resolveLabelIDs(ctx, *input.Labels)
		if err != nil {
			return nil, err
		}
		updateInput["labelIds"] = labelIDs
	}
	if input.DueDate != nil {
		updateInput["dueDate"] = nilIfClear(*input.DueDate)
	}
	if input.Estimate != nil {
		updateInput["estimate"] = *input.Estimate
	}
	if input.Cycle != nil {
		cycleID, err := l.resolveCycleID(ctx, ref.teamID, *input.Cycle)
		if err != nil {
			return nil, err
		}
		updateInput["cycleId"] = nilIfEmpty(cycleID)
	}
	if input.Parent != nil {
		parentID, err := l.resolveParentID(ctx, *input.Parent)
		if err != nil {
			return nil, err
		}
		updateInput["parentId"] = nilIfEmpty(parentID)
	}

	vars := map[string]any{"id": ref.id, "input": updateInput}

	var resp struct {
		Data struct {
			IssueUpdate struct {
				Success bool        `json:"success"`
				Issue   linearIssue `json:"issue"`
			} `json:"issueUpdate"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, mutation, vars, &resp); err != nil {
		return nil, err
	}

	t := resp.Data.IssueUpdate.Issue.toTask(l.name)
	return &t, nil
}

func (l *linearProvider) SearchTasks(ctx context.Context, query string) ([]Task, error) {
	gql := `query($term: String!) {
		searchIssues(term: $term, first: 50, includeComments: true) {
			nodes {
				id identifier title
				priority priorityLabel
				state { name type }
				team { name key }
				project { name }
				labels { nodes { name } }
				dueDate createdAt updatedAt
			}
		}
	}`

	vars := map[string]any{"term": query}

	var resp struct {
		Data struct {
			SearchIssues struct {
				Nodes []linearIssue `json:"nodes"`
			} `json:"searchIssues"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, gql, vars, &resp); err != nil {
		return nil, err
	}

	var tasks []Task
	for _, n := range resp.Data.SearchIssues.Nodes {
		tasks = append(tasks, n.toTask(l.name))
	}
	return tasks, nil
}

// linearPageInfo is Linear's cursor-pagination envelope, shared by every
// paginated query in this file.
type linearPageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type linearTeam struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`
}

// listTeams reads every team, following Linear's cursor pagination.
func (l *linearProvider) listTeams(ctx context.Context) ([]linearTeam, error) {
	const query = `query($after: String) {
		teams(first: 100, after: $after) {
			pageInfo { hasNextPage endCursor }
			nodes { id name key }
		}
	}`

	var (
		teams []linearTeam
		after string
		pages int
	)
	const maxPages = 20 // hard cap: 2000 teams
	for {
		vars := map[string]any{}
		if after != "" {
			vars["after"] = after
		}

		var resp struct {
			Data struct {
				Teams struct {
					PageInfo linearPageInfo `json:"pageInfo"`
					Nodes    []linearTeam   `json:"nodes"`
				} `json:"teams"`
			} `json:"data"`
		}
		if err := l.graphql(ctx, query, vars, &resp); err != nil {
			return nil, err
		}
		teams = append(teams, resp.Data.Teams.Nodes...)

		pages++
		if !resp.Data.Teams.PageInfo.HasNextPage || pages >= maxPages {
			break
		}
		after = resp.Data.Teams.PageInfo.EndCursor
	}
	return teams, nil
}

func (l *linearProvider) ListProjects(ctx context.Context) ([]Project, error) {
	// Teams and projects are fetched as two separate top-level queries: nesting
	// teams.projects(first: 100) explodes to ~60k GraphQL complexity, far over
	// Linear's 10k ceiling. Both connections are cursor-paginated — a bare
	// first: 100 silently truncated large workspaces.
	teams, err := l.listTeams(ctx)
	if err != nil {
		return nil, err
	}

	projects := make([]Project, 0, len(teams))
	for _, t := range teams {
		projects = append(projects, Project{
			Source:     l.name,
			SourceType: "linear",
			ID:         t.ID,
			Name:       t.Name,
			Key:        t.Key,
			Kind:       "team",
		})
	}

	const query = `query($after: String) {
		projects(first: 100, after: $after) {
			pageInfo { hasNextPage endCursor }
			nodes {
				id name description
				state priority priorityLabel
				health url
				startDate targetDate progress
				lead { name displayName }
				teams(first: 1) { nodes { id name key } }
			}
		}
	}`

	var (
		after string
		pages int
	)
	const maxPages = 20 // hard cap: 2000 projects
	for {
		vars := map[string]any{}
		if after != "" {
			vars["after"] = after
		}

		var resp struct {
			Data struct {
				Projects struct {
					PageInfo linearPageInfo  `json:"pageInfo"`
					Nodes    []linearProject `json:"nodes"`
				} `json:"projects"`
			} `json:"data"`
		}
		if err := l.graphql(ctx, query, vars, &resp); err != nil {
			return nil, err
		}

		for _, p := range resp.Data.Projects.Nodes {
			parentTeam := ""
			if len(p.Teams.Nodes) > 0 {
				parentTeam = p.Teams.Nodes[0].Name
			}
			projects = append(projects, p.toProject(l.name, parentTeam))
		}

		pages++
		if !resp.Data.Projects.PageInfo.HasNextPage || pages >= maxPages {
			break
		}
		after = resp.Data.Projects.PageInfo.EndCursor
	}

	return projects, nil
}

func (l *linearProvider) ListStates(ctx context.Context, projectKey string) ([]State, error) {
	// Linear defaults connections to 50 nodes when `first` is omitted, so the
	// unpaginated form of this query hid states on any workspace with more than
	// a couple of teams.
	const query = `query($after: String) {
		workflowStates(first: 250, after: $after) {
			pageInfo { hasNextPage endCursor }
			nodes { id name type team { name key } }
		}
	}`

	var (
		states []State
		after  string
		pages  int
	)
	const maxPages = 20 // hard cap: 5000 states
	for {
		vars := map[string]any{}
		if after != "" {
			vars["after"] = after
		}

		var resp struct {
			Data struct {
				WorkflowStates struct {
					PageInfo linearPageInfo `json:"pageInfo"`
					Nodes    []struct {
						ID   string `json:"id"`
						Name string `json:"name"`
						Type string `json:"type"`
						Team struct {
							Name string `json:"name"`
							Key  string `json:"key"`
						} `json:"team"`
					} `json:"nodes"`
				} `json:"workflowStates"`
			} `json:"data"`
		}
		if err := l.graphql(ctx, query, vars, &resp); err != nil {
			return nil, err
		}

		for _, s := range resp.Data.WorkflowStates.Nodes {
			if projectKey != "" && !strings.EqualFold(s.Team.Key, projectKey) && !strings.EqualFold(s.Team.Name, projectKey) {
				continue
			}
			states = append(states, State{
				ID:      s.ID,
				Name:    s.Name,
				Type:    s.Type,
				Project: s.Team.Name,
			})
		}

		pages++
		if !resp.Data.WorkflowStates.PageInfo.HasNextPage || pages >= maxPages {
			break
		}
		after = resp.Data.WorkflowStates.PageInfo.EndCursor
	}

	return states, nil
}

// ─── Helpers ──────────────────��─────────────────────────────��────────────────

func (l *linearProvider) graphql(ctx context.Context, query string, variables map[string]any, target any) error {
	body := map[string]any{"query": query}
	if variables != nil {
		body["variables"] = variables
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", l.endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", l.apiKey)

	resp, err := l.client.Do(req)
	if err != nil {
		return fmt.Errorf("Linear API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("Linear API returned %d: %s", resp.StatusCode, string(respBody))
	}

	// Check for GraphQL errors
	var errCheck struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &errCheck); err == nil && len(errCheck.Errors) > 0 {
		return fmt.Errorf("Linear GraphQL error: %s", errCheck.Errors[0].Message)
	}

	return json.Unmarshal(respBody, target)
}

// resolveTeamID accepts a team name, team key, or project name.
// If a project name is given, returns the parent team's ID.
func (l *linearProvider) resolveTeamID(ctx context.Context, nameOrKey string) (string, error) {
	projects, err := l.ListProjects(ctx)
	if err != nil {
		return "", err
	}
	// Prefer exact team match (name or key) first.
	for _, p := range projects {
		if p.Kind != "team" {
			continue
		}
		if strings.EqualFold(p.Name, nameOrKey) || strings.EqualFold(p.Key, nameOrKey) {
			return p.ID, nil
		}
	}
	// Fall back to project name match → return parent team's ID.
	for _, p := range projects {
		if p.Kind != "project" {
			continue
		}
		if strings.EqualFold(p.Name, nameOrKey) {
			for _, t := range projects {
				if t.Kind == "team" && strings.EqualFold(t.Name, p.ParentTeam) {
					return t.ID, nil
				}
			}
		}
	}
	return "", fmt.Errorf("team or project %q not found in %s", nameOrKey, l.name)
}

func (l *linearProvider) resolveIssueID(ctx context.Context, identifier string) (string, error) {
	parts := strings.SplitN(identifier, "-", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid identifier %q", identifier)
	}

	query := `query($filter: IssueFilter) {
		issues(filter: $filter, first: 1) { nodes { id } }
	}`
	vars := map[string]any{
		"filter": map[string]any{
			"team":   map[string]any{"key": map[string]any{"eq": parts[0]}},
			"number": map[string]any{"eq": jsonNumber(parts[1])},
		},
	}

	var resp struct {
		Data struct {
			Issues struct {
				Nodes []struct {
					ID string `json:"id"`
				} `json:"nodes"`
			} `json:"issues"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, query, vars, &resp); err != nil {
		return "", err
	}
	if len(resp.Data.Issues.Nodes) == 0 {
		return "", fmt.Errorf("issue %s not found", identifier)
	}
	return resp.Data.Issues.Nodes[0].ID, nil
}

// ─── Types ────────────────────────────────────────────────────────���──────────

// issueRef holds an issue's UUID plus its owning team, needed to resolve
// team-scoped names (states, labels, cycles) during an update.
type issueRef struct {
	id      string
	teamID  string
	teamKey string
}

// resolveIssueRef looks up an issue by "TEAMKEY-NUMBER" and returns its UUID and team.
func (l *linearProvider) resolveIssueRef(ctx context.Context, identifier string) (*issueRef, error) {
	parts := strings.SplitN(identifier, "-", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid identifier %q", identifier)
	}

	query := `query($filter: IssueFilter) {
		issues(filter: $filter, first: 1) { nodes { id team { id key } } }
	}`
	vars := map[string]any{
		"filter": map[string]any{
			"team":   map[string]any{"key": map[string]any{"eq": parts[0]}},
			"number": map[string]any{"eq": jsonNumber(parts[1])},
		},
	}

	var resp struct {
		Data struct {
			Issues struct {
				Nodes []struct {
					ID   string `json:"id"`
					Team struct {
						ID  string `json:"id"`
						Key string `json:"key"`
					} `json:"team"`
				} `json:"nodes"`
			} `json:"issues"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, query, vars, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data.Issues.Nodes) == 0 {
		return nil, fmt.Errorf("issue %s not found", identifier)
	}
	n := resp.Data.Issues.Nodes[0]
	return &issueRef{id: n.ID, teamID: n.Team.ID, teamKey: n.Team.Key}, nil
}

// resolveStateID maps a workflow-state name (or UUID) to its ID within a team.
func (l *linearProvider) resolveStateID(ctx context.Context, teamKey, val string) (string, error) {
	if val == "" {
		return "", fmt.Errorf("state is empty")
	}
	if looksLikeUUID(val) {
		return val, nil
	}
	states, err := l.ListStates(ctx, teamKey)
	if err != nil {
		return "", err
	}
	for _, s := range states {
		if strings.EqualFold(s.Name, val) {
			return s.ID, nil
		}
	}
	return "", fmt.Errorf("state %q not found in team %q (use tasks_states to list valid names)", val, teamKey)
}

// resolveUserID maps a user name/email (or UUID, "me") to a user ID.
// Returns "" for "none"/"" (caller sets the field to null to unassign).
func (l *linearProvider) resolveUserID(ctx context.Context, val string) (string, error) {
	if isClear(val) {
		return "", nil
	}
	if strings.EqualFold(val, "me") {
		var resp struct {
			Data struct {
				Viewer struct {
					ID string `json:"id"`
				} `json:"viewer"`
			} `json:"data"`
		}
		if err := l.graphql(ctx, `{ viewer { id } }`, nil, &resp); err != nil {
			return "", err
		}
		return resp.Data.Viewer.ID, nil
	}
	if looksLikeUUID(val) {
		return val, nil
	}

	members, err := l.ListMembers(ctx, "")
	if err != nil {
		return "", err
	}
	for _, m := range members {
		if strings.EqualFold(m.Name, val) || strings.EqualFold(m.DisplayName, val) || strings.EqualFold(m.Email, val) {
			return m.ID, nil
		}
	}
	return "", fmt.Errorf("user %q not found (use tasks_members to list assignable users)", val)
}

// resolveProjectUUID maps a project name (or UUID) to its Linear project ID.
// Returns "" for "none"/"" (caller sets the field to null to remove it).
func (l *linearProvider) resolveProjectUUID(ctx context.Context, val string) (string, error) {
	if isClear(val) {
		return "", nil
	}
	if looksLikeUUID(val) {
		return val, nil
	}
	projects, err := l.ListProjects(ctx)
	if err != nil {
		return "", err
	}
	for _, p := range projects {
		if p.Kind == "project" && strings.EqualFold(p.Name, val) {
			return p.ID, nil
		}
	}
	return "", fmt.Errorf("project %q not found in %s (use tasks_projects to list projects)", val, l.name)
}

// resolveLabelIDs maps label names (or UUIDs) to label IDs. An empty slice
// clears all labels. Unknown names are an error.
func (l *linearProvider) resolveLabelIDs(ctx context.Context, names []string) ([]string, error) {
	out := []string{}
	if len(names) == 0 {
		return out, nil
	}

	query := `{ issueLabels(first: 250) { nodes { id name } } }`
	var resp struct {
		Data struct {
			IssueLabels struct {
				Nodes []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"nodes"`
			} `json:"issueLabels"`
		} `json:"data"`
	}
	if err := l.graphql(ctx, query, nil, &resp); err != nil {
		return nil, err
	}

	for _, name := range names {
		if looksLikeUUID(name) {
			out = append(out, name)
			continue
		}
		found := ""
		for _, n := range resp.Data.IssueLabels.Nodes {
			if strings.EqualFold(n.Name, name) {
				found = n.ID
				break
			}
		}
		if found == "" {
			return nil, fmt.Errorf("label %q not found", name)
		}
		out = append(out, found)
	}
	return out, nil
}

// resolveCycleID maps a cycle name/number (or UUID, "active") to a cycle ID
// within a team. Returns "" for "none"/"" (caller sets the field to null).
func (l *linearProvider) resolveCycleID(ctx context.Context, teamID, val string) (string, error) {
	if isClear(val) {
		return "", nil
	}
	if looksLikeUUID(val) {
		return val, nil
	}
	if strings.EqualFold(val, "active") {
		query := `query($id: String!) { team(id: $id) { activeCycle { id } } }`
		var resp struct {
			Data struct {
				Team struct {
					ActiveCycle *struct {
						ID string `json:"id"`
					} `json:"activeCycle"`
				} `json:"team"`
			} `json:"data"`
		}
		if err := l.graphql(ctx, query, map[string]any{"id": teamID}, &resp); err != nil {
			return "", err
		}
		if resp.Data.Team.ActiveCycle == nil {
			return "", fmt.Errorf("team has no active cycle")
		}
		return resp.Data.Team.ActiveCycle.ID, nil
	}

	query := `query($teamId: ID!) {
		cycles(filter: { team: { id: { eq: $teamId } } }, first: 100) {
			nodes { id name number }
		}
	}`
	var resp struct {
		Data struct {
			Cycles struct {
				Nodes []struct {
					ID     string `json:"id"`
					Name   string `json:"name"`
					Number int    `json:"number"`
				} `json:"nodes"`
			} `json:"cycles"`
		} `json:"data"`
	}
	if err := l.graphql(ctx, query, map[string]any{"teamId": teamID}, &resp); err != nil {
		return "", err
	}
	for _, c := range resp.Data.Cycles.Nodes {
		if strings.EqualFold(c.Name, val) || fmt.Sprintf("%d", c.Number) == strings.TrimSpace(val) {
			return c.ID, nil
		}
	}
	return "", fmt.Errorf("cycle %q not found in team", val)
}

// resolveParentID maps a parent identifier (e.g. "ABC-42") to a UUID.
// Returns "" for "none"/"" (caller detaches the parent).
func (l *linearProvider) resolveParentID(ctx context.Context, val string) (string, error) {
	if isClear(val) {
		return "", nil
	}
	if looksLikeUUID(val) {
		return val, nil
	}
	return l.resolveIssueID(ctx, val)
}

func (l *linearProvider) AddComment(ctx context.Context, identifier, body string) (*Comment, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("comment body is empty")
	}
	issueID, err := l.resolveIssueID(ctx, identifier)
	if err != nil {
		return nil, err
	}

	mutation := `mutation($input: CommentCreateInput!) {
		commentCreate(input: $input) {
			success
			comment { id createdAt user { name } }
		}
	}`
	vars := map[string]any{"input": map[string]any{"issueId": issueID, "body": body}}

	var resp struct {
		Data struct {
			CommentCreate struct {
				Success bool `json:"success"`
				Comment struct {
					ID        string `json:"id"`
					CreatedAt string `json:"createdAt"`
					User      struct {
						Name string `json:"name"`
					} `json:"user"`
				} `json:"comment"`
			} `json:"commentCreate"`
		} `json:"data"`
	}
	if err := l.graphql(ctx, mutation, vars, &resp); err != nil {
		return nil, err
	}
	if !resp.Data.CommentCreate.Success {
		return nil, fmt.Errorf("Linear commentCreate returned success=false")
	}
	c := resp.Data.CommentCreate.Comment
	return &Comment{Author: c.User.Name, Body: body, CreatedAt: c.CreatedAt}, nil
}

func (l *linearProvider) ListMembers(ctx context.Context, projectKey string) ([]Member, error) {
	// projectKey is accepted for interface symmetry; workspace-wide listing is
	// used in this version (team-scoped filtering is a future improvement).
	query := `{ users(first: 250) { nodes { id name displayName email active } } }`
	var resp struct {
		Data struct {
			Users struct {
				Nodes []struct {
					ID          string `json:"id"`
					Name        string `json:"name"`
					DisplayName string `json:"displayName"`
					Email       string `json:"email"`
					Active      bool   `json:"active"`
				} `json:"nodes"`
			} `json:"users"`
		} `json:"data"`
	}
	if err := l.graphql(ctx, query, nil, &resp); err != nil {
		return nil, err
	}

	var members []Member
	for _, u := range resp.Data.Users.Nodes {
		if !u.Active {
			continue
		}
		members = append(members, Member{
			ID:          u.ID,
			Name:        u.Name,
			DisplayName: u.DisplayName,
			Email:       u.Email,
		})
	}
	return members, nil
}

type linearIssue struct {
	ID            string `json:"id"`
	Identifier    string `json:"identifier"`
	Title         string `json:"title"`
	Priority      int    `json:"priority"`
	PriorityLabel string `json:"priorityLabel"`
	State         struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"state"`
	Team struct {
		Name string `json:"name"`
		Key  string `json:"key"`
	} `json:"team"`
	ProjectField *struct {
		Name string `json:"name"`
	} `json:"project"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	DueDate   *string `json:"dueDate"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
}

type linearIssueDetail struct {
	linearIssue
	Description string   `json:"description"`
	Estimate    *float64 `json:"estimate"`
	Cycle       *struct {
		Name   string `json:"name"`
		Number int    `json:"number"`
	} `json:"cycle"`
	Assignee *struct {
		Name string `json:"name"`
	} `json:"assignee"`
	Comments struct {
		Nodes []struct {
			Body      string `json:"body"`
			CreatedAt string `json:"createdAt"`
			User      struct {
				Name string `json:"name"`
			} `json:"user"`
		} `json:"nodes"`
	} `json:"comments"`
}

func (n *linearIssue) projectName() string {
	if n.ProjectField != nil {
		return n.ProjectField.Name
	}
	return n.Team.Name
}

func (n *linearIssue) toTask(source string) Task {
	due := ""
	if n.DueDate != nil {
		due = *n.DueDate
	}
	project := n.projectName()
	url := fmt.Sprintf("https://linear.app/issue/%s", n.Identifier)
	return Task{
		Source:     source,
		SourceType: "linear",
		Identifier: n.Identifier,
		Title:      n.Title,
		Status:     n.State.Name,
		StatusType: n.State.Type,
		Priority:   n.PriorityLabel,
		Project:    project,
		DueDate:    due,
		URL:        url,
		CreatedAt:  n.CreatedAt,
		UpdatedAt:  n.UpdatedAt,
	}
}

func (n *linearIssueDetail) toTaskDetail(source string) *TaskDetail {
	t := n.linearIssue.toTask(source)

	var comments []Comment
	for _, c := range n.Comments.Nodes {
		comments = append(comments, Comment{
			Author:    c.User.Name,
			Body:      c.Body,
			CreatedAt: c.CreatedAt,
		})
	}

	var labels []string
	for _, l := range n.Labels.Nodes {
		labels = append(labels, l.Name)
	}

	assignee := ""
	if n.Assignee != nil {
		assignee = n.Assignee.Name
	}

	milestone := ""
	if n.Cycle != nil {
		milestone = fmt.Sprintf("%s (#%d)", n.Cycle.Name, n.Cycle.Number)
	}

	return &TaskDetail{
		Task:        t,
		Description: n.Description,
		Comments:    comments,
		Labels:      labels,
		Assignee:    assignee,
		Estimate:    n.Estimate,
		Milestone:   milestone,
	}
}

type linearProject struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	State         string   `json:"state"`
	Priority      int      `json:"priority"`
	PriorityLabel string   `json:"priorityLabel"`
	Health        string   `json:"health"`
	URL           string   `json:"url"`
	StartDate     *string  `json:"startDate"`
	TargetDate    *string  `json:"targetDate"`
	Progress      *float64 `json:"progress"`
	Lead          *struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
	} `json:"lead"`
	Teams struct {
		Nodes []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Key  string `json:"key"`
		} `json:"nodes"`
	} `json:"teams"`
}

func (p *linearProject) toProject(source, parentTeam string) Project {
	start := ""
	if p.StartDate != nil {
		start = *p.StartDate
	}
	target := ""
	if p.TargetDate != nil {
		target = *p.TargetDate
	}
	progress := 0.0
	if p.Progress != nil {
		progress = *p.Progress
	}
	lead := ""
	if p.Lead != nil {
		lead = p.Lead.DisplayName
		if lead == "" {
			lead = p.Lead.Name
		}
	}
	return Project{
		Source:      source,
		SourceType:  "linear",
		ID:          p.ID,
		Name:        p.Name,
		Key:         "",
		Kind:        "project",
		ParentTeam:  parentTeam,
		Description: p.Description,
		Status:      p.State,
		Health:      p.Health,
		Priority:    p.PriorityLabel,
		Lead:        lead,
		StartDate:   start,
		TargetDate:  target,
		Progress:    progress,
		URL:         p.URL,
	}
}

func priorityToInt(p string) int {
	switch strings.ToLower(p) {
	case "urgent":
		return 1
	case "high":
		return 2
	case "medium":
		return 3
	case "low":
		return 4
	default:
		return 0
	}
}

func jsonNumber(s string) json.Number {
	return json.Number(s)
}

// looksLikeUUID reports whether s is a canonical 36-char UUID (8-4-4-4-12).
// Used to decide whether to pass a value straight through or resolve it by name.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}

// isClear reports whether a value means "clear/unassign this field".
func isClear(s string) bool {
	return s == "" || strings.EqualFold(s, "none") || strings.EqualFold(s, "null")
}

// nilIfEmpty returns nil (JSON null) for "", otherwise the string. Used so an
// empty resolved ID unsets the field in a GraphQL mutation input.
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nilIfClear returns nil (JSON null) when s means "clear", otherwise the string.
func nilIfClear(s string) any {
	if isClear(s) {
		return nil
	}
	return s
}

// ─── Documents ────────────────────────────────────────────────────────────────

type linearDocument struct {
	ID      string `json:"id"`
	SlugID  string `json:"slugId"`
	Title   string `json:"title"`
	Icon    string `json:"icon"`
	Color   string `json:"color"`
	URL     string `json:"url"`
	Project *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	Creator *struct {
		Name string `json:"name"`
	} `json:"creator"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type linearDocumentDetail struct {
	linearDocument
	Content string `json:"content"`
}

func (d *linearDocument) toDocument(source string) Document {
	projectName := ""
	if d.Project != nil {
		projectName = d.Project.Name
	}
	creator := ""
	if d.Creator != nil {
		creator = d.Creator.Name
	}
	return Document{
		Source:     source,
		SourceType: "linear",
		ID:         d.ID,
		SlugID:     d.SlugID,
		Title:      d.Title,
		Icon:       d.Icon,
		Color:      d.Color,
		Project:    projectName,
		Creator:    creator,
		URL:        d.URL,
		CreatedAt:  d.CreatedAt,
		UpdatedAt:  d.UpdatedAt,
	}
}

func (d *linearDocumentDetail) toDocumentDetail(source string) *DocumentDetail {
	base := d.linearDocument.toDocument(source)
	return &DocumentDetail{Document: base, Content: d.Content}
}

const documentFields = `id slugId title icon color url
	project { id name }
	creator { name }
	createdAt updatedAt`

// resolveDocProjectIDs accepts a team name/key, or a project name, and returns matching
// Linear Project IDs. Team match expands to all projects in that team. Empty input → nil.
func (l *linearProvider) resolveDocProjectIDs(ctx context.Context, nameOrKey string) ([]string, error) {
	if nameOrKey == "" {
		return nil, nil
	}
	projects, err := l.ListProjects(ctx)
	if err != nil {
		return nil, err
	}

	// Try project match first.
	for _, p := range projects {
		if p.Kind == "project" && strings.EqualFold(p.Name, nameOrKey) {
			return []string{p.ID}, nil
		}
	}
	// Fall back to team match → all projects in that team.
	var out []string
	for _, t := range projects {
		if t.Kind != "team" {
			continue
		}
		if !strings.EqualFold(t.Name, nameOrKey) && !strings.EqualFold(t.Key, nameOrKey) {
			continue
		}
		for _, p := range projects {
			if p.Kind == "project" && strings.EqualFold(p.ParentTeam, t.Name) {
				out = append(out, p.ID)
			}
		}
		break
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no team or project matches %q in %s", nameOrKey, l.name)
	}
	return out, nil
}

func (l *linearProvider) ListDocuments(ctx context.Context, opts DocumentListOpts) ([]Document, error) {
	filter := map[string]any{}
	if opts.Project != "" {
		ids, err := l.resolveDocProjectIDs(ctx, opts.Project)
		if err != nil {
			return nil, err
		}
		filter["project"] = map[string]any{"id": map[string]any{"in": ids}}
	}

	query := fmt.Sprintf(`query($filter: DocumentFilter, $after: String) {
		documents(filter: $filter, first: 100, after: $after) {
			pageInfo { hasNextPage endCursor }
			nodes { %s }
		}
	}`, documentFields)

	var (
		docs  []Document
		after string
		pages int
	)
	const maxPages = 10
	for {
		vars := map[string]any{}
		if len(filter) > 0 {
			vars["filter"] = filter
		}
		if after != "" {
			vars["after"] = after
		}

		var resp struct {
			Data struct {
				Documents struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []linearDocument `json:"nodes"`
				} `json:"documents"`
			} `json:"data"`
		}

		if err := l.graphql(ctx, query, vars, &resp); err != nil {
			return nil, err
		}

		for _, n := range resp.Data.Documents.Nodes {
			if opts.Query != "" && !strings.Contains(strings.ToLower(n.Title), strings.ToLower(opts.Query)) {
				continue
			}
			docs = append(docs, n.toDocument(l.name))
		}

		pages++
		if !resp.Data.Documents.PageInfo.HasNextPage || pages >= maxPages {
			break
		}
		after = resp.Data.Documents.PageInfo.EndCursor
	}

	return docs, nil
}

func (l *linearProvider) GetDocument(ctx context.Context, identifier string) (*DocumentDetail, error) {
	// Try native lookup first (works for both UUID and slugId).
	query := fmt.Sprintf(`query($id: String!) {
		document(id: $id) { %s content }
	}`, documentFields)

	var resp struct {
		Data struct {
			Document *linearDocumentDetail `json:"document"`
		} `json:"data"`
	}

	err := l.graphql(ctx, query, map[string]any{"id": identifier}, &resp)
	if err == nil && resp.Data.Document != nil {
		return resp.Data.Document.toDocumentDetail(l.name), nil
	}

	// Fallback: filter by slugId via documents list.
	fallbackQuery := fmt.Sprintf(`query($filter: DocumentFilter) {
		documents(filter: $filter, first: 1) { nodes { %s content } }
	}`, documentFields)
	vars := map[string]any{
		"filter": map[string]any{"slugId": map[string]any{"eq": identifier}},
	}

	var resp2 struct {
		Data struct {
			Documents struct {
				Nodes []linearDocumentDetail `json:"nodes"`
			} `json:"documents"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, fallbackQuery, vars, &resp2); err != nil {
		return nil, err
	}
	if len(resp2.Data.Documents.Nodes) == 0 {
		return nil, fmt.Errorf("document %q not found in %s", identifier, l.name)
	}
	return resp2.Data.Documents.Nodes[0].toDocumentDetail(l.name), nil
}

func (l *linearProvider) CreateDocument(ctx context.Context, input DocumentCreateInput) (*Document, error) {
	if input.Project == "" {
		return nil, fmt.Errorf("project is required for Linear documents")
	}
	if input.Title == "" {
		return nil, fmt.Errorf("title is required")
	}

	projectIDs, err := l.resolveDocProjectIDs(ctx, input.Project)
	if err != nil {
		return nil, err
	}
	if len(projectIDs) != 1 {
		return nil, fmt.Errorf("project %q must match exactly one Linear project (got %d matches); pass a project name, not a team", input.Project, len(projectIDs))
	}

	mutation := fmt.Sprintf(`mutation($input: DocumentCreateInput!) {
		documentCreate(input: $input) {
			success
			document { %s }
		}
	}`, documentFields)

	docInput := map[string]any{
		"title":     input.Title,
		"projectId": projectIDs[0],
	}
	if input.Content != "" {
		docInput["content"] = input.Content
	}
	if input.Icon != "" {
		docInput["icon"] = input.Icon
	}
	if input.Color != "" {
		docInput["color"] = input.Color
	}

	var resp struct {
		Data struct {
			DocumentCreate struct {
				Success  bool           `json:"success"`
				Document linearDocument `json:"document"`
			} `json:"documentCreate"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, mutation, map[string]any{"input": docInput}, &resp); err != nil {
		return nil, err
	}
	if !resp.Data.DocumentCreate.Success {
		return nil, fmt.Errorf("Linear documentCreate returned success=false")
	}
	d := resp.Data.DocumentCreate.Document.toDocument(l.name)
	return &d, nil
}

func (l *linearProvider) UpdateDocument(ctx context.Context, identifier string, input DocumentUpdateInput) (*Document, error) {
	docID, err := l.resolveDocID(ctx, identifier)
	if err != nil {
		return nil, err
	}

	mutation := fmt.Sprintf(`mutation($id: String!, $input: DocumentUpdateInput!) {
		documentUpdate(id: $id, input: $input) {
			success
			document { %s }
		}
	}`, documentFields)

	updateInput := map[string]any{}
	if input.Title != nil {
		updateInput["title"] = *input.Title
	}
	if input.Content != nil {
		updateInput["content"] = *input.Content
	}
	if input.Icon != nil {
		updateInput["icon"] = *input.Icon
	}
	if input.Color != nil {
		updateInput["color"] = *input.Color
	}

	var resp struct {
		Data struct {
			DocumentUpdate struct {
				Success  bool           `json:"success"`
				Document linearDocument `json:"document"`
			} `json:"documentUpdate"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, mutation, map[string]any{"id": docID, "input": updateInput}, &resp); err != nil {
		return nil, err
	}
	if !resp.Data.DocumentUpdate.Success {
		return nil, fmt.Errorf("Linear documentUpdate returned success=false")
	}
	d := resp.Data.DocumentUpdate.Document.toDocument(l.name)
	return &d, nil
}

func (l *linearProvider) DeleteDocument(ctx context.Context, identifier string) error {
	docID, err := l.resolveDocID(ctx, identifier)
	if err != nil {
		return err
	}

	mutation := `mutation($id: String!) {
		documentDelete(id: $id) { success }
	}`

	var resp struct {
		Data struct {
			DocumentDelete struct {
				Success bool `json:"success"`
			} `json:"documentDelete"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, mutation, map[string]any{"id": docID}, &resp); err != nil {
		return err
	}
	if !resp.Data.DocumentDelete.Success {
		return fmt.Errorf("Linear documentDelete returned success=false")
	}
	return nil
}

func (l *linearProvider) SearchDocuments(ctx context.Context, query string) ([]Document, error) {
	gql := fmt.Sprintf(`query($term: String!) {
		searchDocuments(term: $term, first: 50) { nodes { %s } }
	}`, documentFields)

	var resp struct {
		Data struct {
			SearchDocuments struct {
				Nodes []linearDocument `json:"nodes"`
			} `json:"searchDocuments"`
		} `json:"data"`
	}

	if err := l.graphql(ctx, gql, map[string]any{"term": query}, &resp); err != nil {
		return nil, err
	}

	var docs []Document
	for _, n := range resp.Data.SearchDocuments.Nodes {
		docs = append(docs, n.toDocument(l.name))
	}
	return docs, nil
}

// resolveDocID accepts a UUID or slugId and returns the canonical UUID for mutations.
func (l *linearProvider) resolveDocID(ctx context.Context, identifier string) (string, error) {
	d, err := l.GetDocument(ctx, identifier)
	if err != nil {
		return "", err
	}
	return d.ID, nil
}
