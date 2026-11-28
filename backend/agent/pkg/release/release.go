package release

import "strings"

// Version is the published DoOps release tag embedded by CI into every
// DoOps binary. Local development builds intentionally keep the dev value.
var Version = "dev"

// Commit is immutable build provenance, not a second runtime version.
var Commit = "unknown"

func ValidCommit(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 40 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
