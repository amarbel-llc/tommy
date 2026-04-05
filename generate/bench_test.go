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
	Title       string            ` + "`toml:\"title\"`" + `
	Description string            ` + "`toml:\"description\"`" + `
	Version     int               ` + "`toml:\"version\"`" + `
	MaxRetries  int               ` + "`toml:\"max_retries\"`" + `
	Timeout     float64           ` + "`toml:\"timeout\"`" + `
	Debug       *bool             ` + "`toml:\"debug\"`" + `
	Verbose     *bool             ` + "`toml:\"verbose\"`" + `
	Tags        []string          ` + "`toml:\"tags\"`" + `
	Features    []string          ` + "`toml:\"features\"`" + `
	Env         map[string]string ` + "`toml:\"env\"`" + `
	Database    Database          ` + "`toml:\"database\"`" + `
	Logging     Logging           ` + "`toml:\"logging\"`" + `
	Servers     []Server          ` + "`toml:\"servers\"`" + `
}

type Database struct {
	Host     string ` + "`toml:\"host\"`" + `
	Port     int    ` + "`toml:\"port\"`" + `
	Name     string ` + "`toml:\"name\"`" + `
	User     string ` + "`toml:\"user\"`" + `
	Password string ` + "`toml:\"password\"`" + `
	SSLMode  string ` + "`toml:\"ssl_mode\"`" + `
	MaxConns int    ` + "`toml:\"max_conns\"`" + `
	IdleConns int   ` + "`toml:\"idle_conns\"`" + `
}

type Logging struct {
	Level  string ` + "`toml:\"level\"`" + `
	Format string ` + "`toml:\"format\"`" + `
	Output string ` + "`toml:\"output\"`" + `
	File   string ` + "`toml:\"file\"`" + `
}

type Server struct {
	Host       string            ` + "`toml:\"host\"`" + `
	Port       int               ` + "`toml:\"port\"`" + `
	Role       string            ` + "`toml:\"role\"`" + `
	Weight     int               ` + "`toml:\"weight\"`" + `
	MaxConns   int               ` + "`toml:\"max_conns\"`" + `
	TLS        bool              ` + "`toml:\"tls\"`" + `
	Region     string            ` + "`toml:\"region\"`" + `
	Datacenter string            ` + "`toml:\"datacenter\"`" + `
	Labels     map[string]string ` + "`toml:\"labels\"`" + `
	Healthcheck Healthcheck      ` + "`toml:\"healthcheck\"`" + `
}

type Healthcheck struct {
	Path     string ` + "`toml:\"path\"`" + `
	Interval int    ` + "`toml:\"interval\"`" + `
	Timeout  int    ` + "`toml:\"timeout\"`" + `
	Retries  int    ` + "`toml:\"retries\"`" + `
}
`

const benchTOML = `title = "Production App"
description = "A large configuration for benchmarking codegen backends"
version = 42
max_retries = 3
timeout = 30.5
debug = true
verbose = false
tags = ["web", "api", "v2", "production"]
features = ["auth", "rate-limit", "cors", "logging", "metrics"]

[env]
HOME = "/home/app"
PORT = "8080"
DEBUG = "true"
LOG_LEVEL = "info"
DATABASE_URL = "postgres://localhost/mydb"
REDIS_URL = "redis://localhost:6379"
SECRET_KEY = "supersecret"
API_KEY = "abc123def456"

[database]
host = "db.example.com"
port = 5432
name = "production"
user = "app"
password = "secret"
ssl_mode = "require"
max_conns = 100
idle_conns = 10

[logging]
level = "info"
format = "json"
output = "stdout"
file = "/var/log/app.log"

[[servers]]
host = "alpha.example.com"
port = 8080
role = "primary"
weight = 10
max_conns = 1000
tls = true
region = "us-east-1"
datacenter = "dc1"

[servers.labels]
env = "prod"
team = "backend"
tier = "frontend"

[servers.healthcheck]
path = "/health"
interval = 10
timeout = 5
retries = 3

[[servers]]
host = "beta.example.com"
port = 8081
role = "replica"
weight = 5
max_conns = 500
tls = true
region = "us-west-2"
datacenter = "dc2"

[servers.labels]
env = "prod"
team = "backend"
tier = "backend"

[servers.healthcheck]
path = "/health"
interval = 15
timeout = 5
retries = 2

[[servers]]
host = "gamma.example.com"
port = 8082
role = "replica"
weight = 5
max_conns = 500
tls = false
region = "eu-west-1"
datacenter = "dc3"

[servers.labels]
env = "staging"
team = "infra"

[servers.healthcheck]
path = "/ready"
interval = 30
timeout = 10
retries = 1

[[servers]]
host = "delta.example.com"
port = 8083
role = "replica"
weight = 3
max_conns = 250
tls = true
region = "ap-southeast-1"
datacenter = "dc4"

[servers.labels]
env = "prod"
team = "backend"
tier = "cache"
version = "v2"

[servers.healthcheck]
path = "/health"
interval = 10
timeout = 3
retries = 5

[[servers]]
host = "epsilon.example.com"
port = 8084
role = "standby"
weight = 1
max_conns = 100
tls = true
region = "us-east-1"
datacenter = "dc1"

[servers.labels]
env = "prod"
team = "sre"

[servers.healthcheck]
path = "/health"
interval = 60
timeout = 10
retries = 1
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

// benchTypesOnly is the struct definitions without //go:generate (for external libs).
const benchTypesOnly = `package bench

