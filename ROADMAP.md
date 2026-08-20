# Zygos Roadmap

This document describes where Zygos is headed. It is a living document — priorities shift as the project grows and the community provides feedback.

**Have an idea?** [Open a feature request](https://github.com/Gabriel100201/zygos/issues/new?template=feature_request.md) — all roadmap items below started as one.

---

## What we shipped

### v0.1.0 — Initial release *(April 2026)*

- MCP server over stdio for Linear and Taiga
- Multi-provider support (any number of workspaces per type)
- Full task CRUD: `tasks_list`, `tasks_get`, `tasks_create`, `tasks_update`, `tasks_search`, `tasks_projects`, `tasks_states`
- Linear document CRUD: `docs_list`, `docs_get`, `docs_create`, `docs_update`, `docs_delete`, `docs_search`
- Interactive CLI (`zygos config add/list/remove/test`)
- Graceful degradation — one broken provider never breaks the others
- Pre-built binaries for Linux, macOS, and Windows (amd64 + arm64)

### v0.1.1 *(June 2026)*

- Fixed Linear connection validation exceeding the API complexity limit on large workspaces ([#1](https://github.com/Gabriel100201/zygos/pull/1))
- Added `Ping` method to the `Provider` interface for lightweight credential checks

### v0.2.0 *(July 2026)*

- **OpenProject provider** ([#4](https://github.com/Gabriel100201/zygos/issues/4)) — task aggregation over the OpenProject REST API v3 with API-token authentication. Work packages addressed as `<provider>:wp:<id>`; documents not supported.

---

## What's next

Items are grouped by theme, not by release. Ordering within each section reflects rough priority.

### Web UI

**[#3](https://github.com/Gabriel100201/zygos/issues/3) — `zygos web`: embedded dashboard**
A `zygos web` subcommand that starts a local HTTP server and opens a browser to a single-page dashboard. No separate install — the UI is compiled into the binary with `go:embed`.

Planned views:
- **My Day** — all tasks assigned to you across every provider, the answer to "what do I work on today?"
- **Projects** — card grid with health indicators, progress bars, and target dates
- **Tasks** — filterable list with a detail panel (description, comments, labels)
- **Kanban** — board view grouped by status type (Backlog · Todo · In Progress · In Review · Done)
- **Documents** — Linear docs with full markdown rendering and search

**[#6](https://github.com/Gabriel100201/zygos/issues/6) — `zygos export`: static HTML snapshot**
Generate a self-contained HTML file with all workspace data baked in — shareable with anyone without requiring Zygos to be installed.

---

### New providers

Zygos's provider interface is designed to make new integrations straightforward. Adding a provider requires implementing one Go interface (`provider.Provider`) and registering a new `zygos config add <type>` command.

**[#5](https://github.com/Gabriel100201/zygos/issues/5) — Jira**
The most requested provider. Targets Jira Cloud first via the REST API v3. Jira's `PROJ-123` key format maps naturally to Zygos's existing identifier conventions.

**GitHub Issues** *(not yet filed)*
For teams that use GitHub as their primary issue tracker. Would also enable cross-referencing between code and tasks within the same tool.

---

## How to contribute

All items above are open for contributions. Before starting work on a large feature, please comment on the relevant issue to coordinate — it avoids duplicate effort.

See [CONTRIBUTING.md](./CONTRIBUTING.md) for the development setup, commit convention, and PR flow.

---

## What we will not do

Keeping Zygos focused means saying no to some things:

- **A daemon or background sync process.** Zygos fetches on demand — it does not maintain a persistent connection or local cache. This keeps the binary simple and stateless.
- **A hosted/cloud version.** Zygos runs locally. Credentials never leave your machine.
- **Provider-specific features not expressible in the unified model.** If a feature only makes sense for one provider and cannot be generalized, it belongs in that provider's native client.
