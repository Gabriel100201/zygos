package mcp

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Gabriel100201/zygos/internal/provider"
	"github.com/mark3labs/mcp-go/mcp"
)

func strParam(req mcp.CallToolRequest, key string) string {
	return req.GetString(key, "")
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// optStr returns a pointer to the argument's value when the key is present in
// the request, or nil when it is absent. Unlike strPtr it preserves an explicit
// empty/"none" value, so callers can distinguish "leave unchanged" (absent) from
// "clear this field" (present but "none"/"").
func optStr(req mcp.CallToolRequest, key string) *string {
	args := req.GetArguments()
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	s := fmt.Sprintf("%v", v)
	return &s
}

// splitLabels turns a comma-separated label string into a trimmed slice.
func splitLabels(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func handleTasksList(reg *provider.Registry) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		opts := provider.ListOpts{
			Provider: strParam(req, "provider"),
			Project:  strParam(req, "project"),
			State:    strParam(req, "state"),
			Assigned: req.GetBool("assigned", false),
		}

		tasks, warnings, err := reg.AllTasks(ctx, opts)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error: %v\nWarnings: %s", err, strings.Join(warnings, "; "))), nil
		}

		var sb strings.Builder

		if len(warnings) > 0 {
			sb.WriteString("**Warnings:**\n")
			for _, w := range warnings {
				sb.WriteString(fmt.Sprintf("- %s\n", w))
			}
			sb.WriteString("\n---\n\n")
		}

		if len(tasks) == 0 {
			sb.WriteString("No open tasks found.")
			return mcp.NewToolResultText(sb.String()), nil
		}

		// Group by source
		grouped := make(map[string][]provider.Task)
		var order []string
		for _, t := range tasks {
			if _, seen := grouped[t.Source]; !seen {
				order = append(order, t.Source)
			}
			grouped[t.Source] = append(grouped[t.Source], t)
		}

		for _, source := range order {
			sourceTasks := grouped[source]
			sb.WriteString(fmt.Sprintf("## %s (%s) — %d tasks\n\n", strings.ToUpper(source), sourceTasks[0].SourceType, len(sourceTasks)))
			sb.WriteString("| ID | Title | Status | Priority | Project | Due |\n")
			sb.WriteString("|---|---|---|---|---|---|\n")
			for _, t := range sourceTasks {
				due := t.DueDate
				if due == "" {
					due = "-"
				}
				priority := t.Priority
				if priority == "" {
					priority = "-"
				}
				title := t.Title
				if len(title) > 60 {
					title = title[:57] + "..."
				}
				sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n",
					t.Identifier, title, t.Status, priority, t.Project, due))
			}
			sb.WriteString("\n")
		}

		sb.WriteString(fmt.Sprintf("**Total: %d tasks**", len(tasks)))
		return mcp.NewToolResultText(sb.String()), nil
	}
}

