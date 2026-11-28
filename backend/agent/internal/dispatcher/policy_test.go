package dispatcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rootArgvRule is the built-in argv-level rule. Tests name it explicitly so a
// failure shows which layer refused a command: the argv layer
// (deny_rm_root_filesystem) or the regex fallback (delete_root_filesystem).
const rootArgvRule = "deny_rm_root_filesystem"

// bypassCorpus is the INT-67 acceptance corpus: the shapes an operator can use
// to slip past a whole-command regex, with the rule that must refuse each one.
var bypassCorpus = []struct {
	name    string
	command string
	rule    string
}{
	{"bare root", "rm -rf /", rootArgvRule},
	{"double slash", "rm -rf //", rootArgvRule},
	{"root glob", "rm -rf /*", rootArgvRule},
	{"dot segment", "rm -rf /./", rootArgvRule},
	{"dot dot segment", "rm -rf /../", rootArgvRule},
	{"quoted root", "rm -rf \"/\"", rootArgvRule},
	{"absolute binary", "/bin/rm -rf /", rootArgvRule},
	{"absolute binary split flags", "/usr/bin/rm -r -f /", rootArgvRule},
	{"long options", "rm --recursive --force /", rootArgvRule},
	{"trailing option", "rm -rf / --no-preserve-root", rootArgvRule},
	// The argv layer only parses a direct exec and never runs a shell, so the
	// shell builtin "command" is invisible to it. The case-insensitive regex
	// fallback is the layer that has to refuse this shape, and the rule name in
	// the assertion records exactly that.
	{"shell builtin command", "command rm -rf /", "delete_root_filesystem"},
}

func ruleOf(err error) string {
	var denied *BlockedError
	if !errors.As(err, &denied) {
		return ""
	}
	return denied.Rule
}

func writePolicy(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write policy file: %v", err)
	}
	return path
}

// TestBypassCorpusIsDeniedByStructuredLayer covers every bypass shape shell text
// can take. The rule name per row proves which layer refused it.
func TestBypassCorpusIsDeniedByStructuredLayer(t *testing.T) {
	for _, tt := range bypassCorpus {
		t.Run(tt.name, func(t *testing.T) {
			err := Check(tt.command)
			if got := ruleOf(err); got != tt.rule {
				t.Fatalf("Check(%q) = %v, want rule %s", tt.command, err, tt.rule)
			}
			denied, ok := err.(*BlockedError)
			if !ok || !strings.Contains(err.Error(), tt.rule) || !strings.Contains(err.Error(), denied.Hint) {
				t.Fatalf("denial must name the rule and the hint: %v", err)
			}
		})
	}
}

// TestBypassCorpusIsDeniedThroughDirectArgv checks the same shapes on the
// authoritative path: argv handed straight to exec, with no shell and no
// quoting involved.
func TestBypassCorpusIsDeniedThroughDirectArgv(t *testing.T) {
	argvs := [][]string{
		{"rm", "-rf", "/"},
		{"rm", "-rf", "//"},
		{"rm", "-rf", "/*"},
		{"rm", "-rf", "/./"},
		{"rm", "-rf", "/../"},
		{"/bin/rm", "-rf", "/"},
		{"/usr/bin/rm", "-r", "-f", "/"},
		{"rm", "--recursive", "--force", "/"},
		{"rm", "-rf", "/", "--no-preserve-root"},
	}
	for _, argv := range argvs {
		if _, err := CommandContext(context.Background(), argv[0], argv[1:]...); ruleOf(err) != rootArgvRule {
			t.Fatalf("CommandContext(%q) = %v, want %s", argv, err, rootArgvRule)
		}
	}
}

