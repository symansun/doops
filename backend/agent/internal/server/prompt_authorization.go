package server

import (
	"errors"
	"strings"
)

func validatePromptCredentialAccess(operation, skill string, hasCredentials bool) error {
	if !hasCredentials {
		return nil
	}
	operation = strings.ToLower(strings.TrimSpace(operation))
	if operation == "apply" {
		return nil
	}
	if (operation == "" || operation == "ask") && strings.TrimSpace(skill) == "dosql" {
		return nil
	}
	return errors.New("read-only credential access requires skill=dosql")
}
