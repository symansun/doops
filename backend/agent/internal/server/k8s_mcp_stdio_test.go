package server

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestK8SMCPStdioExposesReadAndHelmOnly(t *testing.T) {
	input := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n",
	)
	var output bytes.Buffer
	if err := RunK8SMCPStdio(input, &output); err != nil {
		t.Fatalf("run MCP server: %v", err)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode MCP response: %v", err)
	}
	result, _ := response["result"].(map[string]interface{})
	tools, _ := result["tools"].([]interface{})
	if len(tools) != 2 {
		t.Fatalf("expected read and helm tools, got %#v", tools)
	}
	names := make([]string, 0, len(tools))
	for _, value := range tools {
		tool, _ := value.(map[string]interface{})
		name := tool["name"].(string)
		names = append(names, name)
		schema, _ := tool["inputSchema"].(map[string]interface{})
		required, _ := schema["required"].([]interface{})
		expectedRequired := "operation"
		if name == "helm" {
			expectedRequired = "operation,release,namespace,confirm"
		}
		if strings.Join(interfaceStrings(required), ",") != expectedRequired {
			t.Fatalf("unexpected required fields: %#v", required)
		}
		properties, _ := schema["properties"].(map[string]interface{})
		if _, exists := properties["session_id"]; exists {
			t.Fatalf("runtime-bound session leaked into model-visible schema: %#v", properties)
		}
		if name == "helm" {
			if _, exists := properties["values"]; exists {
				t.Fatalf("ambiguous legacy values field remains exposed: %#v", properties)
			}
			for _, requiredProperty := range []string{"value_files", "set_strings"} {
				if _, exists := properties[requiredProperty]; !exists {
					t.Fatalf("helm schema missing %q: %#v", requiredProperty, properties)
				}
			}
		}
	}
	if strings.Join(names, ",") != "k8s_read,helm" {
		t.Fatalf("unexpected tools: %#v", names)
	}
}

func TestK8SMCPStdioHelmToolPreservesConfirmationBoundary(t *testing.T) {
	input := strings.NewReader(
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"helm","arguments":{"operation":"upgrade-install","release":"app","namespace":"apps","chart":"deploy/helm/app"}}}` + "\n",
	)
	var output bytes.Buffer
	if err := runK8SMCPStdioContext(
		t.Context(),
		input,
		&output,
		func() (string, error) { return "apply-test", nil },
	); err != nil {
		t.Fatalf("run MCP server: %v", err)
	}
	if !strings.Contains(output.String(), "confirm=true is required") {
		t.Fatalf("helm confirmation error missing: %s", output.String())
	}
}

func TestK8SMCPStdioRejectsRemovedK8sMutationTool(t *testing.T) {
	input := strings.NewReader(
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"k8s","arguments":{"operation":"rollout-restart","resource":"deployment/app"}}}` + "\n",
	)
	var output bytes.Buffer
	if err := runK8SMCPStdioContext(
		t.Context(),
		input,
		&output,
		func() (string, error) { return "apply-test", nil },
	); err != nil {
		t.Fatalf("run MCP server: %v", err)
	}
	if !strings.Contains(output.String(), "unknown tool") {
		t.Fatalf("removed k8s mutation tool accepted: %s", output.String())
	}
}

func TestK8SMCPStdioReadToolRejectsMutation(t *testing.T) {
	input := strings.NewReader(
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"k8s_read","arguments":{"operation":"rollout-restart","resource":"deployment/app","confirm":true}}}` + "\n",
	)
	var output bytes.Buffer
	if err := runK8SMCPStdioContext(
		t.Context(),
		input,
		&output,
		func() (string, error) { return "read-test", nil },
	); err != nil {
		t.Fatalf("run MCP server: %v", err)
	}
	if !strings.Contains(output.String(), "read tool requires a read-only operation") {
		t.Fatalf("read tool accepted mutation: %s", output.String())
	}
}

func TestK8SMCPStdioRejectsModelSuppliedSession(t *testing.T) {
	input := strings.NewReader(
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"helm","arguments":{"session_id":"target-alias","operation":"rollback","release":"app","namespace":"apps","revision":2,"confirm":true}}}` + "\n",
	)
	var output bytes.Buffer
	if err := runK8SMCPStdioContext(
		t.Context(),
		input,
		&output,
		func() (string, error) { return "release-session", nil },
	); err != nil {
		t.Fatalf("run MCP server: %v", err)
	}
	if !strings.Contains(output.String(), "session_id is runtime-bound and must not be supplied") {
		t.Fatalf("model-supplied session was not rejected: %s", output.String())
	}
}

func TestBindK8SMCPSessionUsesRuntimeBinding(t *testing.T) {
	bound, err := bindK8SMCPSession(
		json.RawMessage(`{"operation":"get","kind":"pods"}`),
		"release-session",
	)
	if err != nil {
		t.Fatalf("bind runtime session: %v", err)
	}
	var input map[string]interface{}
	if err := json.Unmarshal(bound, &input); err != nil {
		t.Fatal(err)
	}
	if input["session_id"] != "release-session" {
		t.Fatalf("runtime session was not injected: %#v", input)
	}
}

func interfaceStrings(values []interface{}) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.(string))
	}
	return result
}
