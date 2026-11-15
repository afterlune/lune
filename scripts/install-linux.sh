#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Linux" ]]; then
	printf 'This script must run on Linux.\n' >&2
	exit 1
fi

: "${HOME:?HOME is not set; cannot choose a per-user install directory.}"

script_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
workspace_root="$(cd -- "${script_directory}/.." && pwd -P)"
source_directory="${workspace_root}/bin"
source_app="${source_directory}/lune"
source_helper="${source_directory}/lune-sandbox"

for source_path in "${source_app}" "${source_helper}"; do
	if [[ ! -f "${source_path}" ]]; then
		printf 'Missing build output: %s. Run scripts/build-linux.sh first.\n' "${source_path}" >&2
		exit 1
	fi
done

install_directory="${HOME}/.local/bin"
config_root="${XDG_CONFIG_HOME:-${HOME}/.config}"
config_directory="${config_root}/lune"
config_path="${config_directory}/config.json"

install -d -m 0755 "${install_directory}"
install -d -m 0700 "${config_directory}"
chmod 0700 "${config_directory}"
install -m 0755 "${source_app}" "${install_directory}/lune"
install -m 0755 "${source_helper}" "${install_directory}/lune-sandbox"

# Install engram (persistent memory for AI coding agents via MCP).
engram_installed=false
go_binary="${GO:-${GOROOT:+${GOROOT}/bin/go}}"
if [[ -z "${go_binary}" ]]; then
	go_binary=go
fi
if [[ "${go_binary}" != */* ]]; then
	go_binary="$(command -v "${go_binary}" || true)"
fi

if [[ -n "${go_binary}" && -x "${go_binary}" ]]; then
	printf "Installing / updating engram via 'go install github.com/Gentleman-Programming/engram/v3/cmd/engram@latest'...\n"
	if GOWORK="${GOWORK:-off}" GOBIN="${install_directory}" "${go_binary}" install -trimpath github.com/Gentleman-Programming/engram/v3/cmd/engram@latest; then
		if [[ -x "${install_directory}/engram" ]]; then
			engram_installed=true
			printf 'Installed engram to %s\n' "${install_directory}"
		else
			printf 'Warning: Go install succeeded but engram was not found in %s.\n' "${install_directory}" >&2
		fi
	else
		printf 'Warning: Could not build/install engram; continuing without updating it.\n' >&2
	fi
else
	printf 'Warning: Go was not found; skipping optional engram installation.\n' >&2
fi

created_config=false
if [[ ! -f "${config_path}" ]]; then
	if [[ -f "${workspace_root}/config.json" ]]; then
		config_source="${workspace_root}/config.json"
	elif [[ -f "${workspace_root}/config.example.json" ]]; then
		config_source="${workspace_root}/config.example.json"
	else
		printf 'Neither config.json nor config.example.json was found in %s.\n' "${workspace_root}" >&2
		exit 1
	fi
	install -m 0600 "${config_source}" "${config_path}"
	created_config=true
fi

chmod 0600 "${config_path}"

# Add the default engram MCP server without replacing existing user settings.
if command -v python3 >/dev/null 2>&1; then
	if ! python3 - "${config_path}" <<'PY'
import json
import os
import sys
import tempfile
from pathlib import Path

path = Path(sys.argv[1])

try:
    config = json.loads(path.read_text(encoding="utf-8"))
except (OSError, UnicodeError, json.JSONDecodeError) as error:
    print(f"Could not read {path} as JSON: {error}", file=sys.stderr)
    raise SystemExit(1)

if not isinstance(config, dict):
    print(f"Could not update {path}: the top-level JSON value must be an object.", file=sys.stderr)
    raise SystemExit(1)

mcp_servers = config.get("mcpServers")
if mcp_servers is None:
    mcp_servers = {}
    config["mcpServers"] = mcp_servers
elif not isinstance(mcp_servers, dict):
    print(f"Could not update {path}: mcpServers must be an object.", file=sys.stderr)
    raise SystemExit(1)

if mcp_servers.get("engram") is not None:
    raise SystemExit(0)

mcp_servers["engram"] = {
    "transport": "stdio",
    "command": "engram",
    "args": ["mcp", "--tools=agent"],
}

target = path.resolve(strict=True)
temporary_path = None
try:
    file_descriptor, temporary_path = tempfile.mkstemp(
        prefix=".config.json.", dir=str(target.parent)
    )
    os.fchmod(file_descriptor, 0o600)
    with os.fdopen(file_descriptor, "w", encoding="utf-8") as output:
        json.dump(config, output, ensure_ascii=False, indent=2)
        output.write("\n")
        output.flush()
        os.fsync(output.fileno())
    os.replace(temporary_path, target)
except Exception as error:
    if temporary_path is not None:
        try:
            os.unlink(temporary_path)
        except FileNotFoundError:
            pass
    print(f"Could not write updated config {path}: {error}", file=sys.stderr)
    raise SystemExit(1)

print(f"Configured default 'engram' MCP server in {path}")
PY
	then
		printf 'Warning: Could not update %s with the default engram MCP server.\n' "${config_path}" >&2
	fi
else
	printf 'Warning: python3 was not found; skipped adding the default engram MCP server to %s.\n' "${config_path}" >&2
fi

if [[ "${engram_installed}" == true ]]; then
	printf 'Installed lune, lune-sandbox, and engram to %s\n' "${install_directory}"
else
	printf 'Installed lune and lune-sandbox to %s\n' "${install_directory}"
fi
printf 'User config: %s\n' "${config_path}"
if [[ "${created_config}" == true ]]; then
	printf 'Created config.json from %s. Edit it and add your provider API key before starting Lune.\n' "${config_source}"
fi

case ":${PATH:-}:" in
	*":${install_directory}:"*) ;;
	*)
		printf '%s is not in PATH. Add the following to your shell startup file to run lune from any terminal:\n' "${install_directory}"
		if [[ "${SHELL:-}" == "fish" || "${SHELL:-}" == */fish ]]; then
			printf '  fish_add_path "$HOME/.local/bin"\n'
		else
			printf '  export PATH="$HOME/.local/bin:$PATH"\n'
		fi
		;;
esac
