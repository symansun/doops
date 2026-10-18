package dispatcher

import (
	"fmt"
	"regexp"
	"strings"
)

// rule is one command shape the Agent refuses. Every rule names the supported
// alternative so the caller can switch paths instead of guessing why it failed.
type rule struct {
	name  string
	hint  string
	match *regexp.Regexp
}

// Deleting the root filesystem is the only command shape the Agent refuses;
// everything else a target grant allows runs as given.
var blockRules = []rule{
	{
		name:  "delete_root_filesystem",
		hint:  "deleting the root filesystem is never allowed; name the exact path to remove",
		match: regexp.MustCompile(`\brm\s+(-[a-z]*\s+)*-[a-z]*[rf][a-z]*\s+(-[a-z]*\s+)*/(\*)?(\s|;|&|\||$)`),
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
		if r.match.MatchString(lower) {
			return &BlockedError{Rule: r.name, Hint: r.hint}
		}
	}
	return nil
}
