#!/usr/bin/env bash
set -euo pipefail

: "${DOSQL_MONGODB_NAMESPACE:?DOSQL_MONGODB_NAMESPACE is required}"
: "${DOSQL_MONGODB_DATABASE:?DOSQL_MONGODB_DATABASE is required}"
: "${DOSQL_MONGODB_SECRET_NAME:?DOSQL_MONGODB_SECRET_NAME is required}"
: "${DOSQL_MONGODB_URI_KEY:=connection_uri}"
: "${DOSQL_MONGODB_CLIENT_IMAGE:=mongo:8}"

[[ "$DOSQL_MONGODB_NAMESPACE" =~ ^[a-z0-9-]+$ ]] || { echo "invalid MongoDB namespace" >&2; exit 2; }
[[ "$DOSQL_MONGODB_DATABASE" =~ ^[A-Za-z0-9_.-]+$ ]] || { echo "invalid MongoDB database" >&2; exit 2; }
[[ "$DOSQL_MONGODB_SECRET_NAME" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || { echo "invalid MongoDB Secret name" >&2; exit 2; }
[[ "$DOSQL_MONGODB_URI_KEY" =~ ^[-._A-Za-z0-9]+$ ]] || { echo "invalid MongoDB Secret key" >&2; exit 2; }
[[ "$DOSQL_MONGODB_CLIENT_IMAGE" =~ ^[-./:@A-Za-z0-9_]+$ ]] || { echo "invalid MongoDB client image" >&2; exit 2; }

eval_script=""
previous=""
for arg in "$@"; do
  if [ "$previous" = "--eval" ]; then eval_script="$arg"; break; fi
  previous="$arg"
done
[ -n "$eval_script" ] || { echo "MongoDB evaluator is required" >&2; exit 2; }
eval_b64="$(printf '%s' "$eval_script" | base64 | tr -d '\n')"
operation_b64="$(printf '%s' "${DOSQL_MONGODB_OPERATION:?DOSQL_MONGODB_OPERATION is required}" | base64 | tr -d '\n')"
pod="dosql-mongodb-$RANDOM-$$"
cleanup() { kubectl delete pod -n "$DOSQL_MONGODB_NAMESPACE" "$pod" --ignore-not-found --wait=false >/dev/null 2>&1 || true; }
trap cleanup EXIT
sed_replacement() { printf '%s' "$1" | sed -e 's/[\\/&]/\\\\&/g'; }

cat <<'POD_MANIFEST' | sed \
  -e "s/__POD_NAME__/$(sed_replacement "$pod")/g" \
  -e "s/__NAMESPACE__/$(sed_replacement "$DOSQL_MONGODB_NAMESPACE")/g" \
  -e "s/__SECRET_NAME__/$(sed_replacement "$DOSQL_MONGODB_SECRET_NAME")/g" \
  -e "s/__URI_KEY__/$(sed_replacement "$DOSQL_MONGODB_URI_KEY")/g" \
  -e "s/__DATABASE__/$(sed_replacement "$DOSQL_MONGODB_DATABASE")/g" \
  -e "s/__CLIENT_IMAGE__/$(sed_replacement "$DOSQL_MONGODB_CLIENT_IMAGE")/g" \
  -e "s/__OPERATION_B64__/$(sed_replacement "$operation_b64")/g" \
  -e "s/__READ_ONLY__/$(sed_replacement "${DOSQL_MONGODB_REQUIRE_READ_ONLY_ROLE:-0}")/g" \
  -e "s/__EVAL_B64__/$(sed_replacement "$eval_b64")/g" |
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
    - name: mongodb
      image: __CLIENT_IMAGE__
      env:
        - name: DOSQL_MONGODB_URI
          valueFrom:
            secretKeyRef:
              name: __SECRET_NAME__
              key: __URI_KEY__
        - name: DOSQL_MONGODB_DATABASE
          value: __DATABASE__
        - name: DOSQL_MONGODB_OPERATION_B64
          value: __OPERATION_B64__
        - name: DOSQL_MONGODB_REQUIRE_READ_ONLY_ROLE
          value: __READ_ONLY__
        - name: DOSQL_EVAL_B64
          value: __EVAL_B64__
      command: ["sh", "-ec"]
      args:
        - |
          eval_script="$(printf '%s' "$DOSQL_EVAL_B64" | base64 -d)"
          export DOSQL_MONGODB_OPERATION="$(printf '%s' "$DOSQL_MONGODB_OPERATION_B64" | base64 -d)"
          mongosh --nodb --quiet --eval "$eval_script"
POD_MANIFEST

deadline=$((SECONDS + 180)); phase=""
while (( SECONDS < deadline )); do
  phase="$(kubectl get pod -n "$DOSQL_MONGODB_NAMESPACE" "$pod" -o jsonpath='{.status.phase}')"
  case "$phase" in Succeeded|Failed) break;; esac
  sleep 1
done
test "$phase" = Succeeded || { kubectl logs -n "$DOSQL_MONGODB_NAMESPACE" "$pod" 2>/dev/null || true; exit 1; }
kubectl logs -n "$DOSQL_MONGODB_NAMESPACE" "$pod"
