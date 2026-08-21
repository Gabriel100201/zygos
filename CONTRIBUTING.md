# Contributing to Zygos

Thanks for considering a contribution! This guide covers everything you need to work on Zygos — from your first clone to your first merged PR.

## Table of contents

- [Scope and philosophy](#scope-and-philosophy)
- [Quick start: local development](#quick-start-local-development)
- [Project layout](#project-layout)
- [Running against real providers](#running-against-real-providers)
- [Commit convention](#commit-convention)
- [Pull request flow](#pull-request-flow)
- [Adding a new MCP tool](#adding-a-new-mcp-tool)
- [Adding a new provider](#adding-a-new-provider)
- [Releasing](#releasing)

## Scope and philosophy

Zygos is a **single-binary MCP server** that aggregates tasks (and Linear documents) across Linear, Taiga and OpenProject. We optimize for:

- **Zero runtime dependencies.** No Python sidecar, no database, no Docker.
- **Friendly first run.** The CLI should prompt a newcomer through setup in under 2 minutes.
- **Graceful degradation.** One broken provider must never break the others.
- **Agent-first UX.** The primary user is an LLM calling MCP tools — error messages, schemas, and defaults are designed for that.

Before opening a big PR, please open an issue to discuss the idea. It saves you work if the direction doesn't fit.

## Quick start: local development

### Prerequisites

- **Go 1.25 or newer** — check with `go version`
- **Git**
- (Optional) A Linear API key and/or a Taiga instance to test against

### Clone and build

```bash
git clone https://github.com/Gabriel100201/zygos.git
cd zygos
go build ./...
```

That builds every package. To produce a local binary:

```bash
go install ./cmd/zygos
# The binary lands in $(go env GOPATH)/bin
zygos version  # should print "dev"
```

### Run the smoke tests that CI runs

```bash
go vet ./...
go build ./...
```

CI runs these on Linux, macOS, and Windows on every PR.

## Project layout

```
zygos/
├── cmd/zygos/            # CLI entry point (main.go) and config CLI (config.go)
├── internal/
│   ├── config/             # YAML config loading, validation, CLI-driven mutation
│   ├── mcp/                # MCP server: tool registration (mcp.go) and handlers (handlers.go)
│   └── provider/           # Provider interface + Linear, Taiga and OpenProject implementations
│       ├── provider.go     # Shared types, Provider interface, Registry
│       ├── linear.go       # Linear GraphQL implementation
│       └── taiga.go        # Taiga REST implementation
├── .github/workflows/      # CI and release workflows
├── .goreleaser.yml         # Cross-platform release build config
├── CHANGELOG.md            # User-facing changelog (Keep a Changelog format)
├── config.example.yaml     # Template users copy to ~/.zygos/config.yaml
└── README.md
```

**Rule of thumb:** business logic goes in `internal/provider/`, MCP plumbing goes in `internal/mcp/`, and the CLI is in `cmd/zygos/`. Keep those responsibilities separate.

## Running against real providers

Testing Zygos needs real credentials. Two options:

**Option 1 — use your own config.** Point `ZYGOS_CONFIG` at a dev-only config file with a test workspace:

```bash
export ZYGOS_CONFIG=$HOME/.zygos/dev-config.yaml
zygos config add linear   # interactive prompt
zygos config test         # verify connectivity
```

**Option 2 — register the local build with your MCP client.** For Claude Code:

```bash
# After `go install ./cmd/zygos`
claude mcp add --transport stdio zygos-dev -- zygos mcp
```

Reconnect the MCP server in your client (`/mcp` in Claude Code) to pick up the latest build.

**Never commit `config.yaml`.** It is gitignored, and the file permission is `0600` for a reason — it contains API keys and passwords.

## Commit convention

We use [Conventional Commits](https://www.conventionalcommits.org/). The subject line must start with a type:

| Type | When to use |
|------|-------------|
| `feat:` | New user-facing capability |
| `fix:` | Bug fix |
| `docs:` | README, CHANGELOG, or guide changes only |
| `refactor:` | Behavior-preserving code change |
| `test:` | Adding or fixing tests |
| `chore:` | Maintenance (deps, tooling) with no user impact |
| `ci:` | Changes to workflows or release pipeline |
| `perf:` | Performance improvement |

**Breaking changes:** append `!` after the type (e.g. `feat!: drop Go 1.23 support`) and add a `BREAKING CHANGE:` section in the body.

Examples:

```
feat: add docs_list filter by creator

fix: Linear pagination stopped at 50 issues

ci: bump actions/checkout to v5
```

Keep the subject line under 72 characters. Body is optional but encouraged for non-trivial changes — explain **why**, not what.

## Pull request flow

1. **Open or find an issue** describing the problem you want to solve. For small fixes (typos, docs), you can skip this.
2. **Fork** the repo and create a branch:
   ```bash
   git checkout -b feat/my-feature
   ```
3. **Make your changes.** Run `go vet ./...` and `go build ./...` locally.
4. **Update documentation** when user-facing behavior changes:
   - `README.md` if you add/change a tool or CLI command
   - `CHANGELOG.md` under the `[Unreleased]` section
5. **Commit using the Conventional Commits format** (see above).
6. **Push and open a PR** against `main`. Fill in the PR template.
7. CI must pass on all three OSes before review.
8. After review and approval, the PR is squashed and merged.

## Adding a new MCP tool

A minimal new tool takes edits in three places:

1. **Provider layer** (`internal/provider/`): add a method to the `Provider` interface in `provider.go`, implement it in `linear.go` and `taiga.go`, and add an aggregator method on `Registry` if the tool queries multiple providers.
2. **Handler** (`internal/mcp/handlers.go`): write a `handleXxx` function that reads parameters from the `CallToolRequest`, invokes the registry, and formats the result as markdown.
3. **Registration** (`internal/mcp/mcp.go`): register the tool in `registerTools` with the correct annotations (`ReadOnlyHintAnnotation`, `DestructiveHintAnnotation`, etc.) and parameter schema.

Remember to update the `serverInstructions` constant in `mcp.go` and the tools table in `README.md`.

## Adding a new provider

To support a third backend (GitHub Issues, Jira, etc.):

1. Create `internal/provider/<name>.go` and implement every method on the `Provider` interface.
2. Register the provider type in `cmd/zygos/main.go`'s `cmdMCP` function (the `switch pc.Type` block) and in `internal/config/config.go`'s `validate` method.
3. Add interactive prompts in `cmd/zygos/config.go` (`cmdConfigAdd`).
4. Update the Linear/Taiga identifier format documentation in `README.md`.
5. Providers that don't support a capability (e.g. Taiga has no documents) should return the appropriate sentinel error (`ErrDocsNotSupported`) and the registry methods will filter those out of aggregated results.

## Releasing

See [`RELEASING.md`](./RELEASING.md) for the full release checklist. TL;DR: bump `CHANGELOG.md`, tag `vX.Y.Z`, push the tag. GoReleaser does the rest.
