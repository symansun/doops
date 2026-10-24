package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// `doops guide` is how an AI learns a task; every step it prints must be a real
// command with flags that command accepts, so following the guide never fails.

var (
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

func TestEveryGuideStepIsARealCommand(t *testing.T) {
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
	for name, scenario := range guideScenarios {
		for _, raw := range scenario.steps {
			inv, ok := parseDocInvocation(raw)
			if !ok {
				continue
			}
			checked++
			where := "guide " + name + ": " + strings.TrimSpace(raw)
			if docNoFlagCommands[inv.path[0]] {
				if len(inv.flags) != 0 {
					t.Errorf("%s: `doops %s` takes no flags, got %v", where, inv.path[0], inv.flags)
				}
				continue
			}
			text := help(inv.path)
			if strings.HasPrefix(text, "Error: unknown command") {
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
	if checked < 20 {
		t.Fatalf("checked only %d guide steps; extraction is broken", checked)
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

func TestFailuresNameTheNextCommand(t *testing.T) {
	servers := []Server{{Name: "gw-edu-coder"}, {Name: "gw-zyj"}}
	cases := []struct {
		name string
		got  string
		want []string
	}{
		{"typo in target suggests the closest", targetNotFound(servers, "gw-edu-codr"), []string{`did you mean "gw-edu-coder"`, "next: rerun with --target gw-edu-coder"}},
		{"unrelated target lists the configured ones", targetNotFound(servers, "prod"), []string{"configured targets: gw-edu-coder, gw-zyj", "next: doops list"}},
		{"no targets yet says how to add one", targetNotFound(nil, "x"), []string{"next: doops add --name"}},
		{"typo in command suggests the closest", unknownCommand("exce"), []string{`did you mean "exec"`, "next: doops exec -h"}},
		{"unknown command points to the guide", unknownCommand("frobnicate"), []string{"next: doops guide"}},
		{"nested ssh in exec becomes a doops ssh command", sshCommandSuggestion("t", "ssh ops@db 'uptime'"), []string{"doops ssh --target t --host ops@db --"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, want := range tc.want {
				if !strings.Contains(tc.got, want) {
					t.Fatalf("output %q is missing %q", tc.got, want)
				}
			}
		})
	}
}

func TestRetiredVerifyFlagsAreDroppedNotFatal(t *testing.T) {
	var notes strings.Builder
	got := dropRetiredCredentialVerifyFlags([]string{"--target", "t", "--namespace", "apps", "--use=imagePull", "--id", "c"}, &notes)
	if strings.Join(got, " ") != "--target t --id c" {
		t.Fatalf("kept args = %q", got)
	}
	if !strings.Contains(notes.String(), "--namespace ignored") || !strings.Contains(notes.String(), "--use ignored") {
		t.Fatalf("notes = %q", notes.String())
	}
}
