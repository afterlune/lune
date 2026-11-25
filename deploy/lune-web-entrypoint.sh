#!/bin/sh
set -eu

config_dir="${XDG_CONFIG_HOME:-/var/lib/lune/.config}/lune"
mkdir -p "$config_dir"
if [ ! -s "$config_dir/config.json" ]; then
	cp /run/lune-runtime/config.json "$config_dir/config.json"
	chmod 600 "$config_dir/config.json"
fi

: "${LUNE_WEB_TOKEN:?Set LUNE_WEB_TOKEN in lune/.env}"
umask 077
printf '%s' "$LUNE_WEB_TOKEN" > /tmp/lune-web-token
openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
	-subj "/CN=lune-web" -addext "subjectAltName=DNS:lune-web" \
	-keyout /tmp/lune-web.key -out /tmp/lune-web.crt >/dev/null 2>&1

exec /usr/local/bin/lune-web web \
	--workspace /workspace \
	--shared-state \
	--listen 0.0.0.0:18094 \
	--no-open \
	--tls-cert /tmp/lune-web.crt \
	--tls-key /tmp/lune-web.key \
	--token-file /tmp/lune-web-token
