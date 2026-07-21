#!/usr/bin/env bash
set -euo pipefail

: "${DOSQL_MYSQL_NAMESPACE:?DOSQL_MYSQL_NAMESPACE is required}"
: "${DOSQL_MYSQL_DATABASE:?DOSQL_MYSQL_DATABASE is required}"
: "${DOSQL_MYSQL_SECRET_NAME:?DOSQL_MYSQL_SECRET_NAME is required}"

[[ "$DOSQL_MYSQL_NAMESPACE" =~ ^[a-z0-9-]+$ ]] || {
  echo "DOSQL_MYSQL_NAMESPACE contains unsupported characters" >&2
  exit 2
}
[[ "$DOSQL_MYSQL_DATABASE" =~ ^[A-Za-z0-9_]+$ ]] || {
  echo "DOSQL_MYSQL_DATABASE contains unsupported characters" >&2
  exit 2
}
[[ "$DOSQL_MYSQL_SECRET_NAME" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || {
  echo "DOSQL_MYSQL_SECRET_NAME contains unsupported characters" >&2
  exit 2
}

url_key="${DOSQL_MYSQL_URL_KEY:-SPRING_DATASOURCE_URL}"
user_key="${DOSQL_MYSQL_USER_KEY:-SPRING_DATASOURCE_USERNAME}"
password_key="${DOSQL_MYSQL_PASSWORD_KEY:-SPRING_DATASOURCE_PASSWORD}"
client_image="${DOSQL_MYSQL_CLIENT_IMAGE:-mysql:8.4}"

for key in "$url_key" "$user_key" "$password_key"; do
  [[ "$key" =~ ^[-._A-Za-z0-9]+$ ]] || {
    echo "MySQL secret key contains unsupported characters" >&2
    exit 2
  }
done
[[ "$client_image" =~ ^[-./:@A-Za-z0-9_]+$ ]] || {
  echo "DOSQL_MYSQL_CLIENT_IMAGE contains unsupported characters" >&2
  exit 2
}

sql_b64="$(base64 | tr -d '\n')"
pod="dosql-mysql-$RANDOM-$$"
cleanup() {
  kubectl delete pod -n "$DOSQL_MYSQL_NAMESPACE" "$pod" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT

cat <<POD_MANIFEST | kubectl create -f - >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: ${pod}
  namespace: ${DOSQL_MYSQL_NAMESPACE}
  labels:
    app.kubernetes.io/managed-by: dosql
spec:
  restartPolicy: Never
  containers:
    - name: mysql
      image: ${client_image}
      env:
        - name: SPRING_DATASOURCE_URL
          valueFrom:
            secretKeyRef:
              name: ${DOSQL_MYSQL_SECRET_NAME}
              key: ${url_key}
        - name: SPRING_DATASOURCE_USERNAME
          valueFrom:
            secretKeyRef:
              name: ${DOSQL_MYSQL_SECRET_NAME}
              key: ${user_key}
        - name: SPRING_DATASOURCE_PASSWORD
          valueFrom:
            secretKeyRef:
              name: ${DOSQL_MYSQL_SECRET_NAME}
              key: ${password_key}
        - name: DOSQL_DATABASE
          value: ${DOSQL_MYSQL_DATABASE}
        - name: DOSQL_SQL_B64
          value: ${sql_b64}
      command: ["sh", "-ec"]
      args:
        - |
          case "$SPRING_DATASOURCE_URL" in
            jdbc:mysql://*) ;;
            *) echo "unsupported MySQL datasource URL" >&2; exit 2 ;;
          esac
          endpoint="${SPRING_DATASOURCE_URL#jdbc:mysql://}"
          authority="${endpoint%%/*}"
          configured_database="${endpoint#*/}"
          configured_database="${configured_database%%\?*}"
          test "$configured_database" = "$DOSQL_DATABASE" || {
            echo "configured database does not match requested database" >&2
            exit 2
          }
          host="${authority%%:*}"
          port="${authority##*:}"
          test "$host" != "$authority" || port=3306
          printf '%s' "$DOSQL_SQL_B64" | base64 -d |
            MYSQL_PWD="$SPRING_DATASOURCE_PASSWORD" mysql \
              --batch --raw --skip-column-names \
              --host "$host" \
              --port "$port" \
              --user "$SPRING_DATASOURCE_USERNAME" \
              "$DOSQL_DATABASE"
POD_MANIFEST

deadline=$((SECONDS + 180))
phase=""
while (( SECONDS < deadline )); do
  phase=$(kubectl get pod -n "$DOSQL_MYSQL_NAMESPACE" "$pod" -o jsonpath='{.status.phase}')
  case "$phase" in
    Succeeded|Failed) break ;;
  esac
  sleep 1
done

test "$phase" = "Succeeded" || {
  if [ "$phase" != "Failed" ]; then
    echo "DoSql MySQL client pod timed out in phase ${phase:-unknown}" >&2
  fi
  kubectl logs -n "$DOSQL_MYSQL_NAMESPACE" "$pod" 2>/dev/null || true
  exit 1
}
kubectl logs -n "$DOSQL_MYSQL_NAMESPACE" "$pod"
