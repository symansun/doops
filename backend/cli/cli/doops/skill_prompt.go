package main

import (
	"fmt"
	"strings"
)

// Domain commands reuse the Gateway client and login store; skills never own auth.
func runSkillPrompt(skill, operation, target, session, instruction, credentialMode string, credentials []string, servers []Server, configErr error, ss *SessionStore, verbose bool) error {
	return runSkillPromptWith(skill, operation, target, session, instruction, credentialMode, credentials, nil, servers, configErr, ss, verbose)
}

func runSkillPromptWith(skill, operation, target, session, instruction, credentialMode string, credentials []string, extra map[string]interface{}, servers []Server, configErr error, ss *SessionStore, verbose bool) error {
	session = resolveSession(session)
	if configErr != nil {
		return fmt.Errorf("load Gateway config: %w", configErr)
	}
	server := findServer(servers, target)
	if server == nil {
		return errTargetNotFound(servers, target)
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
	for key, value := range extra {
		arguments[key] = value
	}
	if strings.TrimSpace(credentialMode) != "" {
		arguments["credential_mode"] = credentialMode
	}
	if len(credentials) > 0 {
		arguments["credential_refs"] = credentials
	}
	if err := client.Call("doops_agent_prompt", arguments); err != nil {
		return err
	}
	return RecordHistory(server.Name, session, fmt.Sprintf("%s %s", skill, operation))
}
