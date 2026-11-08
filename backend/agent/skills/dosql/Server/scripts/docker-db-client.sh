#!/usr/bin/env bash
set -euo pipefail

: "${DOSQL_SMOKE_CONTAINER:?DOSQL_SMOKE_CONTAINER is required}"
: "${DOSQL_DB_CLIENT:?DOSQL_DB_CLIENT is required}"

case "$DOSQL_DB_CLIENT" in
  psql)
    exec docker exec -i \
      -e PGHOST="${PGHOST:?PGHOST is required}" \
      -e PGPORT="${PGPORT:?PGPORT is required}" \
      -e PGUSER="${PGUSER:?PGUSER is required}" \
      -e PGPASSWORD="${PGPASSWORD:-}" \
      -e PGDATABASE="${PGDATABASE:?PGDATABASE is required}" \
      "$DOSQL_SMOKE_CONTAINER" psql "$@"
    ;;
  mysql)
    exec docker exec -i \
      -e MYSQL_PWD="${MYSQL_PWD:?MYSQL_PWD is required}" \
      "$DOSQL_SMOKE_CONTAINER" mysql "$@"
    ;;
  mongosh)
    exec docker exec -i \
      -e DOSQL_MONGODB_URI="${DOSQL_MONGODB_URI:?DOSQL_MONGODB_URI is required}" \
      -e DOSQL_MONGODB_OPERATION="${DOSQL_MONGODB_OPERATION:?DOSQL_MONGODB_OPERATION is required}" \
      -e DOSQL_MONGODB_REQUIRE_READ_ONLY_ROLE="${DOSQL_MONGODB_REQUIRE_READ_ONLY_ROLE:?DOSQL_MONGODB_REQUIRE_READ_ONLY_ROLE is required}" \
      "$DOSQL_SMOKE_CONTAINER" mongosh "$@"
    ;;
  *)
    echo "unsupported DOSQL_DB_CLIENT: $DOSQL_DB_CLIENT" >&2
    exit 2
    ;;
esac
