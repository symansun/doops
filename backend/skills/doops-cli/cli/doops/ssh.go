package main

import "strings"

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
