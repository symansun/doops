package dispatcher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PolicyEnv names the optional structured-policy file. The file is JSON; its
// format, the condition kinds and the argv-only boundary are documented once in
// backend/docs/design/COMMAND_FILTER.md.
const PolicyEnv = "DOOPS_COMMAND_POLICY_FILE"

// maxPolicyFile bounds how much the Agent reads from a policy file, so a
// mistyped path such as /dev/zero cannot stall the filter.
const maxPolicyFile = 1 << 20

// Condition kinds a structured rule may use. Each one is evaluated against the
// parsed argv, never against the whole command text.
const (
	// conditionOperand matches one operand (an argv word that is not an
	// option) against a regex, after lexical path normalization.
	conditionOperand = "operand"
	// conditionOperandAt matches the operand at a zero-based ordinal.
	conditionOperandAt = "operandAt"
	// conditionArgument matches any argv word after the tool, options included,
	// against the value as written (quotes removed, no path normalization).
	conditionArgument = "argument"
	// conditionPathUnder matches any operand whose normalized path is the
	// prefix or lies below it.
	conditionPathUnder = "pathUnder"
)

// policyFile is the JSON document a policy file must contain.
type policyFile struct {
	// Version is optional and currently may only be 1.
	Version *int         `json:"version,omitempty"`
	Rules   []policyRule `json:"rules"`
}

// policyRule is one structured denial: a tool plus the argv conditions that
// make it refusable.
type policyRule struct {
	Name string            `json:"name"`
	Tool string            `json:"tool"`
	Hint string            `json:"hint"`
	Args []policyCondition `json:"args"`
}

type policyCondition struct {
	Kind   string `json:"kind"`
	Index  *int   `json:"index,omitempty"`
	Match  string `json:"match,omitempty"`
	Prefix string `json:"prefix,omitempty"`
}

// structuredRule is a compiled policyRule.
type structuredRule struct {
	name       string
	tool       string
	hint       string
	conditions []structuredCondition
}

type structuredCondition struct {
	kind   string
	index  int
	match  *regexp.Regexp
	prefix string
}

// builtinStructuredPolicy is the argv-level layer the Agent always applies.
// It cannot be replaced or switched off: a policy file only adds rules. The
// name stays distinct from the regex rule "delete_root_filesystem" so a denial
// shows which layer refused the command, and the structured name wins when both
// layers match.
var builtinStructuredPolicy = []policyRule{
	{
		Name: "deny_rm_root_filesystem",
		Tool: "rm",
		Hint: "deleting the root filesystem is never allowed; name the exact path to remove",
		Args: []policyCondition{{Kind: conditionOperand, Match: "^/(\\*)?$"}},
	},
}

// structuredRules returns the built-in argv rules plus the rules from the file
// named by PolicyEnv. It fails closed: an unreadable, oversized, malformed or
// colliding policy is an error, so a broken policy file can never quietly drop
// the structured layer and leave only the regex fallback.
func structuredRules() ([]structuredRule, error) {
	builtin, err := compilePolicy("built-in policy", builtinStructuredPolicy)
	if err != nil {
		return nil, err
	}
	// The regex layer is loaded first so a structured rule can never shadow an
	// existing rule name of either layer.
	regexRules, err := configuredRules()
	if err != nil {
		return nil, err
	}
	taken := make(map[string]bool, len(regexRules)+len(builtin))
	for _, existing := range regexRules {
		taken[existing.name] = true
	}
	for _, existing := range builtin {
		if taken[existing.name] {
			return nil, fmt.Errorf("built-in policy rule %q collides with a regex rule", existing.name)
		}
		taken[existing.name] = true
	}

	raw, ok := os.LookupEnv(PolicyEnv)
	if !ok {
		return builtin, nil
	}
	path := strings.TrimSpace(raw)
	if path == "" {
		return nil, fmt.Errorf("%s must name a policy file", PolicyEnv)
	}
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", PolicyEnv, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxPolicyFile+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", PolicyEnv, err)
	}
	if len(data) > maxPolicyFile {
		return nil, fmt.Errorf("%s: policy file is larger than %d bytes", PolicyEnv, maxPolicyFile)
	}
	var document policyFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("%s: policy file must be a JSON object of {version?, rules}: %w", PolicyEnv, err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, fmt.Errorf("%s: policy file contains trailing data", PolicyEnv)
	}
	if document.Version != nil && *document.Version != 1 {
		return nil, fmt.Errorf("%s: unsupported policy version %d", PolicyEnv, *document.Version)
	}
	extra, err := compilePolicy(PolicyEnv, document.Rules)
	if err != nil {
		return nil, err
	}
	for _, rule := range extra {
		if taken[rule.name] {
			return nil, fmt.Errorf("%s: rule name %q already exists in the regex layer, the built-in policy or the file", PolicyEnv, rule.name)
		}
		taken[rule.name] = true
	}
	return append(builtin, extra...), nil
}

// compilePolicy validates one policy document. Every rule needs a unique name, a
// bare tool name, a hint and at least one argv condition.
func compilePolicy(source string, rules []policyRule) ([]structuredRule, error) {
	compiled := make([]structuredRule, 0, len(rules))
	names := make(map[string]bool, len(rules))
	for index, entry := range rules {
		name := strings.TrimSpace(entry.Name)
		if !ruleName.MatchString(name) || names[name] {
			return nil, fmt.Errorf("%s[%d] requires a unique rule name", source, index)
		}
		tool := strings.TrimSpace(entry.Tool)
		if tool == "" || strings.ContainsAny(tool, " 	/") {
			return nil, fmt.Errorf("%s[%d] (%s): tool must be a bare program name such as rm", source, index, name)
		}
		hint := strings.TrimSpace(entry.Hint)
		if hint == "" {
			return nil, fmt.Errorf("%s[%d] (%s): hint is required", source, index, name)
		}
		if len(entry.Args) == 0 {
			return nil, fmt.Errorf("%s[%d] (%s): at least one args condition is required", source, index, name)
		}
		conditions, err := compileConditions(source, index, name, entry.Args)
		if err != nil {
			return nil, err
		}
		names[name] = true
		compiled = append(compiled, structuredRule{name: name, tool: tool, hint: hint, conditions: conditions})
	}
	return compiled, nil
}

