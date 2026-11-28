package server

// INT-82b follow-up: JSON-RPC error codes are a published contract, and a code
// that means two things is worse than no code at all — a client that branches on
// it will treat "retry in a moment" (gateway_draining, -32010) the same as "the
// command may already have run, check its side effects" (execution_agent_restarted,
// which therefore lives on -32011). This guard scans the package's own source so
// a new code or a reused message prefix has to be registered here on purpose.
//
// Scope: only literal message prefixes are compared. Sites whose message is
// built from a variable (err.Error(), fmt.Sprintf, concatenation) cannot be read
// statically and are out of scope; they still have to keep their prefix unique
// when they are turned into literals.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// gatewayErrorCodePrefixes is the registered surface: one line per (code,
// prefix) pair the Gateway can emit as a literal. Adding an entry is a contract
// change; removing one means the prefix no longer exists.
var gatewayErrorCodePrefixes = map[int][]string{
	-32011: {"execution_agent_restarted"},
	-32010: {"gateway_draining"},
	-32009: {"execution_result_unavailable"},
	-32003: {
		"app_metrics_internal_only",
		"credential_authorization_changed",
		"doagent_prompt_rejected",
		"forbidden",
	},
	-32601: {"target_resource_sync_requires_gateway"},
	-32602: {
		"doagent_apply_mutation_context_invalid",
		"dosql_credential_refs_unsupported",
		"target_manifest_is_gateway_managed",
		"target_resource_sync_requires_gateway",
	},
	-32603: {
		"audit_completion_failed",
		"audit_unavailable",
		"credential_binding_encode_failed",
		"doagent_attachment_upload_failed",
		"doagent_mode_configuration_failed",
		"doagent_model_configuration_failed",
		"doagent_mutation_authority_unavailable",
		"doagent_permission_mode_configuration_failed",
		"doagent_session_create_failed",
		"doagent_session_create_invalid",
		"doagent_sse_unavailable",
		"doagent_unavailable",
		"gateway_auth_unavailable",
		"operation_timeout",
	},
}

// gatewayErrorCodeSentinels covers sites whose message is a variable: the code
// comes from a package constant, so the source cannot show the prefix next to
// it. Registering the constant here ties the code and the semantic together —
// changing the constant to an occupied code fails this guard.
var gatewayErrorCodeSentinels = map[string]struct {
	code   int
	prefix string
}{
	"executionAgentRestartedCode": {code: -32011, prefix: "execution_agent_restarted"},
}

// gatewaySharedErrorPrefixes names the prefixes that deliberately appear under
// more than one code, with the reason. Everything else must map to one code.
var gatewaySharedErrorPrefixes = map[string]string{
	"target_resource_sync_requires_gateway": "two different JSON-RPC categories: -32601 for an unsupported resource RPC method (handler_ws.go:198), -32602 for invalid tools/call params carrying a target_manifest without a resource session (handler_ws.go:430)",
}

type errorCodeLiteral struct {
	code   int
	prefix string
	where  string
}

func gatewayErrorCodeLiterals(t *testing.T) []errorCodeLiteral {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse the package: %v", err)
	}
	// Package-level constants such as executionAgentRestartedCode = -32011.
	constants := map[string]int{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				decl, ok := n.(*ast.GenDecl)
				if !ok || decl.Tok != token.CONST {
					return true
				}
				for _, spec := range decl.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok || len(value.Values) == 0 {
						continue
					}
					if code, ok := errorCodeValue(value.Values[0]); ok {
						for _, name := range value.Names {
							constants[name.Name] = code
						}
					}
				}
				return true
			})
		}
	}
	seen := map[string]errorCodeLiteral{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 3 {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "buildErrorResponse" {
					return true
				}
				code, ok := errorCodeArgument(call.Args[1], constants)
				if !ok {
					return true
				}
				message, ok := call.Args[2].(*ast.BasicLit)
				if !ok || message.Kind != token.STRING {
					return true
				}
				text, err := strconv.Unquote(message.Value)
				if err != nil {
					return true
				}
				prefix := text
				if index := strings.Index(text, ":"); index > 0 {
					prefix = text[:index]
				}
				if !snakeCasePrefix(prefix) {
					return true
				}
				key := strconv.Itoa(code) + "\x00" + prefix
				if _, duplicate := seen[key]; duplicate {
					return true
				}
				position := fset.Position(call.Pos())
				seen[key] = errorCodeLiteral{code: code, prefix: prefix, where: position.String()}
				return true
			})
		}
	}
	literals := make([]errorCodeLiteral, 0, len(seen))
	for _, literal := range seen {
		literals = append(literals, literal)
	}
	sort.Slice(literals, func(i, j int) bool {
		if literals[i].code != literals[j].code {
			return literals[i].code > literals[j].code
		}
		return literals[i].prefix < literals[j].prefix
	})
	return literals
}

