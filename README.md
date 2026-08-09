# lifx-command-engine

`lifx-command-engine` is a lightweight local sidecar that turns text into structured LIFX command plans. It does **not** discover devices, access the LAN, or execute commands. A host supplies a device snapshot, validates and previews the returned plan, asks for confirmation, and owns transport.

The first milestone uses the deterministic parser from [`lifxlan-go/pkg/command`](https://github.com/alessio-palumbo/lifxlan-go/tree/main/pkg/command). Model and speech runtimes are optional future extensions.

## Protocol

Run `go run ./cmd/lifx-command-engine`. Send one JSON request per line on stdin; receive one JSON response per line on stdout. IDs may be strings or numbers and are echoed unchanged.

```json
{
  "id": "1",
  "method": "interpret",
  "params": {
    "text": "make desk warm white at 35%",
    "snapshot": {
      "locations": [],
      "groups": [],
      "devices": [
        {
          "serial": "d073d5000001",
          "label": "Desk",
          "group": "Office",
          "location": "Home"
        }
      ]
    }
  }
}
```

The response contains `confidence`, an explainable `confidence_result`, `needs_confirmation`, a display `summary`, and semantic `commands`. Each target includes its serial as the stable host execution identity. Actions use human-scale values: hue in degrees, saturation/brightness in percent, Kelvin, and duration in milliseconds.

Supported methods are:

- `health` — process readiness.
- `capabilities` — protocol version, available methods/runtimes, and an explicit `executes_commands: false` guarantee.
- `interpret` — deterministic text interpretation using a caller-provided snapshot.

Errors have a stable shape: `{"id":...,"error":{"code":"invalid_params","message":"...","data":...}}`. Current codes are `parse_error`, `invalid_request`, `invalid_params`, and `method_not_found`. JSONL is intentionally used instead of HTTP to keep process lifecycle, local-only access, and embedding simple.

## Integration

A client should construct `DeviceSnapshot` from its own state, send `interpret`, inspect confidence and commands, present a preview/confirmation when appropriate, and only then translate the semantic actions into its own LIFX transport calls.

## Development

```sh
go test ./...
go build ./cmd/lifx-command-engine
```

Tests require no devices or model downloads. Public wire DTOs live in `internal/schema`; before a separate Go library API is promised, clients should treat the JSON protocol as the stable integration surface.

## Future milestones

- Add `ModelInterpreter` using FunctionGemma, followed by a rules-first `HybridInterpreter` with confidence-gated fallback. Both must return the same `CommandPlan` schema.
- Add optional whisper.cpp transcription, starting with an audio file path and adding streaming later.
- Add explicit model download, cache, version, and integrity management.
- Add FunctionGemma training, evaluation, and export commands.
- Consider an optional local HTTP transport without replacing JSONL.

The placeholder `Interpreter` and `Transcriber` interfaces define these extension seams; neither runtime is linked or downloaded today.
