package dispatcher

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckBlocksOnlyRootFilesystemDeletion(t *testing.T) {
	t.Parallel()
	d := NewDispatcher()
	tests := []struct {
		name    string
		command string
		blocked bool
	}{
		{"rm -rf root", "rm -rf /", true},
		{"rm -rf root glob", "rm -rf /*", true},
		{"rm -fr root in chain", "cd /tmp && rm -fr / ; echo done", true},
		{"rm with split flags", "rm -r -f /", true},
		{"uppercase", "RM -RF /", true},

		// Everything else a target grant allows runs as given.
		{"rm -rf a subdirectory", "rm -rf /tmp/doops-build", false},
		{"rm -rf relative", "rm -rf ./dist", false},
		{"mkfs", "mkfs.ext4 /dev/sdb1", false},
		{"fdisk", "fdisk /dev/sdb", false},
		{"apply a NodePort service", "kubectl -n test apply -f test-mysql-nodeport.yaml", false},
		{"base64 manifest into apply", "echo eA== | base64 -d | kubectl -n test apply -f -", false},
		{"heredoc into apply", "kubectl apply -f - <<'EOF'\nkind: Service\nEOF", false},
		{"set image on agent", "kubectl -n doops-system set image deployment/doops-agent doops-agent=image:v2", false},
		{"restart agent container", "docker restart doops-agent", false},
		{"helm upgrade", "helm upgrade --install app ./chart -n apps", false},
		{"ssh with remote command", "ssh ops@app.internal 'sudo reboot'", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := d.Check(tt.command)
			if !tt.blocked {
				if err != nil {
					t.Fatalf("Check(%q) = %v, want allowed", tt.command, err)
				}
				return
			}
			var blocked *BlockedError
			if !errors.As(err, &blocked) || blocked.Rule != "delete_root_filesystem" {
				t.Fatalf("Check(%q) = %v, want delete_root_filesystem", tt.command, err)
			}
			if !strings.Contains(err.Error(), blocked.Rule) || !strings.Contains(err.Error(), blocked.Hint) {
				t.Fatalf("blocked error must name the rule and the alternative: %q", err)
			}
		})
	}
}
