package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every `doops ...` command written in the Skill, the docs and the Agent skills
// must name a real command and only flags that command accepts. The docs are
// what an AI reads before it runs the CLI; a stale flag there is a failed call.

var (
	docBacktickCommand = regexp.MustCompile("`((?:\\S*/)?doops\\s[^`\\n]*)`")
	docFlagToken       = regexp.MustCompile(`(?:^|[\s\[|(/])(--?[A-Za-z][A-Za-z0-9-]*)`)
	docQuoted          = regexp.MustCompile(`'[^']*'|"[^"]*"`)
	docDoopsInvocation = regexp.MustCompile(`(?:^|[\s(|;&/])doops\s+(.*)`)
	// A shell line runs doops when doops is its first word (after an optional
	// prompt or env assignments); prose inside text fences is skipped.
	docShellLine = regexp.MustCompile(`^(?:\$\s+)?(?:[A-Z_][A-Z0-9_]*=\S+\s+)*(?:\S*/)?doops\s`)
)

// Commands whose -h does real work instead of printing flags; none take flags.
var docNoFlagCommands = map[string]bool{"session": true, "version": true, "list": true, "history": true}

// Commands followed by a fixed number of subcommand words.
var docSubcommandDepth = map[string]int{"admin": 2, "credential": 1, "image": 1, "registry": 1}

var docGlobalFlags = map[string]bool{"-session": true, "-verbose": true, "-contract": true, "-help": true, "-h": true}

func docSources(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	var files []string
	for _, pattern := range []string{"skills/SKILL.md", "docs/*.md", "README.md", "agent/skills/*/SKILL.md", "agent/skills/*/docs/*.md", "agent/skills/system_prompt.md"} {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	if len(files) < 10 {
		t.Fatalf("found only %d documentation files; the glob roots are wrong", len(files))
	}
	return files
}

// docCommands returns every doops invocation in a document: inline code spans
// and fenced shell blocks, with backslash continuations joined.
func docCommands(text string) []string {
	var commands []string
	for _, match := range docBacktickCommand.FindAllStringSubmatch(text, -1) {
		commands = append(commands, match[1])
	}
	inFence := false
	var pending string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			pending = ""
			continue
		}
		if !inFence {
			continue
		}
		line = pending + strings.TrimSpace(line)
		if strings.HasSuffix(line, `\`) {
			pending = strings.TrimSuffix(line, `\`) + " "
			continue
		}
		pending = ""
		if docShellLine.MatchString(line) {
			commands = append(commands, line)
		}
	}
	return commands
}

type docInvocation struct {
	path  []string
	flags []string
}

func parseDocInvocation(raw string) (docInvocation, bool) {
	match := docDoopsInvocation.FindStringSubmatch(raw)
	if match == nil {
		return docInvocation{}, false
	}
	rest := docQuoted.ReplaceAllString(match[1], "")
	// Remote argv after "--" and shell chaining belong to another program.
	for _, stop := range []string{" -- ", " | ", " && ", " ; ", "; ", " 2>", " >"} {
		if i := strings.Index(rest, stop); i >= 0 {
			rest = rest[:i]
		}
	}
	if strings.HasSuffix(rest, " --") {
		rest = strings.TrimSuffix(rest, " --")
	}
	words := strings.Fields(rest)
	var inv docInvocation
	for i := 0; i < len(words); i++ {
		word := strings.Trim(words[i], "[]()`,.")
		if word == "-session" || word == "--session" {
			i++
			continue
		}
		if docGlobalFlags[word] {
			continue
		}
		if strings.HasPrefix(word, "-") || strings.HasPrefix(word, "<") {
			break
		}
		word = strings.Split(strings.ReplaceAll(word, `\|`, "|"), "|")[0]
		if !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(word) {
			return docInvocation{}, false
		}
		inv.path = append(inv.path, word)
		if len(inv.path) > docSubcommandDepth[inv.path[0]] {
			break
		}
	}
	if len(inv.path) == 0 {
		return docInvocation{}, false
	}
	for _, token := range docFlagToken.FindAllStringSubmatch(rest, -1) {
		flag := strings.TrimLeft(token[1], "-")
		if docGlobalFlags["-"+flag] {
			continue
		}
		inv.flags = append(inv.flags, token[1])
	}
	return inv, true
}

func TestDocumentedCommandsMatchTheCLI(t *testing.T) {
	binary := buildDoopsCLIBinary(t)
	home := t.TempDir()
	helpCache := map[string]string{}
	help := func(path []string) string {
		key := strings.Join(path, " ")
		if cached, ok := helpCache[key]; ok {
			return cached
		}
		command := exec.Command(binary, append(append([]string(nil), path...), "-h")...)
		command.Env = append(os.Environ(), "HOME="+home, "DOOPS_CONFIG="+filepath.Join(home, "config.json"))
		output, _ := command.CombinedOutput()
		helpCache[key] = string(output)
		return helpCache[key]
	}

	checked := 0
	for _, file := range docSources(t) {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range docCommands(string(data)) {
			inv, ok := parseDocInvocation(raw)
			if !ok {
				continue
			}
			checked++
			where := filepath.Base(file) + ": " + strings.TrimSpace(raw)
			if docNoFlagCommands[inv.path[0]] {
				if len(inv.flags) != 0 {
					t.Errorf("%s: `doops %s` takes no flags, got %v", where, inv.path[0], inv.flags)
				}
				continue
			}
			text := help(inv.path)
			if strings.HasPrefix(text, "Unknown command") {
				t.Errorf("%s: unknown command `doops %s`", where, strings.Join(inv.path, " "))
				continue
			}
			for _, flag := range inv.flags {
				name := strings.TrimLeft(flag, "-")
				// Flag help lists "  -name"; custom usage text lists "--name" or "-x".
				if !regexp.MustCompile(`(?m)(^\s+-` + regexp.QuoteMeta(name) + `\b|--` + regexp.QuoteMeta(name) + `\b|[\s\[|]-` + regexp.QuoteMeta(name) + `\b)`).MatchString(text) {
					t.Errorf("%s: `doops %s` has no flag %s", where, strings.Join(inv.path, " "), flag)
				}
			}
		}
	}
	if checked < 100 {
		t.Fatalf("checked only %d documented commands; extraction is broken", checked)
	}
}

func TestParseDocInvocation(t *testing.T) {
	cases := []struct {
		raw   string
		path  string
		flags string
	}{
		{"doops -session <s> exec --target <t> --cmd 'echo --fake'", "exec", "--target --cmd"},
		{"doops -session <s> exec --target <t> -- uname -a", "exec", "--target"},
		{"doops admin user create\\|list --target <t>", "admin user create", "--target"},
		{"doops -session <s> k8s --target <t> get pods [-n <ns>\\|-A] [-l <sel>]", "k8s", "--target -n -A -l"},
		{"doops -session <s> sql --target <t> --mode apply (--sql '<sql>'\\|--file <f>)", "sql", "--target --mode --sql --file"},
		{"doops version", "version", ""},
	}
	for _, tc := range cases {
		inv, ok := parseDocInvocation(tc.raw)
		if !ok || strings.Join(inv.path, " ") != tc.path || strings.Join(inv.flags, " ") != tc.flags {
			t.Errorf("parse(%q) = %v %v %v, want path %q flags %q", tc.raw, ok, inv.path, inv.flags, tc.path, tc.flags)
		}
	}
}
