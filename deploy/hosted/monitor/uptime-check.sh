#!/bin/sh
# Health check for an EXTERNAL uptime monitor (the monitor service inside the stack cannot see the
# server dying). Fits cron, Nagios/Icinga/Zabbix style checks, or a hosted pinger.
#
#   uptime-check.sh https://shop.example.com            # prints one line, exit 0 ok / 2 trouble
#   uptime-check.sh https://shop.example.com --ping https://hc-ping.com/<uuid>
#
# --ping GETs the URL after a good check and <url>/fail after a bad one (healthchecks.io style),
# so a cron line on any other machine is a complete external monitor:
#   */5 * * * * /path/uptime-check.sh https://shop.example.com --ping https://hc-ping.com/<uuid>
#
# A service that can watch a URL by itself needs no script: point it at <base>/healthz and
# make it look for the text "status":"ok" (HTTP 503 already means degraded).
set -u
base="${1:-}"; [ -n "$base" ] || { echo "usage: uptime-check.sh BASE_URL [--ping URL]" >&2; exit 3; }
ping=""; [ "${2:-}" = "--ping" ] && ping="${3:-}"
tmp=$(mktemp) || exit 3
trap 'rm -f "$tmp"' EXIT
code=$(curl -sS -m 15 -o "$tmp" -w '%{http_code}' "${base%/}/healthz" 2>/dev/null || true)
if [ "$code" = 200 ] && tr -d ' \n' <"$tmp" | grep -q '"status":"ok"'; then
  echo "OK shop healthy ($base)"; rc=0
else
  bad=$(tr -d ' \n' <"$tmp" 2>/dev/null | grep -o '"[a-z_]*":{[^}]*"ok":false[^}]*}' | cut -d: -f1 | tr -d '"' | tr '\n' ' ')
  echo "CRITICAL shop http=$code failing: ${bad:-unreachable} ($base)"; rc=2
fi
if [ -n "$ping" ]; then
  if [ "$rc" = 0 ]; then curl -fsS -m 10 -o /dev/null "$ping" || true; else curl -fsS -m 10 -o /dev/null "${ping%/}/fail" || true; fi
fi
exit "$rc"
