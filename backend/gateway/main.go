package main

import (
	"os"

	"github.com/symansun/doops/backend/agent/pkg/gatewaycmd"
)

func main() {
	gatewaycmd.Run(os.Args[1:])
}
