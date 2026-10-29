#!/usr/bin/env bash
set -euo pipefail

script_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
workspace_root="$(cd -- "${script_directory}/.." && pwd -P)"
output_directory="${workspace_root}/bin"
export GOWORK="${GOWORK:-off}"
go_binary="${GO:-${GOROOT:+${GOROOT}/bin/go}}"

if [[ "$(uname -s)" != "Linux" ]]; then
	printf 'This script must run on Linux.\n' >&2
	exit 1
fi

if [[ "${go_binary}" != */* ]]; then
	go_binary="$(command -v "${go_binary}" || true)"
fi

if [[ ! -x "${go_binary}" && -z "${GO:-}" && -z "${GOROOT:-}" ]]; then
	go_binary="$(command -v go || true)"
fi

if [[ -z "${go_binary}" || ! -x "${go_binary}" ]]; then
	printf 'Go was not found. Add it to PATH or set GO=/path/to/go (or GOROOT=/path/to/go-root).\n' >&2
	exit 1
fi

mkdir -p "${output_directory}"
cd "${workspace_root}"

printf 'Building local Linux lune and lune-sandbox...\n'
"${go_binary}" build -trimpath -o "${output_directory}/lune-sandbox" github.com/afterlune/luneCore/cmd/lune-sandbox
"${go_binary}" build -trimpath -o "${output_directory}/lune" ./cmd

for architecture in amd64 arm64; do
	target_directory="${output_directory}/linux-${architecture}"
	mkdir -p "${target_directory}"
	printf 'Building Linux %s lune and lune-sandbox...\n' "${architecture}"
	GOOS=linux GOARCH="${architecture}" CGO_ENABLED=0 "${go_binary}" build -trimpath -o "${target_directory}/lune-sandbox" github.com/afterlune/luneCore/cmd/lune-sandbox
	GOOS=linux GOARCH="${architecture}" CGO_ENABLED=0 "${go_binary}" build -trimpath -o "${target_directory}/lune" ./cmd
done

printf 'Built local binaries in %s and Linux amd64/arm64 binaries under %s.\n' "${output_directory}" "${output_directory}"
