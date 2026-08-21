package mcp

import (
	"github.com/Gabriel100201/zygos/internal/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const serverInstructions = `Zygos is a unified task aggregator across Linear, Taiga and OpenProject.

TOOLS:
  tasks_list     — list open tasks across all providers (filter by provider/project/state/assigned)
  tasks_get      — get full task details by identifier
  tasks_create   — create a task in a specific provider/project (Linear: also assignee, labels, due_date, estimate, cycle, parent)
  tasks_update   — update a task: status/state (by name), assignee, project, labels, due date, estimate, cycle, parent, title, priority
  tasks_search   — search tasks by keyword across all providers
  tasks_projects — list teams and projects from all providers (filter by kind=team|project|all; Linear projects include health, priority, lead, target date, progress)
  tasks_states   — list valid workflow states for a project
  tasks_members  — list assignable users for a provider (Linear); use their names/emails as 'assignee'
  tasks_comment  — post a comment on a task (Linear); read comments via tasks_get
  docs_list      — list Linear documents (filter by provider/project/query)
  docs_get       — read a Linear document by slugId or UUID (returns markdown content)
  docs_create    — create a Linear document inside a project
  docs_update    — update a Linear document's title, content, icon, or color
  docs_delete    — delete a Linear document
  docs_search    — search Linear documents by keyword

IDENTIFIER FORMAT:
  Linear: use the issue key (e.g. ABC-42)
  Taiga user stories: <providerName>:us:<id> (e.g. work:us:234)
  Taiga tasks: <providerName>:task:<id> (e.g. work:task:56)
  OpenProject work packages: <providerName>:wp:<id> (e.g. work:wp:1234)

PROJECT MODEL:
  Linear has two levels: Teams (e.g. team name "Acme" with key ACME) and Projects inside
  each team (e.g. "Website Redesign", "Mobile v2"). tasks_projects lists both — check the
  Kind column to tell them apart. The project filter on tasks_list matches either a team
  (by name or key) or a project (by name).

USAGE:
  Default behaviour of tasks_list is ALL open tasks in the workspace (not just yours).
  Pass assigned=true to filter to tasks assigned to the authenticated user.

  When user asks "what do I have today?" → tasks_list with assigned=true
  When user asks "show me tasks in project X" → tasks_list with project=X (no assigned)
  When user says "create task X in Y" → tasks_create with correct provider/project
  When changing status → pass state="<name>" to tasks_update (names come from tasks_states); state_id (UUID) still works.
  When assigning a person → pass assignee="<name|email>" (or "me"); discover names via tasks_members. Use "none" to unassign.
  When moving to a project → pass project="<name>". When commenting → tasks_comment.
  "none" clears a field (assignee, project, cycle, due_date, parent).`

// NewServer builds the MCP server. version is the binary's build version, set
// via -X main.version at release time; it is what the MCP handshake reports to
// the client, so it must not be hardcoded here.
func NewServer(reg *provider.Registry, version string) *server.MCPServer {
	srv := server.NewMCPServer(
		"zygos",
		version,
		server.WithToolCapabilities(true),
		server.WithInstructions(serverInstructions),
	)

	registerTools(srv, reg)
	return srv
}

func registerTools(srv *server.MCPServer, reg *provider.Registry) {
	// ─── tasks_list ──────────────────────────────────────────────────────
	srv.AddTool(
		mcp.NewTool("tasks_list",
			mcp.WithDescription("List open tasks across all connected project management tools (Linear, Taiga, OpenProject). Returns tasks grouped by provider with status, priority, and due date. Defaults to ALL open tasks in the workspace — set assigned=true to see only tasks assigned to you."),
			mcp.WithTitleAnnotation("List Tasks"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("provider",
				mcp.Description("Filter to a specific provider name (as configured in config.yaml)"),
			),
			mcp.WithString("project",
				mcp.Description("Filter to a specific team or project name. For Linear, matches either a team (by name/key) or a project within a team (by name). Use tasks_projects to discover valid values."),
			),
			mcp.WithString("state",
				mcp.Description("Filter by state: 'active' (in-progress only), 'backlog' (todo/new only), or omit for all open"),
			),
			mcp.WithBoolean("assigned",
				mcp.Description("If true, return only tasks assigned to the authenticated user. Default false: return all open tasks in the workspace."),
			),
		),
		handleTasksList(reg),
	)

	// ─── tasks_get ───────────────────────────────────────────────────────
	srv.AddTool(
		mcp.NewTool("tasks_get",
			mcp.WithDescription("Get full details of a specific task by its identifier. Use Linear issue keys like 'ABC-42' or Taiga identifiers like '<provider>:us:123'. Returns description, comments, labels, and full metadata."),
			mcp.WithTitleAnnotation("Get Task Detail"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("identifier",
				mcp.Required(),
				mcp.Description("Task identifier (e.g. 'ABC-42' for Linear, '<provider>:us:123' or '<provider>:task:45' for Taiga)"),
			),
		),
		handleTasksGet(reg),
	)

	// ─── tasks_create ────────────────────────────────────────────────────
	srv.AddTool(
		mcp.NewTool("tasks_create",
			mcp.WithDescription("Create a new task in a specific provider and project. Use tasks_projects to discover available projects, and tasks_states for valid states."),
			mcp.WithTitleAnnotation("Create Task"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("provider",
				mcp.Required(),
				mcp.Description("Provider name (as configured in config.yaml)"),
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("Project name or key (use tasks_projects to see available options)"),
			),
			mcp.WithString("title",
				mcp.Required(),
				mcp.Description("Task title"),
			),
			mcp.WithString("description",
				mcp.Description("Task description (markdown supported)"),
			),
			mcp.WithString("priority",
				mcp.Description("Priority: urgent, high, medium, low, none (Linear only)"),
			),
			mcp.WithString("state_id",
				mcp.Description("Initial state ID (use tasks_states to get valid IDs)"),
			),
			mcp.WithString("type",
				mcp.Description("For Taiga: 'userstory' or 'task' (default: userstory)"),
			),
			mcp.WithString("assignee",
				mcp.Description("Assign to a user by name, email, or UUID; 'me' for yourself. Use tasks_members to list users. (Linear only)"),
			),
			mcp.WithString("labels",
				mcp.Description("Comma-separated label names, e.g. 'bug,urgent'. (Linear only)"),
			),
			mcp.WithString("due_date",
				mcp.Description("Due date as YYYY-MM-DD. (Linear only)"),
			),
			mcp.WithString("estimate",
				mcp.Description("Point estimate as a number, e.g. '3'. (Linear only)"),
			),
			mcp.WithString("cycle",
				mcp.Description("Cycle by name/number or UUID; 'active' for the current cycle. (Linear only)"),
			),
			mcp.WithString("parent",
				mcp.Description("Parent issue identifier (e.g. 'ABC-40') to create this as a sub-issue. (Linear only)"),
			),
		),
		handleTasksCreate(reg),
	)

	// ─── tasks_update ────────────────────────────────────────────────────
	srv.AddTool(
		mcp.NewTool("tasks_update",
			mcp.WithDescription("Update an existing task. Change status, assignee, project, labels, due date, and more. Most fields accept a human NAME or a UUID (Linear resolves it): e.g. state=\"In Progress\", assignee=\"Jane Doe\", project=\"Mobile v2\". Pass \"none\" to clear a field (unassign, remove from project/cycle, clear due date, detach parent). Only the fields you pass are changed. Linear only for the rich fields; Taiga honours title/description/state_id."),
			mcp.WithTitleAnnotation("Update Task"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("identifier",
				mcp.Required(),
				mcp.Description("Task identifier (e.g. 'ABC-42')"),
			),
			mcp.WithString("title",
				mcp.Description("New title"),
			),
			mcp.WithString("description",
				mcp.Description("New description"),
			),
			mcp.WithString("state",
				mcp.Description("New workflow state by NAME (e.g. 'In Progress', 'Done') or UUID. Preferred over state_id. Use tasks_states to see valid names."),
			),
			mcp.WithString("state_id",
				mcp.Description("New state UUID (legacy; prefer 'state' with a name). Use tasks_states for valid IDs."),
			),
			mcp.WithString("priority",
				mcp.Description("New priority (Linear only): urgent, high, medium, low, none"),
			),
			mcp.WithString("assignee",
				mcp.Description("Assign to a user by name, email, or UUID; 'me' for yourself; 'none' to unassign. Use tasks_members to list users. (Linear only)"),
			),
			mcp.WithString("project",
				mcp.Description("Move to a project by name or UUID; 'none' to remove from its project. Use tasks_projects to list projects. (Linear only)"),
			),
			mcp.WithString("labels",
				mcp.Description("Comma-separated label names, e.g. 'bug,urgent'. Replaces the whole label set. Empty string clears all labels. (Linear only)"),
			),
			mcp.WithString("due_date",
				mcp.Description("Due date as YYYY-MM-DD, or 'none' to clear. (Linear only)"),
			),
			mcp.WithString("estimate",
				mcp.Description("Point estimate as a number, e.g. '3'. (Linear only)"),
			),
			mcp.WithString("cycle",
				mcp.Description("Cycle by name/number or UUID; 'active' for the current cycle; 'none' to clear. (Linear only)"),
			),
			mcp.WithString("parent",
				mcp.Description("Parent issue identifier (e.g. 'ABC-40') to make this a sub-issue; 'none' to detach. (Linear only)"),
			),
		),
		handleTasksUpdate(reg),
	)

	// ─── tasks_search ────────────────────────────────────────────────────
	srv.AddTool(
		mcp.NewTool("tasks_search",
			mcp.WithDescription("Search for tasks across all providers by keyword. Searches titles and descriptions."),
			mcp.WithTitleAnnotation("Search Tasks"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("query",
				mcp.Required(),
				mcp.Description("Search query"),
			),
			mcp.WithString("provider",
				mcp.Description("Limit search to a specific provider"),
			),
		),
		handleTasksSearch(reg),
	)

	// ─── tasks_projects ──────────────────────────────────────────────────
	srv.AddTool(
		mcp.NewTool("tasks_projects",
			mcp.WithDescription("List teams and projects across all connected providers. For Linear, returns both Teams (key + name) and Projects inside each team (with health, priority, lead, target date, progress). Use this to discover where to create tasks or to see a project portfolio overview."),
			mcp.WithTitleAnnotation("List Projects"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("provider",
				mcp.Description("Filter to a specific provider"),
			),
			mcp.WithString("kind",
				mcp.Description("Filter by kind: 'team' (only Linear teams), 'project' (only projects inside teams), or 'all' (default). Use 'project' when you want the portfolio view that mirrors Linear's All Projects screen."),
			),
		),
		handleTasksProjects(reg),
	)

	// ─── docs_list ───────────────────────────────────────────────────────
	srv.AddTool(
		mcp.NewTool("docs_list",
			mcp.WithDescription("List Linear documents across all Linear providers. Filter by provider, project, or title substring. The project filter accepts a project name OR a team name/key (expands to all projects in the team). Taiga providers are skipped (docs not supported)."),
			mcp.WithTitleAnnotation("List Documents"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("provider",
				mcp.Description("Filter to a specific provider name (as configured in config.yaml)"),
			),
			mcp.WithString("project",
				mcp.Description("Filter by project name, or by team name/key (returns all docs from the team's projects)"),
			),
			mcp.WithString("query",
				mcp.Description("Case-insensitive substring filter on document titles"),
			),
		),
		handleDocsList(reg),
	)

	// ─── docs_get ────────────────────────────────────────────────────────
	srv.AddTool(
		mcp.NewTool("docs_get",
			mcp.WithDescription("Fetch a Linear document by slugId or UUID. Returns title, metadata, and the full markdown content."),
			mcp.WithTitleAnnotation("Get Document"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("identifier",
				mcp.Required(),
				mcp.Description("Document slugId (from the URL) or UUID"),
			),
			mcp.WithString("provider",
				mcp.Description("Provider to query; defaults to the first Linear provider"),
			),
		),
		handleDocsGet(reg),
	)

	// ─── docs_create ─────────────────────────────────────────────────────
	srv.AddTool(
		mcp.NewTool("docs_create",
			mcp.WithDescription("Create a Linear document inside a project. Documents belong to exactly one project — passing a team name here will fail with an ambiguity error."),
			mcp.WithTitleAnnotation("Create Document"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("provider",
				mcp.Required(),
				mcp.Description("Provider name (must be a Linear provider)"),
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("Exact Linear project name (use tasks_projects to discover)"),
			),
			mcp.WithString("title",
				mcp.Required(),
				mcp.Description("Document title"),
			),
			mcp.WithString("content",
				mcp.Description("Markdown body"),
			),
			mcp.WithString("icon",
				mcp.Description("Emoji icon (e.g. '📘')"),
			),
			mcp.WithString("color",
				mcp.Description("Color hex code (e.g. '#4EA7FC')"),
			),
		),
		handleDocsCreate(reg),
	)

	// ─── docs_update ─────────────────────────────────────────────────────
	srv.AddTool(
		mcp.NewTool("docs_update",
			mcp.WithDescription("Update a Linear document's title, content, icon, or color. Only the fields you pass are changed."),
			mcp.WithTitleAnnotation("Update Document"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("identifier",
				mcp.Required(),
				mcp.Description("Document slugId or UUID"),
			),
			mcp.WithString("provider",
				mcp.Description("Provider to target; defaults to the first Linear provider"),
			),
			mcp.WithString("title",
				mcp.Description("New title"),
			),
			mcp.WithString("content",
				mcp.Description("New markdown body (replaces existing content)"),
			),
			mcp.WithString("icon",
				mcp.Description("New emoji icon"),
			),
			mcp.WithString("color",
				mcp.Description("New color hex code"),
			),
		),
		handleDocsUpdate(reg),
	)

	// ─── docs_delete ─────────────────────────────────────────────────────
	srv.AddTool(
		mcp.NewTool("docs_delete",
			mcp.WithDescription("Delete a Linear document. This is permanent."),
			mcp.WithTitleAnnotation("Delete Document"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("identifier",
				mcp.Required(),
				mcp.Description("Document slugId or UUID"),
			),
			mcp.WithString("provider",
				mcp.Description("Provider to target; defaults to the first Linear provider"),
			),
		),
		handleDocsDelete(reg),
	)

	// ─── docs_search ─────────────────────────────────────────────────────
	srv.AddTool(
		mcp.NewTool("docs_search",
			mcp.WithDescription("Search Linear documents by keyword across titles and content."),
			mcp.WithTitleAnnotation("Search Documents"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("query",
				mcp.Required(),
				mcp.Description("Search query"),
			),
			mcp.WithString("provider",
				mcp.Description("Limit search to a specific provider"),
			),
		),
		handleDocsSearch(reg),
	)

	// ─── tasks_states ────────────────────────────────────────────────────
	srv.AddTool(
		mcp.NewTool("tasks_states",
			mcp.WithDescription("List available workflow states for a project. Use this to find valid state IDs before updating task status."),
			mcp.WithTitleAnnotation("List States"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("provider",
				mcp.Required(),
				mcp.Description("Provider name"),
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("Project name or key"),
			),
		),
		handleTasksStates(reg),
	)

	// ─── tasks_comment ───────────────────────────────────────────────────
	srv.AddTool(
		mcp.NewTool("tasks_comment",
			mcp.WithDescription("Post a comment on a task. Reading comments is done via tasks_get. (Linear only; Taiga not supported yet.)"),
			mcp.WithTitleAnnotation("Add Comment"),
			mcp.WithReadOnlyHintAnnotation(false),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("identifier",
				mcp.Required(),
				mcp.Description("Task identifier (e.g. 'ABC-42')"),
			),
			mcp.WithString("body",
				mcp.Required(),
				mcp.Description("Comment body (markdown supported)"),
			),
		),
		handleTasksComment(reg),
	)

	// ─── tasks_members ───────────────────────────────────────────────────
	srv.AddTool(
		mcp.NewTool("tasks_members",
			mcp.WithDescription("List assignable users for a provider. Use the returned names or emails as the 'assignee' value in tasks_update / tasks_create. (Linear only; Taiga not supported yet.)"),
			mcp.WithTitleAnnotation("List Members"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("provider",
				mcp.Required(),
				mcp.Description("Provider name (must be a Linear provider)"),
			),
			mcp.WithString("project",
				mcp.Description("Optional project/team scope hint (currently informational)"),
			),
		),
		handleTasksMembers(reg),
	)
}
