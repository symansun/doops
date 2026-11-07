package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const selfUpgradeRBACAccountFixture = `{"apiVersion":"v1","kind":"ServiceAccount","metadata":{"name":"agent-account","namespace":"kz-ops"}}`
const selfUpgradeRBACBindingFixture = `{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"ClusterRoleBinding","metadata":{"name":"kz-ops-agent-account-cluster-admin"},"roleRef":{"apiGroup":"rbac.authorization.k8s.io","kind":"ClusterRole","name":"cluster-admin"},"subjects":[{"kind":"ServiceAccount","name":"agent-account","namespace":"kz-ops"}]}`

func selfUpgradeRBACCommands() map[string]string {
	commands := selfUpgradeFakeCommands()
	commands["original-helm"] = commands["helm"]
	commands["helm"] = `#!/bin/sh
set -eu
if [ "${1:-}" = "template" ]; then
  printf 'helm %s\n' "$*" >> "$CALL_LOG"
  printf '%s\n' "$RBAC_AFTER_MANIFEST"
elif [ "${1:-} ${2:-}" = "get manifest" ]; then
  printf 'helm %s\n' "$*" >> "$CALL_LOG"
  case "${RBAC_SCRIPT:-}:$*" in
    rollback:*"--revision 4"*) printf '%s\n' "$RBAC_AFTER_MANIFEST" ;;
    *) printf '%s\n' "$RBAC_BEFORE_MANIFEST" ;;
  esac
else
  exec original-helm "$@"
fi
`
	commands["original-kubectl"] = commands["kubectl"]
	commands["kubectl"] = `#!/usr/bin/env python3
import json, os, subprocess, sys
args = sys.argv[1:]
if args[2:4] == ["get", "pod"]:
    raw = subprocess.check_output(["original-kubectl"] + args)
    if os.environ.get("CONTROLLER_POD_DENIED") == "1":
        raise SystemExit("controller Pod read forbidden")
    pod = json.loads(raw)
    pod["spec"]["serviceAccountName"] = os.environ.get("CONTROLLER_ACCOUNT", "agent-account")
    print(json.dumps(pod))
else:
    os.execvp("original-kubectl", ["original-kubectl"] + args)
`
	return commands
}

func selfUpgradeRBACManifest(resources ...string) string {
	deployment := `{"kind":"Deployment","metadata":{"name":"doops-agent"},"spec":{"template":{"spec":{"containers":[{"name":"doops-agent","image":"` + testPinnedImage() + `"}]}}}}`
	return strings.Join(append([]string{deployment}, resources...), "\n---\n")
}

