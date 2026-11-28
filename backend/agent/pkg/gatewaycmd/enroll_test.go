package gatewaycmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnrollCreateListsMetadataWithoutRepeatingTheSecret(t *testing.T) {
	db := filepath.Join(t.TempDir(), "gateway.db")
	created := captureStdout(t, func() {
		enroll([]string{"create", "-db", db, "-cluster", "dev", "-instance", "local"})
	})
	token := stdoutField(t, created, "token")
	if !strings.HasPrefix(token, "dga_aen_") {
		t.Fatal("enrollment token has the wrong prefix")
	}
	secret := token[strings.LastIndex(token, "_")+1:]
	listed := captureStdout(t, func() {
		enroll([]string{"list", "-db", db})
	})
	if strings.Contains(listed, secret) || strings.Contains(listed, token) {
		t.Fatal("enrollment list included the secret")
	}
	if !strings.Contains(listed, "cluster=dev") || !strings.Contains(listed, "instance=local") || !strings.Contains(listed, "revoked=false") {
		t.Fatalf("enrollment list = %s", listed)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	stdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = stdout })
	fn()
	writer.Close()
	os.Stdout = stdout
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func stdoutField(t *testing.T, text, key string) string {
	t.Helper()
	prefix := key + "="
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	t.Fatalf("missing %s in enrollment output", key)
	return ""
}