func handleTasksGet(reg *provider.Registry) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		identifier := strParam(req, "identifier")
		if identifier == "" {
			return mcp.NewToolResultError("identifier is required"), nil
		}

		detail, err := reg.GetTask(ctx, identifier)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("# %s — %s\n\n", detail.Identifier, detail.Title))
		sb.WriteString(fmt.Sprintf("**Source:** %s (%s)\n", detail.Source, detail.SourceType))
		sb.WriteString(fmt.Sprintf("**Project:** %s\n", detail.Project))
		sb.WriteString(fmt.Sprintf("**Status:** %s\n", detail.Status))

		if detail.Priority != "" {
			sb.WriteString(fmt.Sprintf("**Priority:** %s\n", detail.Priority))
		}
		if detail.Assignee != "" {
			sb.WriteString(fmt.Sprintf("**Assignee:** %s\n", detail.Assignee))
		}
		if detail.DueDate != "" {
			sb.WriteString(fmt.Sprintf("**Due Date:** %s\n", detail.DueDate))
		}
		if detail.Milestone != "" {
			sb.WriteString(fmt.Sprintf("**Milestone:** %s\n", detail.Milestone))
		}
		if detail.Estimate != nil {
			sb.WriteString(fmt.Sprintf("**Estimate:** %.0f\n", *detail.Estimate))
		}
		if len(detail.Labels) > 0 {
			sb.WriteString(fmt.Sprintf("**Labels:** %s\n", strings.Join(detail.Labels, ", ")))
		}
		if detail.URL != "" {
			sb.WriteString(fmt.Sprintf("**URL:** %s\n", detail.URL))
		}
		sb.WriteString(fmt.Sprintf("**Created:** %s\n", detail.CreatedAt))
		sb.WriteString(fmt.Sprintf("**Updated:** %s\n", detail.UpdatedAt))

		if detail.Description != "" {
			sb.WriteString(fmt.Sprintf("\n## Description\n\n%s\n", detail.Description))
		}

		if len(detail.Subtasks) > 0 {
			sb.WriteString(fmt.Sprintf("\n## Subtasks (%d)\n\n", len(detail.Subtasks)))
			sb.WriteString("| ID | Title | Status | Assignee |\n")
			sb.WriteString("|---|---|---|---|\n")
			for _, st := range detail.Subtasks {
				assignee := st.Assignee
				if assignee == "" {
					assignee = "unassigned"
				}
				sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", st.Identifier, st.Title, st.Status, assignee))
			}
		}

		if len(detail.Comments) > 0 {
			sb.WriteString(fmt.Sprintf("\n## Comments (%d)\n\n", len(detail.Comments)))
			for _, c := range detail.Comments {
				sb.WriteString(fmt.Sprintf("**%s** (%s):\n%s\n\n", c.Author, c.CreatedAt, c.Body))
			}
		}

		return mcp.NewToolResultText(sb.String()), nil
	}
}

func handleTasksCreate(reg *provider.Registry) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		input := provider.CreateInput{
			Provider:    strParam(req, "provider"),
			Project:     strParam(req, "project"),
			Title:       strParam(req, "title"),
			Description: strParam(req, "description"),
			Priority:    strParam(req, "priority"),
			StateID:     strParam(req, "state_id"),
			Type:        strParam(req, "type"),
			Assignee:    strParam(req, "assignee"),
			DueDate:     strParam(req, "due_date"),
			Cycle:       strParam(req, "cycle"),
			Parent:      strParam(req, "parent"),
		}
		if labels := strParam(req, "labels"); labels != "" {
			input.Labels = splitLabels(labels)
		}
		if est := strParam(req, "estimate"); est != "" {
			v, err := strconv.ParseFloat(strings.TrimSpace(est), 64)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid estimate %q: must be a number", est)), nil
			}
			input.Estimate = &v
		}

		if input.Provider == "" || input.Project == "" || input.Title == "" {
			return mcp.NewToolResultError("provider, project, and title are required"), nil
		}

		task, err := reg.CreateTask(ctx, input)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error creating task: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Task created: **%s** — %s\nStatus: %s | Project: %s",
			task.Identifier, task.Title, task.Status, task.Project)), nil
	}
}

func handleTasksUpdate(reg *provider.Registry) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		identifier := strParam(req, "identifier")
		if identifier == "" {
			return mcp.NewToolResultError("identifier is required"), nil
		}

		input := provider.UpdateInput{
			Title:       strPtr(strParam(req, "title")),
			Description: strPtr(strParam(req, "description")),
			StateID:     strPtr(strParam(req, "state_id")),
			Priority:    strPtr(strParam(req, "priority")),
			State:       optStr(req, "state"),
			Assignee:    optStr(req, "assignee"),
			Project:     optStr(req, "project"),
			DueDate:     optStr(req, "due_date"),
			Cycle:       optStr(req, "cycle"),
			Parent:      optStr(req, "parent"),
		}
		if labels := optStr(req, "labels"); labels != nil {
			l := splitLabels(*labels)
			input.Labels = &l
		}
		if est := optStr(req, "estimate"); est != nil {
			if v, err := strconv.ParseFloat(strings.TrimSpace(*est), 64); err == nil {
				input.Estimate = &v
			} else {
				return mcp.NewToolResultError(fmt.Sprintf("invalid estimate %q: must be a number", *est)), nil
			}
		}

		task, err := reg.UpdateTask(ctx, identifier, input)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error updating task: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Task updated: **%s** — %s\nStatus: %s",
			task.Identifier, task.Title, task.Status)), nil
	}
}

