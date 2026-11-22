package server

import (
	"strings"
	"testing"
)

func TestSanitizeSecretTextRedactsCredentialFields(t *testing.T) {
	raw := "authorization=Bearer synthetic cookie=session-canary dbPassword=database-canary githubToken=token-canary " +
		"https://user:url-canary@example.test/repo.git?access_token=query-canary Basic YmFzaWMtY2FuYXJ5 " +
		"https://storage.example.test/object?X-Goog-Signature=signature-canary&X-Amz-Security-Token=security-token-canary&githubToken=github-query-canary " +
		"ghp_" + strings.Repeat("a", 40) + " github_pat_" + strings.Repeat("b", 82) + " AKIA" + strings.Repeat("A", 16) + " " +
		"-----BEGIN PRIVATE KEY-----\npem-canary\n-----END PRIVATE KEY-----"
	sanitized := sanitizeSecretText(raw)
	for _, secret := range []string{
		"synthetic", "session-canary", "database-canary", "token-canary",
		"url-canary", "query-canary", "signature-canary", "security-token-canary",
		"github-query-canary", "YmFzaWMtY2FuYXJ5", "pem-canary",
		"ghp_", "github_pat_", "AKIA",
	} {
		if strings.Contains(sanitized, secret) {
			t.Fatalf("sanitized text retained %q: %s", secret, sanitized)
		}
	}
}
