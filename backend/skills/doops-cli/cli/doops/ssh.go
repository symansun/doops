package main

import (
	"fmt"
	"strings"
)

func looksLikeSSHCommand(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "ssh", "/usr/bin/ssh", "/bin/ssh":
		return true
	default:
		return false
	}
}

func normalizeSSHOptions(options []string) ([]string, error) {
	normalized := make([]string, 0, len(options))
	for index, option := range options {
		option = strings.TrimSpace(option)
		if option == "" {
			return nil, fmt.Errorf("SSH option %d must not be empty", index+1)
		}
		if strings.HasPrefix(option, "-") {
			normalized = append(normalized, option)
			continue
		}
		if !strings.Contains(option, "=") {
			return nil, fmt.Errorf(
				"SSH option %q must be an OpenSSH argv beginning with '-' or key=value; use --option=BatchMode=yes",
				option,
			)
		}
		normalized = append(normalized, "-o"+option)
	}
	return normalized, nil
}
