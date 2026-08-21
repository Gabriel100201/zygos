# Releasing Zygos

This document describes how to cut a new release. The whole pipeline is automated by GoReleaser — a maintainer only needs to bump the changelog and push a tag.

## Versioning

Zygos follows [Semantic Versioning 2.0](https://semver.org/):

| Bump | When |
|------|------|
| `MAJOR` (`v1.0.0` → `v2.0.0`) | Breaking change to the MCP tool schemas, CLI commands, or config file format |
| `MINOR` (`v1.2.0` → `v1.3.0`) | New tool, new CLI subcommand, new provider support, any other additive change |
| `PATCH` (`v1.2.3` → `v1.2.4`) | Bug fixes, doc fixes, dependency bumps with no user-visible effect |

While we are pre-1.0 (`v0.x.y`), a **MINOR bump may contain breaking changes** and we will call them out loudly in the release notes and `CHANGELOG.md`.

## Pre-release checklist

Before cutting a release, make sure:

- [ ] `main` is green in CI (all three OS jobs passing).
- [ ] Every merged PR since the last tag is reflected in `CHANGELOG.md` under `[Unreleased]`.
- [ ] You can build and run the current `main` locally:
      ```bash
      go vet ./...
      go build ./...
      go install ./cmd/zygos
      zygos version   # prints the module version Go embeds, e.g. "0.3.0+dirty"
      zygos config test
      ```
- [ ] `README.md` is consistent with the new behavior (tools list, CLI commands, config shape).
- [ ] No secrets, credentials, or personal data leaked into the diff since the last tag:
      ```bash
      git log v<previous>..HEAD -p -- '*.go' '*.md' '*.yml' '*.yaml' | grep -iE 'lin_api_[a-z0-9]{8}|password.*:.*[a-z0-9]{6}'
      ```

## Cutting a release

Pick the next version (say `v0.2.0`) and do the following on `main`:

```bash
# 1. Move the Unreleased entries under a new version heading in CHANGELOG.md,
#    add the release date, and create a fresh empty Unreleased section at the top.
$EDITOR CHANGELOG.md

# 2. Commit the changelog update
git add CHANGELOG.md
git commit -m "chore: prepare v0.2.0 release"
git push origin main

# 3. Wait for CI to pass on the commit above before tagging
gh run watch --exit-status

# 4. Tag and push — this triggers .github/workflows/release.yml
git tag -a v0.2.0 -m "v0.2.0 — <one-line summary>"
git push origin v0.2.0
```

Within ~1–2 minutes, GoReleaser will:

1. Build `zygos` for Linux, macOS, and Windows (amd64 + arm64, minus windows/arm64).
2. Embed the version string into the binary via `-ldflags "-X main.version={{.Version}}"`.
3. Pack each binary together with `README.md`, `LICENSE`, `CHANGELOG.md`, and `config.example.yaml` into a `.tar.gz` (or `.zip` on Windows).
4. Compute SHA-256 checksums and write them to `checksums.txt`.
5. Draft release notes from the commits between the previous tag and this one, filtered by commit type.
6. Publish the GitHub Release at `https://github.com/Gabriel100201/zygos/releases/tag/v0.2.0`.

## Verifying a release

After the workflow finishes:

```bash
# Confirm all expected assets are attached
gh release view v0.2.0 --repo Gabriel100201/zygos

# Download and smoke-test the Linux amd64 build
curl -sL https://github.com/Gabriel100201/zygos/releases/download/v0.2.0/zygos_0.2.0_linux_amd64.tar.gz | tar -xz
./zygos version   # should print "0.2.0"
```

If you're on a different platform, adapt the filename accordingly.

## Fixing a broken release

If GoReleaser fails mid-run or the published binaries are broken:

```bash
# Delete the local and remote tag
git tag -d v0.2.0
git push --delete origin v0.2.0

# Delete the GitHub release (if one was created)
gh release delete v0.2.0 --repo Gabriel100201/zygos --yes

# Fix the bug on main, then re-cut the tag
git tag -a v0.2.0 -m "v0.2.0 — <summary>"
git push origin v0.2.0
```

Avoid skipping a version number unless you already published a broken release with that version under public-facing documentation. If you shipped a broken release that users may have downloaded, yank it by deleting the release and cutting a `vX.Y.Z+1` **patch** that fixes the issue — do not reuse the version.

## Pre-releases and release candidates

To cut a release candidate, use a pre-release semver suffix:

```bash
git tag -a v0.2.0-rc.1 -m "v0.2.0 release candidate 1"
git push origin v0.2.0-rc.1
```

GoReleaser's `prerelease: auto` setting marks anything with a suffix (`-rc`, `-beta`, etc.) as a pre-release on GitHub so it doesn't show up as the "latest" release.

## Release cadence

We release as soon as a useful bundle of changes is on `main`. There is no fixed schedule.