func compileConditions(source string, ruleIndex int, ruleName string, conditions []policyCondition) ([]structuredCondition, error) {
	compiled := make([]structuredCondition, 0, len(conditions))
	for index, condition := range conditions {
		label := fmt.Sprintf("%s[%d] (%s) args[%d]", source, ruleIndex, ruleName, index)
		condition.Kind = strings.TrimSpace(condition.Kind)
		switch condition.Kind {
		case conditionOperand, conditionArgument:
			if strings.TrimSpace(condition.Match) == "" || condition.Index != nil || condition.Prefix != "" {
				return nil, fmt.Errorf("%s: %s needs only a match regex", label, condition.Kind)
			}
			pattern, err := regexp.Compile(condition.Match)
			if err != nil {
				return nil, fmt.Errorf("%s: invalid regex: %w", label, err)
			}
			compiled = append(compiled, structuredCondition{kind: condition.Kind, match: pattern})
		case conditionOperandAt:
			if condition.Index == nil || *condition.Index < 0 || strings.TrimSpace(condition.Match) == "" || condition.Prefix != "" {
				return nil, fmt.Errorf("%s: operandAt needs a non-negative index and a match regex", label)
			}
			pattern, err := regexp.Compile(condition.Match)
			if err != nil {
				return nil, fmt.Errorf("%s: invalid regex: %w", label, err)
			}
			compiled = append(compiled, structuredCondition{kind: condition.Kind, index: *condition.Index, match: pattern})
		case conditionPathUnder:
			if strings.TrimSpace(condition.Prefix) == "" || condition.Index != nil || condition.Match != "" {
				return nil, fmt.Errorf("%s: pathUnder needs only a prefix", label)
			}
			prefix := normalizePath(strings.TrimSpace(condition.Prefix))
			if prefix == "" {
				return nil, fmt.Errorf("%s: pathUnder prefix must be an absolute path", label)
			}
			if !strings.HasPrefix(prefix, "/") {
				prefix = "/" + prefix
			}
			compiled = append(compiled, structuredCondition{kind: condition.Kind, prefix: prefix})
		default:
			return nil, fmt.Errorf("%s: unknown kind %q (want operand, operandAt, argument or pathUnder)", label, condition.Kind)
		}
	}
	return compiled, nil
}

// matchStructured runs the structured layer over one parsed simple command. It
// returns the first matching rule with a bounded excerpt of the word that
// matched.
func matchStructured(rules []structuredRule, command string, argv []word) *BlockedError {
	if len(argv) == 0 {
		return nil
	}
	tool := filepath.Base(argv[0].value)
	if tool == "" || tool == "." || tool == "/" {
		return nil
	}
	for _, rule := range rules {
		if rule.tool != tool {
			continue
		}
		if location, ok := rule.match(argv); ok {
			return &BlockedError{Rule: rule.name, Hint: rule.hint, Matched: matchedExcerpt(command, location)}
		}
	}
	return nil
}

// match reports whether every condition of the rule holds and where the first
// satisfied condition matched.
func (r structuredRule) match(argv []word) ([2]int, bool) {
	var location [2]int
	for index, condition := range r.conditions {
		word, ok := condition.find(argv)
		if !ok {
			return [2]int{}, false
		}
		if index == 0 {
			location = [2]int{word.offset, word.offset + len(word.raw)}
		}
	}
	return location, true
}

func (c structuredCondition) find(argv []word) (word, bool) {
	if c.kind == conditionArgument {
		for _, current := range argv[1:] {
			if c.match.MatchString(current.value) {
				return current, true
			}
		}
		return word{}, false
	}
	ordinal := 0
	for _, current := range argv[1:] {
		if current.flag {
			continue
		}
		switch c.kind {
		case conditionOperand:
			if c.match.MatchString(normalizePath(current.value)) {
				return current, true
			}
		case conditionOperandAt:
			if ordinal == c.index && c.match.MatchString(normalizePath(current.value)) {
				return current, true
			}
		case conditionPathUnder:
			if pathUnder(normalizePath(current.value), c.prefix) {
				return current, true
			}
		}
		ordinal++
	}
	return word{}, false
}

// pathUnder reports whether the normalized path is the prefix itself or lies
// below it. The root prefix matches every absolute path, which is why the
// built-in root rule uses an operand regex instead.
func pathUnder(path, prefix string) bool {
	if path == "" {
		return false
	}
	if prefix == "/" {
		return strings.HasPrefix(path, "/")
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// checkArgv applies the structured layer to argv that will be exec'd directly,
// with no shell in between. It is the authoritative path: nothing here depends
// on shell quoting.
func checkArgv(name string, args ...string) error {
	rules, err := structuredRules()
	if err != nil {
		return err
	}
	argv := make([]string, 0, len(args)+1)
	argv = append(argv, name)
	argv = append(argv, args...)
	words := commandWords(argv)
	if denied := matchStructured(rules, strings.Join(argv, " "), words); denied != nil {
		return denied
	}
	return nil
}
