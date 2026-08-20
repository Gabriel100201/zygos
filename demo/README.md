# Demo

`assets/demo.gif` is recorded from the real binary — no mock-ups, no editing.
It runs against `demo/stub`, a throwaway server that serves just enough of the
Taiga and OpenProject APIs to have something to aggregate, so the recording
needs no credentials and no network.

## Regenerating

Requires [vhs](https://github.com/charmbracelet/vhs) (`brew install vhs`) and
`jq`.

```bash
./demo/record.sh
```

That script builds `zygos`, starts the stub, writes a throwaway config, runs
`vhs demo/demo.tape`, and cleans up after itself.

## Pieces

| File | What it is |
|---|---|
| `demo.tape` | The vhs script: what gets typed and how long each frame holds |
| `stub/main.go` | Fake Taiga + OpenProject API, fixture data only |
| `call.sh` | Sends one MCP `tools/call` over stdio and prints the text result |
| `record.sh` | Wires the three together and produces the GIF |

The fixture data lives in `stub/main.go`. Editing it changes what the demo
shows.
