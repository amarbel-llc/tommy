package main

import (
	"fmt"
	"io"
	"os"

	"github.com/amarbel-llc/tommy/internal/formatter"
)

func runFmt(args []string) int {
	check := false
	var files []string

	for _, arg := range args {
		switch arg {
		case "--check":
			check = true
		default:
			files = append(files, arg)
		}
	}

	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "usage: tommy fmt [--check] <files...|->\n")
		return 1
	}

	exitCode := 0

	for _, file := range files {
		if err := fmtFile(file, check); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", file, err)
			exitCode = 1
		}
	}

	return exitCode
}

func fmtFile(path string, check bool) error {
	var input []byte
	var err error

	if path == "-" {
		input, err = io.ReadAll(os.Stdin)
	} else {
		input, err = os.ReadFile(path)
	}

	if err != nil {
		return err
	}

	output := formatter.Format(input)

	if check {
		if string(output) != string(input) {
			return fmt.Errorf("not formatted")
		}
		return nil
	}

	if path == "-" {
		_, err = os.Stdout.Write(output)
		return err
	}

	return os.WriteFile(path, output, 0644)
}
