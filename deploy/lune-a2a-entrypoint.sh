#!/bin/sh
set -eu

config_dir="${XDG_CONFIG_HOME:-/var/lib/lune/.config}/luneAgent"
mkdir -p "$config_dir"
cp /run/lune-runtime/config.json "$config_dir/config.json"
chmod 600 "$config_dir/config.json"

exec /usr/local/bin/lune-a2a-proxy
