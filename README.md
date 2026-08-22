# lifx-command-engine

[![CI](https://github.com/alessio-palumbo/lifx-command-engine/actions/workflows/ci.yml/badge.svg)](https://github.com/alessio-palumbo/lifx-command-engine/actions/workflows/ci.yml)

`lifx-command-engine` is a lightweight local sidecar that turns text into structured LIFX command plans. It does **not** discover devices, access the LAN, or execute commands. A host supplies a device snapshot, validates and previews the returned plan, asks for confirmation, and owns transport.

The deterministic parser from [`lifxlan-go/pkg/command`](https://github.com/alessio-palumbo/lifxlan-go/tree/main/pkg/command) remains the baseline. Model and speech runtimes are optional extensions and are never required for rule-only use.

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
          "location": "Home",
          "has_color": true,
          "min_kelvin": 2500,
          "max_kelvin": 9000,
          "current_state": {
            "power": true,
            "hue": 0,
            "saturation": 0,
            "brightness": 60,
            "kelvin": 3500
          }
        }
      ]
    }
  }
}
```

The response contains `confidence`, an explainable `confidence_result`, `needs_confirmation`, a display `summary`, and semantic `commands`. Each target includes its serial as the stable host execution identity. Actions use human-scale values: hue in degrees, saturation/brightness in percent, Kelvin, and duration in milliseconds.

`current_state` is optional for absolute commands but its property values are required for relative operations such as `brighter`, `dim`, `warmer`, `cooler`, `softer`, and `richer`. Hosts should provide their latest known color values and omit unknown color fields instead of substituting zero. Power follows lifxlan-go's state convention: omitted or false means off. A visual color or brightness action therefore adds `power:true` for an off device, while an already-on device receives no redundant power action. Explicit `off` always wins.

A single contextual percentage means brightness, so `turn desk warm white at 35%` sets warm white at 35%; `turn` and `at` are filler words. Mixed-state groups and relative group commands may produce separate commands per device because their required power or resulting property values differ. Other supported compact forms include named and styled colors, white-temperature phrases, durations, and sequential commands separated by punctuation or `then`.

Supported methods are:

- `health` — process readiness.
- `capabilities` — protocol version, available methods/runtimes, and an explicit `executes_commands: false` guarantee.
- `interpret` — deterministic text interpretation using a caller-provided snapshot.
- `transcribe` — optional audio-file transcription when whisper.cpp is configured.

Errors have a stable shape: `{"id":...,"error":{"code":"invalid_params","message":"...","data":...}}`. Current codes are `parse_error`, `invalid_request`, `invalid_params`, `method_not_found`, `method_unavailable`, `request_too_large`, `transcription_failed`, and `unsupported_protocol_version`. JSONL is intentionally used instead of HTTP to keep process lifecycle, local-only access, and embedding simple.

## Integration

A client should construct `DeviceSnapshot` from its own state, send `interpret`, inspect confidence and commands, present a preview/confirmation when appropriate, and only then translate the semantic actions into its own LIFX transport calls.

Interpretation confidence measures how clearly text resolved into a plan, not how consequential execution may be. An exact device, group, location, or `all` selector therefore receives no penalty solely for resolving to multiple devices. Hosts should apply their own scope and safety policy—for example, Hikari may still confirm `turn off all` even when the engine reports a high-confidence interpretation. The engine reserves `needs_confirmation` for interpretation uncertainty such as ambiguous labels, multiple parsed commands, nondeterministic actions, unsupported style language, and model-generated plans.

### Go sidecar client

The public `client` package owns sidecar startup, JSONL framing, concurrent request correlation, context cancellation, crash detection, and clean shutdown. Its DTOs mirror protocol version 1 without exposing internal engine packages:

```go
sidecar, err := client.New(client.Config{
    Path:           "/path/to/lifx-command-engine",
    Args:           []string{"serve", "-config", "/path/to/config.json"},
    RestartOnCrash: true,
})
if err != nil {
    return err
}
defer sidecar.Close()

