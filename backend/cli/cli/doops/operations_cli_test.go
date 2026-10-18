package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRemovedGatewayAdminCommandsAreRejected(t *testing.T) {
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Errorf("removed command reached Gateway: %s %s", r.Method, r.URL.RequestURI())
		w.WriteHeader(http.StatusNotFound)
	}))
	defer gateway.Close()

	binary := buildDoopsCLIBinary(t)
	t.Setenv("DOOPS_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	if err := saveServers([]Server{{Name: "test", Gateway: gateway.URL, Cluster: "prod", Instance: "node"}}); err != nil {
		t.Fatal(err)
	}
	auth := &AuthStore{}
	auth.SetGateway(gateway.URL, "admin-token")
	if err := auth.Save(); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"unlock", "--target", "test"},
		{"admin", "operations", "list", "--target", "test"},
		{"admin", "operations", "cancel", "--target", "test", "--id", "op-1"},
		{"admin", "audit", "optimize", "--target", "test"},
	} {
		t.Run(strings.Join(args[:len(args)-2], " "), func(t *testing.T) {
			command := exec.Command(binary, append([]string{"-session", "removed-cmd"}, args...)...)
			command.Env = os.Environ()
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("removed command succeeded: %s", output)
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("removed commands sent %d Gateway requests", got)
	}
}
