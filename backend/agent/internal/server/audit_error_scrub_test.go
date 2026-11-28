package server

// INT-70: audit error text scrubbing.
//
// audit_events.error is written from the Proxy tunnel's relay error, which is the
// Agent's *url.Error: Go stringifies the whole upstream target, so the client
// query (and, in the historical shape, userinfo and fragment) used to land in the
// audit row. scrubURLCredentials keeps scheme, host, port and path so the row
// still says which upstream failed, and drops everything that can carry a secret.

import (
	"strings"
	"testing"
)

func TestScrubURLCredentials(t *testing.T) {
	for _, test := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "url error keeps host path and wrapped error body",
			in:   `Get "https://user:pw-canary@host:8443/a/b?token=token-canary#f": dial tcp 10.0.0.1:8443: connect: connection refused`,
			want: `Get "https://host:8443/a/b": dial tcp 10.0.0.1:8443: connect: connection refused`,
		},
		{
			name: "text without a url is returned byte for byte",
			in:   `dial tcp 10.0.0.1:8443: connect: connection refused`,
			want: `dial tcp 10.0.0.1:8443: connect: connection refused`,
		},
		{
			name: "empty text",
			in:   "",
			want: "",
		},
		{
			name: "every url in the text is scrubbed",
			in:   `primary "http://a:pw1@one.test:8080/x?token=t1#frag" then "https://b:pw2@two.test/y?token=t2"`,
			want: `primary "http://one.test:8080/x" then "https://two.test/y"`,
		},
		{
			name: "unparsable url fails closed instead of keeping the original text",
			in:   `Get "http://host:8443/a/%zz?token=token-canary": dial tcp host:8443: connect: connection refused`,
			want: `Get "<unparsable-url>": dial tcp host:8443: connect: connection refused`,
		},
		{
			name: "userinfo only",
			in:   `Get "https://user:pw-canary@host:8443/a/b": dial tcp host:8443: connect: connection refused`,
			want: `Get "https://host:8443/a/b": dial tcp host:8443: connect: connection refused`,
		},
		{
			name: "query only",
			in:   "Get \"http://host/path?a=1\": dial tcp host:80: connect: connection refused",
			want: "Get \"http://host/path\": dial tcp host:80: connect: connection refused",
		},
		{
			name: "host only keeps the trailing slash",
			in:   `Get "https://h/": context deadline exceeded`,
			want: `Get "https://h/": context deadline exceeded`,
		},
		{
			name: "host only without a path",
			in:   `Get "http://host": dial tcp host:80: connect: connection refused`,
			want: `Get "http://host": dial tcp host:80: connect: connection refused`,
		},
		{
			name: "escaped path survives unchanged",
			in:   `Get "http://host/a%2Fb?token=token-canary": EOF`,
			want: `Get "http://host/a%2Fb": EOF`,
		},
		{
			name: "uppercase scheme is kept as a scheme, not as credentials",
			in:   `Get "HTTP://Host/Path?q=1#f": EOF`,
			want: `Get "http://Host/Path": EOF`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := scrubURLCredentials(test.in); got != test.want {
				t.Fatalf("scrubURLCredentials(%q) =\n  %q\nwant\n  %q", test.in, got, test.want)
			}
		})
	}
}

// Scrubbing an already scrubbed row must be a no-op: the audit retry path can
// replay a stored terminal row.
func TestScrubURLCredentialsIsIdempotent(t *testing.T) {
	in := `Get "https://user:pw-canary@host:8443/a/b?token=token-canary#f": dial tcp 10.0.0.1:8443: connect: connection refused`
	once := scrubURLCredentials(in)
	if twice := scrubURLCredentials(once); twice != once {
		t.Fatalf("second scrub changed the text:\n  %q\n  %q", once, twice)
	}
	if strings.Contains(once, "token-canary") || strings.Contains(once, "pw-canary") || strings.Contains(once, "#f") {
		t.Fatalf("scrubbed text still carries credentials: %q", once)
	}
}

