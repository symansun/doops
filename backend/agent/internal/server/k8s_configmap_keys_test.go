package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/symansun/doops/backend/agent/api"
)

const configMapKeysCanary = "synthetic-configmap-canary-value"

func TestBuildK8SCommandsConfigMapKeys(t *testing.T) {
	cases := []struct {
		name     string
		params   api.K8SParams
		wantArgs string
	}{
		{"one namespace", api.K8SParams{Operation: "configmap-keys", Namespace: "apps"}, "get configmaps --namespace apps --output json"},
		{"all namespaces", api.K8SParams{Operation: "configmap-keys", AllNamespaces: true}, "get configmaps --all-namespaces --output json"},
		{"one object", api.K8SParams{Operation: "configmap-keys", Namespace: "apps", Name: "orders-config"}, "get configmaps --namespace apps orders-config --output json"},
		{"selector", api.K8SParams{Operation: "configmap-keys", Namespace: "apps", Selector: "app=orders"}, "get configmaps --namespace apps --selector app=orders --output json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.params.SessionID = "configmap-keys"
			commands, err := buildK8SCommands(tc.params)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if len(commands) != 1 || !commands[0].configMapKeysOnly || commands[0].helm {
				t.Fatalf("commands = %#v", commands)
			}
			if got := strings.Join(commands[0].args, " "); got != tc.wantArgs {
				t.Fatalf("args = %q, want %q", got, tc.wantArgs)
			}
			if !gatewayK8SReadOnly(tc.params.Operation) {
				t.Fatal("configmap-keys must be read-only so it never takes a mutation lease")
			}
		})
	}
	for _, bad := range []struct {
		name   string
		params api.K8SParams
		want   string
	}{
		{"caller output", api.K8SParams{Operation: "configmap-keys", Namespace: "apps", Output: "json"}, "fixed output"},
		{"name injection", api.K8SParams{Operation: "configmap-keys", Namespace: "apps", Name: "x --kubeconfig=/y"}, "invalid kubernetes name"},
		{"namespace with all", api.K8SParams{Operation: "configmap-keys", Namespace: "apps", AllNamespaces: true}, "mutually exclusive"},
	} {
		t.Run(bad.name, func(t *testing.T) {
			bad.params.SessionID = "configmap-keys"
			if _, err := buildK8SCommands(bad.params); err == nil || !strings.Contains(err.Error(), bad.want) {
				t.Fatalf("err = %v, want %q", err, bad.want)
			}
		})
	}
}

