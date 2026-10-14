#!/usr/bin/env bash
# Smoke test of a running hosted stack: /healthz is ok and /v1/server-info answers.
#   ./smoke.sh                       # uses https://$SHOP_DOMAIN from .env
#   ./smoke.sh https://shop.example.com
#   ./smoke.sh http://127.0.0.1:18490          # the local kit test
# Exit 0 when every check passed.
#
# In hosted mode (SHOP_MODE=hosted in the environment, or in $HOSTED_ENV_FILE) /v1/server-info is
# required and must say mode=hosted: REQUIRE_SERVER_INFO is switched on automatically.
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
for c in db ocr; do
  if printf '%s' "$flat" | grep -q "\"$c\":{[^}]*\"ok\":true"; then ok "check $c"; else bad "check $c is not ok"; fi
done

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

[ "$fail" = 0 ] && echo "smoke: PASS" || echo "smoke: FAIL"
exit "$fail"
