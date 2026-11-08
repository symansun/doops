// E2E-only offline bootstrap. Synthetic credentials enter through stdin and are
// passed to the existing Gateway commands in memory, never through process argv.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/symansun/doops/backend/agent/pkg/gatewaycmd"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: doops-e2e-seed <database-path> < seed.json")
		os.Exit(2)
	}
	var seed struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Cluster  string `json:"cluster"`
		Instance string `json:"instance"`
	}
	if json.NewDecoder(io.LimitReader(os.Stdin, 4096)).Decode(&seed) != nil ||
		seed.Username == "" || seed.Password == "" || seed.Cluster == "" || seed.Instance == "" {
		fmt.Fprintln(os.Stderr, "invalid E2E seed input")
		os.Exit(2)
	}
	gatewaycmd.Run([]string{"user", "create", "-name", seed.Username, "-password", seed.Password, "-db", os.Args[1]})
	gatewaycmd.Run([]string{"grant", "-user", seed.Username, "-cluster", seed.Cluster, "-instance", seed.Instance, "-db", os.Args[1]})
}
