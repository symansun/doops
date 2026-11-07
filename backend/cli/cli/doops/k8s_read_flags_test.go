package main

import (
	"reflect"
	"testing"
)

func TestBuildK8SRequestReadFlagsAndHelmReads(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want map[string]interface{}
	}{
		{"named get with selector-free", []string{"--target", "t", "get", "deploy", "api", "-n", "apps", "-o", "yaml"},
			map[string]interface{}{"operation": "get", "kind": "deploy", "name": "api", "namespace": "apps", "output": "yaml"}},
		{"selector across namespaces", []string{"--target", "t", "get", "pods", "-A", "-l", "app=api"},
			map[string]interface{}{"operation": "get", "kind": "pods", "all_namespaces": true, "selector": "app=api"}},
		{"previous logs since", []string{"--target", "t", "logs", "pod/api-0", "--namespace", "apps", "-p", "--since", "10m", "--tail", "50"},
			map[string]interface{}{"operation": "logs", "resource": "pod/api-0", "namespace": "apps", "previous": true, "since": "10m", "tail": 50}},
		{"helm list", []string{"--target", "t", "helm", "list", "-A"},
			map[string]interface{}{"operation": "helm-list", "all_namespaces": true}},
		{"helm history", []string{"--target", "t", "helm", "history", "zhiyong", "-n", "zyj"},
			map[string]interface{}{"operation": "helm-history", "name": "zhiyong", "namespace": "zyj"}},
		{"helm values", []string{"--target", "t", "helm", "values", "zhiyong", "-n", "zyj"},
			map[string]interface{}{"operation": "helm-values", "name": "zhiyong", "namespace": "zyj"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := buildK8SRequest(tc.args)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			if !reflect.DeepEqual(req.Payload, tc.want) {
				t.Fatalf("payload = %#v, want %#v", req.Payload, tc.want)
			}
		})
	}
	for _, bad := range [][]string{
		{"--target", "t", "helm", "history"},
		{"--target", "t", "helm", "rollback", "x"},
		{"--target", "t", "helm", "list", "extra"},
	} {
		if _, err := buildK8SRequest(bad); err == nil {
			t.Fatalf("invalid helm read accepted: %v", bad)
		}
	}
}
