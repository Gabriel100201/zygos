# Security Policy

## Reporting a vulnerability

Please report security issues privately through
[GitHub's private vulnerability reporting](https://github.com/Gabriel100201/zygos/security/advisories/new)
rather than opening a public issue.

Include the version (`zygos version`), the provider type involved, and the
steps to reproduce. Expect an acknowledgement within 72 hours and a fix or a
public advisory within 30 days for confirmed issues.

## Supported versions

Only the latest tagged release receives security fixes. The `edge` pre-release
is a build of the latest `main` and carries no support guarantee.

## Threat model

Zygos runs locally, on your machine, under your user account. It has no
server, no telemetry, and no network destination other than the provider APIs
you configure yourself.

**Credentials are stored in plaintext.** `~/.zygos/config.yaml` holds Linear
API keys, OpenProject API tokens, and Taiga passwords as readable text. This is
a deliberate trade-off: a single static binary with no runtime dependencies
cannot reach an OS keychain without giving up that property on at least one
platform. What Zygos does instead:

- the config file is written with mode `0600` and its directory with `0700`, so
  no other user on the machine can read it;
- credentials are never included in error messages, log output, or MCP
  responses — `zygos config list` masks them;
- `ZYGOS_CONFIG` lets you relocate the file onto an encrypted volume.

Treat that file the way you treat `~/.ssh/id_rsa`, and prefer scoped API keys
over personal ones where your provider supports them.

## What is out of scope

- Anything reachable only by an attacker who already has read access to your
  home directory or your shell. At that point the config file is the least of
  the problem.
- Vulnerabilities in Linear, Taiga, or OpenProject themselves. Report those to
  the respective vendors.
- The agent's behaviour. Zygos exposes tools; deciding which of them to call
  is the MCP client's responsibility. Tools that mutate a tracker are annotated
  as non-read-only precisely so your client can ask before running them.
