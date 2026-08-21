# Changelog

All notable changes to Zygos are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-08-20

### Added

- **Full Linear task actions** ([6d39190](https://github.com/Gabriel100201/zygos/commit/6d39190)). `tasks_update` and `tasks_create` previously handled little more than title, description, priority and a state passed as a raw UUID. They now take `state` **by name** — no more copying UUIDs out of the Linear UI — plus `assignee` (`"me"`, a display name or an email), `project`, `labels`, `due_date`, `estimate`, `cycle` and `parent`, each accepting a human name with a UUID fallback. Passing `"none"` clears a field.
- `tasks_members`: list the assignable users of a provider, so an agent can discover the names `assignee` expects.
- `tasks_comment`: post a comment on a task. Comments are read back through `tasks_get`.

### Changed

- **The project is now called Zygos.** ζυγός is the Greek for *yoke*, the beam
  that couples several bodies so they pull as one, and the root of *syzygy* —
  the alignment of three bodies on a single line. Tablero said nothing about
  what the tool does and competed with everything else named "board".

  The binary, module path, config directory and environment variable all follow:
  `tablero` → `zygos`, `github.com/Gabriel100201/tablero` →
  `github.com/Gabriel100201/zygos`, `~/.tablero/` → `~/.zygos/`,
  `TABLERO_CONFIG` → `ZYGOS_CONFIG`.

  **Existing installs keep working with no action.** The config resolver falls
  back to `~/.tablero/config.yaml` when the new path does not exist and still
  reads `TABLERO_CONFIG`; `zygos config list` and `config path` point out when
  the legacy location is in use. The GitHub repository was renamed in place, so
  the old URL redirects and existing clones are unaffected. See the README's
  *Migrating from Tablero* section.

### Added

- A recorded demo in the README, produced from the real binary against a local
  API stub (`demo/`) rather than a hand-made asset — `./demo/record.sh`
  regenerates it.
- Project logo and wordmark, in light and dark variants (`assets/`).


### Fixed

- **Taiga list calls silently returned only the first 30 results.** `tasks_list`,
  `tasks_projects`, `tasks_states` and a task's comments read a single response
  from endpoints that Taiga paginates, so an agent reported a partial backlog as
  if it were complete. All collection reads now follow Taiga's
  `x-pagination-next` links to the end.
- **Taiga writes lost their body when the session token expired.** The retry
  after a 401 replayed the request with an `io.Reader` the first attempt had
  already drained, sending an empty payload. The body is now buffered and
  replayed intact.
- **Linear truncated large workspaces.** `tasks_projects` capped teams and
  projects at 100 each with no cursor, and `tasks_states` requested no page size
  at all — so it inherited Linear's default of 50 and hid workflow states, which
  made `tasks_update` reject valid state names. All three connections are now
  cursor-paginated.
- Filtering by a provider name that does not exist reported `all providers
  failed` instead of naming the unknown provider.
- The MCP handshake advertised a hardcoded version `0.1.0` regardless of the
  binary actually running. It now reports the real build version.

### Added

- Rate-limit and transient-failure handling. Every provider now shares an HTTP
  client that retries `429` responses with exponential backoff, honours
  `Retry-After`, and retries `5xx` on reads only — replaying a failed write
  could create the same task twice.
- Request timeouts on all three provider HTTP clients, which previously had
  none and relied entirely on the caller's context.
- A test suite covering config handling and permissions, the retry transport,
  Taiga and Linear pagination, registry routing and graceful degradation, and
  the MCP tool contract. CI now runs `go test -race` and a `gofmt` check on
  every push, alongside the existing build and vet.
- `SECURITY.md`: reporting process, supported versions, and an explicit threat
  model for the plaintext credential store.

### Changed

- Per-operation timeouts raised from 10s to 45s. Now that list calls paginate, a
  single operation can be a dozen round trips against a large workspace.


## [0.2.0] - 2026-07-17

### Added

- OpenProject provider (`type: openproject`): task aggregation over the OpenProject REST API v3 with API-token (Basic auth) authentication. Supports `tasks_list`, `tasks_get`, `tasks_create`, `tasks_update`, `tasks_search`, `tasks_projects`, and `tasks_states`. Work packages are addressed as `<provider>:wp:<id>`. Documents are not supported. Add one with `zygos config add openproject`.
- Contributor documentation: `CONTRIBUTING.md` (development setup, project layout, commit convention, PR flow, guides for adding tools and providers) and `RELEASING.md` (versioning, pre-release checklist, tag-based release flow, recovery procedure).
- GitHub issue templates for bug reports and feature requests, plus a pull request template with a contributor checklist.
- README now links to the contributor and release guides.
- README Quick start section: three linear steps (install, add workspace, connect to AI agent) designed to get a new user from zero to a working MCP connection in about two minutes.

### Fixed

- OpenProject `tasks_list` with `state="all"` and no other filter returned HTTP 500. An empty filter set was serialized as `filters=null`, which the OpenProject API rejects; the parameter is now omitted when there is nothing to filter by.
- OpenProject `tasks_projects` only returned the first 100 projects. `ListProjects` now paginates like every other list call, which also unbreaks `tasks_create` and project-filtered `tasks_list` on instances with more than 100 projects.

## [0.1.1] - 2026-06-10

### Added

- `Ping` method on the `Provider` interface for lightweight credential checks.

### Fixed

- Linear connection validation exceeded the API complexity limit on large workspaces ([#1](https://github.com/Gabriel100201/zygos/pull/1)).

## [0.1.0] - 2026-04-19

Initial public release.

### Added

- MCP server over stdio for Linear and Taiga task aggregation.
- Multi-provider support: any number of Linear workspaces and Taiga instances in one config.
- Task tools: `tasks_list`, `tasks_get`, `tasks_create`, `tasks_update`, `tasks_search`, `tasks_projects`, `tasks_states`.
- `tasks_list` returns **all open tasks** in the workspace by default; use `assigned=true` to filter to tasks assigned to the authenticated user.
- Linear document CRUD: `docs_list`, `docs_get`, `docs_create`, `docs_update`, `docs_delete`, `docs_search` — `docs_get` returns full markdown content.
- Linear Team vs Project awareness: `tasks_projects` exposes both levels with a `Kind` column; the `project` filter on `tasks_list` and `docs_list` matches either a team (by name or key) or a project (by name).
- Interactive CLI: `zygos config init`, `add linear`, `add taiga`, `list`, `remove`, `test`, `path`. Validates credentials on add (makes a live API call) before saving. Secrets are entered with hidden input and masked in `list`.
- Config file written with mode `0600` (owner read/write only).
- Graceful degradation: unreachable providers (e.g. Taiga behind VPN) surface as warnings; healthy providers still return results.
- Pre-built binaries for Linux, macOS, and Windows (amd64 + arm64) via GitHub Releases.
