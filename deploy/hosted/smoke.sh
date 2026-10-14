#!/usr/bin/env bash
# Smoke test of a running hosted stack: /healthz is ok and /v1/server-info answers.
#   ./smoke.sh                       # uses https://$SHOP_DOMAIN from .env
#   ./smoke.sh https://shop.example.com
#   ./smoke.sh http://127.0.0.1:18490          # the local kit test
# Exit 0 when every check passed.
#
# The kit runs the hosted server, so /v1/server-info is required and must say mode=hosted, the public
# /healthz must show only the status, the admin API must not answer on the public address and the
# Apple notification endpoint must be up. (SHOP_MODE=single in the environment skips these.)
#
# /v1/server-info is added by the hosted work (docs: notes/hosted-plan.md). Until the image has it,
# a 404 or 401 (unknown path) is reported as SKIP, not as a failure; set REQUIRE_SERVER_INFO=1 to make it a failure.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
base="${1:-}"
if [ -z "$base" ]; then
  env_file="${HOSTED_ENV_FILE:-$HERE/.env}"
  domain=$(sed -n 's/^SHOP_DOMAIN=\([^ #]*\).*/\1/p' "$env_file" 2>/dev/null | head -n 1)
  [ -n "$domain" ] || { echo "smoke: pass a base URL or set SHOP_DOMAIN in $env_file" >&2; exit 2; }
  base="https://$domain"
fi
base="${base%/}"
mode="${SHOP_MODE:-}"
if [ -z "$mode" ]; then
  mode=$(sed -n 's/^SHOP_MODE=\([^ #]*\).*/\1/p' "${HOSTED_ENV_FILE:-$HERE/.env}" 2>/dev/null | head -n 1)
fi
[ -n "$mode" ] || mode=hosted      # this kit runs the hosted server; SHOP_MODE=single only for a home install
if [ "$mode" = hosted ]; then REQUIRE_SERVER_INFO=1; fi
fail=0
ok()   { echo "  ok    $*"; }
bad()  { echo "  FAIL  $*"; fail=1; }

echo "smoke: $base"
tmp=$(mktemp); trap 'rm -f "$tmp"' EXIT

code=$(curl -sS -m 20 -o "$tmp" -w '%{http_code}' "$base/healthz" 2>/dev/null || true)
flat=$(tr -d ' \n' <"$tmp")
if [ "$code" = 200 ] && printf '%s' "$flat" | grep -q '"status":"ok"'; then ok "healthz 200, status ok"
else bad "healthz http=$code body=$(printf '%s' "$flat" | cut -c1-300)"; fi
# The hosted /healthz shows only the status to a remote caller; the single-household server also
# lists its checks. Check them when they are there.
if printf '%s' "$flat" | grep -q '"checks"'; then
  for c in db ocr; do
    if printf '%s' "$flat" | grep -q "\"$c\":{[^}]*\"ok\":true"; then ok "check $c"; else bad "check $c is not ok"; fi
  done
elif [ "$mode" = hosted ]; then
  if printf '%s' "$flat" | grep -q '"checks"\|ocr'; then bad "public /healthz shows internal details"; else ok "public /healthz shows only the status"; fi
fi

code=$(curl -sS -m 20 -o "$tmp" -w '%{http_code}' "$base/v1/server-info" 2>/dev/null || true)
if [ "$code" = 200 ]; then
  ok "server-info: $(tr -d '\n' <"$tmp" | cut -c1-200)"
  if [ "$mode" = hosted ]; then
    if tr -d ' \n' <"$tmp" | grep -q '"mode":"hosted"'; then ok "server-info says mode hosted"; else bad "SHOP_MODE=hosted but server-info does not say mode hosted"; fi
  fi
elif { [ "$code" = 404 ] || [ "$code" = 401 ]; } && [ "${REQUIRE_SERVER_INFO:-0}" != 1 ]; then
  echo "  SKIP  server-info not in this image yet (http $code)"
else
  bad "server-info http=$code"
fi

code=$(curl -sS -m 20 -o "$tmp" -w '%{http_code}' "$base/v1/auth/status" 2>/dev/null || true)
if [ "$code" = 200 ] && grep -q '"server":"shop"' "$tmp"; then ok "auth/status identifies a Shop server"; else bad "auth/status http=$code"; fi

code=$(curl -sS -m 20 -o /dev/null -w '%{http_code}' "$base/v1/receipts" 2>/dev/null || true)
if [ "$code" = 401 ]; then ok "API refuses requests without a token (401)"; else bad "unauthenticated /v1/receipts returned $code, want 401"; fi

if [ "$mode" = hosted ]; then
  # The admin API listens on the container's loopback only; through the front it must not exist.
  code=$(curl -sS -m 20 -o "$tmp" -w '%{http_code}' -H 'Authorization: Bearer x' -H 'X-Admin-Operator: smoke' "$base/admin/v1/accounts?account_id=x" 2>/dev/null || true)
  case "$code" in 401|403|404) ok "admin API is not served on the public address (http $code)" ;; *) bad "admin route answered http=$code on the public address" ;; esac
  # Apple's server notifications arrive here: garbage must be refused as a bad payload, not 404/502.
  code=$(curl -sS -m 20 -o "$tmp" -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d '{"signedPayload":"x"}' "$base/v1/billing/apple/notifications" 2>/dev/null || true)
  if [ "$code" = 400 ]; then ok "notification endpoint is up and rejects a bad payload (400)"; else bad "notification endpoint answered http=$code, want 400"; fi
fi

[ "$fail" = 0 ] && echo "smoke: PASS" || echo "smoke: FAIL"
exit "$fail"
