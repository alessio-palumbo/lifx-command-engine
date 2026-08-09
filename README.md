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

- Add domain fine-tuning/export workflows; the Transformers FunctionGemma runtime and evaluation boundary are available.
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

### FunctionGemma Transformers runner

The maintained optional runner lives in `runtimes/functiongemma`. It expects an existing local FunctionGemma model directory and defaults to offline loading:

```sh
python3 -m venv /private/tmp/lifx-functiongemma-venv
/private/tmp/lifx-functiongemma-venv/bin/pip install \
  -r runtimes/functiongemma/requirements.txt

go run ./cmd/lifx-command-engine \
  -model-command "$PWD/runtimes/functiongemma/runner.py" \
  -model-arg=--model \
  -model-arg=/path/to/functiongemma-model
```

The runner uses the model's Transformers chat template and the `propose_lifx_plan` function declaration. It accepts model contract version 1, emits one CommandPlan on stdout, and writes failures to stderr. `--device` accepts `auto`, `cpu`, `cuda`, or `mps`; pass it using additional `-model-arg` flags. The optional runner flag `--allow-download` lets Transformers fetch missing files, but is deliberately disabled by default. Model licensing and access remain the user's responsibility.

Run its dependency-free parser/contract tests with:

```sh
python3 -m unittest discover -s runtimes/functiongemma -p 'test_*.py' -v
```

### Interpreter evaluation

The evaluation CLI reads versioned JSONL fixtures, runs the selected interpreter, prints a machine-readable report, and exits 1 when any case fails:

```sh
# Establish the deterministic rules baseline.
go run ./cmd/lifx-command-engine-eval -mode rules

# Evaluate FunctionGemma directly.
go run ./cmd/lifx-command-engine-eval \
  -mode model \
  -model-command "$PWD/runtimes/functiongemma/runner.py" \
  -model-arg=--model \
  -model-arg=/path/to/functiongemma-model

# Evaluate the production rules-first fallback path.
go run ./cmd/lifx-command-engine-eval \
  -mode hybrid \
  -model-command "$PWD/runtimes/functiongemma/runner.py" \
  -model-arg=--model \
  -model-arg=/path/to/functiongemma-model
```

Reports include per-case failures, target and action accuracy, invalid plans, runtime errors, fallback eligibility/use, average latency, and p95 latency. The initial rules baseline is expected to fail the style, implicit-target, and movie-language cases; those failures define the value expected from a domain-tuned model. Override the corpus with `-fixtures` and the overall deadline with `-timeout`.

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

For a microphone-to-command-plan smoke test, install SoX and jq, then run:

```sh
brew install sox jq
./scripts/test-voice-command.sh \
  --serial d073d5000001 \
  --label Desk \
  --group Office \
  --location Home
```

The `--serial`, `--label`, `--group`, and `--location` flags do not come from the recording. They construct a minimal one-device snapshot representing information that a real host such as Hikari or lifx-dash already owns. Speech supplies a phrase such as “turn desk on”; the snapshot tells the engine that `Desk` exists, how it is grouped, and which stable serial the resulting command should target. The serial must therefore be a real 12-character LIFX serial, while group and location are optional.

The script records for five seconds, transcribes the temporary WAV, feeds the transcript into `interpret` with that snapshot, prints the resulting CommandPlan, and removes the recording. It intentionally models only one device to keep the smoke test small; multi-device inventory testing belongs in host integration tests. The script uses the sibling `whisper.cpp` checkout and `ggml-small.bin` by default. Override them with `WHISPER_COMMAND`, `WHISPER_MODEL`, or `WHISPER_CPP_DIR`; use `--seconds N` to change recording time. Run `./scripts/test-voice-command.sh --help` for flag descriptions.
