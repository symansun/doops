package gatewaycmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/doops/agent/internal/server"
)

// cliPasswordCanary is the only credential value these tests use. Every
// success and failure path asserts that it never reaches stdout or stderr.
const cliPasswordCanary = "synthetic"

type cliRun struct {
	stdout   string
	stderr   string
	exitCode int
}

func cliEnv() []string {
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "GOROOT", "DOOPS_GATEWAY_STORE", "DOOPS_GATEWAY_MYSQL_DSN", "DOOPS_GATEWAY_DATA_DIR":
			continue
		}
		env = append(env, entry)
	}
	return env
}

// buildGatewayCLI compiles the real entrypoint into t.TempDir(). The tests run
// the shipped binary, not the in-process command functions, so exit codes,
// stdin handling and stream separation are the ones operators see.
func buildGatewayCLI(t *testing.T) string {
	t.Helper()
	gatewayDir, err := filepath.Abs(filepath.Join("..", "..", "..", "gateway"))
	if err != nil {
		t.Fatalf("resolve gateway module: %v", err)
	}
	if _, err := os.Stat(filepath.Join(gatewayDir, "go.mod")); err != nil {
		t.Fatalf("gateway module not found at %s: %v", gatewayDir, err)
	}
	binary := filepath.Join(t.TempDir(), "doops-gateway")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = gatewayDir
	build.Env = cliEnv()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build doops-gateway: %v\n%s", err, output)
	}
	return binary
}

func runGatewayCLI(t *testing.T, binary, stdin string, args ...string) cliRun {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = cliEnv()
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	return cliRun{stdout: stdout.String(), stderr: stderr.String(), exitCode: code}
}