// TestSafeRemovalsAreAllowed is the other half of the corpus: the structured
// layer must not widen into ordinary cleanup.
func TestSafeRemovalsAreAllowed(t *testing.T) {
	commands := []string{
		"rm -rf /tmp/build",
		"rm -rf ./dist",
		"rm -rf /var/log/doops/old",
		"rm -f /tmp/x.txt",
		"rm -rf /data/backups/2026-11",
	}
	for _, command := range commands {
		if err := Check(command); err != nil {
			t.Fatalf("Check(%q) = %v, want allowed", command, err)
		}
	}
	for _, argv := range [][]string{
		{"rm", "-rf", "/tmp/build"},
		{"rm", "-rf", "./dist"},
		{"rm", "-rf", "/var/log/doops/old"},
		{"rm", "-f", "/tmp/x.txt"},
		{"rm", "-rf", "/data/backups/2026-11"},
	} {
		if _, err := CommandContext(context.Background(), argv[0], argv[1:]...); err != nil {
			t.Fatalf("CommandContext(%q) = %v, want allowed", argv, err)
		}
	}
}

// TestPolicyFileRulesMatchParsedArgv exercises each condition kind against the
// parsed argv: operands, a zero-based operand ordinal, an option word and a path
// prefix.
func TestPolicyFileRulesMatchParsedArgv(t *testing.T) {
	path := writePolicy(t, "{\"version\":1,\"rules\":["+
		"{\"name\":\"deny_mv_root\",\"tool\":\"mv\",\"hint\":\"keep the root in place\",\"args\":[{\"kind\":\"operand\",\"match\":\"^/$\"}]},"+
		"{\"name\":\"deny_rm_data_source\",\"tool\":\"rm\",\"hint\":\"keep /data\",\"args\":[{\"kind\":\"operandAt\",\"index\":0,\"match\":\"^/data$\"}]},"+
		"{\"name\":\"deny_find_delete_under_etc\",\"tool\":\"find\",\"hint\":\"list first\",\"args\":[{\"kind\":\"pathUnder\",\"prefix\":\"/etc\"},{\"kind\":\"argument\",\"match\":\"^-delete$\"}]}"+
		"]}")
	t.Setenv(PolicyEnv, path)
	if err := ValidateConfig(); err != nil {
		t.Fatalf("valid policy file rejected: %v", err)
	}

	tests := []struct {
		command string
		rule    string
	}{
		{"mv -f / /tmp/dropped", "deny_mv_root"},
		{"mv /root/keep /srv/keep", ""},
		{"rm -rf /data", "deny_rm_data_source"},
		{"rm -rf /data/backups", ""},
		{"find /etc -name '*.conf' -delete", "deny_find_delete_under_etc"},
		{"find /etc -name '*.conf'", ""},
		{"find /var/log -name '*.conf' -delete", ""},
		{"rm -rf /", rootArgvRule},
	}
	for _, tt := range tests {
		if got := ruleOf(Check(tt.command)); got != tt.rule {
			t.Fatalf("Check(%q) = %q, want %q", tt.command, got, tt.rule)
		}
	}
}

// TestStructuredRuleNameWinsOverRegex pins the documented order: the argv layer
// is consulted first, so its rule name is the one reported when both layers
// match the same command.
func TestStructuredRuleNameWinsOverRegex(t *testing.T) {
	path := writePolicy(t, "{\"rules\":[{\"name\":\"deny_mv_root\",\"tool\":\"mv\",\"hint\":\"keep the root in place\",\"args\":[{\"kind\":\"operand\",\"match\":\"^/$\"}]}]}")
	t.Setenv(PolicyEnv, path)
	t.Setenv(RulesEnv, "[{\"name\":\"regex_mv_root\",\"match\":\"mv .* /$\",\"hint\":\"regex fallback\"}]")

	if got := ruleOf(Check("mv -f /")); got != "deny_mv_root" {
		t.Fatalf("Check(mv -f /) = %q, want the argv rule deny_mv_root", got)
	}
	// A shape only the regex layer sees still reports the regex rule.
	if got := ruleOf(Check("sudo mv -f /")); got != "regex_mv_root" {
		t.Fatalf("Check(sudo mv -f /) = %q, want the regex rule regex_mv_root", got)
	}
	denied := ReportedDenial("blocked_command(deny_mv_root): next: keep the root in place; matched: \"mv -f /\"\n")
	if denied == nil || denied.Rule != "deny_mv_root" {
		t.Fatalf("ReportedDenial did not resolve the argv rule: %#v", denied)
	}
}

