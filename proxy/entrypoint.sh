#!/bin/sh
set -eu

# Optional TCP forward so the sandbox can reach exactly one host port (a local
# MCP server or database). Format: LISTEN_PORT:TARGET_HOST:TARGET_PORT
if [ -n "${FORWARD:-}" ]; then
  listen="${FORWARD%%:*}"
  target="${FORWARD#*:}"
  echo "forward: proxy:${listen} -> ${target}"
  socat "TCP-LISTEN:${listen},fork,reuseaddr" "TCP:${target}" &
fi

echo "allowlist:"
grep -vE '^[[:space:]]*(#|$)' /etc/moat/allowlist.txt | sed 's/^/  /'

# Stream the access log to the container's stdout for `docker logs` / audit.
mkdir -p /var/log/squid && touch /var/log/squid/access.log && chown -R squid:squid /var/log/squid
tail -F -n 0 /var/log/squid/access.log &

squid -k parse -f /etc/squid/squid.conf
exec squid -N -d 1 -f /etc/squid/squid.conf
