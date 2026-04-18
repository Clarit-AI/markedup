package main

import (
	"os"

	"github.com/Clarit-AI/markedup/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
