package dispatcher

import (
	"regexp"
	"strings"
)

type PathType int

const (
	PathFast PathType = iota
	PathSlow
	PathBlocked
)

type Dispatcher struct {
	blacklist     []*regexp.Regexp
	naturalLangRe *regexp.Regexp
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		blacklist: []*regexp.Regexp{
			regexp.MustCompile(`rm -rf /`),
			regexp.MustCompile(`fdisk`),
			regexp.MustCompile(`mkfs`),
			regexp.MustCompile(`(?s)\b(kubectl|oc)\b.*\b(set\s+image|rollout\s+(restart|undo)|apply|delete|edit|patch|replace|scale)\b.*\bdoops-agent\b`),
			regexp.MustCompile(`(?s)\bhelm\b.*\b(upgrade|install|rollback|uninstall)\b.*\bdoops-agent\b`),
			regexp.MustCompile(`(?s)\b(docker|nerdctl|podman|crictl)\b.*\b(restart|stop|rm|update|run)\b.*\bdoops-agent\b`),
			regexp.MustCompile(`(?s)\bsystemctl\b.*\b(restart|stop)\b.*\bdoops-agent\b`),
			regexp.MustCompile(`(?s)\bservice\b.*\bdoops-agent\b.*\b(restart|stop)\b`),
			regexp.MustCompile(`(?s)\b(pkill|killall)\b.*\bdoops-agent\b`),
		},
		// Natural language heuristic: if it contains only letters and spaces and is relatively long
		naturalLangRe: regexp.MustCompile(`^[\p{L}\s]{10,}$`),
	}
}

func (d *Dispatcher) Classify(command string) PathType {
	lower := strings.ToLower(strings.TrimSpace(command))

	// Check blacklist first
	for _, re := range d.blacklist {
		if re.MatchString(lower) {
			return PathBlocked
		}
	}

	// Check if it looks like natural language (Simplified heuristic)
	// If it contains spaces and no typical shell markers like / . - it might be NL
	if d.naturalLangRe.MatchString(command) && !strings.ContainsAny(command, "/.-_") {
		return PathSlow
	}

	return PathFast
}