// Given the controller uses the Agent ServiceAccount, an upgrade or rollback
// that changes Helm's ownership of its identity must stop before Helm mutation.
// In particular, rolling back from managed SA/RBAC to an old Deployment-only
// revision must not delete the controller's own authorization.
func TestSelfUpgradeRBACTransitionStopsBeforeMutation(t *testing.T) {
	managed := []string{selfUpgradeRBACAccountFixture, selfUpgradeRBACBindingFixture}
	roleBinding := strings.Replace(managed[1], `"kind":"ClusterRoleBinding"`, `"kind":"RoleBinding"`, 1)
	implicitSubjectNamespace := strings.Replace(roleBinding, `,"namespace":"kz-ops"`, "", 1)
	for _, tc := range []struct {
		name   string
		before []string
		after  []string
		deny   bool
	}{
		{"same managed identity", managed, managed, false},
		{"external identity stays external", nil, nil, false},
		{"first adoption", nil, managed, true},
		{"rollback before adoption", managed, nil, true},
		{"account removed", managed, managed[1:], true},
		{"binding removed", managed, managed[:1], true},
		{"namespaced binding removed", []string{managed[0], roleBinding}, managed[:1], true},
		{"implicit subject namespace binding removed", []string{managed[0], implicitSubjectNamespace}, managed[:1], true},
		{"role changed", managed, []string{managed[0], strings.Replace(managed[1], `"name":"cluster-admin"`, `"name":"view"`, 1)}, true},
		{"subject changed", managed, []string{managed[0], strings.Replace(managed[1], `"namespace":"kz-ops"`, `"namespace":"other"`, 1)}, true},
		{"metadata changed", managed, []string{strings.Replace(managed[0], `"metadata":{`, `"metadata":{"annotations":{"example.test/rollout":"new"},`, 1), managed[1]}, false},
		{"different namespace identity", nil, []string{strings.ReplaceAll(managed[0], "kz-ops", "other"), strings.ReplaceAll(managed[1], "kz-ops", "other")}, false},
		{"different account identity", nil, []string{strings.ReplaceAll(managed[0], "agent-account", "other-agent"), strings.ReplaceAll(managed[1], "agent-account", "other-agent")}, false},
	} {
		for name, script := range map[string]string{
			"apply": selfUpgradeApplyScript, "preflight": selfUpgradePreflightScript, "rollback": selfUpgradeRollbackScript,
		} {
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				callLog := filepath.Join(t.TempDir(), "calls.log")
				env := selfUpgradeScriptEnv(callLog)
				env["DOOPS_UPGRADE_KUBECONFIG_SOURCE"] = "serviceAccount"
				env["AUTH_USERNAME"] = "system:serviceaccount:kz-ops:agent-account"
				env["OLD_IMAGE"] = testPinnedImage()
				env["RBAC_SCRIPT"] = name
				env["RBAC_BEFORE_MANIFEST"] = selfUpgradeRBACManifest(tc.before...)
				env["RBAC_AFTER_MANIFEST"] = selfUpgradeRBACManifest(tc.after...)
				out, err := runSelfUpgradeScript(t, script, selfUpgradeRBACCommands(), env)
				if tc.deny != (err != nil) {
					t.Fatalf("err=%v, want denied=%t:\n%s", err, tc.deny, out)
				}
				calls, readErr := os.ReadFile(callLog)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if tc.deny {
					if got := selfUpgradeFailureCategory(out); got != selfUpgradeCategoryRBACControllerIdentityChanged {
						t.Fatalf("category=%q:\n%s", got, out)
					}
					if !strings.Contains(out, "authorized external controller") {
						t.Fatalf("missing recovery instruction:\n%s", out)
					}
					for _, mutation := range []string{"helm upgrade ", "helm rollback ", "patch deployment"} {
						if strings.Contains(string(calls), mutation) {
							t.Fatalf("identity rejection reached %s:\n%s", mutation, calls)
						}
					}
				} else if !strings.Contains(out, "controller_rbac=unchanged") {
					t.Fatalf("guard did not verify the controller:\n%s", out)
				}
			})
		}
	}
}

