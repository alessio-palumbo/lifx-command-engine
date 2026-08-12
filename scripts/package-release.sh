#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 VERSION OUTPUT_DIR" >&2
  exit 2
fi

version=$1
output_dir=$2

if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "invalid semantic version tag: $version" >&2
  exit 2
fi

mkdir -p "$output_dir"
rm -f "$output_dir"/*.tar.gz "$output_dir"/checksums.txt

for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
  os=${target%/*}
  arch=${target#*/}
  archive="lifx-command-engine_${version}_${os}_${arch}"
  stage=$(mktemp -d)
  trap 'rm -rf "$stage"' EXIT

  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags="-s -w" -o "$stage/lifx-command-engine" ./cmd/lifx-command-engine
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags="-s -w" -o "$stage/lifx-command-engine-eval" ./cmd/lifx-command-engine-eval
  cp README.md LICENSE THIRD_PARTY_NOTICES.md config.example.json "$stage/"
  tar -C "$stage" -czf "$output_dir/$archive.tar.gz" .
  rm -rf "$stage"
  trap - EXIT
done

(
  cd "$output_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum ./*.tar.gz > checksums.txt
  else
    shasum -a 256 ./*.tar.gz > checksums.txt
  fi
)
