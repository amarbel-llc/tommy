package generate

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
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
	if err := RenderFile(&buf, pkgName, infos); err != nil {
		return fmt.Errorf("render: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("gofmt: %w\nraw output:\n%s", err, buf.String())
	}

	outName := strings.TrimSuffix(filename, ".go") + "_tommy.go"
	outPath := filepath.Join(dir, outName)
	return os.WriteFile(outPath, formatted, 0o644)
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
