package dispatcher

import "testing"

func TestClassifyBlocklist(t *testing.T) {
	t.Parallel()
	d := NewDispatcher()
	tests := []struct {
		name    string
		command string
		blocked bool
	}{
		// Direct mutation of the agent's own workload.
		{"set image on agent", "kubectl -n doops-system set image deployment/doops-agent doops-agent=image:v2", true},
		{"rollout restart agent", "kubectl rollout restart deployment/doops-agent -n doops-system", true},
		{"docker restart agent", "docker restart doops-agent", true},
		{"systemctl restart agent", "systemctl restart doops-agent", true},
		{"service restart agent", "service doops-agent restart", true},
		{"apply agent manifest", "kubectl apply -f doops-agent.yaml", true},

		// Encoded manifests piped into kubectl/oc mutation verbs.
		{"base64 to apply", "printf '%s' 'x' | base64 -d | kubectl apply -f -", true},
		{"base64 to create with namespace", "printf '%s' 'x' | base64 -d | kubectl -n apps create -f -", true},
		{"base64 to replace with context", "printf '%s' 'x' | base64 -d | kubectl --context=prod replace -f -", true},
		{"base64 to oc apply", "printf '%s' 'x' | base64 -d | oc -n apps apply -f -", true},
		{"base64 to uppercase KUBECTL", "printf '%s' 'x' | base64 -d | KUBECTL APPLY -f -", true},

		// Other services stay mutable.
		{"set image on other service", "kubectl set image deployment/api api=image:v2", false},
		{"apply other manifest", "kubectl apply -f api.yaml", false},
		{"restart nginx", "systemctl restart nginx", false},
		{"helm upgrade agent in foreground", "helm upgrade --install doops-agent ./deploy/helm/doops-agent -n kz-ops", false},
		{"helm rollback agent in foreground", "helm rollback doops-agent 2 -n kz-ops", false},
		{"helm uninstall agent in foreground", "helm uninstall doops-agent -n kz-ops", false},

		// SQL text carrying mutation keywords must reach the database untouched.
		{
			"base64 SQL into kubectl exec psql",
			"echo 'Q1JFQVRFIFVOSVFVRSBJTkRFWA==' | base64 -d | kubectl -n middleware exec -i pg-0 -- psql -U app -d app",
			false,
		},
		{
			"base64 into kubectl exec with create keyword in argv",
			"printf '%s' 'x' | base64 -d | kubectl exec -i mysql-0 -n middleware -- mysql -e 'create index i on t(c)'",
			false,
		},
		{
			"base64 into oc exec with replace keyword in argv",
			"base64 -d < q.b64 | oc exec -i db-0 -- psql -c 'REPLACE INTO t VALUES (1)'",
			false,
		},
		{
			"base64 into kubectl exec with apply keyword in argv",
			"base64 -d < q.b64 | kubectl exec -i db-0 -- psql -c 'select apply from t'",
			false,
		},

		// Explicit SSH commands are the caller's responsibility.
		{"ssh with remote command", "ssh ops@app.internal 'hostname && systemctl status api'", false},
		{"ssh jump host reboot", "ssh -J bastion.example.com root@db.internal 'sudo reboot'", false},
		{"absolute ssh tunnel", "/usr/bin/ssh -o BatchMode=yes -L 15432:db.internal:5432 ops@bastion.example.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := d.Classify(tt.command) == PathBlocked
			if got != tt.blocked {
				t.Errorf("Classify(%q) blocked = %v, want %v", tt.command, got, tt.blocked)
			}
		})
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
