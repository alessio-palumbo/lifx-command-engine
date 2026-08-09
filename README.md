# lifx-command-engine

`lifx-command-engine` is a lightweight local sidecar that turns text into structured LIFX command plans. It does **not** discover devices, access the LAN, or execute commands. A host supplies a device snapshot, validates and previews the returned plan, asks for confirmation, and owns transport.

The first milestone uses the deterministic parser from [`lifxlan-go/pkg/command`](https://github.com/alessio-palumbo/lifxlan-go/tree/main/pkg/command). Model and speech runtimes are optional future extensions.

## Protocol

Run `go run ./cmd/lifx-command-engine`. Send one JSON request per line on stdin; receive one JSON response per line on stdout. IDs may be strings or numbers and are echoed unchanged. Requests may include `"protocol_version":"1"`; omitted versions currently mean version 1. Unknown fields, unsupported versions, and lines larger than 1 MiB are rejected.

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
- `transcribe` — optional audio-file transcription when whisper.cpp is configured.

Errors have a stable shape: `{"id":...,"error":{"code":"invalid_params","message":"...","data":...}}`. Current codes are `parse_error`, `invalid_request`, `invalid_params`, `method_not_found`, `method_unavailable`, `request_too_large`, `transcription_failed`, and `unsupported_protocol_version`. JSONL is intentionally used instead of HTTP to keep process lifecycle, local-only access, and embedding simple.

## Integration

A client should construct `DeviceSnapshot` from its own state, send `interpret`, inspect confidence and commands, present a preview/confirmation when appropriate, and only then translate the semantic actions into its own LIFX transport calls.

## Development

```sh
go test ./...
go build ./cmd/lifx-command-engine
```

Tests require no devices or model downloads. Public wire DTOs live in `internal/schema`; before a separate Go library API is promised, clients should treat the JSON protocol as the stable integration surface.

## Future milestones

- Add a maintained FunctionGemma runtime adapter and domain fine-tuning/evaluation workflow; the runtime-neutral model and hybrid interpreter boundary is now available.
- Add streaming transcription and long-lived whisper.cpp runtime support; audio-file transcription is now available.
- Add explicit model download, cache, version, and integrity management.
- Add FunctionGemma training, evaluation, and export commands.
- Consider an optional local HTTP transport without replacing JSONL.

The placeholder `Interpreter` and `Transcriber` interfaces define these extension seams; no model runtime is linked or downloaded.

## Optional model fallback

Pass `-model-command /path/to/runtime` to enable the rules-first hybrid interpreter. Arguments can be added with repeatable `-model-arg value` flags. The command is invoked directly without a shell, receives one model request as JSON on stdin, and must return exactly one CommandPlan JSON object on stdout. This supports a local FunctionGemma runner implemented with Transformers, LiteRT-LM, llama.cpp, MLX, or another compatible runtime without linking it into the Go service.

The rule parser returns immediately at high confidence. Lower-confidence input is offered to the configured model command. Every model plan is strictly decoded, range checked, constrained to serials in the caller's snapshot, normalized with trusted snapshot metadata, and marked as requiring confirmation. If the runtime is missing or fails, the original rule plan is returned with a `model fallback unavailable` confidence reason.

The engine never downloads a model. FunctionGemma model selection, licensing, fine-tuned weights, tokenizer/chat-template handling, and caches belong to the configured runtime. A runtime should use FunctionGemma's required developer instruction and tool-calling format, then translate its proposed call into the CommandPlan contract supplied in the request's `output_schema` field.

[`testdata/functiongemma-eval.jsonl`](testdata/functiongemma-eval.jsonl) contains the initial runtime/fine-tune evaluation cases, including style language, implicit targets, ambiguity, multi-target commands, unknown targets, and irrelevant requests.

## Optional speech-to-text

Build or install `whisper-cli`, provide an existing local model, and enable transcription explicitly:

```sh
go run ./cmd/lifx-command-engine \
  -whisper-command /path/to/whisper-cli \
  -whisper-model /path/to/ggml-base.en.bin
```

The engine does not download whisper.cpp or model weights. When configured, `capabilities` advertises `transcription: true` and the `transcribe` method. Input is an audio file path owned by the caller:

```json
{"id":"speech-1","method":"transcribe","params":{"audio_path":"/path/to/command.wav","language":"en"}}
```

The versioned result contains normalized text, detected language, and timestamped segments. Audio paths must resolve to regular files. The executable is invoked directly without a shell, JSON is read from an isolated temporary output directory with a size bound, cancellation terminates the child process, and device interpretation remains a separate `interpret` request so hosts can preview each stage independently. Extra whisper.cpp arguments can be supplied with repeatable `-whisper-arg value` flags; for example, use `-whisper-arg=-ng` when whisper.cpp must run without a GPU backend.