if err := sidecar.Start(ctx); err != nil {
    return err
}
plan, err := sidecar.Interpret(ctx, client.InterpretInput{
    Text:     "turn desk on",
    Snapshot: snapshot,
})
```

Typed methods are available for `Health`, `Capabilities`, `Interpret`, and `Transcribe`. `TranscribeAndInterpret` returns both stages for microphone workflows, but never validates against live state, requests confirmation, or executes the resulting plan. Those responsibilities remain with Hikari, lifx-dash, or another host.

When enabled, restart occurs on the next request after a crash. Failed requests are never automatically replayed, avoiding duplicate or stale host actions. Each call accepts a context; cancellation stops waiting and safely discards its eventual response without disrupting other callers. Protocol errors can be inspected with `errors.As(err, *client.APIError)`.

Run the standalone example against a built sidecar:

```sh
go build -o /private/tmp/lifx-command-engine ./cmd/lifx-command-engine
go run ./examples/go-client -engine /private/tmp/lifx-command-engine \
  -text "turn desk on"
```

## Development

```sh
go test ./...
go build ./cmd/lifx-command-engine
```

Tests require no devices or model downloads. Public wire DTOs live in `internal/schema`; before a separate Go library API is promised, clients should treat the JSON protocol as the stable integration surface.

CI runs on Ubuntu and macOS and verifies module tidiness, vet, race-enabled Go tests, both command builds, the deterministic parser evaluation corpus, and dependency-free FunctionGemma contract tests. It never downloads model weights or optional runtimes.

## Releases

Pushing a semantic version tag such as `v0.2.0` runs validation and publishes rule-only archives for macOS and Linux on amd64 and arm64:

```sh
git tag -a v0.2.0 -m "v0.2.0"
git push origin v0.2.0
```

Each archive contains `lifx-command-engine`, `lifx-command-engine-eval`, the example configuration, README, MIT license, and third-party notices. A `checksums.txt` file contains SHA-256 hashes. Releases deliberately exclude Python, FunctionGemma weights, whisper.cpp, and speech models.

The same packages can be built locally without publishing:

```sh
./scripts/package-release.sh v0.2.0 /private/tmp/lifx-command-engine-release
```

Windows is not yet included because sidecar lifecycle and packaging have not been exercised on Windows CI.

## Configuration and diagnostics

Running the binary without a subcommand remains equivalent to `serve` and starts the JSONL loop. Runtime flags can instead be stored in a versioned JSON configuration based on [`config.example.json`](config.example.json):

```sh
go run ./cmd/lifx-command-engine serve -config /path/to/config.json
```

Paths are used exactly as written, which keeps configuration behavior independent of the current working directory. Explicit runtime flags override their corresponding configuration fields; repeatable `-model-arg` or `-whisper-arg` flags replace that argument list when supplied.

The read-only `doctor` command checks the deterministic interpreter, configured executables, FunctionGemma model directory, Python `torch`/`transformers` imports, whisper.cpp model file and selected CLI/server mode, and configuration consistency. It does not load a Whisper model or start whisper-server. Optional unconfigured runtimes are warnings, while broken configured runtimes produce a failing exit status:

```sh
go run ./cmd/lifx-command-engine doctor -config /path/to/config.json
```

Output is JSON so host installers and packaging scripts can consume it.

## Optional model installation

List models known to the tooling without accessing the network:

```sh
go run ./cmd/lifx-command-engine models list
```

Installation is always explicit. The initial source is KaggleHub, matching FunctionGemma's Kaggle distribution:

```sh
/path/to/python -m pip install kagglehub
go run ./cmd/lifx-command-engine models install functiongemma-270m-it \
  -source kaggle \
  -python /path/to/python
