package main

import (
	"fmt"
	"os"

	"github.com/user/doops/agent/internal/server"
)

func main() {
	if err := server.RunDoopsPlanMCP(os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
