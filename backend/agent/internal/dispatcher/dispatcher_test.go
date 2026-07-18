package dispatcher

import "testing"

func TestDispatcherBlocksDirectAgentSelfMutation(t *testing.T) {
	t.Parallel()
	dispatcher := NewDispatcher()
	commands := []string{
		"kubectl -n doops-system set image deployment/doops-agent doops-agent=image:v2",
		"kubectl rollout restart deployment/doops-agent -n doops-system",
		"docker restart doops-agent",
		"systemctl restart doops-agent",
		"service doops-agent restart",
		"kubectl apply -f doops-agent.yaml",
		"printf '%s' 'encoded-job' | base64 -d | kubectl apply -f -",
	}
	for _, command := range commands {
		if got := dispatcher.Classify(command); got != PathBlocked {
			t.Errorf("Classify(%q) = %v, want PathBlocked", command, got)
		}
	}
}

func TestDispatcherAllowsOtherServiceMutation(t *testing.T) {
	t.Parallel()
	dispatcher := NewDispatcher()
	commands := []string{
		"kubectl set image deployment/api api=image:v2",
		"kubectl apply -f api.yaml",
		"helm upgrade --install doops-agent ./deploy/helm/doops-agent -n kz-ops",
		"helm rollback doops-agent 2 -n kz-ops",
		"helm uninstall doops-agent -n kz-ops",
		"systemctl restart nginx",
	}
	for _, command := range commands {
		if got := dispatcher.Classify(command); got == PathBlocked {
			t.Errorf("Classify(%q) = PathBlocked, want allowed", command)
		}
	}
}