```

By default KaggleHub uses its own cache. Add `-output /path/to/models` for an explicit destination and `-timeout 1h` for a slower connection. Kaggle authentication, access consent, and licensing remain under the user's Kaggle account. The installer pins the catalogued revision, computes SHA-256 for every downloaded file, and writes `.lifx-command-engine-model.json` inside the resulting model directory. Its JSON result includes the resolved path for use in the runtime configuration. Normal startup never invokes KaggleHub or downloads anything. See the [official KaggleHub model download documentation](https://github.com/Kaggle/kagglehub#download-model).

## Future milestones

- Add domain fine-tuning/export workflows; the Transformers FunctionGemma runtime and evaluation boundary are available.
- Add streaming transcription; persistent and per-request audio-file transcription are available.
- Add explicit model download, cache, version, and integrity management.
- Add FunctionGemma training, evaluation, and export commands.
- Consider an optional local HTTP transport without replacing JSONL.

The placeholder `Interpreter` and `Transcriber` interfaces define these extension seams; no model runtime is linked or downloaded.

## License

The original source code is available under the [MIT License](LICENSE). Optional model weights and third-party runtimes retain their own terms; see [third-party notices](THIRD_PARTY_NOTICES.md). In particular, FunctionGemma weights are not MIT-licensed and are not distributed by this repository.

## Optional model fallback

Pass `-model-command /path/to/runtime` to enable the rules-first hybrid interpreter. Arguments can be added with repeatable `-model-arg value` flags. By default, the command is invoked directly once per fallback, receives one request as JSON on stdin, and returns one CommandPlan on stdout. This generic boundary supports local runtimes implemented with Transformers, LiteRT-LM, llama.cpp, MLX, or another compatible framework without linking it into the Go service.

Add `-model-persistent` for runtimes that implement the version 1 persistent protocol. The engine appends `--serve`, waits for a readiness handshake, then exchanges JSONL requests and response envelopes over the same process. Requests are serialized; cancellation or a process/protocol failure discards the child so the next fallback starts a clean runtime. The maintained FunctionGemma runner supports both modes.

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
  -model-command /private/tmp/lifx-functiongemma-venv/bin/python \
  -model-persistent \
  -model-arg="$PWD/runtimes/functiongemma/runner.py" \
  -model-arg=--model \
  -model-arg=/path/to/functiongemma-model
```

The runner uses the model's Transformers chat template and a flat, typed `set_lifx_state` function declaration. The model selects semantic inventory entries such as `device:Desk`, `group:Office`, or `location:Home`; the runner resolves them to serials from the supplied snapshot. An explicit device label in the request takes precedence over a broader model-selected group or location. A deterministic relevance gate rejects requests with neither a known selector nor lighting-specific language before inference.

The runner also applies a conservative action policy to model calls. Power-only requests stay power-only, explicit brightness percentages are preserved, and action fields unsupported by the wording are removed. The initial style vocabulary defines `cozy` as warm white at 35% (`2700K`, zero saturation) and `movie` as 20% brightness; these are product semantics rather than unconstrained model guesses. All model plans still require host confirmation, and Go performs final target/range/schema validation. The runner accepts model contract version 1, emits one CommandPlan in one-shot mode, or serves versioned JSONL envelopes after a readiness handshake in persistent mode. Failures are written to stderr or returned as per-request error envelopes.

`--device` accepts `auto`, `cpu`, `cuda`, or `mps`; pass it using additional `-model-arg` flags. `--debug` writes the untouched model generation to stderr for direct runner diagnostics. The optional `--allow-download` flag lets Transformers fetch missing files, but is deliberately disabled by default. Model licensing and access remain the user's responsibility.

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
  -model-persistent \
  -model-command /private/tmp/lifx-functiongemma-venv/bin/python \
  -model-arg="$PWD/runtimes/functiongemma/runner.py" \
  -model-arg=--model \
  -model-arg=/path/to/functiongemma-model

# Evaluate the production rules-first fallback path.
go run ./cmd/lifx-command-engine-eval \
  -mode hybrid \
  -model-persistent \
  -model-command /private/tmp/lifx-functiongemma-venv/bin/python \
  -model-arg="$PWD/runtimes/functiongemma/runner.py" \
  -model-arg=--model \
  -model-arg=/path/to/functiongemma-model
