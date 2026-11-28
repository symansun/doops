package gatewaycmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/user/doops/agent/internal/server"
)

// secretKeyUsage is the single usage text for both the secret-key subcommand and
// its rotate verb. It names the environment variables but never a key value.
const secretKeyUsage = `usage: doops-gateway secret-key rotate -db <path> [-dry-run]

Re-encrypt every stored credential payload and the machine-login vault with
DOOPS_GATEWAY_SECRET_KEY, then move the key marker to that key. Run it while the
Gateway is stopped, or with only one Gateway instance writing.

The master keys are read from the environment only:
  DOOPS_GATEWAY_SECRET_KEY           primary key; the only key that encrypts
  DOOPS_GATEWAY_SECRET_KEY_PREVIOUS  old key; decrypt-only during the window

A key flag (for example -key) is refused: argv is visible to every local user in
'ps' and is recorded in shell history. Never pass a master key on argv.

Flags:
  -db string   Gateway SQLite database path (required)
  -dry-run     Count the payloads that still need the previous key and write
               nothing at all: no payload rewrite, no marker update, no file
               write

Output is counts and marker state only; keys, ciphertexts, digests and payloads
are never printed.

Exit codes:
  0  success, including -dry-run
  1  runtime failure; the marker is left unchanged and a stable category is
     printed
  2  usage error, including a key on argv
`

// secretKey dispatches the secret-key subcommands. rotate is the only verb: this
// command must never grow a "show" or "set" path that could expose a key.
func secretKey(args []string) {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, secretKeyUsage)
		os.Exit(2)
	}
	if name, found := secretKeyArgvFlag(args); found {
		fmt.Fprintf(os.Stderr, "doops-gateway secret-key: -%s is refused: argv is visible to every local user in 'ps' and is recorded in shell history, so a master key is accepted only from DOOPS_GATEWAY_SECRET_KEY and DOOPS_GATEWAY_SECRET_KEY_PREVIOUS\n\n", name)
		fmt.Fprint(os.Stderr, secretKeyUsage)
		os.Exit(2)
	}
	if isHelpFlag(args[0]) {
		fmt.Fprint(os.Stdout, secretKeyUsage)
		return
	}
	switch args[0] {
	case "rotate":
		secretKeyRotate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown secret-key command: %s\n\n", args[0])
		fmt.Fprint(os.Stderr, secretKeyUsage)
		os.Exit(2)
	}
}

// secretKeyArgvFlag reports the first flag whose name contains "key". It runs
// before flag parsing and before the store opens, so a key on argv is refused
// without touching the database.
func secretKeyArgvFlag(args []string) (string, bool) {
	for _, arg := range args {
		if len(arg) < 2 || arg[0] != '-' {
			continue
		}
		name := strings.TrimLeft(arg, "-")
		if name == "" {
			continue
		}
		name, _, _ = strings.Cut(name, "=")
		if strings.Contains(strings.ToLower(name), "key") {
			return name, true
		}
	}
	return "", false
}

func isHelpFlag(arg string) bool {
	switch arg {
	case "-h", "-help", "--help", "--h":
		return true
	}
	return false
}

func secretKeyRotate(args []string) {
	for _, arg := range args {
		if isHelpFlag(arg) {
			fmt.Fprint(os.Stdout, secretKeyUsage)
			return
		}
	}
	fs := flag.NewFlagSet("secret-key rotate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dbPath := fs.String("db", "", "Gateway SQLite database path (required)")
	dryRun := fs.Bool("dry-run", false, "Count the payloads that still need the previous key and write nothing")
	fs.Usage = func() { fmt.Fprint(fs.Output(), secretKeyUsage) }
	switch err := fs.Parse(args); {
	case err == nil:
	case errors.Is(err, flag.ErrHelp):
		return
	default:
		os.Exit(2)
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument: %s\n\n", fs.Arg(0))
		fmt.Fprint(os.Stderr, secretKeyUsage)
		os.Exit(2)
	}
	if strings.TrimSpace(*dbPath) == "" {
		fmt.Fprint(os.Stderr, "doops-gateway secret-key rotate: -db is required\n\n")
		fmt.Fprint(os.Stderr, secretKeyUsage)
		os.Exit(2)
	}

	ctx := context.Background()
	if *dryRun {
		preview, err := previewSecretRotation(ctx, *dbPath)
		if err != nil {
			secretKeyFail(err)
		}
		fmt.Printf("secret-key rotate: dry_run=true credential_versions=%d machine_login_vault=%d skipped=0 already_primary=%d\n",
			preview.CredentialVersions, preview.MachineLoginVault, preview.AlreadyPrimary)
		fmt.Println("secret-key marker: unchanged (dry-run)")
		return
	}

	store, err := server.OpenGatewayStore(*dbPath)
	if err != nil {
		secretKeyFail(err)
	}
	defer store.Close()
	report, err := store.RotateSecretKey(ctx, server.SecretRotationOptions{})
	if err != nil {
		secretKeyFail(err)
	}
	fmt.Printf("secret-key rotate: credential_versions=%d machine_login_vault=%d skipped=%d already_primary=%d\n",
		report.CredentialVersions, report.MachineLoginVault, report.Skipped, report.AlreadyPrimary)
	fmt.Println("secret-key marker: rotated")
}

// previewSecretRotation keeps -dry-run on the SQLite -db path read-only. The
// MySQL backend has no database file, so it keeps the store's own read-only
// query path instead.
func previewSecretRotation(ctx context.Context, dbPath string) (server.SecretRotationPreview, error) {
	if strings.TrimSpace(os.Getenv("DOOPS_GATEWAY_STORE")) == "mysql" {
		store, err := server.OpenGatewayStore(dbPath)
		if err != nil {
			return server.SecretRotationPreview{}, err
		}
		defer store.Close()
		return store.PreviewSecretRotation(ctx)
	}
	return server.PreviewSecretRotationAtPath(ctx, dbPath)
}

// secretKeyFail prints one stable category and exits 1. The library's errors
// carry table names only, and the category mapping drops the wrapped message so
// no path, key, ciphertext, digest or payload can reach the terminal.
func secretKeyFail(err error) {
	category := secretKeyErrorCategory(err)
	fmt.Fprintf(os.Stderr, "secret-key rotate: error=%s\n", category)
	fmt.Fprintf(os.Stderr, "secret-key marker: unchanged reason=%s\n", category)
	os.Exit(1)
}

func secretKeyErrorCategory(err error) string {
	if errors.Is(err, server.ErrSecretRotationPayload) && strings.TrimSpace(os.Getenv("DOOPS_GATEWAY_SECRET_KEY_PREVIOUS")) == "" {
		return "gateway_secret_key_previous_missing"
	}
	switch {
	case errors.Is(err, server.ErrSecretKeyConflict):
		return "gateway_secret_key_conflict"
	case errors.Is(err, server.ErrSecretKeyUnavailable):
		return "gateway_secret_key_unavailable"
	case errors.Is(err, server.ErrSecretKeyMismatch):
		return "gateway_secret_key_mismatch"
	case errors.Is(err, server.ErrSecretRotationIncomplete):
		return "gateway_secret_key_rotation_incomplete"
	case errors.Is(err, server.ErrSecretRotationPayload):
		return "gateway_secret_key_rotation_payload"
	default:
		return "gateway_secret_key_rotation_failed"
	}
}