type Config struct {
	Title       string            ` + "`toml:\"title\"`" + `
	Description string            ` + "`toml:\"description\"`" + `
	Version     int               ` + "`toml:\"version\"`" + `
	MaxRetries  int               ` + "`toml:\"max_retries\"`" + `
	Timeout     float64           ` + "`toml:\"timeout\"`" + `
	Debug       *bool             ` + "`toml:\"debug\"`" + `
	Verbose     *bool             ` + "`toml:\"verbose\"`" + `
	Tags        []string          ` + "`toml:\"tags\"`" + `
	Features    []string          ` + "`toml:\"features\"`" + `
	Env         map[string]string ` + "`toml:\"env\"`" + `
	Database    Database          ` + "`toml:\"database\"`" + `
	Logging     Logging           ` + "`toml:\"logging\"`" + `
	Servers     []Server          ` + "`toml:\"servers\"`" + `
}

type Database struct {
	Host      string ` + "`toml:\"host\"`" + `
	Port      int    ` + "`toml:\"port\"`" + `
	Name      string ` + "`toml:\"name\"`" + `
	User      string ` + "`toml:\"user\"`" + `
	Password  string ` + "`toml:\"password\"`" + `
	SSLMode   string ` + "`toml:\"ssl_mode\"`" + `
	MaxConns  int    ` + "`toml:\"max_conns\"`" + `
	IdleConns int    ` + "`toml:\"idle_conns\"`" + `
}

type Logging struct {
	Level  string ` + "`toml:\"level\"`" + `
	Format string ` + "`toml:\"format\"`" + `
	Output string ` + "`toml:\"output\"`" + `
	File   string ` + "`toml:\"file\"`" + `
}

type Server struct {
	Host        string            ` + "`toml:\"host\"`" + `
	Port        int               ` + "`toml:\"port\"`" + `
	Role        string            ` + "`toml:\"role\"`" + `
	Weight      int               ` + "`toml:\"weight\"`" + `
	MaxConns    int               ` + "`toml:\"max_conns\"`" + `
	TLS         bool              ` + "`toml:\"tls\"`" + `
	Region      string            ` + "`toml:\"region\"`" + `
	Datacenter  string            ` + "`toml:\"datacenter\"`" + `
	Labels      map[string]string ` + "`toml:\"labels\"`" + `
	Healthcheck Healthcheck       ` + "`toml:\"healthcheck\"`" + `
}

type Healthcheck struct {
	Path     string ` + "`toml:\"path\"`" + `
	Interval int    ` + "`toml:\"interval\"`" + `
	Timeout  int    ` + "`toml:\"timeout\"`" + `
	Retries  int    ` + "`toml:\"retries\"`" + `
}
`

const benchTestBurntSushi = `package bench

import (
	"testing"

	"github.com/BurntSushi/toml"
)

var benchInput = []byte(` + "`" + benchTOML + "`" + `)

func BenchmarkDecode(b *testing.B) {
	for b.Loop() {
		var cfg Config
		if err := toml.Unmarshal(benchInput, &cfg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncode(b *testing.B) {
	var cfg Config
	if err := toml.Unmarshal(benchInput, &cfg); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := toml.Marshal(cfg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRoundTrip(b *testing.B) {
	for b.Loop() {
		var cfg Config
		if err := toml.Unmarshal(benchInput, &cfg); err != nil {
			b.Fatal(err)
		}
		if _, err := toml.Marshal(cfg); err != nil {
			b.Fatal(err)
		}
	}
}
`

const benchTestPelletier = `package bench

import (
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

var benchInput = []byte(` + "`" + benchTOML + "`" + `)

func BenchmarkDecode(b *testing.B) {
	for b.Loop() {
		var cfg Config
		if err := toml.Unmarshal(benchInput, &cfg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncode(b *testing.B) {
	var cfg Config
	if err := toml.Unmarshal(benchInput, &cfg); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := toml.Marshal(cfg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRoundTrip(b *testing.B) {
	for b.Loop() {
		var cfg Config
		if err := toml.Unmarshal(benchInput, &cfg); err != nil {
			b.Fatal(err)
		}
		if _, err := toml.Marshal(cfg); err != nil {
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
		{"legacy", "legacy"},
		{"api", "api"},
		{"cst", "cst"},
		{"jen", ""},
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

	// External libraries
	externals := []struct {
		name    string
		gomod   string
		testSrc string
	}{
		{
			"burntsushi",
			"module example.com/bench\n\ngo 1.26\n\nrequire github.com/BurntSushi/toml v1.6.0\n",
			benchTestBurntSushi,
		},
		{
			"pelletier",
			"module example.com/bench\n\ngo 1.26\n\nrequire github.com/pelletier/go-toml/v2 v2.2.4\n",
			benchTestPelletier,
		},
	}

	for _, ext := range externals {
		t.Run(ext.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, dir, "go.mod", ext.gomod)
			writeFixture(t, dir, "types.go", benchTypesOnly)
			writeFixture(t, dir, "bench_test.go", ext.testSrc)

			tidy := exec.Command("go", "mod", "tidy")
			tidy.Dir = dir
			if out, err := tidy.CombinedOutput(); err != nil {
				t.Fatalf("go mod tidy: %s\n%s", err, out)
			}

			cmd := exec.Command("go", "test", "-bench=.", "-benchmem", "-benchtime=500ms", "-count=1", "./...")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOFLAGS=")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go test -bench failed for %s:\n%s", ext.name, out)
			}

			results[ext.name] = extractBenchLines(string(out))
			t.Logf("=== %s ===\n%s", ext.name, results[ext.name])
		})
	}

	// Print summary
	allNames := []string{"old", "api", "cst", "jen", "burntsushi", "pelletier"}
	t.Log("\n=== BENCHMARK COMPARISON ===")
	for _, name := range allNames {
		if r, ok := results[name]; ok {
			t.Logf("\n--- %s ---\n%s", name, r)
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
// Use go test -bench=BenchmarkDecodeOnly -benchmem ./generate/
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
