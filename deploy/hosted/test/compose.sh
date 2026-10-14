#!/usr/bin/env bash
# The ONLY way `make hosted-kit-test` starts docker compose. It refuses every project name except
# shop-hosted-test, so the live stack (project "shop", port 8080, volume shop_shop-data) and the
# production kit project ("shop-hosted") can never be addressed from the test. Throwaway volumes,
# port 127.0.0.1:18490, no public DNS.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
KIT="$(dirname "$HERE")"
PROJECT="shop-hosted-test"

if [ "${COMPOSE_PROJECT_NAME:-$PROJECT}" != "$PROJECT" ]; then
  echo "test/compose.sh: refusing to run compose: COMPOSE_PROJECT_NAME is '$COMPOSE_PROJECT_NAME', only '$PROJECT' is allowed" >&2
  exit 64
fi
for a in "$@"; do
  case "$a" in
    -p|--project-name|-p=*|--project-name=*|--project-directory|-f|--file)
      echo "test/compose.sh: refusing to run compose: '$a' is not allowed, the project and files are fixed" >&2; exit 64 ;;
  esac
done
case " $* " in
  *" down "*) echo "test/compose.sh: compose project '$PROJECT': down $*" >&2 ;;
esac

ENV_FILE="${HOSTED_ENV_FILE:-$HERE/test.env}"
export COMPOSE_PROJECT_NAME="$PROJECT"
cd "$KIT"
exec docker compose -p "$PROJECT" --env-file "$ENV_FILE" \
  -f "$KIT/docker-compose.yml" -f "$HERE/docker-compose.test.yml" "$@"
