package dispatcher

import (
	"fmt"
	"regexp"
	"strings"
)

var helmMutationPattern = regexp.MustCompile(
	`(?s)\bhelm\b.*\b(upgrade|install|rollback|uninstall)\b`,
)

// rule is one command shape the Agent refuses. Every rule names the supported
// alternative so the caller can switch paths instead of guessing why it failed.
type rule struct {
	name   string
	hint   string
	match  *regexp.Regexp
	except *regexp.Regexp
}

// An Agent cannot replace its own workload from its own process; everything else
// a target grant allows runs as given.
var selfReplacementHint = "an Agent cannot replace its own workload; use `doops upgrade` for a Helm Agent " +
	"or the external release scripts in docs/BUILD_DEPLOY_SOP.md"

var blockRules = []rule{
	{
		name:  "delete_root_filesystem",
		hint:  "deleting the root filesystem is never allowed; name the exact path to remove",
		match: regexp.MustCompile(`\brm\s+(-[a-z]*\s+)*-[a-z]*[rf][a-z]*\s+(-[a-z]*\s+)*/(\*)?(\s|;|&|\||$)`),
	},
	{
		name:  "format_filesystem",
		hint:  "formatting a filesystem is not allowed through exec",
		match: regexp.MustCompile(`\bmkfs(\.\w+)?\b`),
	},
	{
		name:   "edit_partitions",
		hint:   "editing partitions is not allowed through exec; `fdisk -l` and `lsblk` are allowed",
		match:  regexp.MustCompile(`\bfdisk\b`),
		except: regexp.MustCompile(`\bfdisk\s+(-l|--list)\b`),
	},
	{
		name:  "agent_self_replacement",
		hint:  selfReplacementHint,
		match: regexp.MustCompile(`(?s)\b(kubectl|oc)\b.*\b(set\s+image|rollout\s+(restart|undo)|apply|delete|edit|patch|replace|scale)\b.*\bdoops-agent\b`),
	},
	{
		name:  "agent_self_replacement",
		hint:  selfReplacementHint,
		match: regexp.MustCompile(`(?s)\b(docker|nerdctl|podman|crictl)\b.*\b(restart|stop|rm|update|run)\b.*\bdoops-agent\b`),
	},
	{
		name:  "agent_self_replacement",
		hint:  selfReplacementHint,
		match: regexp.MustCompile(`(?s)\bsystemctl\b.*\b(restart|stop)\b.*\bdoops-agent\b`),
	},
	{
		name:  "agent_self_replacement",
		hint:  selfReplacementHint,
		match: regexp.MustCompile(`(?s)\bservice\b.*\bdoops-agent\b.*\b(restart|stop)\b`),
	},
	{
		name:  "agent_self_replacement",
		hint:  selfReplacementHint,
		match: regexp.MustCompile(`(?s)\b(pkill|killall)\b.*\bdoops-agent\b`),
	},
}

// BlockedError reports which rule refused a command and what to do instead.
type BlockedError struct {
	Rule string
	Hint string
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("blocked_command(%s): %s", e.Rule, e.Hint)
}

type Dispatcher struct {
	rules []rule
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{rules: blockRules}
}

// Check returns a *BlockedError for the first rule the command hits, or nil.
func (d *Dispatcher) Check(command string) error {
	lower := strings.ToLower(strings.TrimSpace(command))
	for _, r := range d.rules {
		if r.match.MatchString(lower) && (r.except == nil || !r.except.MatchString(lower)) {
			return &BlockedError{Rule: r.name, Hint: r.hint}
		}
	}
	return nil
}

func (d *Dispatcher) IsHelmMutation(command string) bool {
	return helmMutationPattern.MatchString(strings.ToLower(strings.TrimSpace(command)))
}