func handleTasksSearch(reg *provider.Registry) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := strParam(req, "query")
		if query == "" {
			return mcp.NewToolResultError("query is required"), nil
		}

		providerFilter := strParam(req, "provider")
		tasks, warnings, err := reg.SearchTasks(ctx, query, providerFilter)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
		}

		var sb strings.Builder
		if len(warnings) > 0 {
			for _, w := range warnings {
				sb.WriteString(fmt.Sprintf("Warning: %s\n", w))
			}
			sb.WriteString("\n")
		}

		if len(tasks) == 0 {
			sb.WriteString(fmt.Sprintf("No tasks found matching %q", query))
			return mcp.NewToolResultText(sb.String()), nil
		}

		sb.WriteString(fmt.Sprintf("## Search results for %q — %d matches\n\n", query, len(tasks)))
		sb.WriteString("| ID | Title | Status | Source | Project |\n")
		sb.WriteString("|---|---|---|---|---|\n")
		for _, t := range tasks {
			title := t.Title
			if len(title) > 60 {
				title = title[:57] + "..."
			}
			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n",
				t.Identifier, title, t.Status, t.Source, t.Project))
		}

		return mcp.NewToolResultText(sb.String()), nil
	}
}

func handleTasksProjects(reg *provider.Registry) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		providerFilter := strParam(req, "provider")
		kindFilter := strings.ToLower(strParam(req, "kind"))
		if kindFilter == "" {
			kindFilter = "all"
		}
		if kindFilter != "all" && kindFilter != "team" && kindFilter != "project" {
			return mcp.NewToolResultError(fmt.Sprintf("invalid kind %q (valid: team, project, all)", kindFilter)), nil
		}

		projects, warnings, err := reg.AllProjects(ctx, providerFilter)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
		}

		var sb strings.Builder
		if len(warnings) > 0 {
			for _, w := range warnings {
				sb.WriteString(fmt.Sprintf("Warning: %s\n", w))
			}
			sb.WriteString("\n")
		}

		// Filter by kind.
		var teams, projs []provider.Project
		for _, p := range projects {
			switch p.Kind {
			case "team":
				teams = append(teams, p)
			case "project":
				projs = append(projs, p)
			default:
				// Unknown kind falls into teams bucket so it's still visible.
				teams = append(teams, p)
			}
		}

		showTeams := kindFilter == "all" || kindFilter == "team"
		showProjects := kindFilter == "all" || kindFilter == "project"

		if (!showTeams || len(teams) == 0) && (!showProjects || len(projs) == 0) {
			sb.WriteString("No projects found.")
			return mcp.NewToolResultText(sb.String()), nil
		}

		if showTeams && len(teams) > 0 {
			sb.WriteString("## Teams\n\n")
			sb.WriteString("| Provider | Source | Name | Key |\n")
			sb.WriteString("|---|---|---|---|\n")
			for _, t := range teams {
				key := t.Key
				if key == "" {
					key = "-"
				}
				sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
					t.Source, t.SourceType, t.Name, key))
			}
			sb.WriteString("\n")
		}

		if showProjects && len(projs) > 0 {
			sb.WriteString("## Projects\n\n")
			sb.WriteString("| Provider | Team | Name | Status | Health | Priority | Lead | Target | Progress |\n")
			sb.WriteString("|---|---|---|---|---|---|---|---|---|\n")
			for _, p := range projs {
				team := p.ParentTeam
				if team == "" {
					team = "-"
				}
				status := dashIfEmpty(p.Status)
				health := dashIfEmpty(p.Health)
				priority := dashIfEmpty(p.Priority)
				lead := dashIfEmpty(p.Lead)
				target := dashIfEmpty(p.TargetDate)
				progress := fmt.Sprintf("%.0f%%", p.Progress*100)
				sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
					p.Source, team, p.Name, status, health, priority, lead, target, progress))
			}
			sb.WriteString("\n")
		}

		return mcp.NewToolResultText(sb.String()), nil
	}
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// ─── Documents ────────────────────────────────────────────────────────────────

