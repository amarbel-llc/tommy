package main

import (
	"fmt"
	"os"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: tommy <command>\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "fmt":
		os.Exit(runFmt(os.Args[2:]))
	case "generate":
		os.Exit(runGenerate(os.Args[2:]))
	case "version":
		fmt.Printf("tommy %s (%s)\n", version, commit)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}
