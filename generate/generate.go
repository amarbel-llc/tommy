package generate

import (
	"bytes"
	"fmt"
	"go/version"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/imports"
	gofumpt "mvdan.cc/gofumpt/format"
)

// OutputPath returns the generated-file path for a source file: <base>_tommy.go
// in the same directory. Exposed so callers can report or diff the target
// without re-deriving the convention.
func OutputPath(dir, filename string) string {
	return filepath.Join(dir, strings.TrimSuffix(filename, ".go")+"_tommy.go")
}

// Render analyzes filename and returns the formatted generated-file content
// WITHOUT writing it. Generate writes Render's output; `tommy generate --check`
// diffs it against the on-disk file. Deterministic for a fixed input + tommy
// build (the header carries BuildVersion/BuildCommit), so a content mismatch
// means the file is stale or was produced by a different tommy.
func Render(dir, filename string) ([]byte, error) {
	infos, err := Analyze(dir, filename)
	if err != nil {
		return nil, fmt.Errorf("analyze: %w", err)
	}

	if len(infos) == 0 {
		return nil, fmt.Errorf("no structs with //go:generate tommy generate found in %s", filename)
	}

	pkgName, err := detectPackageName(dir, filename)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := RenderFile(&buf, pkgName, infos); err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}

	formatted, err := imports.Process(OutputPath(dir, filename), buf.Bytes(), goimportsOpts)
	if err != nil {
		return nil, fmt.Errorf("goimports: %w\nraw output:\n%s", err, buf.String())
	}

	// goimports only runs gofmt, which leaves consecutive top-level declarations
	// packed (no blank line between them). Consumers gate generated code on
	// gofumpt, so format with gofumpt as a final pass (#134). LangVersion is the
	// consumer module's Go version so the output matches the version their own
	// gofumpt gate runs under, not just gofumpt's go1 default.
	formatted, err = gofumpt.Source(formatted, gofumpt.Options{LangVersion: detectGoLangVersion(dir)})
	if err != nil {
		return nil, fmt.Errorf("gofumpt: %w\nraw output:\n%s", err, formatted)
	}
	return formatted, nil
}

func Generate(dir, filename string) error {
	formatted, err := Render(dir, filename)
	if err != nil {
		return err
	}
	return os.WriteFile(OutputPath(dir, filename), formatted, 0o644)
}

// goimportsOpts is the imports.Process configuration used for all
// generated output. FormatOnly skips import resolution (we never add
// or remove imports — the template already declares them); the pass
// only sorts existing entries and splits stdlib from third-party.
var goimportsOpts = &imports.Options{
	Comments:   true,
	TabIndent:  true,
	TabWidth:   8,
	FormatOnly: true,
}

// detectGoLangVersion returns the enclosing module's Go language version as a
// go/version-valid string (e.g. "go1.26"), read from the `go` directive of the
// nearest go.mod walking up from dir. gofumpt uses it to decide which
// version-gated rules apply; matching the consumer module's version keeps
// tommy's formatting in lock-step with the gofumpt gate that module runs.
// Best-effort: returns "" (gofumpt's go1 default) when no go.mod or directive
// is found, or the directive isn't a valid version.
func detectGoLangVersion(dir string) string {
	d, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		data, err := os.ReadFile(filepath.Join(d, "go.mod"))
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[0] == "go" {
					if v := "go" + fields[1]; version.IsValid(v) {
						return v
					}
					return ""
				}
			}
			return "" // go.mod present but no `go` directive
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "" // reached filesystem root without finding go.mod
		}
		d = parent
	}
}

func detectPackageName(dir, filename string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, filename))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			return strings.Fields(line)[1], nil
		}
	}
	return "", fmt.Errorf("no package declaration in %s", filename)
}
