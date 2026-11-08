package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Helm deletes a volume its previous revision rendered and the new chart drops,
// but it does not remove a mount of that volume that was added outside Helm.
// The API server then rejects the Deployment. The controller removes exactly
// those stale mounts, after backing them up, and restores them on rollback.
//
// Given/When/Then matrix:
//
//	Given no volume is deleted, When the upgrade runs, Then no mount is touched.
//	Given a deleted volume still has a live mount, When the upgrade runs, Then
//	the mount is backed up in the cluster, removed, and the upgrade completes.
//	Given a deleted volume has no live mount, When the upgrade runs, Then no
//	mount is touched.
//	Given a volume Helm never rendered, When the upgrade runs, Then Helm keeps
//	it and its mount is not touched.
//	Given the upgrade fails after the removal, When the controller rolls back,
//	Then the removed mount is restored and the rollback is verified.
//	Given the restore is rejected, When the controller rolls back, Then the
//	failure is reported and manual intervention is required.
//	Given the backup cannot be written, When the upgrade runs, Then nothing
//	changes.
//	Given the removal is rejected, When the upgrade runs, Then the controller
//	rolls back and nothing stays changed.
//	Given the preflight Job, When it runs, Then it lists the stale mounts and
//	changes nothing.
//	Given the new chart renders no Agent Deployment, When the checks run, Then
//	they fail with stale_volume_mounts_unresolved and nothing changes.
func TestSelfUpgradeRemovesStaleVolumeMounts(t *testing.T) {
	for _, tc := range []struct {
		name           string
		script         string
		previousVols   string
		renderedVols   string
		liveMounts     string
		env            map[string]string
		wantErr        bool
		wantOut        []string
		wantBackup     bool
		wantRemove     bool
		wantRestore    bool
		wantUpgrade    bool
		wantRolledBack bool
	}{
		{
			name: "no volume is deleted", script: selfUpgradeApplyScript,
			previousVols: "kube-admin,agent-home", renderedVols: "kube-admin,agent-home",
			liveMounts: "kube-admin:/root/.kube/config,agent-home:/root",
			wantOut:    []string{"stale_volume_mounts=none", "self-upgrade complete"}, wantUpgrade: true,
		},
		{
			name: "a deleted volume still has a live mount", script: selfUpgradeApplyScript,
			previousVols: "kube-admin,doagent-config", renderedVols: "kube-admin",
			liveMounts: "kube-admin:/root/.kube/config,doagent-config:/opt/doagent_config",
			wantOut: []string{"stale_volume_mounts=doops-agent:doagent-config:/opt/doagent_config",
				"stale_volume_mounts_removed workload=doops-agent", "self-upgrade complete"},
			wantBackup: true, wantRemove: true, wantUpgrade: true,
		},
		{
			name: "a deleted volume has no live mount", script: selfUpgradeApplyScript,
			previousVols: "kube-admin,doagent-config", renderedVols: "kube-admin",
			liveMounts: "kube-admin:/root/.kube/config",
			wantOut:    []string{"stale_volume_mounts=none", "self-upgrade complete"}, wantUpgrade: true,
		},
		{
			name: "a volume Helm never rendered keeps its mount", script: selfUpgradeApplyScript,
			previousVols: "kube-admin", renderedVols: "kube-admin",
			liveMounts: "kube-admin:/root/.kube/config,doagent-config:/opt/doagent_config",
			wantOut:    []string{"stale_volume_mounts=none", "self-upgrade complete"}, wantUpgrade: true,
		},
		{
			name: "the upgrade fails after the removal", script: selfUpgradeApplyScript,
			previousVols: "kube-admin,doagent-config", renderedVols: "kube-admin",
			liveMounts: "doagent-config:/opt/doagent_config",
			env:        map[string]string{"BAD_FIELD": "readyReplicas"},
			wantErr:    true,
			wantOut:    []string{"volume_mounts_restored count=1", "automatic rollback complete revision=5"},
			wantBackup: true, wantRemove: true, wantRestore: true, wantUpgrade: true, wantRolledBack: true,
		},
		{
			name: "the restore is rejected", script: selfUpgradeApplyScript,
			previousVols: "kube-admin,doagent-config", renderedVols: "kube-admin",
			liveMounts: "doagent-config:/opt/doagent_config",
			env:        map[string]string{"BAD_FIELD": "readyReplicas", "RESTORE_PATCH_FAIL": "1"},
			wantErr:    true,
			wantOut: []string{"Error: automatic rollback failed: the removed volume mounts could not be restored",
				"manual intervention is required"},
			wantBackup: true, wantRemove: true, wantRestore: true, wantUpgrade: true, wantRolledBack: true,
		},
		{
			name: "the backup cannot be written", script: selfUpgradeApplyScript,
			previousVols: "kube-admin,doagent-config", renderedVols: "kube-admin",
			liveMounts: "doagent-config:/opt/doagent_config",
			env:        map[string]string{"MOUNT_BACKUP_FAIL": "1"},
			wantErr:    true,
			wantOut:    []string{"mount backup persistence failed"},
			// The backup is attempted and refused; nothing after it runs.
			wantBackup: true,
		},
		{
			name: "the removal is rejected", script: selfUpgradeApplyScript,
			previousVols: "kube-admin,doagent-config", renderedVols: "kube-admin",
			liveMounts: "doagent-config:/opt/doagent_config",
			env:        map[string]string{"REMOVE_PATCH_FAIL": "1"},
			wantErr:    true,
			wantOut:    []string{"remove patch rejected", "volume_mounts_restored count=0", "automatic rollback complete revision=5"},
			wantBackup: true, wantRemove: true, wantRolledBack: true,
		},
		{
			name: "the preflight lists the stale mounts and changes nothing", script: selfUpgradePreflightScript,
			previousVols: "kube-admin,doagent-config", renderedVols: "kube-admin",
			liveMounts: "doagent-config:/opt/doagent_config",
			wantOut:    []string{"stale_volume_mounts=doops-agent:doagent-config:/opt/doagent_config", "preflight-passed"},
		},
		{
			name: "the new chart renders no Agent Deployment", script: selfUpgradeApplyScript,
			previousVols: "kube-admin", liveMounts: "kube-admin:/root/.kube/config",
			env:     map[string]string{"HELM_TEMPLATE_EMPTY": "1"},
			wantErr: true,
			wantOut: []string{"stale_volume_mounts_unresolved: the new chart must render exactly one doops-agent Deployment"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			callLog := filepath.Join(t.TempDir(), "calls.log")
			env := selfUpgradeScriptEnv(callLog)
			env["PREVIOUS_VOLUMES"] = tc.previousVols
			env["RENDERED_VOLUMES"] = tc.renderedVols
			env["LIVE_MOUNTS"] = tc.liveMounts
			for key, value := range tc.env {
				env[key] = value
			}
			out, err := runSelfUpgradeScript(t, tc.script, selfUpgradeFakeCommands(), env)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr %t:\n%s", err, tc.wantErr, out)
			}
			for _, part := range tc.wantOut {
				if !strings.Contains(out, part) {
					t.Fatalf("output missing %q:\n%s", part, out)
				}
			}
			logBytes, readErr := os.ReadFile(callLog)
			if readErr != nil {
				t.Fatal(readErr)
			}
			lines := strings.Split(strings.TrimSpace(string(logBytes)), "\n")
			index := func(match func(string) bool) int {
				for i, line := range lines {
					if match(line) {
						return i
					}
				}
				return -1
			}
			backup := index(func(l string) bool {
				return strings.Contains(l, "kubectl -n kz-ops apply -f ") && !strings.Contains(l, "apply -f -")
			})
			remove := index(func(l string) bool {
				return strings.Contains(l, "patch deployment") && strings.Contains(l, `"op":"remove"`)
			})
			restore := index(func(l string) bool {
				return strings.Contains(l, "patch deployment") && strings.Contains(l, `"op":"add"`)
			})
			upgrade := index(func(l string) bool {
				return strings.Contains(l, "helm upgrade") && !strings.Contains(l, "--dry-run=server")
			})
			rollback := index(func(l string) bool { return strings.Contains(l, "helm rollback") })

			for _, check := range []struct {
				what string
				at   int
				want bool
			}{
				{"mount backup", backup, tc.wantBackup},
				{"mount removal", remove, tc.wantRemove},
				{"mount restore", restore, tc.wantRestore},
				{"helm upgrade", upgrade, tc.wantUpgrade},
				{"helm rollback", rollback, tc.wantRolledBack},
			} {
				if (check.at >= 0) != check.want {
					t.Fatalf("%s present=%t, want %t:\n%s", check.what, check.at >= 0, check.want, logBytes)
				}
			}
			if tc.wantRemove && !(backup < remove) {
				t.Fatalf("the mounts must be backed up before they are removed:\n%s", logBytes)
			}
			if tc.wantRemove && upgrade >= 0 && !(remove < upgrade) {
				t.Fatalf("the stale mounts must be removed before the Helm upgrade:\n%s", logBytes)
			}
			if tc.wantRestore && !(rollback < restore) {
				t.Fatalf("the mounts must be restored after Helm brings the volume back:\n%s", logBytes)
			}
			if remove >= 0 {
				// The removal is guarded by a test of the mount it expects, so a
				// Deployment that changed since the snapshot rejects the patch.
				if !strings.Contains(lines[remove], `"op":"test","path":"/spec/template/spec/containers/1/volumeMounts/`) ||
					!strings.Contains(lines[remove], `"value":"doagent-config"`) {
					t.Fatalf("the removal is not guarded by the expected mount:\n%s", lines[remove])
				}
				if strings.Contains(lines[remove], "kube-admin") {
					t.Fatalf("a mount of a kept volume was removed:\n%s", lines[remove])
				}
			}
			if tc.wantBackup && tc.env["MOUNT_BACKUP_FAIL"] != "1" {
				saved, readErr := os.ReadFile(callLog + ".mountbackup")
				if readErr != nil {
					t.Fatalf("the mount backup was not written: %v", readErr)
				}
				for _, part := range []string{`"doops-upgrade-mount-backup-doops-agent-live"`, `doagent-config`, `/opt/doagent_config`} {
					if !strings.Contains(string(saved), part) {
						t.Fatalf("the mount backup is missing %s:\n%s", part, saved)
					}
				}
			}
		})
	}
}
