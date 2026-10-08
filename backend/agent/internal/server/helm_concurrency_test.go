package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunHelmCommandConcurrentSupersedeBindsAcceptance(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	helmLog := filepath.Join(root, "helm.log")
	if err := os.WriteFile(filepath.Join(root, "helm"), []byte(`#!/bin/sh
set -eu
state="${HELM_STATE_DIR}"
log="${HELM_LOG}"
description=""
requested_revision=""
previous=""
for arg in "$@"; do
  if [ "$previous" = "--description" ]; then description="$arg"; fi
  if [ "$previous" = "--revision" ]; then requested_revision="$arg"; fi
  previous="$arg"
done

if [ "$1" = "upgrade" ]; then
  for arg in "$@"; do
    if [ "$arg" = "--dry-run=server" ]; then
      printf '%s\n' '{"manifest":"kind: Deployment\nmetadata:\n  name: app\nspec:\n  template:\n    spec:\n      containers:\n        - image: registry.example/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"}'
      exit 0
    fi
  done
  if [ -f "${state}/current-revision" ]; then
    revision=$(( $(cat "${state}/current-revision") + 1 ))
  else
    revision=1
  fi
  printf '%s\n' "$revision" >"${state}/current-revision"
  printf '%s' "$description" >"${state}/current-description"
  case "$description" in
    doops:session-a:*)
      touch "${state}/session-a-upgraded"
      while [ ! -f "${state}/session-b-upgraded" ]; do sleep 0.01; done
      ;;
    doops:session-b:*)
      touch "${state}/session-b-upgraded"
      ;;
  esac
  printf '{"version":%s,"info":{"description":"%s","notes":"chart-output-canary"}}\n' "$revision" "$description"
  exit 0
fi

if [ "$1" = "get" ] && [ "$2" = "manifest" ]; then
  if [ -z "$requested_revision" ]; then
    echo "manifest revision is required" >&2
    exit 1
  fi
  printf 'manifest revision=%s\n' "$requested_revision" >>"${log}"
  touch "${state}/manifest-${requested_revision}"
  cat <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
spec:
  template:
    spec:
      containers:
        - name: app
          image: registry.example/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
YAML
  exit 0
fi

if [ "$1" = "status" ]; then
  revision="$(cat "${state}/current-revision")"
  description="$(cat "${state}/current-description")"
  printf 'status revision=%s\n' "$revision" >>"${log}"
  printf '{"version":%s,"info":{"description":"%s"}}\n' "$revision" "$description"
  exit 0
fi

echo "unexpected helm command: $*" >&2
exit 1
`), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "kubectl"), []byte(`#!/bin/sh
set -eu
cat <<'JSON'
{"metadata":{"generation":7},"spec":{"replicas":1,"template":{"spec":{"containers":[{"image":"registry.example/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}}},"status":{"observedGeneration":7,"updatedReplicas":1,"readyReplicas":1,"availableReplicas":1,"unavailableReplicas":0}}
JSON
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	t.Setenv("HELM_STATE_DIR", state)
	t.Setenv("HELM_LOG", helmLog)

	firstParams := testHelmParams()
	firstParams.SessionID = "session-a"
	firstCommand, err := buildHelmCommand(firstParams)
	if err != nil {
		t.Fatalf("build first Helm command: %v", err)
	}
	secondParams := testHelmParams()
	secondParams.SessionID = "session-b"
	secondCommand, err := buildHelmCommand(secondParams)
	if err != nil {
		t.Fatalf("build second Helm command: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	type result struct {
		verification string
		err          error
	}
	firstDone := make(chan result, 1)
	go func() {
		verification, runErr := runHelmCommand(ctx, firstCommand, nil)
		firstDone <- result{verification: verification, err: runErr}
	}()
	waitForHelmFixtureFile(t, filepath.Join(state, "session-a-upgraded"))

	secondDone := make(chan result, 1)
	go func() {
		verification, runErr := runHelmCommand(ctx, secondCommand, nil)
		secondDone <- result{verification: verification, err: runErr}
	}()
	secondResult := <-secondDone
	firstResult := <-firstDone

	if secondResult.err != nil {
		t.Fatalf("current concurrent session failed: %v", secondResult.err)
	}
	if !strings.Contains(secondResult.verification, "final Deployment spec verified") {
		t.Fatalf("current session verification missing: %q", secondResult.verification)
	}
	if firstResult.err == nil || firstResult.err.Error() != "helm_verification_failed" {
		t.Fatalf("superseded session was reported as successful: verification=%q err=%v", firstResult.verification, firstResult.err)
	}

	logBytes, err := os.ReadFile(helmLog)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "manifest revision=1") || !strings.Contains(log, "manifest revision=2") {
		t.Fatalf("manifest reads were not pinned to both mutation revisions: %q", log)
	}
	if !strings.Contains(log, "status revision=2") {
		t.Fatalf("current release was not rechecked after acceptance: %q", log)
	}
}

func waitForHelmFixtureFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for Helm fixture file %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
