package server

import "github.com/user/doops/agent/internal/dispatcher"

// The dispatcher package must not import server (server starts the managed
// doagent and mounts the filtering wrapper), so it exposes two narrow hooks.
// The server owns the single redaction helper used for command summaries and
// installs it here, so a denial match and an audit command summary never
// disagree about what is a credential.
func init() {
	dispatcher.Redactor = redactSecretText
	dispatcher.MatchedCap = utf8Head
}
