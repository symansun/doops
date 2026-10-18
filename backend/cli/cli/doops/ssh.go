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
				"SSH option %q must be an allowed flag or key=value; use --option=BatchMode=yes",
				option,
			)
		}
		normalized = append(normalized, "-o"+option)
	}
	if err := validateSSHOptionArgv(normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func validateSSHOptionArgv(options []string) error {
	for index := 0; index < len(options); index++ {
		option := options[index]
		if !strings.HasPrefix(option, "-") || option == "-" || option == "--" {
			return fmt.Errorf("SSH option must be a flag")
		}
		switch {
		case option == "-4" || option == "-6":
		case option == "-p" || strings.HasPrefix(option, "-p"):
			value, next, err := cliSSHOptionValue("-p", option, options, index)
			if err != nil {
				return err
			}
			if err := cliValidatePort(value); err != nil {
				return err
			}
			index = next
		case option == "-i" || strings.HasPrefix(option, "-i"):
			value, next, err := cliSSHOptionValue("-i", option, options, index)
			if err != nil {
				return err
			}
			if value == "" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "\r\n") {
				return fmt.Errorf("SSH identity file is invalid")
			}
			index = next
		case option == "-o" || strings.HasPrefix(option, "-o"):
			value, next, err := cliSSHOptionValue("-o", option, options, index)
			if err != nil {
				return err
			}
			if err := cliValidateSSHAssignment(value); err != nil {
				return err
			}
			index = next
		default:
			return fmt.Errorf("SSH option is not allowed")
		}
	}
	return nil
}

func cliSSHOptionValue(flag, option string, options []string, index int) (string, int, error) {
	if option == flag {
		if index+1 >= len(options) {
			return "", index, fmt.Errorf("SSH option %s requires a value", flag)
		}
		value := strings.TrimSpace(options[index+1])
		if value == "" || strings.HasPrefix(value, "-") {
			return "", index, fmt.Errorf("SSH option %s requires a value", flag)
		}
		return value, index + 1, nil
	}
	value := strings.TrimSpace(option[len(flag):])
	if value == "" {
		return "", index, fmt.Errorf("SSH option %s has an invalid value", flag)
	}
	return value, index, nil
}

func cliValidateSSHAssignment(assignment string) error {
	key, value, ok := strings.Cut(strings.TrimSpace(assignment), "=")
	if !ok {
		return fmt.Errorf("SSH -o value must be key=value")
	}
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if key == "" || value == "" || strings.ContainsAny(value, "\r\n") || !cliSSHKeyword(key) {
		return fmt.Errorf("SSH config option is not allowed")
	}
	switch key {
	case "batchmode", "identitiesonly":
		if !strings.EqualFold(value, "yes") && !strings.EqualFold(value, "no") {
			return fmt.Errorf("SSH config option %s has an invalid value", key)
		}
	case "stricthostkeychecking":
		switch strings.ToLower(value) {
		case "yes", "no", "ask", "accept-new":
		default:
			return fmt.Errorf("SSH config option %s has an invalid value", key)
		}
	case "connecttimeout":
		return cliValidateTimeout(value)
	case "port":
		return cliValidatePort(value)
	case "identityfile":
		if strings.HasPrefix(value, "-") {
			return fmt.Errorf("SSH identity file is invalid")
		}
	default:
		return fmt.Errorf("SSH config option %s is not allowed", key)
	}
	return nil
}

func cliSSHKeyword(key string) bool {
	if key == "" || len(key) > 64 {
		return false
	}
	for _, r := range key {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

func cliValidatePort(value string) error {
	for _, r := range value {
		if r < '0' || r > '9' {
			return fmt.Errorf("SSH port is invalid")
		}
	}
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return fmt.Errorf("SSH port is invalid")
	}
	port := 0
	for _, r := range value {
		port = port*10 + int(r-'0')
		if port > 65535 {
			return fmt.Errorf("SSH port is invalid")
		}
	}
	if port < 1 {
		return fmt.Errorf("SSH port is invalid")
	}
	return nil
}

func cliValidateTimeout(value string) error {
	if value == "0" {
		return nil
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return fmt.Errorf("SSH connect timeout is invalid")
		}
	}
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return fmt.Errorf("SSH connect timeout is invalid")
	}
	timeout := 0
	for _, r := range value {
		timeout = timeout*10 + int(r-'0')
		if timeout > 86400 {
			return fmt.Errorf("SSH connect timeout is invalid")
		}
	}
	return nil
}
