package generate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const benchGoFile = `package bench

//go:generate tommy generate
type Config struct {
	Title   string            ` + "`toml:\"title\"`" + `
	Version int               ` + "`toml:\"version\"`" + `
	Debug   *bool             ` + "`toml:\"debug\"`" + `
	Tags    []string          ` + "`toml:\"tags\"`" + `
	Env     map[string]string ` + "`toml:\"env\"`" + `
	Servers []Server          ` + "`toml:\"servers\"`" + `
}

type Server struct {
	Host string ` + "`toml:\"host\"`" + `
	Port int    ` + "`toml:\"port\"`" + `
	Role string ` + "`toml:\"role\"`" + `
}
`

const benchTOML = `title = "My App"
version = 42
debug = true
tags = ["web", "api", "v2"]

[env]
HOME = "/home/app"
PORT = "8080"
DEBUG = "true"

[[servers]]
host = "alpha.example.com"
port = 8080
role = "primary"

[[servers]]
host = "beta.example.com"
port = 8081
role = "replica"

[[servers]]
host = "gamma.example.com"
port = 8082
role = "replica"
`

const benchTestFile = `package bench

import "testing"

var benchInput = []byte(` + "`" + benchTOML + "`" + `)

func BenchmarkDecode(b *testing.B) {
	for b.Loop() {
		doc, err := DecodeConfig(benchInput)
		if err != nil {
			b.Fatal(err)
		}
		_ = doc.Data()
	}
}

func BenchmarkEncode(b *testing.B) {
	doc, err := DecodeConfig(benchInput)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		_, err := doc.Encode()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRoundTrip(b *testing.B) {
	for b.Loop() {
		doc, err := DecodeConfig(benchInput)
		if err != nil {
			b.Fatal(err)
		}
		_, err = doc.Encode()
		if err != nil {
			b.Fatal(err)
		}
	}
}
`

func TestBenchmarkBackends(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping benchmark test in short mode")
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	backends := []struct {
		name string
		env  string
	}{
		{"old", ""},
		{"api", "api"},
		{"cst", "cst"},
		{"jen", "jen"},
	}

	results := make(map[string]string)

	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			dir := t.TempDir()

			writeFixture(t, dir, "go.mod", strings.Join([]string{
				"module example.com/bench", "", "go 1.26", "",
				"require github.com/amarbel-llc/tommy v0.0.0", "",
				"replace github.com/amarbel-llc/tommy => " + repoRoot, "",
			}, "\n"))

			writeFixture(t, dir, "config.go", benchGoFile)

			// go mod tidy before Generate so packages.Load works
			tidy := exec.Command("go", "mod", "tidy")
			tidy.Dir = dir
			if out, err := tidy.CombinedOutput(); err != nil {
				t.Fatalf("go mod tidy: %s\n%s", err, out)
			}

			if backend.env != "" {
				t.Setenv("TOMMY_CODEGEN_IR", backend.env)
			} else {
				t.Setenv("TOMMY_CODEGEN_IR", "")
			}

			if err := Generate(dir, "config.go"); err != nil {
				t.Fatalf("Generate(%s): %v", backend.name, err)
			}

			// Write bench test AFTER Generate so DecodeConfig is declared
			writeFixture(t, dir, "bench_test.go", benchTestFile)

			// Re-tidy after Generate so generated imports are resolved
			tidy2 := exec.Command("go", "mod", "tidy")
			tidy2.Dir = dir
			if out, err := tidy2.CombinedOutput(); err != nil {
				t.Fatalf("go mod tidy (post-gen): %s\n%s", err, out)
			}

			cmd := exec.Command("go", "test", "-bench=.", "-benchmem", "-benchtime=500ms", "-count=1", "./...")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOFLAGS=")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go test -bench failed for %s:\n%s", backend.name, out)
			}

			results[backend.name] = extractBenchLines(string(out))
			t.Logf("=== %s ===\n%s", backend.name, results[backend.name])
		})
	}

	// Print summary
	t.Log("\n=== BENCHMARK COMPARISON ===")
	for _, b := range backends {
		if r, ok := results[b.name]; ok {
			t.Logf("\n--- %s ---\n%s", b.name, r)
		}
	}
}

func extractBenchLines(output string) string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "Benchmark") || strings.HasPrefix(line, "goos:") || strings.HasPrefix(line, "goarch:") || strings.HasPrefix(line, "pkg:") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// BenchmarkDecodeOnly runs decode benchmarks in-process for the current backend.
// Use TOMMY_CODEGEN_IR=jen go test -bench=BenchmarkDecodeOnly -benchmem ./generate/
func BenchmarkDecodeOnly(b *testing.B) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		b.Fatal(err)
	}

	dir := b.TempDir()
	writeFixtureB(b, dir, "go.mod", fmt.Sprintf(
		"module example.com/bench\n\ngo 1.26\n\nrequire github.com/amarbel-llc/tommy v0.0.0\n\nreplace github.com/amarbel-llc/tommy => %s\n", repoRoot))
	writeFixtureB(b, dir, "config.go", benchGoFile)
	writeFixtureB(b, dir, "bench_test.go", benchTestFile)

	if err := Generate(dir, "config.go"); err != nil {
		b.Fatalf("Generate: %v", err)
	}

	cmd := exec.Command("go", "test", "-bench=.", "-benchmem", "-benchtime=1s", "-count=3", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		b.Fatalf("go test -bench failed:\n%s", out)
	}
	b.Logf("\n%s", extractBenchLines(string(out)))
}

func writeFixtureB(b *testing.B, dir, filename, content string) {
	b.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
		b.Fatal(err)
	}
}
