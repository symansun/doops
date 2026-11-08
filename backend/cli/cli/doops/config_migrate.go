package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func errIfAbandonedLegacyConfig(canonicalPath string) error {
	if strings.TrimSpace(os.Getenv("DOOPS_CONFIG")) != "" {
		return nil
	}
	home, _ := os.UserHomeDir()
	legacyPath := filepath.Join(home, ".config", "doops", "config.json")
	if _, err := os.Stat(legacyPath); err != nil {
		return nil
	}
	return fmt.Errorf("legacy config %s is retired; migrate gateway/cluster/instance into %s and delete ip/port — do not load the old file", legacyPath, canonicalPath)
}

func annotateRetiredTargetFields(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, `unknown field "ip"`) || strings.Contains(msg, `unknown field "port"`) {
		return fmt.Errorf("%w: direct agent targets have been removed; use gateway/cluster/instance instead", err)
	}
	return err
}