// The scrubber covers every scheme:// URL, not only http(s): historical audit
// rows also hold database and broker targets whose userinfo or query carried a
// credential. The rule is the same one http(s) uses -- keep scheme, host, port
// and path, drop userinfo, query and fragment -- so the http(s) cases above are
// untouched.
func TestScrubURLCredentialsOtherSchemes(t *testing.T) {
	for _, test := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "postgres userinfo and query",
			in:   "dial \"postgres://app:PASSWORD@db:5432/app?sslmode=require\": timeout",
			want: "dial \"postgres://db:5432/app\": timeout",
		},
		{
			name: "redis userinfo carries only a password",
			in:   "dial \"redis://:password@cache:6379/0\": i/o timeout",
			want: "dial \"redis://cache:6379/0\": i/o timeout",
		},
		{
			name: "mysql userinfo and query",
			in:   "dial \"mysql://mongodb+srv:PASSWORD@mysql:3306/shop?charset=utf8mb4#frag\": refused",
			want: "dial \"mysql://mysql:3306/shop\": refused",
		},
		{
			name: "file url without userinfo keeps its empty authority and path",
			in:   "open file:///var/lib/doops/data.db: permission denied",
			want: "open file:///var/lib/doops/data.db: permission denied",
		},
		{
			name: "unix socket url keeps its path and drops the query",
			in:   "dial \"unix:///run/doops.sock?wait=1\": no such file",
			want: "dial \"unix:///run/doops.sock\": no such file",
		},
		{
			name: "scheme-only url fails closed",
			in:   "not a url: a://",
			want: "not a url: <unparsable-url>",
		},
		{
			name: "rfc 3986 scheme characters are part of the scheme",
			in:   "pull svn+ssh://deploy:PASSWORD@git.example/repo: EOF",
			want: "pull svn+ssh://git.example/repo: EOF",
		},
		{
			name: "uppercase non-http scheme is normalized like http",
			in:   "dial \"POSTGRES://User:PASSWORD@Host/App?x=1#f\": EOF",
			want: "dial \"postgres://Host/App\": EOF",
		},
		{
			name: "secret-shaped userinfo without a password is removed",
			in:   "dial amqp://mongodb+srv@broker:5672/vhost: EOF",
			want: "dial amqp://broker:5672/vhost: EOF",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := scrubURLCredentials(test.in)
			if got != test.want {
				t.Fatalf("scrubURLCredentials(%q) =\n  %q\nwant\n  %q", test.in, got, test.want)
			}
			if twice := scrubURLCredentials(got); twice != got {
				t.Fatalf("second scrub changed the text:\n  %q\n  %q", got, twice)
			}
		})
	}
}

// Bytes that are not part of a URL must survive byte for byte, including tabs,
// newlines, quotes, and the non-ASCII prose around the URL.
func TestScrubURLCredentialsKeepsBytesOutsideURLs(t *testing.T) {
	in := "prefix\t\"postgres://u:PASSWORD@h/db?q=1\"\n中文 file:///etc/x suffix  \x00?"
	want := "prefix\t\"postgres://h/db\"\n中文 file:///etc/x suffix  \x00?"
	if got := scrubURLCredentials(in); got != want {
		t.Fatalf("scrubURLCredentials(%q) =\n  %q\nwant\n  %q", in, got, want)
	}
}

// The tokenizer still ends a URL only at ASCII whitespace, quotes and angle
// brackets. A Chinese punctuation mark directly after a URL therefore stays
// inside its path and is percent-encoded by EscapedPath. That behaviour already
// existed for http(s); this pins that extending the scheme range did not change
// it.
func TestScrubURLCredentialsNonASCIIProseBoundary(t *testing.T) {
	in := "连接 postgres://app:PASSWORD@db:5432/app，详见日志"
	want := "连接 postgres://db:5432/app%EF%BC%8C%E8%AF%A6%E8%A7%81%E6%97%A5%E5%BF%97"
	got := scrubURLCredentials(in)
	if got != want {
		t.Fatalf("scrubURLCredentials(%q) =\n  %q\nwant\n  %q", in, got, want)
	}
	if twice := scrubURLCredentials(got); twice != got {
		t.Fatalf("second scrub changed the text:\n  %q\n  %q", got, twice)
	}
}
