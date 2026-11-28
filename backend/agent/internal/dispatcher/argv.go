package dispatcher

import (
	"regexp"
	"strings"
)

// word is one token of a simple command the way a direct exec would receive it:
// quotes and backslash escapes removed, with the exact source span kept so a
// denial can quote where it matched.
type word struct {
	value  string
	raw    string
	offset int
	// flag marks an option word. Operands are the argv words a program would
	// see as paths or positional values: options, redirections and the "--"
	// terminator are never operands.
	flag bool
}

var (
	// attachedRedirect covers ">file", "2>>log" and "<<EOF": the target is
	// attached to the operator, so only that word is dropped.
	attachedRedirect = regexp.MustCompile("^[0-9]*(>>|<<<|<<|<>|>\\||>|<)(.+)$")
	// bareRedirect covers "> file", "2>&1" and "<< EOF": the operator stands
	// alone, so the operator and the word after it are both dropped.
	bareRedirect = regexp.MustCompile("^[0-9]*(>>|<<<|<<|<>|>\\||>|<)$")
	// shortBundle is a single-dash option word with two or more letters, such
	// as "-rf" or "find -delete". It is kept as written and also expanded into
	// one option word per letter, so a rule can match "-f" inside "-rf" while a
	// rule for "-delete" still sees the spelled-out form.
	shortBundle = regexp.MustCompile("^-[A-Za-z]{2,}$")
)

// splitSimpleCommands splits submitted shell text into the simple commands a
// shell would run and tokenizes each one into argv words. It is deliberately
// not a shell parser: it splits on ";", "&", "|" and newlines, removes quotes
// and backslash escapes, and stops there. Variables, globs, command
// substitution, heredoc bodies and shell builtins such as "command" or "env"
// are never expanded, so the argv layer cannot see them; Check keeps the regex
// layer as the backstop for those shapes.
func splitSimpleCommands(text string) [][]word {
	var commands [][]word
	var words []word
	var value, raw strings.Builder
	start := -1
	var quote byte

	begin := func(index int) {
		if start < 0 {
			start = index
		}
	}
	flushWord := func() {
		if start < 0 {
			return
		}
		words = append(words, word{value: value.String(), raw: raw.String(), offset: start})
		value.Reset()
		raw.Reset()
		start = -1
	}
	flushCommand := func() {
		flushWord()
		if len(words) > 0 {
			if command := normalizeCommandWords(words); len(command) > 0 {
				commands = append(commands, command)
			}
		}
		words = nil
	}

	for index := 0; index < len(text); index++ {
		c := text[index]
		switch quote {
		case '\'':
			raw.WriteByte(c)
			if c == '\'' {
				quote = 0
				continue
			}
			value.WriteByte(c)
		case '"':
			if c == '"' {
				quote = 0
				raw.WriteByte(c)
				continue
			}
			if c == '\\' && index+1 < len(text) {
				next := text[index+1]
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					value.WriteByte(next)
					raw.WriteByte('\\')
					raw.WriteByte(next)
					index++
					continue
				}
			}
			value.WriteByte(c)
			raw.WriteByte(c)
		default:
			switch c {
			case ' ', '\t', '\r':
				flushWord()
			case '\n', ';', '|', '&':
				flushCommand()
			case '\'', '"':
				begin(index)
				quote = c
				raw.WriteByte(c)
			case '\\':
				if index+1 < len(text) {
					index++
					begin(index - 1)
					value.WriteByte(text[index])
					raw.WriteByte('\\')
					raw.WriteByte(text[index])
				}
			default:
				begin(index)
				value.WriteByte(c)
				raw.WriteByte(c)
			}
		}
	}
	flushCommand()
	return commands
}

// commandWords turns already-split argv (a direct exec) into classified words.
// This is the authoritative argv path: no shell is involved, so nothing has to
// be guessed.
func commandWords(argv []string) []word {
	words := make([]word, 0, len(argv))
	offset := 0
	for _, argument := range argv {
		words = append(words, word{value: argument, raw: argument, offset: offset})
		offset += len(argument) + 1
	}
	return normalizeCommandWords(words)
}

// normalizeCommandWords drops redirections (they are shell syntax, not
// arguments), honours the "--" terminator, marks options and expands merged
// short options.
func normalizeCommandWords(words []word) []word {
	normalized := make([]word, 0, len(words))
	terminated := false
	skipTarget := false
	for _, current := range words {
		if skipTarget {
			skipTarget = false
			continue
		}
		if !terminated {
			switch {
			case current.value == "--":
				// The terminator itself is not an operand and not an option.
				terminated = true
				continue
			case bareRedirect.MatchString(current.value):
				skipTarget = true
				continue
			case attachedRedirect.MatchString(current.value):
				continue
			}
		}
		current.flag = !terminated && isOption(current.value)
		normalized = append(normalized, current)
	}
	// A merged short-option word keeps its own entry and gains one entry per
	// letter, so "rm -rf /" matches both a rule for "-rf" and a rule for "-f".
	// Long single-dash options such as "find -delete" are therefore still
	// matchable by their spelled-out form.
	expanded := make([]word, 0, len(normalized))
	for _, current := range normalized {
		expanded = append(expanded, current)
		if !current.flag || !shortBundle.MatchString(current.value) {
			continue
		}
		for _, option := range current.value[1:] {
			expanded = append(expanded, word{
				value:  "-" + string(option),
				raw:    current.raw,
				offset: current.offset,
				flag:   true,
			})
		}
	}
	return expanded
}

func isOption(value string) bool {
	return len(value) > 1 && value[0] == '-'
}

// normalizePath reduces an operand to the path it lexically names: repeated
// slashes collapse, "." segments disappear and ".." segments are resolved
// (clamped at "/" for absolute paths). Globs and variables are kept verbatim
// because they are never expanded here.
func normalizePath(value string) string {
	if value == "" {
		return ""
	}
	absolute := strings.HasPrefix(value, "/")
	segments := make([]string, 0, 4)
	for _, segment := range strings.Split(value, "/") {
		switch segment {
		case "", ".":
			continue
		case "..":
			switch {
			case len(segments) > 0 && segments[len(segments)-1] != "..":
				segments = segments[:len(segments)-1]
			case !absolute:
				segments = append(segments, "..")
			}
		default:
			segments = append(segments, segment)
		}
	}
	joined := strings.Join(segments, "/")
	if absolute {
		return "/" + joined
	}
	return joined
}