// TestPolicyArgvParsingHandlesTerminatorsAndRedirections shows why matching on
// parsed argv matters: redirections are shell syntax and must not become
// operands, while "--" ends option parsing so a dash word after it is a path.
func TestPolicyArgvParsingHandlesTerminatorsAndRedirections(t *testing.T) {
	path := writePolicy(t, "{\"rules\":["+
		"{\"name\":\"deny_shred_root\",\"tool\":\"shred\",\"hint\":\"name the file\",\"args\":[{\"kind\":\"operandAt\",\"index\":0,\"match\":\"^/$\"}]},"+
		"{\"name\":\"deny_touch_dash_file\",\"tool\":\"touch\",\"hint\":\"name the file\",\"args\":[{\"kind\":\"operandAt\",\"index\":0,\"match\":\"^-f$\"}]}"+
		"]}")
	t.Setenv(PolicyEnv, path)

	tests := []struct {
		command string
		rule    string
	}{
		{"shred -u / > /dev/null", "deny_shred_root"},
		{"shred -u / 2>>log", "deny_shred_root"},
		{"shred -- /", "deny_shred_root"},
		{"shred -u /tmp/x", ""},
		{"touch -- -f", "deny_touch_dash_file"},
		{"touch -f", ""},
	}
	for _, tt := range tests {
		if got := ruleOf(Check(tt.command)); got != tt.rule {
			t.Fatalf("Check(%q) = %q, want %q", tt.command, got, tt.rule)
		}
	}
}

// TestBrokenPolicyFailsClosed keeps a broken policy file from silently leaving
// only the regex fallback in place.
func TestBrokenPolicyFailsClosed(t *testing.T) {
	bodies := map[string]string{
		"empty file":            "",
		"not an object":         "[]",
		"unknown field":         "{\"rules\":[{\"name\":\"x\",\"tool\":\"rm\",\"hint\":\"h\",\"args\":[{\"kind\":\"operand\",\"match\":\"^/$\"}],\"other\":1}]}",
		"trailing data":         "{\"rules\":[]} {}",
		"unsupported version":   "{\"version\":2,\"rules\":[]}",
		"unknown condition":     "{\"rules\":[{\"name\":\"x\",\"tool\":\"rm\",\"hint\":\"h\",\"args\":[{\"kind\":\"nope\",\"match\":\"^/$\"}]}]}",
		"invalid regex":         "{\"rules\":[{\"name\":\"x\",\"tool\":\"rm\",\"hint\":\"h\",\"args\":[{\"kind\":\"operand\",\"match\":\"[\"}]}]}",
		"tool with a path":      "{\"rules\":[{\"name\":\"x\",\"tool\":\"/bin/rm\",\"hint\":\"h\",\"args\":[{\"kind\":\"operand\",\"match\":\"^/$\"}]}]}",
		"missing hint":          "{\"rules\":[{\"name\":\"x\",\"tool\":\"rm\",\"args\":[{\"kind\":\"operand\",\"match\":\"^/$\"}]}]}",
		"no conditions":         "{\"rules\":[{\"name\":\"x\",\"tool\":\"rm\",\"hint\":\"h\",\"args\":[]}]}",
		"duplicate name":        "{\"rules\":[{\"name\":\"x\",\"tool\":\"rm\",\"hint\":\"h\",\"args\":[{\"kind\":\"operand\",\"match\":\"^/$\"}]},{\"name\":\"x\",\"tool\":\"rm\",\"hint\":\"h\",\"args\":[{\"kind\":\"operand\",\"match\":\"^/$\"}]}]}",
		"collides with regex":   "{\"rules\":[{\"name\":\"delete_root_filesystem\",\"tool\":\"rm\",\"hint\":\"h\",\"args\":[{\"kind\":\"operand\",\"match\":\"^/$\"}]}]}",
		"collides with builtin": "{\"rules\":[{\"name\":\"deny_rm_root_filesystem\",\"tool\":\"rm\",\"hint\":\"h\",\"args\":[{\"kind\":\"operand\",\"match\":\"^/$\"}]}]}",
		"operandAt no index":    "{\"rules\":[{\"name\":\"x\",\"tool\":\"rm\",\"hint\":\"h\",\"args\":[{\"kind\":\"operandAt\",\"match\":\"^/$\"}]}]}",
		"pathUnder no prefix":   "{\"rules\":[{\"name\":\"x\",\"tool\":\"rm\",\"hint\":\"h\",\"args\":[{\"kind\":\"pathUnder\",\"match\":\"^/$\"}]}]}",
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			t.Setenv(PolicyEnv, writePolicy(t, body))
			if ValidateConfig() == nil {
				t.Fatalf("ValidateConfig accepted a broken policy: %s", body)
			}
			if err := Check("printf ok"); err == nil {
				t.Fatalf("Check accepted a broken policy: %s", body)
			}
		})
	}

	t.Run("empty path", func(t *testing.T) {
		t.Setenv(PolicyEnv, "   ")
		if ValidateConfig() == nil || Check("printf ok") == nil {
			t.Fatal("an empty policy path must fail closed")
		}
	})
	t.Run("missing file", func(t *testing.T) {
		t.Setenv(PolicyEnv, filepath.Join(t.TempDir(), "absent.json"))
		if ValidateConfig() == nil || Check("printf ok") == nil {
			t.Fatal("a missing policy file must fail closed")
		}
	})
	t.Run("oversized file", func(t *testing.T) {
		t.Setenv(PolicyEnv, writePolicy(t, strings.Repeat(" ", maxPolicyFile+1)))
		if ValidateConfig() == nil || Check("printf ok") == nil {
			t.Fatal("an oversized policy file must fail closed")
		}
	})
}

