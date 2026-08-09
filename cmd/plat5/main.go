package main

import (
	"os"

	"github.com/plat5dev/cli/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
