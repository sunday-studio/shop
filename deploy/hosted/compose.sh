#!/usr/bin/env bash
# The ONLY way the hosted kit's scripts start docker compose for the production stack.
# It pins the project to "shop-hosted", uses deploy/hosted/.env and docker-compose.yml, and refuses
# anything that could address another project or wipe volumes. Usage: ./compose.sh ps | logs api | ...
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT="shop-hosted"

if [ "${COMPOSE_PROJECT_NAME:-$PROJECT}" != "$PROJECT" ]; then
  echo "compose.sh: refusing to run compose: COMPOSE_PROJECT_NAME is '$COMPOSE_PROJECT_NAME', only '$PROJECT' is allowed" >&2
  exit 64
fi
prev=""
for a in "$@"; do
  case "$a" in
    -p|--project-name|-p=*|--project-name=*|--project-directory|-f|--file)
      echo "compose.sh: refusing to run compose: '$a' is not allowed, the project and files are fixed" >&2; exit 64 ;;
  esac
  case "$prev:$a" in
    down:-v|down:--volumes|down:--rmi)
      echo "compose.sh: refusing 'down $a' on the production stack: it would delete data volumes" >&2; exit 64 ;;
  esac
  case "$a" in
    -*) ;;
    *) prev="$a" ;;
  esac
done

ENV_FILE="${HOSTED_ENV_FILE:-$HERE/.env}"
[ -f "$ENV_FILE" ] || { echo "compose.sh: $ENV_FILE not found; copy .env.example to .env and fill it in" >&2; exit 66; }
cd "$HERE"
exec docker compose -p "$PROJECT" --env-file "$ENV_FILE" -f "$HERE/docker-compose.yml" "$@"