// fakeConfigMapKubectl installs a kubectl that records its arguments and
// prints the given stdout/stderr with the given exit code.
func fakeConfigMapKubectl(t *testing.T, stdout, stderr string, exit int) string {
	t.Helper()
	bin := t.TempDir()
	argsFile := filepath.Join(bin, "args")
	stdoutFile := filepath.Join(bin, "stdout")
	stderrFile := filepath.Join(bin, "stderr")
	if err := os.WriteFile(stdoutFile, []byte(stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stderrFile, []byte(stderr), 0o600); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(bin, "kubectl"), "#!/bin/sh\n"+
		"printf '%s\\n' \"$*\" > '"+argsFile+"'\n"+
		"cat '"+stdoutFile+"'\n"+
		"cat '"+stderrFile+"' >&2\n"+
		"exit "+strconv.Itoa(exit)+"\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsFile
}

func runConfigMapKeysTool(t *testing.T, params map[string]interface{}) (string, string, error) {
	t.Helper()
	params["session_id"] = "configmap-keys"
	params["operation"] = "configmap-keys"
	raw, _ := json.Marshal(params)
	var output strings.Builder
	result, err := handleK8STool(t.Context(), raw, func(event notificationEvent) { output.WriteString(event.Data) })
	return output.String(), result, err
}

func TestConfigMapKeysReturnNamesWithoutValues(t *testing.T) {
	list := `{"apiVersion":"v1","kind":"List","items":[
  {"kind":"ConfigMap","metadata":{"namespace":"apps","name":"orders-config"},
   "data":{"LOG_LEVEL":"` + configMapKeysCanary + `","DB_PASSWORD":"` + configMapKeysCanary + `"},
   "binaryData":{"cert.der":"` + configMapKeysCanary + `"}},
  {"kind":"ConfigMap","metadata":{"namespace":"data","name":"empty-config"}}
]}`
	argsFile := fakeConfigMapKubectl(t, list, "", 0)
	output, result, err := runConfigMapKeysTool(t, map[string]interface{}{"all_namespaces": true})
	if err != nil {
		t.Fatalf("configmap-keys: %v", err)
	}
	if result != "Kubernetes operation complete." {
		t.Fatalf("result = %q", result)
	}
	if strings.Contains(output, configMapKeysCanary) {
		t.Fatalf("configmap-keys leaked a value: %q", output)
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	want := [][]string{
		{"NAMESPACE", "NAME", "KEYS"},
		{"apps", "orders-config", "DB_PASSWORD,LOG_LEVEL,cert.der"},
		{"data", "empty-config", "<none>"},
	}
	for index, line := range lines {
		if got := strings.Fields(line); strings.Join(got, "|") != strings.Join(want[index], "|") {
			t.Fatalf("line %d = %q, want %q", index, line, want[index])
		}
	}
	args, _ := os.ReadFile(argsFile)
	if got := strings.TrimSpace(string(args)); got != "get configmaps --all-namespaces --output json" {
		t.Fatalf("kubectl args = %q", got)
	}
}

func TestConfigMapKeysSingleObjectAndEmptyList(t *testing.T) {
	single := `{"kind":"ConfigMap","metadata":{"namespace":"apps","name":"orders-config"},"data":{"A":"` + configMapKeysCanary + `"}}`
	fakeConfigMapKubectl(t, single, "", 0)
	output, _, err := runConfigMapKeysTool(t, map[string]interface{}{"namespace": "apps", "name": "orders-config"})
	if err != nil {
		t.Fatalf("single: %v", err)
	}
	if strings.Contains(output, configMapKeysCanary) || !strings.Contains(output, "orders-config") {
		t.Fatalf("single output = %q", output)
	}

	fakeConfigMapKubectl(t, `{"apiVersion":"v1","kind":"List","items":[]}`, "", 0)
	output, _, err = runConfigMapKeysTool(t, map[string]interface{}{"namespace": "apps", "selector": "app=none"})
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if got := strings.TrimSpace(output); strings.Join(strings.Fields(got), "|") != "NAMESPACE|NAME|KEYS" {
		t.Fatalf("empty list must print only the header, got %q", output)
	}
}

func TestConfigMapKeysFailuresCarryNoValues(t *testing.T) {
	t.Run("kubectl error keeps stderr", func(t *testing.T) {
		fakeConfigMapKubectl(t, "", `Error from server (NotFound): configmaps "nope" not found`, 1)
		output, _, err := runConfigMapKeysTool(t, map[string]interface{}{"namespace": "apps", "name": "nope"})
		if err == nil || !strings.Contains(err.Error(), "NotFound") {
			t.Fatalf("err = %v", err)
		}
		if output != "" {
			t.Fatalf("a failed read must not print a table: %q", output)
		}
	})
	t.Run("malformed output is not quoted", func(t *testing.T) {
		fakeConfigMapKubectl(t, `{"kind":"List","items":[{"data":{"A":"`+configMapKeysCanary+`"`, "", 0)
		_, _, err := runConfigMapKeysTool(t, map[string]interface{}{"namespace": "apps"})
		if err == nil || strings.Contains(err.Error(), configMapKeysCanary) {
			t.Fatalf("decode error must not quote input: %v", err)
		}
	})
	t.Run("object without a name is rejected", func(t *testing.T) {
		fakeConfigMapKubectl(t, `{"kind":"List","items":[{"data":{"A":"`+configMapKeysCanary+`"}}]}`, "", 0)
		output, _, err := runConfigMapKeysTool(t, map[string]interface{}{"namespace": "apps"})
		if err == nil || strings.Contains(err.Error(), configMapKeysCanary) || output != "" {
			t.Fatalf("err = %v output = %q", err, output)
		}
	})
	t.Run("oversized listing must be narrowed", func(t *testing.T) {
		previous := configMapKeysOutputLimit
		configMapKeysOutputLimit = 64
		t.Cleanup(func() { configMapKeysOutputLimit = previous })
		fakeConfigMapKubectl(t, `{"kind":"List","items":[{"metadata":{"name":"a"},"data":{"A":"`+strings.Repeat("x", 200)+configMapKeysCanary+`"}}]}`, "", 0)
		output, _, err := runConfigMapKeysTool(t, map[string]interface{}{"all_namespaces": true})
		if err == nil || !strings.Contains(err.Error(), "narrow it") || strings.Contains(err.Error(), configMapKeysCanary) || output != "" {
			t.Fatalf("err = %v output = %q", err, output)
		}
	})
}

func TestHelmStatusKeepsOnlyReleaseMetadata(t *testing.T) {
	commands, err := buildK8SCommands(api.K8SParams{SessionID: "helm-status", Operation: "helm-status", Name: "orders", Namespace: "apps"})
	if err != nil || len(commands) != 1 || !commands[0].helm || !commands[0].statusOnly {
		t.Fatalf("commands = %#v err = %v", commands, err)
	}
	status := `{"name":"orders","namespace":"apps","version":3,
  "info":{"status":"deployed","description":"Upgrade complete","first_deployed":"2026-11-01T00:00:00Z","last_deployed":"2026-11-02T00:00:00Z","notes":"token=` + configMapKeysCanary + `"},
  "config":{"db":{"password":"` + configMapKeysCanary + `"}},
  "manifest":"kind: Secret\nstringData:\n  password: ` + configMapKeysCanary + `\n",
  "hooks":[{"manifest":"` + configMapKeysCanary + `"}]}`
	var output strings.Builder
	if err := pushHelmStatusSummary([]byte(status), func(event notificationEvent) { output.WriteString(event.Data) }); err != nil {
		t.Fatalf("summary: %v", err)
	}
	if strings.Contains(output.String(), configMapKeysCanary) {
		t.Fatalf("helm status leaked a value: %q", output.String())
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(output.String()), &got); err != nil {
		t.Fatalf("summary is not JSON: %q", output.String())
	}
	for _, dropped := range []string{"config", "manifest", "hooks"} {
		if _, ok := got[dropped]; ok {
			t.Fatalf("%s must be dropped: %q", dropped, output.String())
		}
	}
	info, _ := got["info"].(map[string]interface{})
	if got["name"] != "orders" || got["version"] != float64(3) || info["status"] != "deployed" || info["notes"] != nil {
		t.Fatalf("summary = %q", output.String())
	}

	for _, bad := range []struct {
		name  string
		input string
	}{
		{"truncated JSON", `{"config":{"a":"` + configMapKeysCanary + `"`},
		{"JSON without a release name", `{"config":{"a":"` + configMapKeysCanary + `"},"manifest":"` + configMapKeysCanary + `"}`},
		{"JSON array", `["` + configMapKeysCanary + `"]`},
	} {
		t.Run(bad.name, func(t *testing.T) {
			var printed strings.Builder
			err := pushHelmStatusSummary([]byte(bad.input), func(event notificationEvent) { printed.WriteString(event.Data) })
			if err == nil || strings.Contains(err.Error(), configMapKeysCanary) || printed.Len() != 0 {
				t.Fatalf("err = %v printed = %q", err, printed.String())
			}
		})
	}
}

func TestHelmStatusRedactsCredentialsInDescription(t *testing.T) {
	for _, tc := range []struct {
		name        string
		description string
	}{
		{"password assignment", "Upgrade failed: password=" + configMapKeysCanary},
		{"secret assignment", "rendered secret_token=" + configMapKeysCanary},
		{"bearer header", "Authorization: Bearer " + configMapKeysCanary},
		{"userinfo in URL", "pull https://deploy:" + configMapKeysCanary + "@registry.example/app failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			description, _ := json.Marshal(tc.description)
			status := `{"name":"orders","namespace":"apps","version":4,"info":{"status":"failed","description":` + string(description) + `}}`
			var output strings.Builder
			if err := pushHelmStatusSummary([]byte(status), func(event notificationEvent) { output.WriteString(event.Data) }); err != nil {
				t.Fatalf("summary: %v", err)
			}
			if strings.Contains(output.String(), configMapKeysCanary) || !strings.Contains(output.String(), "REDACTED") {
				t.Fatalf("description was not redacted: %q", output.String())
			}
			if !strings.Contains(output.String(), `"status":"failed"`) {
				t.Fatalf("release status lost: %q", output.String())
			}
		})
	}
}
