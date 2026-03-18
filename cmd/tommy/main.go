package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: tommy <command>\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "fmt":
		fmt.Fprintf(os.Stderr, "tommy fmt: not yet implemented\n")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}
