#!/usr/bin/env bash
set -euo pipefail

usage() {
	cat >&2 <<EOF
usage: $0 --manifest FILE [--model MODEL_OR_PATH ...] [options]

Transcribes every WAV in a JSONL manifest with each selected whisper.cpp model
and writes one JSON result per model/sample pair to stdout. This never sends a
command to a LIFX device.

Manifest rows:
  {"audio_path":"audio/turn-tv-off.wav","expected":"turn tv off"}

Options:
  --manifest FILE       JSONL corpus; relative audio paths use its directory
  --model VALUE         Model name or path; repeat to compare models
                        Names resolve to WHISPER_MODELS_DIR/ggml-NAME.bin
                        (default: base.en)
  --language CODE       Input language sent to the engine (default: en)
  --whisper-arg VALUE   Extra whisper-cli argument; repeat as needed
  --engine PATH         Existing lifx-command-engine binary (built if omitted)
  -h, --help            Show this help

Environment:
  WHISPER_CPP_DIR, WHISPER_MODELS_DIR, WHISPER_COMMAND, GOCACHE

Examples:
  $0 --manifest testdata/voice/manifest.jsonl \
    --model tiny.en --model base.en --model small.en

  $0 --manifest testdata/voice/manifest.jsonl --model small \
    --language en --whisper-arg=-ng \
    --whisper-arg=--prompt --whisper-arg='LIFX names: TV, Desk, Moon'
EOF
}

manifest=
language=en
engine_binary=
declare -a models=()
declare -a whisper_args=()

while [ "$#" -gt 0 ]; do
	case "$1" in
		--manifest) [ "$#" -ge 2 ] || { usage; exit 2; }; manifest=$2; shift 2 ;;
		--model) [ "$#" -ge 2 ] || { usage; exit 2; }; models+=("$2"); shift 2 ;;
		--language) [ "$#" -ge 2 ] || { usage; exit 2; }; language=$2; shift 2 ;;
		--whisper-arg) [ "$#" -ge 2 ] || { usage; exit 2; }; whisper_args+=("$2"); shift 2 ;;
		--whisper-arg=*) whisper_args+=("${1#*=}"); shift ;;
		--engine) [ "$#" -ge 2 ] || { usage; exit 2; }; engine_binary=$2; shift 2 ;;
		-h|--help) usage; exit 0 ;;
		*) echo "error: unknown argument: $1" >&2; usage; exit 2 ;;
	esac
done

[ -n "$manifest" ] || { echo "error: --manifest is required" >&2; usage; exit 2; }
[ -f "$manifest" ] || { echo "error: manifest not found: $manifest" >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "error: jq is required" >&2; exit 1; }

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
project_dir=$(dirname "$script_dir")
whisper_dir=${WHISPER_CPP_DIR:-$(dirname "$project_dir")/whisper.cpp}
models_dir=${WHISPER_MODELS_DIR:-$whisper_dir/models}
whisper_command=${WHISPER_COMMAND:-$whisper_dir/build/bin/whisper-cli}
manifest_dir=$(CDPATH='' cd -- "$(dirname -- "$manifest")" && pwd)

[ -x "$whisper_command" ] || { echo "error: whisper-cli not executable: $whisper_command" >&2; exit 1; }

if [ "${#models[@]}" -eq 0 ]; then
	models=(base.en)
fi

temp_dir=$(mktemp -d "${TMPDIR:-/tmp}/lifx-command-engine-voice-eval.XXXXXX")
cleanup() { rm -rf "$temp_dir"; }
trap cleanup EXIT HUP INT TERM

if [ -z "$engine_binary" ]; then
	engine_binary=$temp_dir/lifx-command-engine
	(cd "$project_dir" && GOCACHE=${GOCACHE:-/private/tmp/lifx-command-engine-go-cache} go build -o "$engine_binary" ./cmd/lifx-command-engine)
fi
[ -x "$engine_binary" ] || { echo "error: engine is not executable: $engine_binary" >&2; exit 1; }

resolve_model() {
	case "$1" in
		*/*|*.bin) printf '%s\n' "$1" ;;
		*) printf '%s/ggml-%s.bin\n' "$models_dir" "$1" ;;
	esac
}

normalize() {
	jq -Rn --arg text "$1" '$text | ascii_downcase | gsub("[^a-z0-9]+"; " ") | gsub("^ +| +$"; "")'
}

line_number=0
while IFS= read -r row || [ -n "$row" ]; do
	line_number=$((line_number + 1))
	[ -n "$row" ] || continue
	if ! audio=$(printf '%s\n' "$row" | jq -er '.audio_path | strings | select(length > 0)'); then
		echo "error: manifest line $line_number requires a non-empty audio_path" >&2
		exit 1
	fi
	if ! expected=$(printf '%s\n' "$row" | jq -er '.expected | strings'); then
		echo "error: manifest line $line_number requires an expected string" >&2
		exit 1
	fi
	case "$audio" in
		/*) audio_path=$audio ;;
		*) audio_path=$manifest_dir/$audio ;;
	esac

	for model in "${models[@]}"; do
		model_path=$(resolve_model "$model")
		if [ ! -f "$audio_path" ] || [ ! -f "$model_path" ]; then
			missing="audio"
			[ -f "$audio_path" ] || missing="audio file not found: $audio_path"
			[ -f "$model_path" ] || missing="model file not found: $model_path"
			jq -cn --arg model "$model" --arg audio_path "$audio" --arg expected "$expected" --arg error "$missing" \
				'{model:$model,audio_path:$audio_path,expected:$expected,error:$error}'
			continue
		fi

		request=$(jq -cn --arg path "$audio_path" --arg language "$language" \
			'{id:"voice-eval",method:"transcribe",params:{audio_path:$path,language:$language}}')
		declare -a command=("$engine_binary" -whisper-command "$whisper_command" -whisper-model "$model_path")
		for arg in "${whisper_args[@]}"; do command+=(-whisper-arg "$arg"); done
		response=$(printf '%s\n' "$request" | "${command[@]}")
		if transcript=$(printf '%s\n' "$response" | jq -er '.result.text | strings'); then
			expected_normalized=$(normalize "$expected")
			transcript_normalized=$(normalize "$transcript")
			jq -cn --arg model "$model" --arg model_path "$model_path" \
				--arg audio_path "$audio" --arg expected "$expected" --arg transcript "$transcript" \
				--argjson expected_normalized "$expected_normalized" --argjson transcript_normalized "$transcript_normalized" \
				'{model:$model,model_path:$model_path,audio_path:$audio_path,expected:$expected,transcript:$transcript,normalized_match:($expected_normalized == $transcript_normalized)}'
		else
			error=$(printf '%s\n' "$response" | jq -c '.error // {code:"invalid_response",message:"transcriber returned no text"}')
			jq -cn --arg model "$model" --arg audio_path "$audio" --arg expected "$expected" --argjson error "$error" \
				'{model:$model,audio_path:$audio_path,expected:$expected,error:$error}'
		fi
	done
done < "$manifest"
