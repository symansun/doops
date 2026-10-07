package main

import (
	"fmt"
	"strings"
)

// Domain commands reuse the Gateway client and login store; skills never own auth.
func runSkillPrompt(skill, operation, target, session, instruction string, credentials []string, servers []Server, configErr error, ss *SessionStore, verbose bool) error {
	if strings.TrimSpace(session) == "" {
		return fmt.Errorf("-session is required")
	}
	if configErr != nil {
		return fmt.Errorf("load Gateway config: %w", configErr)
	}
	server := findServer(servers, target)
	if server == nil {
		return fmt.Errorf("Gateway target %q not found", target)
	}
	token, err := ResolveToken(*server)
	if err != nil {
		return err
	}
	client := NewMCPClient(*server, ss, session, verbose)
	client.Token = token
	defer client.Close()
	arguments := map[string]interface{}{
		"operation": operation, "skill": skill, "instruction": instruction,
	}
	if len(credentials) > 0 {
		arguments["credential_refs"] = credentials
	}
	if err := client.Call("doops_agent_prompt", arguments); err != nil {
		return err
	}
	return RecordHistory(server.Name, session, fmt.Sprintf("%s %s", skill, operation))
}
