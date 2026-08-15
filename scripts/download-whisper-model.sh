#!/usr/bin/env bash
set -euo pipefail

usage() {
	cat >&2 <<EOF
usage: $0 [MODEL]

Downloads a supported whisper.cpp GGML model using whisper.cpp's official
download helper. MODEL defaults to base.en.

Supported models: tiny.en, base.en, small.en, small

Environment:
  WHISPER_CPP_DIR      whisper.cpp checkout (default: sibling of this project)
  WHISPER_MODELS_DIR   destination directory (default: WHISPER_CPP_DIR/models)

This script is an explicit development/setup helper. Normal builds, tests, and
lifx-command-engine startup never invoke it or download model files.
EOF
}

case "${1:-}" in
	-h|--help) usage; exit 0 ;;
esac

if [ "$#" -gt 1 ]; then
	usage
	exit 2
fi

model=${1:-base.en}
case "$model" in
	tiny.en|base.en|small.en|small) ;;
	*)
		echo "error: unsupported model '$model' (expected tiny.en, base.en, small.en, or small)" >&2
		exit 2
		;;
esac

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
project_dir=$(dirname "$script_dir")
whisper_dir=${WHISPER_CPP_DIR:-$(dirname "$project_dir")/whisper.cpp}
models_dir=${WHISPER_MODELS_DIR:-$whisper_dir/models}
downloader=$whisper_dir/models/download-ggml-model.sh

if [ ! -x "$downloader" ]; then
	echo "error: whisper.cpp downloader is not executable: $downloader" >&2
	echo "set WHISPER_CPP_DIR to a whisper.cpp checkout" >&2
	exit 1
fi

mkdir -p "$models_dir"
"$downloader" "$model" "$models_dir"

model_path=$models_dir/ggml-$model.bin
if [ ! -f "$model_path" ]; then
	echo "error: downloader completed but model was not found: $model_path" >&2
	exit 1
fi

printf 'WHISPER_MODEL=%s\n' "$model_path"
