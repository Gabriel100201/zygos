#!/usr/bin/env bash
# Call one Zygos MCP tool over stdio and print its text result.
#
#   demo/call.sh tasks_list '{"state":"active"}'
#
# Used to record the README demo; a real client speaks this protocol for you.
set -euo pipefail

tool="$1"
args="${2-}"
[ -n "$args" ] || args='{}'
bin="${ZYGOS_BIN:-zygos}"

{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"demo","version":"1"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized"}'
  printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"%s","arguments":%s}}\n' "$tool" "$args"
} | "$bin" mcp | jq -r 'select(.id==2) | .result.content[0].text'
