package main

import (
	"bytes"
	"fmt"
	"os"

	"code.linenisgreat.com/tommy/generate"
)

func runGenerate(args []string) int {
	check := false
	for _, a := range args {
		if a == "--check" || a == "-check" {
			check = true
		}
	}

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

	// Stamp this binary's identity into the generated header so a stale codegen
	// binary against a newer tommy library is visible (#125). version/commit are
	// the ldflags-injected main vars (eng-versioning(7)).
	generate.BuildVersion = version
	generate.BuildCommit = commit

	outPath := generate.OutputPath(dir, goFile)

	if check {
		// CI staleness/skew guard: regenerate to memory and diff against the
		// committed file (no write). A mismatch means it's out of date OR was
		// produced by a different tommy (the header stamp differs) — #125.
		want, err := generate.Render(dir, goFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tommy generate --check: %s\n", err)
			return 1
		}
		got, readErr := os.ReadFile(outPath)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "tommy generate --check: %s is missing; run `go generate`\n", outPath)
			return 1
		}
		if !bytes.Equal(got, want) {
			fmt.Fprintf(os.Stderr, "tommy generate --check: %s is out of date (run `go generate`; check that your tommy binary matches the tommy library version)\n", outPath)
			return 1
		}
		return 0
	}

	// Report the resolved output path + this tommy's identity, so a wrong target
	// (output-filename mismatch) or a stale binary is visible at a glance.
	fmt.Fprintf(os.Stderr, "tommy %s (%s): writing %s\n", version, commit, outPath)

	if err := generate.Generate(dir, goFile); err != nil {
		fmt.Fprintf(os.Stderr, "tommy generate: %s\n", err)
		return 1
	}

	return 0
}
