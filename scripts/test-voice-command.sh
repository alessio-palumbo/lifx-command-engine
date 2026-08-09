#!/bin/sh
set -eu

usage() {
	cat >&2 <<EOF
usage: $0 --serial SERIAL --label LABEL [--group GROUP] [--location LOCATION] [--seconds N]

Records a spoken command, transcribes it, and interprets it against a minimal
one-device snapshot. The device flags are not derived from speech: they model
the inventory that a real host such as Hikari or lifx-dash would supply.

  --serial SERIAL       Stable LIFX serial used in the returned command target
  --label LABEL         Spoken/display name, for example "Desk"
  --group GROUP         Optional group name, for example "Office"
  --location LOCATION   Optional location name, for example "Home"
  --seconds N           Recording duration (default: 5)

Example: say "turn desk on" while testing a device labelled Desk.
EOF
  exit 2
}

serial=
label=
group=
location=
seconds=5

while [ "$#" -gt 0 ]; do
  case "$1" in
    --serial) [ "$#" -ge 2 ] || usage; serial=$2; shift 2 ;;
    --label) [ "$#" -ge 2 ] || usage; label=$2; shift 2 ;;
    --group) [ "$#" -ge 2 ] || usage; group=$2; shift 2 ;;
    --location) [ "$#" -ge 2 ] || usage; location=$2; shift 2 ;;
    --seconds) [ "$#" -ge 2 ] || usage; seconds=$2; shift 2 ;;
    -h|--help) usage ;;
    *) usage ;;
  esac
done

if [ -z "$serial" ] || [ -z "$label" ]; then usage; fi
command -v rec >/dev/null 2>&1 || { echo "error: rec is required (brew install sox)" >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "error: jq is required (brew install jq)" >&2; exit 1; }

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
project_dir=$(dirname "$script_dir")
whisper_dir=${WHISPER_CPP_DIR:-$(dirname "$project_dir")/whisper.cpp}
whisper_command=${WHISPER_COMMAND:-$whisper_dir/build/bin/whisper-cli}
whisper_model=${WHISPER_MODEL:-$whisper_dir/models/ggml-small.bin}
engine_binary=${LIFX_COMMAND_ENGINE_BINARY:-/private/tmp/lifx-command-engine}
temp_dir=$(mktemp -d /private/tmp/lifx-command-test.XXXXXX)
audio_file=$temp_dir/audio.wav

cleanup() { rm -rf "$temp_dir"; }
trap cleanup EXIT HUP INT TERM

[ -x "$whisper_command" ] || { echo "error: whisper-cli not executable: $whisper_command" >&2; exit 1; }
[ -f "$whisper_model" ] || { echo "error: whisper model not found: $whisper_model" >&2; exit 1; }

echo "Building lifx-command-engine..." >&2
(cd "$project_dir" && GOCACHE=${GOCACHE:-/private/tmp/lifx-command-engine-go-cache} go build -o "$engine_binary" ./cmd/lifx-command-engine)

echo "Recording for $seconds seconds. Speak a command now..." >&2
rec -q -c 1 -r 16000 -b 16 "$audio_file" trim 0 "$seconds"

transcribe_request=$(jq -cn --arg path "$audio_file" '{id:"speech-test",method:"transcribe",params:{audio_path:$path,language:"en"}}')
transcribe_response=$(printf '%s\n' "$transcribe_request" | "$engine_binary" \
  -whisper-command "$whisper_command" \
  -whisper-model "$whisper_model" \
  -whisper-arg=-ng)

text=$(printf '%s\n' "$transcribe_response" | jq -er '.result.text | select(length > 0)') || {
  echo "Transcription failed:" >&2
  printf '%s\n' "$transcribe_response" | jq . >&2
  exit 1
}
echo "Transcript: $text" >&2

interpret_request=$(jq -cn \
  --arg text "$text" --arg serial "$serial" --arg label "$label" \
  --arg group "$group" --arg location "$location" \
  '{id:"interpret-test",method:"interpret",params:{text:$text,snapshot:{locations:[],groups:[],devices:[{serial:$serial,label:$label,group:$group,location:$location}]}}}')

printf '%s\n' "$interpret_request" | "$engine_binary" | jq .