func handleDocsList(reg *provider.Registry) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		opts := provider.DocumentListOpts{
			Provider: strParam(req, "provider"),
			Project:  strParam(req, "project"),
			Query:    strParam(req, "query"),
		}

		docs, warnings, err := reg.AllDocuments(ctx, opts)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
		}

		var sb strings.Builder
		if len(warnings) > 0 {
			sb.WriteString("**Warnings:**\n")
			for _, w := range warnings {
				sb.WriteString(fmt.Sprintf("- %s\n", w))
			}
			sb.WriteString("\n---\n\n")
		}

		if len(docs) == 0 {
			sb.WriteString("No documents found.")
			return mcp.NewToolResultText(sb.String()), nil
		}

		grouped := make(map[string][]provider.Document)
		var order []string
		for _, d := range docs {
			if _, seen := grouped[d.Source]; !seen {
				order = append(order, d.Source)
			}
			grouped[d.Source] = append(grouped[d.Source], d)
		}

		for _, source := range order {
			group := grouped[source]
			sb.WriteString(fmt.Sprintf("## %s — %d docs\n\n", strings.ToUpper(source), len(group)))
			sb.WriteString("| Slug | Title | Project | Creator | Updated |\n")
			sb.WriteString("|---|---|---|---|---|\n")
			for _, d := range group {
				title := d.Title
				if len(title) > 60 {
					title = title[:57] + "..."
				}
				project := d.Project
				if project == "" {
					project = "-"
				}
				creator := d.Creator
				if creator == "" {
					creator = "-"
				}
				sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n",
					d.SlugID, title, project, creator, d.UpdatedAt))
			}
			sb.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf("**Total: %d docs**", len(docs)))
		return mcp.NewToolResultText(sb.String()), nil
	}
}

func handleDocsGet(reg *provider.Registry) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		identifier := strParam(req, "identifier")
		if identifier == "" {
			return mcp.NewToolResultError("identifier is required (slugId or UUID)"), nil
		}
		providerName := strParam(req, "provider")

		doc, err := reg.GetDocument(ctx, providerName, identifier)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("# %s\n\n", doc.Title))
		sb.WriteString(fmt.Sprintf("**Source:** %s (%s)\n", doc.Source, doc.SourceType))
		sb.WriteString(fmt.Sprintf("**Slug:** %s\n", doc.SlugID))
		sb.WriteString(fmt.Sprintf("**ID:** %s\n", doc.ID))
		if doc.Project != "" {
			sb.WriteString(fmt.Sprintf("**Project:** %s\n", doc.Project))
		}
		if doc.Creator != "" {
			sb.WriteString(fmt.Sprintf("**Creator:** %s\n", doc.Creator))
		}
		if doc.URL != "" {
			sb.WriteString(fmt.Sprintf("**URL:** %s\n", doc.URL))
		}
		sb.WriteString(fmt.Sprintf("**Created:** %s\n", doc.CreatedAt))
		sb.WriteString(fmt.Sprintf("**Updated:** %s\n", doc.UpdatedAt))

		if doc.Content != "" {
			sb.WriteString("\n---\n\n")
			sb.WriteString(doc.Content)
		}
		return mcp.NewToolResultText(sb.String()), nil
	}
}

func handleDocsCreate(reg *provider.Registry) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		input := provider.DocumentCreateInput{
			Provider: strParam(req, "provider"),
			Project:  strParam(req, "project"),
			Title:    strParam(req, "title"),
			Content:  strParam(req, "content"),
			Icon:     strParam(req, "icon"),
			Color:    strParam(req, "color"),
		}

		if input.Provider == "" || input.Project == "" || input.Title == "" {
			return mcp.NewToolResultError("provider, project, and title are required"), nil
		}

		doc, err := reg.CreateDocument(ctx, input)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error creating document: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Document created: **%s**\nSlug: %s | Project: %s | URL: %s",
			doc.Title, doc.SlugID, doc.Project, doc.URL)), nil
	}
}

func handleDocsUpdate(reg *provider.Registry) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		identifier := strParam(req, "identifier")
		if identifier == "" {
			return mcp.NewToolResultError("identifier is required (slugId or UUID)"), nil
		}
		providerName := strParam(req, "provider")

		input := provider.DocumentUpdateInput{
			Title:   strPtr(strParam(req, "title")),
			Content: strPtr(strParam(req, "content")),
			Icon:    strPtr(strParam(req, "icon")),
			Color:   strPtr(strParam(req, "color")),
		}

		doc, err := reg.UpdateDocument(ctx, providerName, identifier, input)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error updating document: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Document updated: **%s**\nSlug: %s | Project: %s",
			doc.Title, doc.SlugID, doc.Project)), nil
	}
}

