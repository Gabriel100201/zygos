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

rm -rf /tmp/zygosdemo
mkdir -p /tmp/zygosdemo/.zygos
cat > /tmp/zygosdemo/.zygos/config.yaml <<'YAML'
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

echo "Recording the GIF…"
vhs demo/demo.tape

echo "Capturing the still…"
vhs demo/screenshot.tape
python3 - <<'PYEOF'
from PIL import Image

# vhs 0.11's Screenshot command writes nothing, so the still is the last frame
# of a short recording of the same session.
img = Image.open("demo/.screenshot-scratch.gif")
img.seek(img.n_frames - 1)
img.convert("RGB").save("assets/screenshot.png")
PYEOF
rm -f demo/.screenshot-scratch.gif

echo "Wrote assets/demo.gif and assets/screenshot.png"
