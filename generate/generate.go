package generate

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/imports"
)

func Generate(dir, filename string) error {
	infos, err := Analyze(dir, filename)
	if err != nil {
		return fmt.Errorf("analyze: %w", err)
	}

	if len(infos) == 0 {
		return fmt.Errorf("no structs with //go:generate tommy generate found in %s", filename)
	}

	pkgName, err := detectPackageName(dir, filename)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := RenderFileJen(&buf, pkgName, infos); err != nil {
		return fmt.Errorf("render: %w", err)
	}

	outName := strings.TrimSuffix(filename, ".go") + "_tommy.go"
	outPath := filepath.Join(dir, outName)

	formatted, err := imports.Process(outPath, buf.Bytes(), goimportsOpts)
	if err != nil {
		return fmt.Errorf("goimports: %w\nraw output:\n%s", err, buf.String())
	}

	return os.WriteFile(outPath, formatted, 0o644)
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