func mustCLIStore(t *testing.T, dbPath string) *server.GatewayStore {
	t.Helper()
	store, err := server.OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatalf("open store %s: %v", dbPath, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func assertNoCredentialInOutput(t *testing.T, run cliRun, canaries ...string) {
	t.Helper()
	for _, canary := range canaries {
		if strings.Contains(run.stdout, canary) {
			t.Fatalf("credential %q echoed on stdout: %q", canary, run.stdout)
		}
		if strings.Contains(run.stderr, canary) {
			t.Fatalf("credential %q echoed on stderr: %q", canary, run.stderr)
		}
	}
}

func assertStrongGeneratedPassword(t *testing.T, generated string) {
	t.Helper()
	if len(generated) != generatedPasswordLength {
		t.Fatalf("generated password length = %d, want %d", len(generated), generatedPasswordLength)
	}
	if strings.ContainsAny(generated, "0O1lI") {
		t.Fatalf("generated password contains a look-alike character: %q", generated)
	}
	var lower, upper, digit, symbol bool
	for _, char := range generated {
		switch {
		case char >= 'a' && char <= 'z':
			lower = true
		case char >= 'A' && char <= 'Z':
			upper = true
		case char >= '0' && char <= '9':
			digit = true
		case strings.ContainsRune("!@#%^*-_=+", char):
			symbol = true
		default:
			t.Fatalf("generated password contains unexpected character %q", char)
		}
	}
	if !lower || !upper || !digit || !symbol {
		t.Fatalf("generated password misses a character class: lower=%v upper=%v digit=%v symbol=%v value=%q",
			lower, upper, digit, symbol, generated)
	}
}

func generatedPasswordFromOutput(t *testing.T, stdout string) string {
	t.Helper()
	var generated string
	prints := 0
	for _, line := range strings.Split(stdout, "\n") {
		value, ok := strings.CutPrefix(line, "password=")
		if !ok || value == "enabled" || strings.HasPrefix(value, "updated ") {
			continue
		}
		prints++
		generated = value
	}
	if prints != 1 {
		t.Fatalf("generated password must be printed exactly once, got %d in %q", prints, stdout)
	}
	return generated
}

func TestGatewayUserPasswordCLI(t *testing.T) {
	binary := buildGatewayCLI(t)

	t.Run("create_from_stdin_never_echoes_credential", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "gateway.db")
		run := runGatewayCLI(t, binary, cliPasswordCanary+"\n",
			"user", "create", "-db", db, "-name", "stdin-admin", "-password-stdin")
		if run.exitCode != 0 {
			t.Fatalf("exit = %d, want 0; stderr=%q", run.exitCode, run.stderr)
		}
		assertNoCredentialInOutput(t, run, cliPasswordCanary)
		if !strings.Contains(run.stdout, "name=stdin-admin") || !strings.Contains(run.stdout, "password=enabled") {
			t.Fatalf("success output must be metadata only, got %q", run.stdout)
		}
		store := mustCLIStore(t, db)
		if _, err := store.VerifyUserPassword("stdin-admin", cliPasswordCanary); err != nil {
			t.Fatalf("password read from stdin cannot log in: %v", err)
		}
	})

	t.Run("stdin_reader_strips_crlf", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "gateway.db")
		run := runGatewayCLI(t, binary, cliPasswordCanary+"\r\n",
			"user", "create", "-db", db, "-name", "crlf-admin", "-password-stdin")
		if run.exitCode != 0 {
			t.Fatalf("exit = %d, want 0; stderr=%q", run.exitCode, run.stderr)
		}
		store := mustCLIStore(t, db)
		if _, err := store.VerifyUserPassword("crlf-admin", cliPasswordCanary); err != nil {
			t.Fatalf("CRLF must be trimmed from the stdin line: %v", err)
		}
	})

	t.Run("empty_stdin_fails_closed", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "gateway.db")
		run := runGatewayCLI(t, binary, "\n",
			"user", "create", "-db", db, "-name", "empty-admin", "-password-stdin")
		if run.exitCode != 2 {
			t.Fatalf("exit = %d, want 2; stderr=%q", run.exitCode, run.stderr)
		}
		if !strings.Contains(run.stderr, "no password on stdin") {
			t.Fatalf("empty stdin must be explained, got %q", run.stderr)
		}
		if _, err := os.Stat(db); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("store must not be created before credential validation: %v", err)
		}
	})

	t.Run("argv_passwd_fails_closed", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "gateway.db")
		for _, args := range [][]string{
			{"user", "create", "-db", db, "-name", "argv-admin", "-password", "argv-only-canary"},
			{"user", "passwd", "-db", db, "-name", "argv-admin", "-password", "argv-only-canary"},
		} {
			run := runGatewayCLI(t, binary, "", args...)
			if run.exitCode != 2 {
				t.Fatalf("%v: exit = %d, want 2; stdout=%q stderr=%q", args, run.exitCode, run.stdout, run.stderr)
			}
			for _, want := range []string{"-password-stdin", "-generate-password", "ps", "shell history"} {
				if !strings.Contains(run.stderr, want) {
					t.Fatalf("%v: stderr must explain the refusal and name %q, got %q", args, want, run.stderr)
				}
			}
			assertNoCredentialInOutput(t, run, "argv-only-canary")
		}
		if _, err := os.Stat(db); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("refused argv password must not open the store: %v", err)
		}
	})

	t.Run("conflicting_flags_name_both", func(t *testing.T) {
		cases := []struct {
			name       string
			args       []string
			firstFlag  string
			secondFlag string
		}{
			{
				name:       "argv_and_stdin",
				args:       []string{"-password", "argv-only-canary", "-password-stdin"},
				firstFlag:  "-password",
				secondFlag: "-password-stdin",
			},
			{
				name:       "argv_and_generate",
				args:       []string{"-password", "argv-only-canary", "-generate-password"},
				firstFlag:  "-password",
				secondFlag: "-generate-password",
			},
			{
				name:       "stdin_and_generate",
				args:       []string{"-password-stdin", "-generate-password"},
				firstFlag:  "-password-stdin",
				secondFlag: "-generate-password",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				db := filepath.Join(t.TempDir(), "gateway.db")
				args := append([]string{"user", "create", "-db", db, "-name", "conflict-admin"}, tc.args...)
				run := runGatewayCLI(t, binary, cliPasswordCanary+"\n", args...)
				if run.exitCode != 2 {
					t.Fatalf("exit = %d, want 2; stderr=%q", run.exitCode, run.stderr)
				}
				want := "conflicting flags " + tc.firstFlag + " and " + tc.secondFlag
				if !strings.Contains(run.stderr, want) {
					t.Fatalf("stderr must name both conflicting flags (%q), got %q", want, run.stderr)
				}
				assertNoCredentialInOutput(t, run, "argv-only-canary", cliPasswordCanary)
			})
		}
	})

	t.Run("generate_prints_strong_password_once", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "gateway.db")
		run := runGatewayCLI(t, binary, "",
			"user", "create", "-db", db, "-name", "generated-admin", "-generate-password")
		if run.exitCode != 0 {
			t.Fatalf("exit = %d, want 0; stderr=%q", run.exitCode, run.stderr)
		}
		generated := generatedPasswordFromOutput(t, run.stdout)
		assertStrongGeneratedPassword(t, generated)
		if strings.Count(run.stdout, generated) != 1 {
			t.Fatalf("generated password must be printed once, stdout=%q", run.stdout)
		}
		if !strings.Contains(run.stdout, "will not be shown again") {
			t.Fatalf("generated password must warn that it is not shown again, got %q", run.stdout)
		}
		store := mustCLIStore(t, db)
		if _, err := store.VerifyUserPassword("generated-admin", generated); err != nil {
			t.Fatalf("generated password cannot log in: %v", err)
		}
	})

	t.Run("passwd_from_stdin_rotates_credential", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "gateway.db")
		rotated := cliPasswordCanary + "-rotated"
		created := runGatewayCLI(t, binary, cliPasswordCanary+"\n",
			"user", "create", "-db", db, "-name", "rotate-admin", "-password-stdin")
		if created.exitCode != 0 {
			t.Fatalf("create exit = %d; stderr=%q", created.exitCode, created.stderr)
		}
		updated := runGatewayCLI(t, binary, rotated+"\n",
			"user", "passwd", "-db", db, "-name", "rotate-admin", "-password-stdin")
		if updated.exitCode != 0 {
			t.Fatalf("passwd exit = %d; stderr=%q", updated.exitCode, updated.stderr)
		}
		assertNoCredentialInOutput(t, updated, cliPasswordCanary, rotated)
		if !strings.Contains(updated.stdout, "password=updated user=rotate-admin") {
			t.Fatalf("passwd output must be metadata only, got %q", updated.stdout)
		}
		store := mustCLIStore(t, db)
		if _, err := store.VerifyUserPassword("rotate-admin", cliPasswordCanary); !errors.Is(err, server.ErrUnauthorized) {
			t.Fatalf("old password must stop working, got err=%v", err)
		}
		if _, err := store.VerifyUserPassword("rotate-admin", rotated); err != nil {
			t.Fatalf("new password cannot log in: %v", err)
		}
	})

	t.Run("passwd_requires_a_credential_input", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "gateway.db")
		run := runGatewayCLI(t, binary, "", "user", "passwd", "-db", db, "-name", "rotate-admin")
		if run.exitCode != 2 {
			t.Fatalf("exit = %d, want 2; stderr=%q", run.exitCode, run.stderr)
		}
		if !strings.Contains(run.stderr, "-password-stdin") || !strings.Contains(run.stderr, "-generate-password") {
			t.Fatalf("stderr must name both supported inputs, got %q", run.stderr)
		}
	})

	t.Run("create_requires_a_credential_input", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "gateway.db")
		run := runGatewayCLI(t, binary, "", "user", "create", "-db", db, "-name", "no-credential-admin")
		if run.exitCode != 2 {
			t.Fatalf("exit = %d, want 2; stdout=%q stderr=%q", run.exitCode, run.stdout, run.stderr)
		}
		for _, want := range []string{"-password-stdin", "-generate-password", "required"} {
			if !strings.Contains(run.stderr, want) {
				t.Fatalf("stderr must name %q, got %q", want, run.stderr)
			}
		}
		if _, err := os.Stat(db); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("store must not be created when the credential input is missing: %v", err)
		}
	})
}
