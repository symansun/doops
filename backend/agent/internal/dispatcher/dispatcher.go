package dispatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"unicode/utf8"
)

// rule is one command shape the Agent refuses. Every rule names the supported
// alternative so the caller can switch paths instead of guessing why it failed.
type rule struct {
	name  string
	hint  string
	match *regexp.Regexp
}

var blockRules = []rule{
	{
		name:  "delete_root_filesystem",
		hint:  "deleting the root filesystem is never allowed; name the exact path to remove",
		match: regexp.MustCompile(`(?i)\brm\s+(-[a-z]*\s+)*-[a-z]*[rf][a-z]*\s+(-[a-z]*\s+)*/(\*)?(\s|;|&|\||$)`),
	},
}

// MatchedLimit bounds the Matched excerpt in BlockedError. Only the first
// MatchedLimit bytes of the submitted command are ever copied; the rest never
// leaves the filter.
const MatchedLimit = 80

// matchContext bounds how much of the command on either side of the match is
// kept. It stays well below MatchedLimit.
const matchContext = 24

var (
	// Redactor redacts credential-like text from a Matched excerpt. The server
	// package owns the single redaction helper and installs it at startup.
	Redactor func(string) string
	// MatchedCap keeps a Matched excerpt within limit bytes of valid UTF-8
	// without splitting a rune. The server package installs its utf8 helper.
	MatchedCap func(string, int) string
)

// BlockedError reports which rule refused a command, where the rule matched and
// what to do instead.
//
// Rule and Hint are trusted configuration. Matched is derived from the
// submitted command, so it is redacted and escaped before it is reported to a
// caller. It is never raw tool output.
type BlockedError struct {
	Rule string
	Hint string
	// Matched is the redacted, quoted, at most MatchLimit-byte excerpt of the
	// command where the rule matched. Empty when the excerpt cannot be shown.
	Matched string
}

// Error keeps the historical blocked_command(rule): next: hint prefix, which
// ReportedDenial and operators parse, and appends where the rule matched.
func (e *BlockedError) Error() string {
	message := fmt.Sprintf("blocked_command(%s): next: %s", e.Rule, e.Hint)
	if e.Matched != "" {
		message += fmt.Sprintf("; matched: %q", e.Matched)
	}
	return message
}

const RulesEnv = "DOOPS_COMMAND_DENY_RULES"

var ruleName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var plainArgument = regexp.MustCompile(`^[a-zA-Z0-9_./,:=@%+-]+$`)

func configuredRules() ([]rule, error) {
	raw, ok := os.LookupEnv(RulesEnv)
	if !ok {
		return blockRules, nil
	}
	var entries []struct{ Name, Match, Hint string }
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil || entries == nil {
		return nil, fmt.Errorf("%s must be a JSON array of {name, match, hint}", RulesEnv)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, fmt.Errorf("%s contains trailing data", RulesEnv)
	}
	rules := append([]rule(nil), blockRules...)
	names := make(map[string]bool, len(rules)+len(entries))
	for _, existing := range rules {
		names[existing.name] = true
	}
	for index, entry := range entries {
		if !ruleName.MatchString(entry.Name) || names[entry.Name] || strings.TrimSpace(entry.Match) == "" || strings.TrimSpace(entry.Hint) == "" {
			return nil, fmt.Errorf("%s[%d] requires a unique rule name, match and hint", RulesEnv, index)
		}
		pattern, err := regexp.Compile(entry.Match)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: invalid regex for %s: %w", RulesEnv, index, entry.Name, err)
		}
		names[entry.Name] = true
		rules = append(rules, rule{entry.Name, strings.TrimSpace(entry.Hint), pattern})
	}
	return rules, nil
}

func ValidateConfig() error {
	_, err := configuredRules()
	return err
}

func Check(command string) error {
	rules, err := configuredRules()
	if err != nil {
		return err
	}
	if denied := matchCommand(rules, command); denied != nil {
		return denied
	}
	return nil
}

// matchCommand returns the first rule that matches command, with a bounded
// excerpt of the match. The excerpt is escaped first, then redacted by the same
// helper that redacts command summaries, and cut to MatchedLimit bytes.
func matchCommand(rules []rule, command string) *BlockedError {
	command = strings.TrimSpace(command)
	for _, r := range rules {
		location := r.match.FindStringIndex(command)
		if location == nil {
			continue
		}
		denied := &BlockedError{Rule: r.name, Hint: r.hint}
		start := max(location[0]-matchContext, 0)
		if start > 0 {
			for start < location[0] && !utf8.RuneStart(command[start]) {
				start++
			}
		}
		end := min(location[1]+matchContext, len(command))
		for end < len(command) && !utf8.RuneStart(command[end]) {
			end++
		}
		excerpt := command[start:end]
		if Redactor != nil {
			excerpt = Redactor(excerpt)
		}
		if MatchedCap != nil {
			excerpt = MatchedCap(excerpt, MatchedLimit)
		} else if len(excerpt) > MatchedLimit {
			excerpt = ""
		}
		if strings.TrimSpace(excerpt) != "" {
			if start > 0 {
				excerpt = "..." + excerpt
			}
			if end < len(command) {
				excerpt += "..."
			}
			denied.Matched = excerpt
		}
		return denied
	}
	return nil
}

// ReportedDenial extracts a public diagnostic from an already-failed native
// tool call. Only return locally configured text, never arbitrary tool output.
//
// A legacy message without the matched excerpt is also accepted as a whole
// line, so an older Agent's text still maps to its rule.
func ReportedDenial(output string) *BlockedError {
	rules, err := configuredRules()
	if err != nil {
		return nil
	}
	for _, r := range rules {
		denied := &BlockedError{Rule: r.name, Hint: r.hint}
		prefix := fmt.Sprintf("blocked_command(%s): next: %s", denied.Rule, denied.Hint)
		offset := strings.Index(output, prefix)
		if offset < 0 {
			continue
		}
		rest := output[offset+len(prefix):]
		if rest == "" || rest[0] == '\n' || rest[0] == '\r' || rest[0] == ';' || strings.HasPrefix(rest, `; matched: `) {
			return denied
		}
	}
	return nil
}

// ShellCommand checks the submitted text before either caller starts Bash.
// doagent uses a login shell, while the gateway exec contract uses bash -c.
func ShellCommand(command string, login bool) (*exec.Cmd, error) {
	if err := Check(command); err != nil {
		return nil, err
	}
	if login {
		return exec.Command("/bin/bash", "-lc", command), nil
	}
	return exec.Command("bash", "-c", command), nil
}

func CommandContext(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	words := make([]string, 1, len(args)+1)
	words[0] = name
	for _, arg := range args {
		if !plainArgument.MatchString(arg) {
			arg = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
		}
		words = append(words, arg)
	}
	if err := Check(strings.Join(words, " ")); err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, name, args...), nil
}
