package main

import (
	"fmt"
	"os"

	"github.com/amarbel-llc/tommy/generate"
)

func runGenerate(args []string) int {
	goFile := os.Getenv("GOFILE")
	if goFile == "" {
		fmt.Fprintf(os.Stderr, "tommy generate: $GOFILE not set (must be run via go generate)\n")
		return 1
	}

	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tommy generate: %s\n", err)
		return 1
	}

	if err := generate.Generate(dir, goFile); err != nil {
		fmt.Fprintf(os.Stderr, "tommy generate: %s\n", err)
		return 1
	}

	return 0
}
