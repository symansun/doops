#!/usr/bin/env bash
set -euo pipefail

: "${DOSQL_POSTGRES_NAMESPACE:?DOSQL_POSTGRES_NAMESPACE is required}"
: "${DOSQL_POSTGRES_DATABASE:?DOSQL_POSTGRES_DATABASE is required}"
: "${DOSQL_POSTGRES_SECRET_NAME:?DOSQL_POSTGRES_SECRET_NAME is required}"
: "${DOSQL_POSTGRES_URI_KEY:=connection_uri}"
: "${DOSQL_POSTGRES_CLIENT_IMAGE:=postgres:16}"

[[ "$DOSQL_POSTGRES_NAMESPACE" =~ ^[a-z0-9-]+$ ]] || { echo "invalid PostgreSQL namespace" >&2; exit 2; }
[[ "$DOSQL_POSTGRES_DATABASE" =~ ^[A-Za-z0-9_.-]+$ ]] || { echo "invalid PostgreSQL database" >&2; exit 2; }
[[ "$DOSQL_POSTGRES_SECRET_NAME" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || { echo "invalid PostgreSQL Secret name" >&2; exit 2; }
[[ "$DOSQL_POSTGRES_URI_KEY" =~ ^[-._A-Za-z0-9]+$ ]] || { echo "invalid PostgreSQL Secret key" >&2; exit 2; }
[[ "$DOSQL_POSTGRES_CLIENT_IMAGE" =~ ^[-./:@A-Za-z0-9_]+$ ]] || { echo "invalid PostgreSQL client image" >&2; exit 2; }

sql_b64="$(base64 | tr -d '\n')"
pod="dosql-postgres-$RANDOM-$$"
cleanup() { kubectl delete pod -n "$DOSQL_POSTGRES_NAMESPACE" "$pod" --ignore-not-found --wait=false >/dev/null 2>&1 || true; }
trap cleanup EXIT
sed_replacement() { printf '%s' "$1" | sed -e 's/[\\/&]/\\\\&/g'; }

cat <<'POD_MANIFEST' | sed \
  -e "s/__POD_NAME__/$(sed_replacement "$pod")/g" \
  -e "s/__NAMESPACE__/$(sed_replacement "$DOSQL_POSTGRES_NAMESPACE")/g" \
  -e "s/__SECRET_NAME__/$(sed_replacement "$DOSQL_POSTGRES_SECRET_NAME")/g" \
  -e "s/__URI_KEY__/$(sed_replacement "$DOSQL_POSTGRES_URI_KEY")/g" \
  -e "s/__DATABASE__/$(sed_replacement "$DOSQL_POSTGRES_DATABASE")/g" \
  -e "s/__CLIENT_IMAGE__/$(sed_replacement "$DOSQL_POSTGRES_CLIENT_IMAGE")/g" \
  -e "s/__SQL_B64__/$(sed_replacement "$sql_b64")/g" |
  kubectl create -f - >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: __POD_NAME__
  namespace: __NAMESPACE__
  labels:
    app.kubernetes.io/managed-by: dosql
spec:
  restartPolicy: Never
  containers:
    - name: postgres
      image: __CLIENT_IMAGE__
      env:
        - name: DOSQL_CONNECTION_URI
          valueFrom:
            secretKeyRef:
              name: __SECRET_NAME__
              key: __URI_KEY__
        - name: DOSQL_DATABASE
          value: __DATABASE__
        - name: DOSQL_SQL_B64
          value: __SQL_B64__
      command: ["sh", "-ec"]
      args:
        - |
          db="${DOSQL_CONNECTION_URI##*/}"
          db="${db%%\?*}"
          test "$db" = "$DOSQL_DATABASE" || { echo "configured database does not match requested database" >&2; exit 2; }
          printf '%s' "$DOSQL_SQL_B64" | base64 -d | psql "$DOSQL_CONNECTION_URI" --no-psqlrc --set ON_ERROR_STOP=1 --no-align --tuples-only
POD_MANIFEST

deadline=$((SECONDS + 180)); phase=""
while (( SECONDS < deadline )); do
  phase="$(kubectl get pod -n "$DOSQL_POSTGRES_NAMESPACE" "$pod" -o jsonpath='{.status.phase}')"
  case "$phase" in Succeeded|Failed) break;; esac
  sleep 1
done
test "$phase" = Succeeded || { kubectl logs -n "$DOSQL_POSTGRES_NAMESPACE" "$pod" 2>/dev/null || true; exit 1; }
kubectl logs -n "$DOSQL_POSTGRES_NAMESPACE" "$pod"