func errorCodeValue(expression ast.Expr) (int, bool) {
	switch value := expression.(type) {
	case *ast.BasicLit:
		if value.Kind != token.INT {
			return 0, false
		}
		code, err := strconv.Atoi(value.Value)
		return code, err == nil
	case *ast.UnaryExpr:
		if value.Op != token.SUB {
			return 0, false
		}
		literal, ok := value.X.(*ast.BasicLit)
		if !ok || literal.Kind != token.INT {
			return 0, false
		}
		code, err := strconv.Atoi(literal.Value)
		return -code, err == nil
	}
	return 0, false
}

func errorCodeArgument(expression ast.Expr, constants map[string]int) (int, bool) {
	if code, ok := errorCodeValue(expression); ok {
		return code, true
	}
	if identifier, ok := expression.(*ast.Ident); ok {
		code, known := constants[identifier.Name]
		return code, known
	}
	return 0, false
}

func snakeCasePrefix(prefix string) bool {
	if prefix == "" {
		return false
	}
	for _, r := range prefix {
		if r != '_' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func ownerPrefix(registered map[int][]string, code int) string {
	prefixes := registered[code]
	if len(prefixes) == 0 {
		return "an unregistered semantic"
	}
	return prefixes[0]
}

func gatewayErrorCodeConstantValues(t *testing.T) map[string]int {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse the package: %v", err)
	}
	constants := map[string]int{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				decl, ok := n.(*ast.GenDecl)
				if !ok || decl.Tok != token.CONST {
					return true
				}
				for _, spec := range decl.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok || len(value.Values) == 0 {
						continue
					}
					if code, ok := errorCodeValue(value.Values[0]); ok {
						for _, name := range value.Names {
							constants[name.Name] = code
						}
					}
				}
				return true
			})
		}
	}
	return constants
}

func TestGatewayErrorCodePrefixesStayUnique(t *testing.T) {
	literals := gatewayErrorCodeLiterals(t)
	constants := gatewayErrorCodeConstantValues(t)
	registered := map[int]map[string]bool{}
	for code, prefixes := range gatewayErrorCodePrefixes {
		registered[code] = map[string]bool{}
		for _, prefix := range prefixes {
			if other, duplicate := registered[code][prefix]; duplicate {
				t.Fatalf("gatewayErrorCodePrefixes lists %d/%q twice (second: %v)", code, prefix, other)
			}
			registered[code][prefix] = true
		}
	}
	// A registered sentinel accounts for its pair even though the pair never
	// appears as a literal.
	claimed := map[int]map[string]bool{}
	prefixOwner := map[string]int{}
	for code, prefixes := range gatewayErrorCodePrefixes {
		for _, prefix := range prefixes {
			if _, taken := prefixOwner[prefix]; !taken {
				prefixOwner[prefix] = code
			}
		}
	}
	for name, sentinel := range gatewayErrorCodeSentinels {
		value, declared := constants[name]
		if !declared {
			t.Errorf("sentinel constant %q is gone; the contract still registers it as %d/%q", name, sentinel.code, sentinel.prefix)
			continue
		}
		if value != sentinel.code {
			owner := prefixOwner[sentinel.prefix]
			t.Errorf("constant %s = %d, but the semantic %q is registered on %d (that code already belongs to %q); "+
				"one code carries one semantic — pick an unoccupied code",
				name, value, sentinel.prefix, owner, ownerPrefix(gatewayErrorCodePrefixes, value))
		}
		if claimed[sentinel.code] == nil {
			claimed[sentinel.code] = map[string]bool{}
		}
		claimed[sentinel.code][sentinel.prefix] = true
	}
	observed := map[int]map[string]bool{}
	byPrefix := map[string][]errorCodeLiteral{}
	for _, literal := range literals {
		if observed[literal.code] == nil {
			observed[literal.code] = map[string]bool{}
		}
		observed[literal.code][literal.prefix] = true
		byPrefix[literal.prefix] = append(byPrefix[literal.prefix], literal)
		if !registered[literal.code][literal.prefix] {
			t.Errorf("unregistered JSON-RPC error pair: code %d with prefix %q at %s; "+
				"register it in gatewayErrorCodePrefixes, and do not reuse a prefix on a second code",
				literal.code, literal.prefix, literal.where)
		}
	}
	for code, prefixes := range gatewayErrorCodePrefixes {
		for _, prefix := range prefixes {
			if !observed[code][prefix] && !claimed[code][prefix] {
				t.Errorf("stale registration: code %d with prefix %q no longer appears in the package", code, prefix)
			}
		}
	}
	// A prefix is one semantic. Two codes for one prefix is exactly the overload
	// this guard exists to prevent, unless the exemption table explains it.
	for prefix, hits := range byPrefix {
		codes := map[int]bool{}
		where := make([]string, 0, len(hits))
		for _, hit := range hits {
			codes[hit.code] = true
			where = append(where, hit.where)
		}
		if len(codes) < 2 {
			continue
		}
		if _, exempt := gatewaySharedErrorPrefixes[prefix]; exempt {
			continue
		}
		t.Errorf("prefix %q is emitted with %d different codes at %v; a prefix is one semantic — "+
			"pick one code, or add an exemption to gatewaySharedErrorPrefixes with the reason",
			prefix, len(codes), where)
	}
}