func handleDocsDelete(reg *provider.Registry) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		identifier := strParam(req, "identifier")
		if identifier == "" {
			return mcp.NewToolResultError("identifier is required (slugId or UUID)"), nil
		}
		providerName := strParam(req, "provider")

		if err := reg.DeleteDocument(ctx, providerName, identifier); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error deleting document: %v", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Document %s deleted.", identifier)), nil
	}
}

func handleDocsSearch(reg *provider.Registry) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := strParam(req, "query")
		if query == "" {
			return mcp.NewToolResultError("query is required"), nil
		}
		providerFilter := strParam(req, "provider")

		docs, warnings, err := reg.SearchDocuments(ctx, query, providerFilter)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
		}

		var sb strings.Builder
		for _, w := range warnings {
			sb.WriteString(fmt.Sprintf("Warning: %s\n", w))
		}
		if len(warnings) > 0 {
			sb.WriteString("\n")
		}

		if len(docs) == 0 {
			sb.WriteString(fmt.Sprintf("No documents found matching %q", query))
			return mcp.NewToolResultText(sb.String()), nil
		}

		sb.WriteString(fmt.Sprintf("## Document search for %q — %d matches\n\n", query, len(docs)))
		sb.WriteString("| Slug | Title | Project | Source |\n")
		sb.WriteString("|---|---|---|---|\n")
		for _, d := range docs {
			title := d.Title
			if len(title) > 60 {
				title = title[:57] + "..."
			}
			project := d.Project
			if project == "" {
				project = "-"
			}
			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", d.SlugID, title, project, d.Source))
		}
		return mcp.NewToolResultText(sb.String()), nil
	}
}

func handleTasksComment(reg *provider.Registry) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		identifier := strParam(req, "identifier")
		body := strParam(req, "body")
		if identifier == "" || body == "" {
			return mcp.NewToolResultError("identifier and body are required"), nil
		}

		comment, err := reg.AddComment(ctx, identifier, body)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error adding comment: %v", err)), nil
		}

		author := comment.Author
		if author == "" {
			author = "you"
		}
		return mcp.NewToolResultText(fmt.Sprintf("Comment added to **%s** by %s.", identifier, author)), nil
	}
}

func handleTasksMembers(reg *provider.Registry) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		providerName := strParam(req, "provider")
		project := strParam(req, "project")
		if providerName == "" {
			return mcp.NewToolResultError("provider is required"), nil
		}

		members, err := reg.ListMembers(ctx, providerName, project)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
		}
		if len(members) == 0 {
			return mcp.NewToolResultText("No assignable members found."), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("## Assignable members in %s — %d\n\n", providerName, len(members)))
		sb.WriteString("| Name | Display Name | Email |\n")
		sb.WriteString("|---|---|---|\n")
		for _, m := range members {
			sb.WriteString(fmt.Sprintf("| %s | %s | %s |\n",
				dashIfEmpty(m.Name), dashIfEmpty(m.DisplayName), dashIfEmpty(m.Email)))
		}
		sb.WriteString("\nPass a name or email as `assignee` in tasks_update / tasks_create.")
		return mcp.NewToolResultText(sb.String()), nil
	}
}

func handleTasksStates(reg *provider.Registry) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		providerName := strParam(req, "provider")
		project := strParam(req, "project")

		if providerName == "" || project == "" {
			return mcp.NewToolResultError("provider and project are required"), nil
		}

		states, err := reg.States(ctx, providerName, project)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
		}

		if len(states) == 0 {
			return mcp.NewToolResultText("No states found for this project."), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("## Workflow states for %s/%s\n\n", providerName, project))
		sb.WriteString("| ID | Name | Type |\n")
		sb.WriteString("|---|---|---|\n")
		for _, s := range states {
			sb.WriteString(fmt.Sprintf("| %s | %s | %s |\n", s.ID, s.Name, s.Type))
		}

		return mcp.NewToolResultText(sb.String()), nil
	}
}
