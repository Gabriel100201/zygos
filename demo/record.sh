#!/usr/bin/env bash
# Rebuild the README demo GIF from scratch. See demo/README.md.
set -euo pipefail

cd "$(dirname "$0")/.."

command -v vhs >/dev/null || { echo "vhs is required: brew install vhs" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

echo "Building zygos…"
go build -ldflags "-X main.version=demo" -o /tmp/zygos ./cmd/zygos

# call.sh is invoked from the tape as `zygos-call`, so it needs to be on PATH.
install -m 0755 demo/call.sh /tmp/zygos-call

echo "Starting the demo stub…"
go run ./demo/stub -addr :8099 >/tmp/zygos-stub.log 2>&1 &
stub=$!
trap 'kill $stub 2>/dev/null || true' EXIT
sleep 2

mkdir -p /tmp/zygosdemo
cat > /tmp/zygosdemo/config.yaml <<'YAML'
providers:
  - name: cliente
    type: taiga
    url: http://localhost:8099
    username: gabo
    password: demo-password
  - name: legacy
    type: openproject
    url: http://localhost:8099
    api_key: demo-api-key
YAML

echo "Recording…"
vhs demo/demo.tape

echo "Wrote assets/demo.gif"
