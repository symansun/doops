package server

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/user/doops/agent/api"
)

func TestBuildK8SCommandsExtendedReadSurface(t *testing.T) {
	cases := []struct {
		name     string
		params   api.K8SParams
		wantArgs string
		helm     bool
		keysOnly bool
	}{
		{"configmap get", api.K8SParams{Operation: "get", Kind: "configmaps", Namespace: "apps"}, "get configmaps --namespace apps", false, false},
		{"networkpolicy yaml", api.K8SParams{Operation: "get", Kind: "networkpolicy", Name: "worker", Namespace: "apps", Output: "yaml"}, "get networkpolicy --namespace apps worker --output yaml", false, false},
		{"label selector", api.K8SParams{Operation: "get", Kind: "pods", Namespace: "apps", Selector: "app in (api,web)"}, "get pods --namespace apps --selector app in (api,web)", false, false},
		{"all namespaces", api.K8SParams{Operation: "get", Kind: "pods", AllNamespaces: true}, "get pods --all-namespaces", false, false},
		{"jsonpath output", api.K8SParams{Operation: "get", Kind: "deploy", Namespace: "apps", Output: "jsonpath={.items[*].metadata.name}"}, "get deploy --namespace apps --output jsonpath={.items[*].metadata.name}", false, false},
		{"previous logs since", api.K8SParams{Operation: "logs", Resource: "pod/api-0", Namespace: "apps", Previous: true, Since: "10m", Tail: 50}, "logs pod/api-0 --namespace apps --tail 50 --previous --since 10m", false, false},
		{"top with selector", api.K8SParams{Operation: "top", Kind: "pods", Namespace: "apps", Selector: "app=api"}, "top pods --namespace apps --selector app=api", false, false},
		{"crd get", api.K8SParams{Operation: "get", Kind: "crd"}, "get crd", false, false},
		{"helm list all", api.K8SParams{Operation: "helm-list", AllNamespaces: true}, "list --output json --all-namespaces", true, false},
		{"helm history", api.K8SParams{Operation: "helm-history", Name: "zhiyong", Namespace: "zyj"}, "history zhiyong --namespace zyj --max 20 --output json", true, false},
		{"helm status", api.K8SParams{Operation: "helm-status", Name: "zhiyong", Namespace: "zyj"}, "status zhiyong --namespace zyj --output json", true, false},
		{"helm values keys only", api.K8SParams{Operation: "helm-values", Name: "zhiyong", Namespace: "zyj"}, "get values zhiyong --namespace zyj --all --output json", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.params.SessionID = "read-surface"
			commands, err := buildK8SCommands(tc.params)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if len(commands) != 1 {
				t.Fatalf("commands = %#v", commands)
			}
			got := commands[0]
			if args := strings.Join(got.args, " "); args != tc.wantArgs {
				t.Fatalf("args = %q, want %q", args, tc.wantArgs)
			}
			if got.helm != tc.helm || got.valueKeysOnly != tc.keysOnly {
				t.Fatalf("helm=%t keysOnly=%t, want %t/%t", got.helm, got.valueKeysOnly, tc.helm, tc.keysOnly)
			}
			if !gatewayK8SReadOnly(tc.params.Operation) {
				t.Fatalf("%s must be classified read-only so it never takes a mutation lease", tc.params.Operation)
			}
		})
	}
}

func TestBuildK8SCommandsRejectsUnsafeReads(t *testing.T) {
	cases := []struct {
		name   string
		params api.K8SParams
		want   string
	}{
		{"secret get", api.K8SParams{Operation: "get", Kind: "secrets", Namespace: "apps"}, "carries credentials"},
		{"serviceaccount alias", api.K8SParams{Operation: "get", Kind: "sa", Namespace: "apps"}, "carries credentials"},
		{"secret by resource", api.K8SParams{Operation: "logs", Resource: "secret/db"}, "carries credentials"},
		{"selector injection", api.K8SParams{Operation: "get", Kind: "pods", Selector: "app=x;rm -rf /"}, "invalid kubernetes label selector"},
		{"bad since", api.K8SParams{Operation: "logs", Resource: "pod/a", Since: "yesterday"}, "invalid logs since"},
		{"namespace with all", api.K8SParams{Operation: "get", Kind: "pods", Namespace: "a", AllNamespaces: true}, "mutually exclusive"},
		{"output newline", api.K8SParams{Operation: "get", Kind: "pods", Output: "jsonpath={.a}\n--kubeconfig=/x"}, "unsupported kubernetes output"},
		{"go-template output", api.K8SParams{Operation: "get", Kind: "pods", Output: "go-template={{.}}"}, "unsupported kubernetes output"},
		{"helm history needs release", api.K8SParams{Operation: "helm-history", Namespace: "zyj"}, "name (the Helm release) is required"},
		{"helm status needs namespace", api.K8SParams{Operation: "helm-status", Name: "zhiyong"}, "namespace is required"},
		{"helm release injection", api.K8SParams{Operation: "helm-values", Name: "x --kubeconfig=/y", Namespace: "zyj"}, "invalid kubernetes name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.params.SessionID = "read-surface"
			if _, err := buildK8SCommands(tc.params); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestHelmValueKeyPathsHideValues(t *testing.T) {
	values := map[string]interface{}{
		"image":    map[string]interface{}{"tag": "release-1", "repository": "repo/app"},
		"database": map[string]interface{}{"password": "synthetic-canary-value"},
		"replicas": float64(2),
		"empty":    map[string]interface{}{},
	}
	keys := helmValueKeyPaths("", values)
	sort.Strings(keys)
	want := []string{"database.password", "empty", "image.repository", "image.tag", "replicas"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	if strings.Contains(strings.Join(keys, ","), "synthetic-canary-value") {
		t.Fatal("value leaked into key paths")
	}
}
