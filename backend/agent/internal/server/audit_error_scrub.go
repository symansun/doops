package server

import (
	"net/url"
	"strings"
	"time"
)

// unparsableURLPlaceholder replaces a URL that cannot be parsed. The original
// text is never kept: a URL the parser rejects is exactly the one whose
// credential boundaries are unknown, so keeping it would persist a secret.
const unparsableURLPlaceholder = "<unparsable-url>"

// urlTokenDelimiters end one scheme:// URL token inside free-form error text.
// Go's url.Error wraps its URL in double quotes, and nested error messages add
// the quote and bracket forms; parenthesis is deliberately absent because it is
// legal inside a path.
const urlTokenDelimiters = " \t\r\n\"'\x60<>"

// scrubURLCredentials rewrites every URL in text so that only the scheme, host
// (with port) and path survive. Userinfo, query and fragment are removed: an
// upstream transport error stringifies its full target, so a token that a
// client put in the query, or credentials a legacy upstream URL carried in
// userinfo, would otherwise reach audit_events.error verbatim.
//
// A URL is any "scheme://" token whose scheme matches RFC 3986
// ([A-Za-z][A-Za-z0-9+.-]*), not only http(s): historical audit rows also hold
// postgres://, redis:// and mysql:// targets with userinfo. Query is dropped
// for every scheme, without trying to judge which keys are harmless: a
// postgres URL's sslmode=require is not a secret, but the same position can
// carry a credential under a harmless-looking key, so the rule treats every
// query as credentials and prefers losing information over keeping a secret.
//
// The text outside the URLs is returned byte for byte unchanged, and every URL
// that cannot be parsed is replaced as a whole by "<unparsable-url>": losing the
// URL is preferable to persisting credentials in a shape we failed to identify.
func scrubURLCredentials(text string) string {
	if text == "" {
		return text
	}
	var out strings.Builder
	out.Grow(len(text))
	for i := 0; i < len(text); {
		start := nextURLStart(text, i)
		if start < 0 {
			out.WriteString(text[i:])
			break
		}
		out.WriteString(text[i:start])
		end := start
		for end < len(text) && !strings.ContainsRune(urlTokenDelimiters, rune(text[end])) {
			end++
		}
		out.WriteString(scrubOneURL(text[start:end]))
		i = end
	}
	return out.String()
}

// nextURLStart returns the index of the scheme of the first "scheme://" token
// at or after from, or -1 when the text holds no further URL. The scheme is
// walked backwards from the colon so a word that merely ends in "://" keeps its
// whole word as the scheme; a token whose prospective scheme does not start
// with a letter is not a URL and stays untouched.
func nextURLStart(text string, from int) int {
	for from < len(text) {
		offset := strings.Index(text[from:], "://")
		if offset < 0 {
			return -1
		}
		colon := from + offset
		start := colon
		for start > 0 && isURLSchemeChar(text[start-1]) {
			start--
		}
		if start < colon && isURLSchemeStart(text[start]) {
			return start
		}
		from = colon + 1
	}
	return -1
}

func isURLSchemeStart(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}

func isURLSchemeChar(b byte) bool {
	return isURLSchemeStart(b) || b >= '0' && b <= '9' || b == '+' || b == '-' || b == '.'
}

// scrubOneURL keeps scheme, host (with port) and path of one scheme:// URL and
// drops userinfo, query and fragment. http(s) keeps its historical strictness:
// an empty host fails closed. Other schemes may legitimately omit the
// authority (file:///path, unix:///run/x.sock); for those the URL fails closed
// only when it carries nothing but the scheme itself (a://). Anything Go's URL
// parser cannot read, or any opaque shape, still fails closed.
func scrubOneURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" {
		return unparsableURLPlaceholder
	}
	httpScheme := parsed.Scheme == "http" || parsed.Scheme == "https"
	if parsed.Host == "" && (httpScheme || parsed.Path == "") {
		return unparsableURLPlaceholder
	}
	return parsed.Scheme + "://" + parsed.Host + parsed.EscapedPath()
}

// scrubAuditError is the audit boundary for FinishAudit.Error text the Gateway
// did not author itself: an Agent or upstream error envelope can quote the URL
// its tool tried to reach, including userinfo and query values.
//
// The URL scrub runs first and the existing secret sanitizer second, on purpose.
// sanitizeSecretText trims the text to its 1200-byte tail; rewriting URLs before
// that trim means a URL is never cut in half (a truncated URL fails closed to
// "<unparsable-url>" and takes the host and path the audit row needs with it),
// and the sanitizer still redacts Authorization-style values the URL scrub does
// not know about.
func scrubAuditError(text string) string {
	return sanitizeSecretText(scrubURLCredentials(text))
}

// recordProxyFinish attaches a scrubbed terminal AuditFinish to the active
// proxy operation so finishActiveOperation persists audit_events, File-First
// audit logs, and any execution.finished webhook event in one atomic commit
// instead of double-writing audit_events.
func (c *ClientService) recordProxyFinish(opID string, finish AuditFinish) {
	finish.Error = scrubURLCredentials(finish.Error)
	if finish.EndedAt.IsZero() {
		finish.EndedAt = time.Now().UTC()
	}
	c.activeOpsMu.Lock()
	if op := c.activeOps[opID]; op != nil {
		copied := finish
		op.finish = &copied
	}
	c.activeOpsMu.Unlock()
}

// finishProxyAudit is the proxy tunnel's audit write boundary: the tunnel relays
// the Agent's http.Client.Do error text, which stringifies the whole upstream
// target, so the terminal error is de-credentialed before it can reach
// audit_events.error. Status, byte counts and every other field pass through
// untouched, as does the part of the error text that is not a URL.
func (c *ClientService) finishProxyAudit(id int64, finish AuditFinish) error {
	finish.Error = scrubURLCredentials(finish.Error)
	return c.finishExecutionAudit(id, finish)
}
