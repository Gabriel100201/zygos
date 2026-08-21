<!--
Thanks for your contribution! Please fill out the sections below.
Keep this template populated in the PR description — it helps reviewers land your change faster.
-->

## Summary

<!-- What does this PR do, in one or two sentences? -->

## Motivation

<!-- Why is this change needed? Link the issue it closes, e.g. "Closes #42". -->

## Type of change

<!-- Pick the one that fits best. This should match the Conventional Commits type of your commits. -->

- [ ] `feat` — new user-facing capability
- [ ] `fix` — bug fix
- [ ] `refactor` — behavior-preserving code change
- [ ] `docs` — documentation only
- [ ] `test` — test changes only
- [ ] `chore` / `ci` — tooling, dependencies, workflow changes
- [ ] Breaking change (explain below)

## How I tested this

<!--
Describe the manual and/or automated testing you did. For example:
- `go vet ./... && go build ./...` passes
- Ran `zygos config add linear` locally with a test workspace; credentials validated correctly
- Called `tasks_list project=MOVE assigned=false` via Claude Code, got the expected 22 tasks

If the change is user-facing, please include a concrete example of the before/after behavior.
-->

## Checklist

- [ ] Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:`, …)
- [ ] `go vet ./...` and `go build ./...` succeed locally
- [ ] User-facing changes are reflected in `README.md`
- [ ] An entry was added under `[Unreleased]` in `CHANGELOG.md`
- [ ] No secrets, API keys, or personal data in the diff
- [ ] For new MCP tools: schema is registered in `internal/mcp/mcp.go` with correct `ReadOnlyHintAnnotation` / `DestructiveHintAnnotation`

## Breaking changes

<!-- If this PR breaks anything (tool schema, config format, CLI command), describe it and the migration path. Otherwise write "None". -->

## Screenshots / output (optional)

<!-- Paste relevant command output, before/after tables, or screenshots here. -->
