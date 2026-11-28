package server

import "strings"

// capabilityToolSpec declares the Core platform domain-scoped MCP tools and
// structured attestation policy for a Core built-in capability skill.
// External/installable plugins (such as dodns, doemail, or future user plugins)
// are NOT hardcoded here: they ship their own MCP server via platform_mcp_servers
// and receive scoped tool visibility dynamically from customSyncedMCPToolsForServer.
type capabilityToolSpec struct {
	ReadTools                     []string
	MutationTools                 []string
	RequiresStructuredMutationMCP bool
}

var builtinCapabilityToolSpecs = map[string]capabilityToolSpec{
	"": {
		MutationTools:                 []string{"mcp_doops_helm"},
		RequiresStructuredMutationMCP: true,
	},
	"k8s": {
		MutationTools:                 []string{"mcp_doops_helm"},
		RequiresStructuredMutationMCP: true,
	},
	"image-build": {
		MutationTools:                 []string{"mcp_doops_helm"},
		RequiresStructuredMutationMCP: true,
	},
	"doweb": {
		MutationTools:                 []string{"mcp_doops_web"},
		RequiresStructuredMutationMCP: true,
	},
	"dosql": {
		ReadTools:                     []string{"mcp_dosql_read"},
		MutationTools:                 []string{"mcp_dosql_exec"},
		RequiresStructuredMutationMCP: true,
	},
}

// structuredDoopsMutationSpec declares a Core runtime-attested DoOps MCP
// mutation tool in the "doops" MCP domain.
type structuredDoopsMutationSpec struct {
	ModelTool         string
	RuntimeTool       string
	Server            string
	MCPTool           string
	AllowedOperations func() []string
}

var structuredDoopsMutationSpecs = []structuredDoopsMutationSpec{
	{
		ModelTool:         "doops_helm",
		RuntimeTool:       "mcp_doops_helm",
		Server:            mcpDomainDoops,
		MCPTool:           helmMCPToolName,
		AllowedOperations: func() []string { return helmMutationOperations },
	},
	{
		ModelTool:         "doops_web",
		RuntimeTool:       "mcp_doops_web",
		Server:            mcpDomainDoops,
		MCPTool:           webMCPToolName,
		AllowedOperations: func() []string { return webMutationOperations },
	},
}

func lookupStructuredDoopsMutationSpec(modelTool string) (structuredDoopsMutationSpec, bool) {
	for _, spec := range structuredDoopsMutationSpecs {
		if spec.ModelTool == modelTool {
			return spec, true
		}
	}
	return structuredDoopsMutationSpec{}, false
}

func isStructuredDoopsMutationTool(modelTool string) bool {
	_, ok := lookupStructuredDoopsMutationSpec(modelTool)
	return ok
}

// doagentAllowedTools describes the tools that DoOps makes visible to the
// embedded doagent session. The PermissionEngine remains the authority for
// whether a visible tool may run; this list selects the execution surface for
// the current task.
//
// Core capabilities (Helm/K8s, DoWeb, DoSql) resolve strictly from
// builtinCapabilityToolSpecs. Installed external plugins (exact skill slug
// matching a synced custom MCP server) dynamically expose ONLY their own synced
// MCP server's tools in apply mode alongside audited Bash, never leaking Core
// mutation MCPs or another plugin's MCP tools. Untrimmed or unknown skills fail
// closed with zero structured mutation MCP tools.
func doagentAllowedTools(apply bool, skill string, kubernetes bool) []string {
	tools := []string{"Read", "glob", "grep", "Skill", "Bash"}
	if !apply {
		tools = append(tools, customSyncedMCPAllowedTools()...)
	}
	if kubernetes {
		tools = append(tools, "mcp_doops_k8s_read")
	}
	tools = append(tools, "mcp_doops_web_read", "mcp_doops_kb_read")
	// Exact-match lookup intentionally fails closed for untrimmed skill names.
	if spec, ok := builtinCapabilityToolSpecs[skill]; ok {
		if len(spec.ReadTools) > 0 {
			tools = append(tools, spec.ReadTools...)
		}
		if apply && len(spec.MutationTools) > 0 {
			tools = append(tools, spec.MutationTools...)
		}
		return tools
	}
	// External plugin skill: dynamically bind only the synced MCP server whose
	// slug matches this exact skill name (never leaking Core or sibling MCPs).
	if apply && skill != "" && skill == strings.TrimSpace(skill) && skillSlugPattern.MatchString(skill) {
		tools = append(tools, customSyncedMCPToolsForServer(skill)...)
	}
	return tools
}

// skillRequiresStructuredMutationMCP reports whether an apply session for the
// given skill must present Core runtime-attested MCP receipts (mcp_doops_helm /
// mcp_doops_web / mcp_dosql_exec) instead of plugin-scoped MCP or audited Bash
// execution evidence.
func skillRequiresStructuredMutationMCP(skill string) bool {
	if spec, ok := builtinCapabilityToolSpecs[strings.TrimSpace(skill)]; ok {
		return spec.RequiresStructuredMutationMCP
	}
	return false
}