```

Reports include per-case failures, target and action accuracy, invalid plans, runtime errors, fallback eligibility/use, average latency, and p95 latency. Action expectations are exact: unsolicited action fields fail a case. The corpus uses multi-device snapshots so a model cannot pass target selection by choosing the only available light. The initial rules baseline is expected to fail the style, implicit-target, and movie-language cases; those failures define the value expected from the model. Persistent evaluation includes startup in the first fallback's latency and reuses the loaded model afterward. Override the corpus with `-fixtures` and the overall deadline with `-timeout`.

The separate `testdata/rule-parser-eval.jsonl` corpus protects richer deterministic behavior, including contextual brightness percentages, inferred power-on, styled colors, white temperatures, minute durations, current-state-relative changes, per-device group results, and sequential commands:

```sh
go run ./cmd/lifx-command-engine-eval \
  -mode rules \
  -fixtures testdata/rule-parser-eval.jsonl
```

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

The versioned result contains normalized text, detected language, and timestamped segments. Audio paths must resolve to regular files. The executable is invoked directly without a shell, JSON is read from an isolated temporary output directory with a size bound, cancellation terminates the child process, and device interpretation remains a separate `interpret` request so hosts can preview each stage independently.

### Persistent whisper-server mode

The default mode above starts `whisper-cli` once per request. To keep a model loaded between requests, explicitly configure `whisper-server` instead:

```sh
go run ./cmd/lifx-command-engine \
  -whisper-command /path/to/whisper-server \
  -whisper-model /path/to/ggml-tiny.en.bin \
  -whisper-persistent \
  -whisper-arg=-bo \
  -whisper-arg=1 \
  -whisper-arg=-bs \
  -whisper-arg=1 \
  -whisper-arg=-nf \
  -whisper-arg=-ac \
  -whisper-arg=768
```

Equivalent configuration:

```json
{
  "schema_version": "1",
  "whisper": {
    "command": "/path/to/whisper-server",
    "model_path": "/path/to/ggml-tiny.en.bin",
    "persistent": true,
    "args": ["-bo", "1", "-bs", "1", "-nf", "-ac", "768"]
  }
}
```

Persistent mode loads the model and waits for the inference endpoint before the JSONL service becomes ready, so host startup timeouts must allow for model loading on the target hardware. The engine selects an ephemeral port, binds whisper-server only to `127.0.0.1`, serializes inference requests, and sends `verbose_json` requests to `/inference`. It streams the WAV into the outgoing multipart request rather than first loading the complete file into Go memory; current whisper-server versions may still buffer the upload internally.

If the server crashes, the active transcription fails and is never replayed. The following transcription request makes one bounded startup attempt sequence and then either uses the new process or returns a runtime error. There is no automatic fallback to whisper-cli because `whisper-server` and `whisper-cli` are different executables. Closing the engine, including normal stdin closure, stops and reaps its server process.

Cancelling a transcription closes the local HTTP request immediately. Compatible current whisper-server versions observe that disconnect and abort inference; older or modified builds may continue computation internally. Persistent mode requires a whisper-server compatible with `OPTIONS /inference`, multipart `POST /inference`, request-level `language`, and `response_format=verbose_json`.

The engine manages model, host, port, endpoint, audio file, language, response format and timestamps. Persistent configuration therefore rejects their corresponding command-line flags, including `--model`, `--host`, `--port`, `--request-path`, `--inference-path`, `--convert`, `--language`, `--file`, output flags, and `--no-timestamps`, including `--flag=value` forms. Decoder tuning, thread, prompt and GPU options remain configurable.

Persistent mode preserves the existing `transcribe` request and result schemas and advertises the same transcription capability as CLI mode. It still accepts finalized audio files; microphone streaming, VAD-driven incremental audio and partial transcripts are separate future protocol work.

### Model setup and selection

Model installation is explicit and never occurs during a build, test, or normal engine startup. With a local whisper.cpp checkout, use its official downloader through the repository helper:

```sh
# Defaults to base.en.
./scripts/download-whisper-model.sh