func TestSelfUpgradeRBACGuardDoesNotReadPodForIndependentAuthentication(t *testing.T) {
	callLog := filepath.Join(t.TempDir(), "calls.log")
	script := selfUpgradeKubeconfigPrelude + selfUpgradeRBACGuard + `verify_controller_rbac missing-before missing-after
`
	env := selfUpgradeScriptEnv(callLog)
	env["DOOPS_UPGRADE_KUBECONFIG_SOURCE"] = "mounted"
	out, err := runSelfUpgradeScript(t, script, selfUpgradeFakeCommands(), env)
	if err != nil {
		t.Fatalf("mounted controller was blocked: %v:\n%s", err, out)
	}
	if !strings.Contains(out, "controller_rbac=independent") {
		t.Fatalf("independent authentication was not verified:\n%s", out)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil || string(calls) != "kubectl auth whoami -o json\n" {
		t.Fatalf("independent authentication read more than whoami: %v:\n%s", err, calls)
	}
}

func TestSelfUpgradeRBACGuardProtectsMountedServiceAccountAuthentication(t *testing.T) {
	for _, namespace := range []string{"kz-ops", "other-namespace"} {
		for name, script := range map[string]string{
			"apply": selfUpgradeApplyScript, "preflight": selfUpgradePreflightScript, "rollback": selfUpgradeRollbackScript,
		} {
			t.Run(namespace+"/"+name, func(t *testing.T) {
				callLog := filepath.Join(t.TempDir(), "calls.log")
				env := selfUpgradeScriptEnv(callLog)
				env["DOOPS_UPGRADE_KUBECONFIG_SOURCE"] = "mounted"
				env["AUTH_USERNAME"] = "system:serviceaccount:" + namespace + ":mounted-account"
				env["CONTROLLER_ACCOUNT"] = "default"
				env["CONTROLLER_POD_DENIED"] = "1"
				env["OLD_IMAGE"] = testPinnedImage()
				env["RBAC_SCRIPT"] = name
				account := strings.ReplaceAll(strings.ReplaceAll(selfUpgradeRBACAccountFixture, "kz-ops", namespace), "agent-account", "mounted-account")
				binding := strings.ReplaceAll(strings.ReplaceAll(selfUpgradeRBACBindingFixture, "kz-ops", namespace), "agent-account", "mounted-account")
				env["RBAC_BEFORE_MANIFEST"] = selfUpgradeRBACManifest(account, binding)
				env["RBAC_AFTER_MANIFEST"] = selfUpgradeRBACManifest()
				out, err := runSelfUpgradeScript(t, script, selfUpgradeRBACCommands(), env)
				if err == nil || selfUpgradeFailureCategory(out) != selfUpgradeCategoryRBACControllerIdentityChanged ||
					!strings.Contains(out, "Helm would change the controller ServiceAccount") {
					t.Fatalf("mounted ServiceAccount was not protected: %v:\n%s", err, out)
				}
				calls, readErr := os.ReadFile(callLog)
				if readErr != nil {
					t.Fatal(readErr)
				}
				for _, forbidden := range []string{"get pod upgrade-controller", "helm upgrade ", "helm rollback ", "patch deployment"} {
					if strings.Contains(string(calls), forbidden) {
						t.Fatalf("mounted ServiceAccount reached %s:\n%s", forbidden, calls)
					}
				}
			})
		}
	}
}

func TestSelfUpgradeRBACGuardRefusesAnUnverifiedControllerIdentity(t *testing.T) {
	for name, extra := range map[string]map[string]string{
		"missing Pod account":      {"CONTROLLER_ACCOUNT": ""},
		"different Pod account":    {"CONTROLLER_ACCOUNT": "default"},
		"Pod read denied":          {"CONTROLLER_POD_DENIED": "1"},
		"whoami denied":            {"AUTH_WHOAMI_DENIED": "1"},
		"missing authentication":   {"AUTH_USERNAME": ""},
		"different authentication": {"AUTH_USERNAME": "kubernetes-admin"},
		"different namespace":      {"AUTH_USERNAME": "system:serviceaccount:other:agent-account"},
		"invalid account identity": {"AUTH_USERNAME": "system:serviceaccount:kz-ops:"},
		"mounted whoami denied":    {"DOOPS_UPGRADE_KUBECONFIG_SOURCE": "mounted", "AUTH_WHOAMI_DENIED": "1"},
	} {
		t.Run(name, func(t *testing.T) {
			callLog := filepath.Join(t.TempDir(), "calls.log")
			env := selfUpgradeScriptEnv(callLog)
			env["DOOPS_UPGRADE_KUBECONFIG_SOURCE"] = "serviceAccount"
			env["AUTH_USERNAME"] = "system:serviceaccount:kz-ops:agent-account"
			env["OLD_IMAGE"] = testPinnedImage()
			env["RBAC_BEFORE_MANIFEST"] = selfUpgradeRBACManifest()
			env["RBAC_AFTER_MANIFEST"] = selfUpgradeRBACManifest()
			for key, value := range extra {
				env[key] = value
			}
			out, err := runSelfUpgradeScript(t, selfUpgradePreflightScript, selfUpgradeRBACCommands(), env)
			if err == nil || selfUpgradeFailureCategory(out) != selfUpgradeCategoryRBACControllerIdentityChanged {
				t.Fatalf("unverified controller was not refused: %v:\n%s", err, out)
			}
			calls, readErr := os.ReadFile(callLog)
			if readErr != nil || strings.Contains(string(calls), "helm upgrade ") {
				t.Fatalf("unverified controller reached Helm: %v:\n%s", readErr, calls)
			}
		})
	}
}