// TestRegexLayerAloneAllowsBypasses is reverse experiment (a). With the argv
// layer removed, several corpus shapes run. That is what proves the structured
// layer, and not a lucky regex, is refusing them.
func TestRegexLayerAloneAllowsBypasses(t *testing.T) {
	rules, err := configuredRules()
	if err != nil {
		t.Fatalf("configuredRules: %v", err)
	}
	var allowed []string
	for _, tt := range bypassCorpus {
		if matchCommand(rules, tt.command) == nil {
			allowed = append(allowed, tt.command)
		}
	}
	if len(allowed) < 3 {
		t.Fatalf("the regex layer alone refused %d of %d bypass shapes; the corpus no longer proves the structured layer does the work", len(bypassCorpus)-len(allowed), len(bypassCorpus))
	}
	t.Logf("regex layer alone allows %d of %d bypass shapes: %q", len(allowed), len(bypassCorpus), allowed)
}

// TestWholeTextMatchingLosesArgvShapes is reverse experiment (b). Matching the
// whole command as a single word loses quote removal and short-option merging,
// so the same commands get through. Neither shape can be caught by treating the
// text as one token.
func TestWholeTextMatchingLosesArgvShapes(t *testing.T) {
	path := writePolicy(t, "{\"rules\":[{\"name\":\"deny_rm_force_root\",\"tool\":\"rm\",\"hint\":\"keep the root\",\"args\":[{\"kind\":\"argument\",\"match\":\"^-f$\"},{\"kind\":\"operand\",\"match\":\"^/$\"}]}]}")
	t.Setenv(PolicyEnv, path)
	rules, err := structuredRules()
	if err != nil {
		t.Fatalf("structuredRules: %v", err)
	}
	for _, command := range []string{"rm -rf /", "rm -rf \"/\""} {
		parsed := false
		for _, argv := range splitSimpleCommands(command) {
			if matchStructured(rules, command, argv) != nil {
				parsed = true
			}
		}
		if !parsed {
			t.Fatalf("parsed argv did not refuse %q, so the experiment proves nothing", command)
		}
		wholeText := matchStructured(rules, command, []word{{value: command, raw: command}})
		if wholeText != nil {
			t.Fatalf("whole-text matching refused %q; the argv parsing is not what catches it", command)
		}
		t.Logf("whole-text matching lets %q through while parsed argv refuses it", command)
	}
}
