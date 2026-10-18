package dispatcher

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckBlocksOnlyDestructiveAndSelfReplacingCommands(t *testing.T) {
	t.Parallel()
	d := NewDispatcher()
	tests := []struct {
		name    string
		command string
		rule    string // empty means the command runs
	}{
		// Direct mutation of the agent's own workload.
		{"set image on agent", "kubectl -n doops-system set image deployment/doops-agent doops-agent=image:v2", "agent_self_replacement"},
		{"rollout restart agent", "kubectl rollout restart deployment/doops-agent -n doops-system", "agent_self_replacement"},
		{"apply agent manifest", "kubectl apply -f doops-agent.yaml", "agent_self_replacement"},
		{"docker restart agent", "docker restart doops-agent", "agent_self_replacement"},
		{"systemctl restart agent", "systemctl restart doops-agent", "agent_self_replacement"},
		{"service restart agent", "service doops-agent restart", "agent_self_replacement"},
		{"pkill agent", "pkill -f doops-agent", "agent_self_replacement"},

		// Destructive host commands.
		{"rm -rf root", "rm -rf /", "delete_root_filesystem"},
		{"rm -rf root glob", "rm -rf /*", "delete_root_filesystem"},
		{"rm -fr root in chain", "cd /tmp && rm -fr / ; echo done", "delete_root_filesystem"},
		{"mkfs", "mkfs.ext4 /dev/sdb1", "format_filesystem"},
		{"fdisk edit", "fdisk /dev/sdb", "edit_partitions"},

		// Ordinary target work runs as given.
		{"rm -rf a subdirectory", "rm -rf /tmp/doops-build", ""},
		{"rm -rf relative", "rm -rf ./dist", ""},
		{"fdisk list", "fdisk -l", ""},
		{"apply other manifest", "kubectl apply -f api.yaml", ""},
		{"apply a NodePort service", "kubectl -n test apply -f test-mysql-nodeport.yaml", ""},
		{"base64 manifest into apply", "echo eA== | base64 -d | kubectl -n test apply -f -", ""},
		{"base64 manifest into create", "printf '%s' 'x' | base64 -d | kubectl -n apps create -f -", ""},
		{"heredoc into apply", "kubectl apply -f - <<'EOF'\nkind: Service\nEOF", ""},
		{"set image on other service", "kubectl set image deployment/api api=image:v2", ""},
		{"restart nginx", "systemctl restart nginx", ""},
		{"helm upgrade agent in foreground", "helm upgrade --install doops-agent ./deploy/helm/doops-agent -n kz-ops", ""},
		{"base64 SQL into psql", "echo 'Q1JFQVRF' | base64 -d | kubectl -n middleware exec -i pg-0 -- psql -U app -d app", ""},
		{"ssh with remote command", "ssh ops@app.internal 'hostname && systemctl status api'", ""},
		{"natural-looking text", "show me the disk usage on this node please", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := d.Check(tt.command)
			if tt.rule == "" {
				if err != nil {
					t.Fatalf("Check(%q) = %v, want allowed", tt.command, err)
				}
				return
			}
			var blocked *BlockedError
			if !errors.As(err, &blocked) || blocked.Rule != tt.rule {
				t.Fatalf("Check(%q) = %v, want rule %s", tt.command, err, tt.rule)
			}
			if blocked.Hint == "" || !strings.Contains(err.Error(), tt.rule) || !strings.Contains(err.Error(), blocked.Hint) {
				t.Fatalf("blocked error must name the rule and the alternative: %q", err)
			}
		})
	}
}

func TestSelfReplacementHintPointsToSupportedUpgrade(t *testing.T) {
	err := NewDispatcher().Check("kubectl rollout restart deployment/doops-agent")
	if err == nil || !strings.Contains(err.Error(), "doops upgrade") {
		t.Fatalf("self-replacement error must point to doops upgrade: %v", err)
	}
}

func TestHelmMutationDetection(t *testing.T) {
	t.Parallel()
	d := NewDispatcher()
	tests := []struct {
		name    string
		command string
		helm    bool
	}{
		{"install agent", "helm install doops-agent ./deploy/helm/doops-agent -n kz-ops", true},
		{"reordered upgrade agent", "cd deploy/helm/doops-agent && helm upgrade --install agent . -n kz-ops", true},
		{"rollback other release", "helm rollback app 2 -n apps", true},
		{"read-only helm", "helm list -n apps", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := d.IsHelmMutation(tt.command); got != tt.helm {
				t.Errorf("IsHelmMutation(%q) = %v, want %v", tt.command, got, tt.helm)
			}
		})
	}
}
