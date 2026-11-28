package server

import (
	"errors"
	"strings"
)

func validatePromptCredentialAccess(operation string, hasCredentials bool) error {
	if !hasCredentials {
		return nil
	}
	operation = strings.ToLower(strings.TrimSpace(operation))
	if operation == "" || operation == "ask" || operation == "apply" {
		return nil
	}
	return errors.New("credential bindings require an ask or apply operation")
}