# Other supported comparison candidates.
./scripts/download-whisper-model.sh tiny.en
./scripts/download-whisper-model.sh small.en
./scripts/download-whisper-model.sh small

# Override both the checkout and model destination.
WHISPER_CPP_DIR=/path/to/whisper.cpp \
WHISPER_MODELS_DIR=/path/to/models \
  ./scripts/download-whisper-model.sh base.en
```

The practical choice depends on the microphone, hardware, accent, background noise, and real device names, so evaluate on the target system:

- `tiny.en` is the fastest and smallest English baseline, but is generally the most error-prone.
- `base.en` is the recommended initial voice-build candidate, balancing latency and short-command accuracy.
- `small.en` is the primary higher-accuracy English comparison, with greater memory use and latency.
- `small` is multilingual and useful when commands may not be English or when experimenting with language detection or translation.

Neither the helper nor release archives redistribute whisper.cpp or its models. Their upstream licenses and terms continue to apply.

### Runtime arguments

Extra whisper.cpp arguments can be supplied with repeatable `-whisper-arg` flags. Use an equals sign when a value begins with a dash:

```sh
go run ./cmd/lifx-command-engine \
  -whisper-command /path/to/whisper-cli \
  -whisper-model /path/to/ggml-base.en.bin \
  -whisper-arg=-ng \
  -whisper-arg=--prompt \
  -whisper-arg="LIFX names: TV, Desk, Kitchen, Moon, Wall Tiles"
```

Here `-ng` disables GPU use and `--prompt` gives Whisper spelling/context hints for unusual LIFX labels. The engine owns arguments needed for its output contract, including the audio path, JSON output, and language. Set the input language using the request's `"language":"en"` property rather than passing `--language en` through `-whisper-arg`; this avoids conflicting command-line values. Omit the request language or use `"auto"` for detection when evaluating multilingual input.

Hikari's optional `HIKARI_WHISPER_ARGS` setting is host-side configuration: Hikari translates its values into repeated sidecar `-whisper-arg` arguments. It should be used for options such as `-ng` and `--prompt`, while the transcribe request carries the language.

### Voice evaluation

[`testdata/voice/phrases.txt`](testdata/voice/phrases.txt) provides a starter set of short LIFX phrases. Record the phrases as mono 16 kHz WAV files, copy [`testdata/voice/manifest.example.jsonl`](testdata/voice/manifest.example.jsonl), and update each `audio_path` to its recording. Relative paths are resolved from the manifest directory.

Compare the same recordings across models with:

```sh
./scripts/evaluate-whisper-models.sh \
  --manifest /path/to/voice-corpus/manifest.jsonl \
  --model tiny.en \
  --model base.en \
  --model small.en \
  --whisper-arg=-ng > /private/tmp/whisper-results.jsonl

jq . /private/tmp/whisper-results.jsonl
```

Each JSONL result includes the model, audio path, expected text, transcript, and a case/punctuation-insensitive `normalized_match`, or a structured runtime/setup error. This evaluates transcription only and never constructs or executes a device command. A correctly transcribed phrase may still use an action that the deterministic command parser does not support; assess `transcribe` and `interpret` separately when diagnosing failures.

The starter names are deliberately generic. Add recordings containing actual device labels, group names, and location names from the intended installation, especially easily confused names and names with unusual spelling. Do not put device serials into speech samples: those belong to the host-provided snapshot.

The evaluator uses the sibling whisper.cpp checkout and its `models` directory by default. Override these with `WHISPER_CPP_DIR`, `WHISPER_MODELS_DIR`, or `WHISPER_COMMAND`. Pass an explicit `.bin` path to `--model` when a model is stored elsewhere.

### Live voice smoke test

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
