package main

import (
	"fmt"
	"strings"
)

type stringListFlag []string

func (values *stringListFlag) String() string {
	return strings.Join(*values, ",")
}

func (values *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("value must not be empty")
	}
	for _, existing := range *values {
		if existing == value {
			return fmt.Errorf("duplicate value %q", value)
		}
	}
	*values = append(*values, value)
	return nil
}
