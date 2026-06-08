package generate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testGoEnv returns the GOFLAGS override for the synthetic-module `go`
// subprocesses these integration tests spawn. Locally we clear GOFLAGS to force
// network module resolution (the default dev path). Under TOMMY_TEST_OFFLINE
// (set by the nix go-generate check) we return nothing, so the subprocess
// inherits the derivation's offline env (GOFLAGS=-mod=mod, GOPROXY=off,
// GOMODCACHE=<staged cache>) and resolves tommy + its deps without network.
// Analyze's in-process packages.Load already inherits that env unchanged.
func testGoEnv() []string {
	if os.Getenv("TOMMY_TEST_OFFLINE") != "" {
		return nil
	}
	return []string{"GOFLAGS="}
}

func TestIntegrationRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	// Absolute path to the repo root for the replace directive.
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/roundtrip",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package roundtrip

//go:generate tommy generate
type Config struct {
	Name    string `+"`"+`toml:"name"`+"`"+`
	Port    int    `+"`"+`toml:"port"`+"`"+`
	Enabled bool   `+"`"+`toml:"enabled"`+"`"+`
}
`)

	// Run code generation.
	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Verify generated file exists.
	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	// Write a test that exercises the generated decode/encode round-trip.
	writeFixture(t, dir, "roundtrip_test.go", `package roundtrip

import (
	"strings"
	"testing"
)

const testInput = `+"`"+`# Application config
name = "myapp"
port = 8080
enabled = true
`+"`"+`

func TestDecodeEncode(t *testing.T) {
	doc, err := DecodeConfig([]byte(testInput))
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}

	data := doc.Data()
	if data.Name != "myapp" {
		t.Fatalf("Name = %q, want %q", data.Name, "myapp")
	}
	if data.Port != 8080 {
		t.Fatalf("Port = %d, want %d", data.Port, 8080)
	}
	if !data.Enabled {
		t.Fatal("Enabled = false, want true")
	}

	// Modify a field and encode.
	data.Port = 9090

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	result := string(out)

	// Comment must survive the round-trip.
	if !strings.Contains(result, "# Application config") {
		t.Fatalf("comment lost in round-trip:\n%s", result)
	}

	// The modified value must appear.
	if !strings.Contains(result, "9090") {
		t.Fatalf("modified port not found:\n%s", result)
	}

	// The original value must not appear.
	if strings.Contains(result, "8080") {
		t.Fatalf("old port value still present:\n%s", result)
	}

	// Decode the re-encoded output to verify it round-trips cleanly.
	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("second DecodeConfig: %v", err)
	}
	d2 := doc2.Data()
	if d2.Port != 9090 {
		t.Fatalf("re-decoded Port = %d, want 9090", d2.Port)
	}
	if d2.Name != "myapp" {
		t.Fatalf("re-decoded Name = %q, want %q", d2.Name, "myapp")
	}
}
`)

	// Run go test in the temp dir.
	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

// Regression for the nested map[string]NamedMap scoping bug surfaced by the #91
// fuzzer extension: a mapmap nested inside a map-struct entry (itself inside an
// array-table entry) must scope its sub-tables to the enclosing entry, not the
// document root, and must trim the runtime map key against the enclosing scope's
// header (the loop's _ch must not shadow that scope). See #86/#87 for the kind.
func TestIntegrationNestedMapMapScoping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "go.mod", "module example.com/mm\n\ngo 1.26\n\nrequire github.com/amarbel-llc/tommy v0.0.0\n\nreplace github.com/amarbel-llc/tommy => "+repoRoot+"\n")
	writeFixture(t, dir, "config.go", `package mm

type Labels map[string]string

type Inner struct {
	M map[string]Labels `+"`"+`toml:"m"`+"`"+`
}

// MapStruct holds a map[string]Inner, so a mapmap nests inside a map-struct entry.
//go:generate tommy generate
type MapStructCfg struct {
	Am map[string]Inner `+"`"+`toml:"am"`+"`"+`
}

// ArrayMapStruct nests mapmap inside a map-struct entry inside an array entry.
//go:generate tommy generate
type ArrayMapStructCfg struct {
	Items []MapStructCfg `+"`"+`toml:"items"`+"`"+`
}
`)
	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	writeFixture(t, dir, "mm_test.go", `package mm

import (
	"reflect"
	"testing"
)

func TestMM(t *testing.T) {
	// map-struct entry containing a mapmap.
	md, _ := DecodeMapStructCfg([]byte(""))
	wantMS := MapStructCfg{Am: map[string]Inner{
		"e0": {M: map[string]Labels{"k0": {"a": "1"}, "k1": {"b": "2"}}},
		"e1": {M: map[string]Labels{"k0": {"c": "3"}}},
	}}
	*md.Data() = wantMS
	out, _ := md.Encode()
	md2, err := DecodeMapStructCfg(out)
	if err != nil || !reflect.DeepEqual(*md2.Data(), wantMS) {
		t.Fatalf("MAPSTRUCT mismatch err=%v\nwant %#v\ngot  %#v\n--TOML--\n%s", err, wantMS, *md2.Data(), out)
	}

	// array entry -> map-struct entry -> mapmap (the failing fuzzer shape).
	ad, _ := DecodeArrayMapStructCfg([]byte(""))
	wantA := ArrayMapStructCfg{Items: []MapStructCfg{
		{Am: map[string]Inner{"e0": {M: map[string]Labels{"x": {"a": "1"}}}}},
		{Am: map[string]Inner{"e1": {M: map[string]Labels{"y": {"b": "2"}}}}},
	}}
	*ad.Data() = wantA
	out2, _ := ad.Encode()
	ad2, err := DecodeArrayMapStructCfg(out2)
	if err != nil || !reflect.DeepEqual(*ad2.Data(), wantA) {
		t.Fatalf("ARRAY-MAPSTRUCT mismatch err=%v\nwant %#v\ngot  %#v\n--TOML--\n%s", err, wantA, *ad2.Data(), out2)
	}
}
`)
	cmd := exec.Command("go", "test", "-run", "TestMM", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed:\n%s", output)
	}
}

// Regression for #100: a nil *Struct field must stay nil when its [table] is
// absent, even if one of the struct's inner fields shares a bare key with a
// sibling field of the parent. The #55 flat-key fallback must not claim the
// sibling-owned key and wrongly materialize the pointer.
func TestIntegrationFlatFallbackSiblingKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "go.mod", "module example.com/ff\n\ngo 1.26\n\nrequire github.com/amarbel-llc/tommy v0.0.0\n\nreplace github.com/amarbel-llc/tommy => "+repoRoot+"\n")
	writeFixture(t, dir, "config.go", `package ff

type Inner struct {
	F1 *int `+"`"+`toml:"f1"`+"`"+`
}

//go:generate tommy generate
type Config struct {
	Ptr *Inner `+"`"+`toml:"ptr"`+"`"+`
	F1  int    `+"`"+`toml:"f1"`+"`"+`
}
`)
	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	writeFixture(t, dir, "ff_test.go", `package ff

import "testing"

func TestFF(t *testing.T) {
	// No [ptr] table; the sibling f1 belongs to Config, not Inner.
	d, err := DecodeConfig([]byte("f1 = -46445\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := d.Data()
	if got.Ptr != nil {
		t.Fatalf("Ptr should be nil (no [ptr] table), got %#v", got.Ptr)
	}
	if got.F1 != -46445 {
		t.Fatalf("F1 = %d, want -46445", got.F1)
	}
}
`)
	cmd := exec.Command("go", "test", "-run", "TestFF", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed:\n%s", out)
	}
}

// Regression for #105 (bug #4): a struct (Outer) with both an array-table field
// and a nested struct field whose own fields are ALL array-tables (Inner) must
// not bind an unused `tableNode`. compEncodeNeedsContainer counted the nested
// struct as needing the parent container, but a header-omitting all-array struct
// (#89) references neither, so the parent's tableNode was bound-but-unused and
// the generated code failed to compile. Same-package shape (no delegation).
func TestIntegrationNestedAllArrayStructEncodes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "go.mod", "module example.com/naas\n\ngo 1.26\n\nrequire github.com/amarbel-llc/tommy v0.0.0\n\nreplace github.com/amarbel-llc/tommy => "+repoRoot+"\n")
	writeFixture(t, dir, "config.go", `package naas

//go:generate tommy generate
type Config struct {
	Outer Outer `+"`"+`toml:"outer"`+"`"+`
}

type Outer struct {
	Items []Item `+"`"+`toml:"items"`+"`"+`
	Inner Inner  `+"`"+`toml:"inner"`+"`"+`
}

type Item struct {
	N string `+"`"+`toml:"n"`+"`"+`
}

type Inner struct {
	Things []Thing `+"`"+`toml:"things"`+"`"+`
}

type Thing struct {
	M string `+"`"+`toml:"m"`+"`"+`
}
`)
	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	writeFixture(t, dir, "naas_test.go", `package naas

import (
	"reflect"
	"testing"
)

func TestNAAS(t *testing.T) {
	d, err := DecodeConfig([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	want := Outer{
		Items: []Item{{N: "a"}, {N: "b"}},
		Inner: Inner{Things: []Thing{{M: "x"}, {M: "y"}}},
	}
	d.Data().Outer = want
	out, err := d.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	d2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v\n%s", err, out)
	}
	if !reflect.DeepEqual(d2.Data().Outer, want) {
		t.Fatalf("round-trip mismatch:\ngot:  %#v\nwant: %#v\ntoml:\n%s", d2.Data().Outer, want, out)
	}
}
`)
	cmd := exec.Command("go", "test", "-run", "TestNAAS", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed:\n%s", out)
	}
}

// Regression for #98: primitive slices of element types beyond string/int
// ([]bool, []float64, []int64, []uint64, and a pointer variant) must round-trip.
// Before the fix cstSliceExtractFunc fell through to the string extractor and
// EncodeValue rejected the slice — both silently dropped non-string/int slices.
func TestIntegrationNonStringIntSlices(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "go.mod", "module example.com/slices98\n\ngo 1.26\n\nrequire github.com/amarbel-llc/tommy v0.0.0\n\nreplace github.com/amarbel-llc/tommy => "+repoRoot+"\n")
	writeFixture(t, dir, "config.go", `package slices98

//go:generate tommy generate
type Config struct {
	B   []bool    `+"`"+`toml:"b"`+"`"+`
	F   []float64 `+"`"+`toml:"f"`+"`"+`
	I64 []int64   `+"`"+`toml:"i64"`+"`"+`
	U64 []uint64  `+"`"+`toml:"u64"`+"`"+`
	PB  []*bool   `+"`"+`toml:"pb"`+"`"+`
}
`)
	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	writeFixture(t, dir, "slices98_test.go", `package slices98

import (
	"reflect"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestSlices98(t *testing.T) {
	want := Config{
		B:   []bool{true, false, true},
		F:   []float64{1.5, -2.25, 0.5},
		I64: []int64{-9000000000, 42},
		U64: []uint64{18000000000, 7},
		PB:  []*bool{ptr(true), ptr(false)},
	}
	d, err := DecodeConfig([]byte(""))
	if err != nil { t.Fatal(err) }
	*d.Data() = want
	out, err := d.Encode()
	if err != nil { t.Fatalf("encode: %v", err) }
	d2, err := DecodeConfig(out)
	if err != nil { t.Fatalf("re-decode: %v\n%s", err, out) }
	if !reflect.DeepEqual(*d2.Data(), want) {
		t.Fatalf("round-trip mismatch\nwant %#v\ngot  %#v\ntoml:\n%s", want, *d2.Data(), out)
	}
}
`)
	cmd := exec.Command("go", "test", "-run", "TestSlices98", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed:\n%s", out)
	}
}

// Coverage for #96: sized integer and float32 fields — scalars, slices, and a
// pointer-slice — must round-trip. Sized slices decode via the widest extractor
// ([]int64/[]uint64/[]float64) narrowed per-element by the registry cast, and
// encode by widening back through the base slice encoders.
// Regression for #102: a sub-table defined twice within ONE array-table entry's
// scope ([e.sub] appearing twice under one [[e]]) must error on decode, like the
// top-level #92 duplicate-table guard — the scoped context is sound for this now
// that #99 fixed its scoping. A duplicate across DIFFERENT entries is fine (each
// entry is its own scope).
func TestIntegrationScopedDuplicateTableErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "go.mod", "module example.com/scopedup\n\ngo 1.26\n\nrequire github.com/amarbel-llc/tommy v0.0.0\n\nreplace github.com/amarbel-llc/tommy => "+repoRoot+"\n")
	writeFixture(t, dir, "config.go", `package scopedup

type Sub struct {
	V string `+"`"+`toml:"v"`+"`"+`
}

type Entry struct {
	Sub *Sub `+"`"+`toml:"sub"`+"`"+`
}

//go:generate tommy generate
type Config struct {
	E []Entry `+"`"+`toml:"e"`+"`"+`
}
`)
	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	writeFixture(t, dir, "scopedup_test.go", `package scopedup

import (
	"strings"
	"testing"
)

func TestScopedDup(t *testing.T) {
	// Two [e.sub] under the SAME [[e]] entry — a duplicate within one scope.
	dup := "[[e]]\n\n[e.sub]\nv = \"a\"\n\n[e.sub]\nv = \"b\"\n"
	if _, err := DecodeConfig([]byte(dup)); err == nil {
		t.Fatal("expected error for duplicate [e.sub] in one entry, got nil")
	} else if !strings.Contains(err.Error(), "duplicate table") {
		t.Fatalf("error should mention the duplicate table, got: %v", err)
	}

	// Same header under DIFFERENT entries is fine — separate scopes.
	ok := "[[e]]\n\n[e.sub]\nv = \"a\"\n\n[[e]]\n\n[e.sub]\nv = \"b\"\n"
	d, err := DecodeConfig([]byte(ok))
	if err != nil {
		t.Fatalf("two entries each with one [e.sub] should decode, got: %v", err)
	}
	if g := d.Data(); len(g.E) != 2 || g.E[0].Sub == nil || g.E[1].Sub == nil || g.E[0].Sub.V != "a" || g.E[1].Sub.V != "b" {
		t.Fatalf("unexpected decode: %#v", d.Data())
	}
}
`)
	cmd := exec.Command("go", "test", "-run", "TestScopedDup", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed:\n%s", out)
	}
}

func TestIntegrationSizedScalarSlices(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "go.mod", "module example.com/sized96\n\ngo 1.26\n\nrequire github.com/amarbel-llc/tommy v0.0.0\n\nreplace github.com/amarbel-llc/tommy => "+repoRoot+"\n")
	writeFixture(t, dir, "config.go", `package sized96

//go:generate tommy generate
type Config struct {
	I8  int8      `+"`"+`toml:"i8"`+"`"+`
	S8  []int8    `+"`"+`toml:"s8"`+"`"+`
	U8  uint8     `+"`"+`toml:"u8"`+"`"+`
	SU8 []uint8   `+"`"+`toml:"su8"`+"`"+`
	I16 []int16   `+"`"+`toml:"i16"`+"`"+`
	U32 []uint32  `+"`"+`toml:"u32"`+"`"+`
	F32 float32   `+"`"+`toml:"f32"`+"`"+`
	SF  []float32 `+"`"+`toml:"sf"`+"`"+`
	PI8 []*int8   `+"`"+`toml:"pi8"`+"`"+`
}
`)
	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	writeFixture(t, dir, "sized_test.go", `package sized96

import (
	"reflect"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestSized96(t *testing.T) {
	want := Config{
		I8:  -42,
		S8:  []int8{-128, 0, 127},
		U8:  200,
		SU8: []uint8{0, 255},
		I16: []int16{-30000, 30000},
		U32: []uint32{0, 4000000000},
		F32: 1.5,
		SF:  []float32{-2.25, 0.5},
		PI8: []*int8{ptr(int8(-5)), ptr(int8(5))},
	}
	d, err := DecodeConfig([]byte(""))
	if err != nil { t.Fatal(err) }
	*d.Data() = want
	out, err := d.Encode()
	if err != nil { t.Fatalf("encode: %v", err) }
	d2, err := DecodeConfig(out)
	if err != nil { t.Fatalf("re-decode: %v\n%s", err, out) }
	if !reflect.DeepEqual(*d2.Data(), want) {
		t.Fatalf("round-trip mismatch\nwant %#v\ngot  %#v\ntoml:\n%s", want, *d2.Data(), out)
	}
}
`)
	cmd := exec.Command("go", "test", "-run", "TestSized96", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed:\n%s", out)
	}
}

// Regression for #95 (map[string]string half): a map key needing TOML quoting
// (a dot or a space) must round-trip. Before the fix a dotted key serialized as
// the wrong `a.b = v` (a nested key) and a spaced key as invalid `a b = v` that
// re-decoded to an empty map (data loss). Now the key is quoted on encode and
// unquoted on decode.
func TestIntegrationQuotedMapStringKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "go.mod", "module example.com/qmk\n\ngo 1.26\n\nrequire github.com/amarbel-llc/tommy v0.0.0\n\nreplace github.com/amarbel-llc/tommy => "+repoRoot+"\n")
	writeFixture(t, dir, "config.go", `package qmk

//go:generate tommy generate
type Config struct {
	M map[string]string `+"`"+`toml:"m"`+"`"+`
}
`)
	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	writeFixture(t, dir, "qmk_test.go", `package qmk

import (
	"reflect"
	"strings"
	"testing"
)

func TestQMK(t *testing.T) {
	want := Config{M: map[string]string{"a.b": "x", "a b": "y", "plain": "z"}}
	d, err := DecodeConfig([]byte(""))
	if err != nil { t.Fatal(err) }
	*d.Data() = want
	out, err := d.Encode()
	if err != nil { t.Fatalf("encode: %v", err) }
	s := string(out)
	if !strings.Contains(s, "\"a.b\" = ") || !strings.Contains(s, "\"a b\" = ") {
		t.Fatalf("keys needing quoting should be quoted, got:\n%s", s)
	}
	if !strings.Contains(s, "\nplain = ") {
		t.Fatalf("a bare key should stay unquoted, got:\n%s", s)
	}
	d2, err := DecodeConfig(out)
	if err != nil { t.Fatalf("re-decode: %v\n%s", err, out) }
	if !reflect.DeepEqual(*d2.Data(), want) {
		t.Fatalf("round-trip mismatch\nwant %#v\ngot  %#v\ntoml:\n%s", want, *d2.Data(), out)
	}
}
`)
	cmd := exec.Command("go", "test", "-run", "TestQMK", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed:\n%s", out)
	}
}

// Regression for #21 (maps): a nil map[string]string omits its [table] and
// re-decodes nil; a present-but-empty map emits an empty [m] table and
// re-decodes a non-nil empty map. (struct/nested-map kinds normalize nil≡empty
// because their sub-table encoding has no distinct empty form.)
func TestIntegrationNilEmptyMapRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "go.mod", "module example.com/nem\n\ngo 1.26\n\nrequire github.com/amarbel-llc/tommy v0.0.0\n\nreplace github.com/amarbel-llc/tommy => "+repoRoot+"\n")
	writeFixture(t, dir, "config.go", `package nem

//go:generate tommy generate
type Config struct {
	Name string            `+"`"+`toml:"name"`+"`"+`
	M    map[string]string `+"`"+`toml:"m"`+"`"+`
}
`)
	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	writeFixture(t, dir, "nem_test.go", `package nem

import (
	"strings"
	"testing"
)

func rt(t *testing.T, set func(c *Config)) (Config, string) {
	doc, err := DecodeConfig([]byte("name = \"app\"\n"))
	if err != nil { t.Fatal(err) }
	set(doc.Data())
	out, err := doc.Encode()
	if err != nil { t.Fatal(err) }
	d2, err := DecodeConfig(out)
	if err != nil { t.Fatalf("re-decode: %v\n%s", err, out) }
	return *d2.Data(), string(out)
}

func TestNEM(t *testing.T) {
	nilRT, nilOut := rt(t, func(c *Config) { c.M = nil })
	if nilRT.M != nil {
		t.Fatalf("nil map should round-trip to nil, got %#v", nilRT.M)
	}
	if strings.Contains(nilOut, "[m]") {
		t.Fatalf("nil map should omit the [m] table, got:\n%s", nilOut)
	}

	emptyRT, emptyOut := rt(t, func(c *Config) { c.M = map[string]string{} })
	if emptyRT.M == nil || len(emptyRT.M) != 0 {
		t.Fatalf("empty map should round-trip to non-nil empty, got %#v", emptyRT.M)
	}
	if !strings.Contains(emptyOut, "[m]") {
		t.Fatalf("empty map should emit an empty [m] table, got:\n%s", emptyOut)
	}
}
`)
	cmd := exec.Command("go", "test", "-run", "TestNEM", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed:\n%s", out)
	}
}

// Regression for #101: an absent *struct whose inner field is itself
// table-valued (a map, here) must not false-match a root- or grandparent-level
// table. The #55/#89 flat-key fallback only makes sense for inner fields that
// read bare keys from the parent container; a map field resolves via a
// document-root [m] table scan, which previously matched the sibling Config.M
// and wrongly materialized Ptr. Acceptance test for the flat-fallback rework.
func TestIntegrationFlatFallbackTableValuedInner(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "go.mod", "module example.com/fftv\n\ngo 1.26\n\nrequire github.com/amarbel-llc/tommy v0.0.0\n\nreplace github.com/amarbel-llc/tommy => "+repoRoot+"\n")
	writeFixture(t, dir, "config.go", `package fftv

type Inner struct {
	M map[string]string `+"`"+`toml:"m"`+"`"+`
}

//go:generate tommy generate
type Config struct {
	Ptr *Inner            `+"`"+`toml:"ptr"`+"`"+`
	M   map[string]string `+"`"+`toml:"m"`+"`"+`
}
`)
	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	writeFixture(t, dir, "fftv_test.go", `package fftv

import "testing"

func TestFFTV(t *testing.T) {
	// Only a root [m] table (Config.M); no [ptr] table, so Ptr must be nil.
	d, err := DecodeConfig([]byte("[m]\nk = \"v\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := d.Data()
	if got.Ptr != nil {
		t.Fatalf("Ptr should be nil (no [ptr] table), got %#v", got.Ptr)
	}
	if got.M["k"] != "v" {
		t.Fatalf("Config.M[k] = %q, want \"v\"", got.M["k"])
	}
}
`)
	cmd := exec.Command("go", "test", "-run", "TestFFTV", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed:\n%s", out)
	}
}

// Regression for #92: a generated decoder must reject a table header defined
// more than once (per the TOML spec, "Defining a table more than once is
// invalid"), rather than silently using the first. Covers both the non-pointer
// (cdInTable) and pointer (cdNilGuard) struct-field decode paths.
func TestIntegrationDuplicateTableErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/duptable",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package duptable

//go:generate tommy generate
type Config struct {
	Server Server  `+"`"+`toml:"server"`+"`"+`
	Ptr    *Server `+"`"+`toml:"ptr"`+"`"+`
}

type Server struct {
	Host string `+"`"+`toml:"host"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "dup_test.go", `package duptable

import (
	"strings"
	"testing"
)

func TestDuplicateTableRejected(t *testing.T) {
	// A non-pointer struct field's table defined twice must error.
	dup := "[server]\nhost = \"a\"\n\n[server]\nhost = \"b\"\n"
	if _, err := DecodeConfig([]byte(dup)); err == nil {
		t.Fatal("expected error for duplicate [server], got nil")
	} else if !strings.Contains(err.Error(), "duplicate table") {
		t.Fatalf("error should mention the duplicate table, got: %v", err)
	}

	// A pointer struct field's table defined twice must error too.
	dupPtr := "[ptr]\nhost = \"a\"\n\n[ptr]\nhost = \"b\"\n"
	if _, err := DecodeConfig([]byte(dupPtr)); err == nil {
		t.Fatal("expected error for duplicate [ptr], got nil")
	}

	// False-positive guard: each table once decodes cleanly.
	valid := "[server]\nhost = \"x\"\n\n[ptr]\nhost = \"y\"\n"
	d, err := DecodeConfig([]byte(valid))
	if err != nil {
		t.Fatalf("valid doc errored: %v", err)
	}
	got := d.Data()
	if got.Server.Host != "x" || got.Ptr == nil || got.Ptr.Host != "y" {
		t.Fatalf("valid doc decoded wrong: %#v", *got)
	}
}
`)

	cmd := exec.Command("go", "test", "-run", "TestDuplicateTableRejected", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed:\n%s", output)
	}
}

// Regression for #90: a generated decoder must reject a repeated key within a
// table (per the TOML spec, "Defining a key multiple times is invalid") rather
// than silently keeping the last occurrence. The same key name in distinct
// scopes (separate array-table entries) must stay valid.
func TestIntegrationDuplicateKeyErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/dupkey",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package dupkey

//go:generate tommy generate
type Config struct {
	Name    string   `+"`"+`toml:"name"`+"`"+`
	Servers []Server `+"`"+`toml:"servers"`+"`"+`
}

type Server struct {
	Host string `+"`"+`toml:"host"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "dup_test.go", `package dupkey

import (
	"strings"
	"testing"
)

func TestDuplicateKeyRejected(t *testing.T) {
	// Duplicate top-level key must error, not silently keep the last value.
	if _, err := DecodeConfig([]byte("name = \"a\"\nname = \"b\"\n")); err == nil {
		t.Fatal("expected error for duplicate top-level key, got nil")
	} else if !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("error should mention the duplicate key, got: %v", err)
	}

	// Duplicate key inside an array-table entry must error too.
	dupEntry := "[[servers]]\nhost = \"x\"\nhost = \"y\"\n"
	if _, err := DecodeConfig([]byte(dupEntry)); err == nil {
		t.Fatal("expected error for duplicate key within an array-table entry, got nil")
	}

	// False-positive guard: the same key name in DISTINCT scopes (separate
	// array-table entries) is valid and must decode cleanly.
	valid := "name = \"app\"\n\n[[servers]]\nhost = \"x\"\n\n[[servers]]\nhost = \"y\"\n"
	d, err := DecodeConfig([]byte(valid))
	if err != nil {
		t.Fatalf("valid doc with repeated key name across entries errored: %v", err)
	}
	got := d.Data()
	if got.Name != "app" || len(got.Servers) != 2 || got.Servers[0].Host != "x" || got.Servers[1].Host != "y" {
		t.Fatalf("valid doc decoded wrong: %#v", *got)
	}
}
`)

	cmd := exec.Command("go", "test", "-run", "TestDuplicateKeyRejected", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

// Regression for #89: a struct whose fields are ALL array-of-tables must not
// emit a bare parent [table] header — the array-table headers already imply the
// parent. An empty inner slice yielded a content-less [section]; a non-empty one
// yielded a redundant [section] above the [[section.items]] entries.
func TestIntegrationAllArrayFieldStructNoEmptyTable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/allarray",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package allarray

//go:generate tommy generate
type Outer struct {
	Section Section `+"`"+`toml:"section"`+"`"+`
}

type Section struct {
	Items []Item `+"`"+`toml:"items"`+"`"+`
}

type Item struct {
	Name string `+"`"+`toml:"name"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "wire_test.go", `package allarray

import (
	"reflect"
	"strings"
	"testing"
)

func TestNoSpuriousSectionHeader(t *testing.T) {
	// Empty array-table field: encode must not emit a content-less [section].
	d, err := DecodeOuter([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	*d.Data() = Outer{Section: Section{Items: nil}}
	out, err := d.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "[section]") {
		t.Fatalf("empty all-array struct emitted a bare [section]:\n%q", string(out))
	}

	// Non-empty: the array-table headers carry the parent, so no bare [section];
	// and the document must still round-trip.
	d2, err := DecodeOuter([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	want := Outer{Section: Section{Items: []Item{{Name: "a"}, {Name: "b"}}}}
	*d2.Data() = want
	out2, err := d2.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out2), "[section]") {
		t.Fatalf("non-empty all-array struct emitted a bare [section]:\n%s", out2)
	}
	if !strings.Contains(string(out2), "[[section.items]]") {
		t.Fatalf("want [[section.items]] entries in output:\n%s", out2)
	}
	d3, err := DecodeOuter(out2)
	if err != nil {
		t.Fatalf("re-decode: %v\n%s", err, out2)
	}
	if !reflect.DeepEqual(*d3.Data(), want) {
		t.Fatalf("round-trip mismatch\nwant %#v\ngot  %#v\n%s", want, *d3.Data(), out2)
	}
}
`)

	cmd := exec.Command("go", "test", "-run", "TestNoSpuriousSectionHeader", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

func TestIntegrationSizedIntegers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/sizedints",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package sizedints

//go:generate tommy generate
type Config struct {
	A int8    `+"`"+`toml:"a"`+"`"+`
	B int16   `+"`"+`toml:"b"`+"`"+`
	C int32   `+"`"+`toml:"c"`+"`"+`
	D uint    `+"`"+`toml:"d"`+"`"+`
	E uint8   `+"`"+`toml:"e"`+"`"+`
	F uint16  `+"`"+`toml:"f"`+"`"+`
	G uint32  `+"`"+`toml:"g"`+"`"+`
	H float32 `+"`"+`toml:"h"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "sizedints_test.go", `package sizedints

import "testing"

func TestSizedIntRoundTrip(t *testing.T) {
	input := []byte(`+"`"+`a = 42
b = 1000
c = 100000
d = 99
e = 200
f = 50000
g = 3000000
h = 3.14
`+"`"+`)

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	d := doc.Data()
	if d.A != 42 {
		t.Fatalf("A = %d, want 42", d.A)
	}
	if d.B != 1000 {
		t.Fatalf("B = %d, want 1000", d.B)
	}
	if d.C != 100000 {
		t.Fatalf("C = %d, want 100000", d.C)
	}
	if d.D != 99 {
		t.Fatalf("D = %d, want 99", d.D)
	}
	if d.E != 200 {
		t.Fatalf("E = %d, want 200", d.E)
	}
	if d.F != 50000 {
		t.Fatalf("F = %d, want 50000", d.F)
	}
	if d.G != 3000000 {
		t.Fatalf("G = %d, want 3000000", d.G)
	}
	if d.H < 3.13 || d.H > 3.15 {
		t.Fatalf("H = %f, want ~3.14", d.H)
	}

	// Modify and re-encode.
	d.A = 10
	d.H = 2.5
	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if d2.A != 10 {
		t.Fatalf("re-decoded A = %d, want 10", d2.A)
	}
	if d2.H < 2.49 || d2.H > 2.51 {
		t.Fatalf("re-decoded H = %f, want ~2.5", d2.H)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

func TestIntegrationSlicePointerPrimitive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/sliceptr",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package sliceptr

//go:generate tommy generate
type Config struct {
	Names []*string `+"`"+`toml:"names"`+"`"+`
	Ports []*int    `+"`"+`toml:"ports"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "sliceptr_test.go", `package sliceptr

import "testing"

func TestSlicePointerPrimitiveRoundTrip(t *testing.T) {
	input := []byte(`+"`"+`names = ["alice", "bob"]
ports = [8080, 9090]
`+"`"+`)

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	d := doc.Data()
	if len(d.Names) != 2 {
		t.Fatalf("Names len = %d, want 2", len(d.Names))
	}
	if *d.Names[0] != "alice" {
		t.Fatalf("Names[0] = %q, want %q", *d.Names[0], "alice")
	}
	if *d.Names[1] != "bob" {
		t.Fatalf("Names[1] = %q, want %q", *d.Names[1], "bob")
	}
	if len(d.Ports) != 2 {
		t.Fatalf("Ports len = %d, want 2", len(d.Ports))
	}
	if *d.Ports[0] != 8080 {
		t.Fatalf("Ports[0] = %d, want 8080", *d.Ports[0])
	}

	// Modify and re-encode.
	newName := "charlie"
	d.Names[0] = &newName
	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if *d2.Names[0] != "charlie" {
		t.Fatalf("re-decoded Names[0] = %q, want %q", *d2.Names[0], "charlie")
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

func TestIntegrationMapStringPointerStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/mapptr",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package mapptr

//go:generate tommy generate
type Config struct {
	Servers map[string]*Server `+"`"+`toml:"servers"`+"`"+`
}

type Server struct {
	Host string `+"`"+`toml:"host"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "mapptr_test.go", `package mapptr

import "testing"

func TestMapPointerStructRoundTrip(t *testing.T) {
	input := []byte(`+"`"+`[servers.prod]
host = "prod.example.com"
port = 443

[servers.dev]
host = "dev.example.com"
port = 8080
`+"`"+`)

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	d := doc.Data()
	if len(d.Servers) != 2 {
		t.Fatalf("Servers len = %d, want 2", len(d.Servers))
	}
	if d.Servers["prod"] == nil {
		t.Fatal("Servers[prod] is nil")
	}
	if d.Servers["prod"].Host != "prod.example.com" {
		t.Fatalf("Servers[prod].Host = %q, want %q", d.Servers["prod"].Host, "prod.example.com")
	}
	if d.Servers["prod"].Port != 443 {
		t.Fatalf("Servers[prod].Port = %d, want 443", d.Servers["prod"].Port)
	}
	if d.Servers["dev"].Port != 8080 {
		t.Fatalf("Servers[dev].Port = %d, want 8080", d.Servers["dev"].Port)
	}

	// Modify and re-encode.
	d.Servers["prod"].Port = 8443
	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if d2.Servers["prod"].Port != 8443 {
		t.Fatalf("re-decoded Servers[prod].Port = %d, want 8443", d2.Servers["prod"].Port)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

func TestIntegrationPointerStructWithSliceStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/ptrslice",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	// Reproducer for #52: *Struct containing []Struct generates code
	// that references undefined `d` receiver in DecodeInto/EncodeFrom.
	writeFixture(t, dir, "config.go", `package ptrslice

//go:generate tommy generate
type Config struct {
	Exec    *ExecConfig    `+"`"+`toml:"exec"`+"`"+`
	Servers []ServerConfig `+"`"+`toml:"servers"`+"`"+`
}

type ExecConfig struct {
	Allow []ExecRule `+"`"+`toml:"allow"`+"`"+`
	Deny  []ExecRule `+"`"+`toml:"deny"`+"`"+`
}

type ExecRule struct {
	Binary string            `+"`"+`toml:"binary"`+"`"+`
	Args   []string          `+"`"+`toml:"args"`+"`"+`
	Cwd    []string          `+"`"+`toml:"cwd"`+"`"+`
	Env    map[string]string `+"`"+`toml:"env"`+"`"+`
}

type ServerConfig struct {
	Name string `+"`"+`toml:"name"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "ptrslice_test.go", `package ptrslice

import "testing"

func TestPointerStructWithSliceStruct(t *testing.T) {
	input := []byte(`+"`"+`[[servers]]
name = "grit"

[exec]

[[exec.allow]]
binary = "go"
args = ["build"]
cwd = ["/tmp"]

[[exec.deny]]
binary = "rm"
`+"`"+`)

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	d := doc.Data()
	if d.Exec == nil {
		t.Fatal("Exec is nil")
	}
	if len(d.Exec.Allow) != 1 {
		t.Fatalf("Allow len = %d, want 1", len(d.Exec.Allow))
	}
	if d.Exec.Allow[0].Binary != "go" {
		t.Fatalf("Allow[0].Binary = %q, want %q", d.Exec.Allow[0].Binary, "go")
	}
	if len(d.Exec.Deny) != 1 {
		t.Fatalf("Deny len = %d, want 1", len(d.Exec.Deny))
	}
	if len(d.Servers) != 1 {
		t.Fatalf("Servers len = %d, want 1", len(d.Servers))
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if d2.Exec.Allow[0].Binary != "go" {
		t.Fatalf("re-decoded Allow[0].Binary = %q, want %q", d2.Exec.Allow[0].Binary, "go")
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

// Reproducer for #55: when *Struct contains only []Struct fields and the TOML
// uses implicit parent tables (no explicit [exec] header), FindTableInContainer
// returns nil and the entire block is skipped.
func TestIntegrationImplicitParentTable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/implicit",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package implicit

//go:generate tommy generate
type Config struct {
	Exec *ExecConfig `+"`"+`toml:"exec"`+"`"+`
}

type ExecConfig struct {
	Allow []ExecRule `+"`"+`toml:"allow"`+"`"+`
	Deny  []ExecRule `+"`"+`toml:"deny"`+"`"+`
}

type ExecRule struct {
	Binary string `+"`"+`toml:"binary"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// No explicit [exec] header — only [[exec.allow]] and [[exec.deny]].
	// The parent "exec" table is implicit.
	writeFixture(t, dir, "implicit_test.go", `package implicit

import "testing"

func TestImplicitParentTable(t *testing.T) {
	input := []byte(`+"`"+`[[exec.allow]]
binary = "git"

[[exec.allow]]
binary = "go"

[[exec.deny]]
binary = "sudo"
`+"`"+`)

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	d := doc.Data()
	if d.Exec == nil {
		t.Fatal("Exec is nil — implicit parent table not detected")
	}
	if len(d.Exec.Allow) != 2 {
		t.Fatalf("Allow len = %d, want 2", len(d.Exec.Allow))
	}
	if d.Exec.Allow[0].Binary != "git" {
		t.Fatalf("Allow[0].Binary = %q, want %q", d.Exec.Allow[0].Binary, "git")
	}
	if d.Exec.Allow[1].Binary != "go" {
		t.Fatalf("Allow[1].Binary = %q, want %q", d.Exec.Allow[1].Binary, "go")
	}
	if len(d.Exec.Deny) != 1 {
		t.Fatalf("Deny len = %d, want 1", len(d.Exec.Deny))
	}
	if d.Exec.Deny[0].Binary != "sudo" {
		t.Fatalf("Deny[0].Binary = %q, want %q", d.Exec.Deny[0].Binary, "sudo")
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if d2.Exec == nil {
		t.Fatal("re-decoded Exec is nil")
	}
	if len(d2.Exec.Allow) != 2 {
		t.Fatalf("re-decoded Allow len = %d, want 2", len(d2.Exec.Allow))
	}
	if d2.Exec.Allow[0].Binary != "git" {
		t.Fatalf("re-decoded Allow[0].Binary = %q, want %q", d2.Exec.Allow[0].Binary, "git")
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

func TestIntegrationArrayOfTables(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/aot",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package aot

//go:generate tommy generate
type Config struct {
	Title   string   `+"`"+`toml:"title"`+"`"+`
	Servers []Server `+"`"+`toml:"servers"`+"`"+`
}

type Server struct {
	Name    string `+"`"+`toml:"name"`+"`"+`
	Command string `+"`"+`toml:"command"`+"`"+`
}
`)

	// Run code generation.
	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Verify generated file exists.
	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "aot_test.go", `package aot

import "testing"

func TestAOTRoundTrip(t *testing.T) {
	input := []byte("# my servers\ntitle = \"config\"\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux serve\"\n")
	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if len(cfg.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfg.Servers))
	}

	cfg.Servers[1].Command = "lux mcp"
	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "# my servers\ntitle = \"config\"\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux mcp\"\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}
`)

	// Run go test in the temp dir.
	cmd := exec.Command("go", "test", "-v", "-run", "TestAOTRoundTrip", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated test failed:\n%s", output)
	}
}

func TestIntegrationCustomAndPointerTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/custom",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "types.go", `package main

import (
	"fmt"
	"strings"
)

type Command struct {
	parts []string
}

func (c *Command) UnmarshalTOML(data any) error {
	switch v := data.(type) {
	case string:
		c.parts = strings.Fields(v)
		return nil
	case []any:
		c.parts = make([]string, len(v))
		for i, elem := range v {
			s, ok := elem.(string)
			if !ok {
				return fmt.Errorf("element %d not a string", i)
			}
			c.parts[i] = s
		}
		return nil
	default:
		return fmt.Errorf("unsupported type %T", data)
	}
}

func (c Command) MarshalTOML() (any, error) {
	return strings.Join(c.parts, " "), nil
}

func (c Command) String() string {
	return strings.Join(c.parts, " ")
}

type AnnotationFilter struct {
	ReadOnlyHint *bool `+"`"+`toml:"readOnlyHint"`+"`"+`
}
`)

	writeFixture(t, dir, "config.go", `package main

//go:generate tommy generate
type Config struct {
	Servers []ServerConfig `+"`"+`toml:"servers"`+"`"+`
}

type ServerConfig struct {
	Name        string            `+"`"+`toml:"name"`+"`"+`
	Command     Command           `+"`"+`toml:"command"`+"`"+`
	Annotations *AnnotationFilter `+"`"+`toml:"annotations"`+"`"+`
}
`)

	// Run code generation.
	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Verify generated file exists.
	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "main_test.go", `package main

import "testing"

func TestCustomTypes(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n")
	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if len(cfg.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(cfg.Servers))
	}
	if cfg.Servers[0].Command.String() != "grit mcp" {
		t.Fatalf("expected command 'grit mcp', got %q", cfg.Servers[0].Command.String())
	}
	if cfg.Servers[0].Annotations != nil {
		t.Fatal("expected nil annotations")
	}
}
`)

	// Run go test in the temp dir.
	cmd := exec.Command("go", "test", "-v", "-run", "TestCustomTypes", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationMoxyMigration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/moxy",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package main

import (
	"fmt"
	"strings"
)

//go:generate tommy generate
type Config struct {
	Servers []ServerConfig `+"`"+`toml:"servers"`+"`"+`
}

type ServerConfig struct {
	Name                  string            `+"`"+`toml:"name"`+"`"+`
	Command               Command           `+"`"+`toml:"command"`+"`"+`
	Annotations           *AnnotationFilter `+"`"+`toml:"annotations"`+"`"+`
	Paginate              bool              `+"`"+`toml:"paginate"`+"`"+`
	GenerateResourceTools *bool             `+"`"+`toml:"generate-resource-tools"`+"`"+`
}

type Command struct {
	parts []string
}

func (c *Command) UnmarshalTOML(data any) error {
	switch v := data.(type) {
	case string:
		c.parts = strings.Fields(v)
		if len(c.parts) == 0 {
			return fmt.Errorf("command string is empty")
		}
		return nil
	case []any:
		c.parts = make([]string, len(v))
		for i, elem := range v {
			s, ok := elem.(string)
			if !ok {
				return fmt.Errorf("command array element %d is not a string", i)
			}
			c.parts[i] = s
		}
		if len(c.parts) == 0 {
			return fmt.Errorf("command array is empty")
		}
		return nil
	default:
		return fmt.Errorf("command must be a string or array of strings")
	}
}

func (c Command) MarshalTOML() (any, error) {
	return strings.Join(c.parts, " "), nil
}

func (c Command) String() string {
	return strings.Join(c.parts, " ")
}

func MakeCommand(parts ...string) Command {
	return Command{parts: parts}
}

type AnnotationFilter struct {
	ReadOnlyHint    *bool `+"`"+`toml:"readOnlyHint"`+"`"+`
	DestructiveHint *bool `+"`"+`toml:"destructiveHint"`+"`"+`
	IdempotentHint  *bool `+"`"+`toml:"idempotentHint"`+"`"+`
	OpenWorldHint   *bool `+"`"+`toml:"openWorldHint"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "main.go", "package main\n\nfunc main() {}\n")

	writeFixture(t, dir, "moxy_test.go", `package main

import "testing"

func TestDecodeBasicMoxyfile(t *testing.T) {
	input := []byte("#  MCP server configuration\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux serve --verbose\"\npaginate = true\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if len(cfg.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfg.Servers))
	}
	if cfg.Servers[0].Name != "grit" {
		t.Fatalf("expected Name 'grit', got %q", cfg.Servers[0].Name)
	}
	if cfg.Servers[0].Command.String() != "grit mcp" {
		t.Fatalf("expected Command 'grit mcp', got %q", cfg.Servers[0].Command.String())
	}
	if cfg.Servers[0].Annotations != nil {
		t.Fatal("expected nil Annotations for grit")
	}
	if cfg.Servers[0].Paginate != false {
		t.Fatal("expected Paginate false for grit")
	}
	if cfg.Servers[1].Name != "lux" {
		t.Fatalf("expected Name 'lux', got %q", cfg.Servers[1].Name)
	}
	if cfg.Servers[1].Command.String() != "lux serve --verbose" {
		t.Fatalf("expected Command 'lux serve --verbose', got %q", cfg.Servers[1].Command.String())
	}
	if cfg.Servers[1].Paginate != true {
		t.Fatal("expected Paginate true for lux")
	}
}

func TestDecodeWithAnnotationSubTable(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[servers.annotations]\nreadOnlyHint = true\ndestructiveHint = false\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Servers[0].Annotations == nil {
		t.Fatal("expected non-nil Annotations")
	}
	if cfg.Servers[0].Annotations.ReadOnlyHint == nil || *cfg.Servers[0].Annotations.ReadOnlyHint != true {
		t.Fatal("expected ReadOnlyHint true")
	}
	if cfg.Servers[0].Annotations.DestructiveHint == nil || *cfg.Servers[0].Annotations.DestructiveHint != false {
		t.Fatal("expected DestructiveHint false")
	}
	if cfg.Servers[0].Annotations.IdempotentHint != nil {
		t.Fatal("expected IdempotentHint nil (not present)")
	}
}

func TestRoundTripPreservesComments(t *testing.T) {
	input := []byte("# MCP server configuration\n\n[[servers]]\nname = \"grit\"  # the git server\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux serve\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	cfg.Servers[1].Command = MakeCommand("lux", "mcp")

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "# MCP server configuration\n\n[[servers]]\nname = \"grit\"  # the git server\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux mcp\"\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}

func TestRoundTripGenerateResourceTools(t *testing.T) {
	input := []byte("[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\ngenerate-resource-tools = true\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Servers[0].GenerateResourceTools == nil {
		t.Fatal("expected non-nil GenerateResourceTools")
	}
	if *cfg.Servers[0].GenerateResourceTools != true {
		t.Fatal("expected GenerateResourceTools true")
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(input) {
		t.Fatalf("expected byte-identical round-trip.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestWriteServerEquivalent(t *testing.T) {
	input := []byte("# my servers\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	cfg.Servers = append(cfg.Servers, ServerConfig{
		Name:    "lux",
		Command: MakeCommand("lux", "serve"),
	})

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "# my servers\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux serve\"\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}

func TestDecodeNoAnnotationSubTable(t *testing.T) {
	// Flat annotation keys in the server table should be picked up as a fallback.
	input := []byte("[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\nreadOnlyHint = true\ndestructiveHint = false\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Servers[0].Annotations == nil {
		t.Fatal("expected non-nil Annotations from flat keys")
	}
	if cfg.Servers[0].Annotations.ReadOnlyHint == nil || *cfg.Servers[0].Annotations.ReadOnlyHint != true {
		t.Fatal("expected ReadOnlyHint true")
	}
	if cfg.Servers[0].Annotations.DestructiveHint == nil || *cfg.Servers[0].Annotations.DestructiveHint != false {
		t.Fatal("expected DestructiveHint false")
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationFlatKeyFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/flatkey",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package flatkey

import (
	"fmt"
	"strings"
)

//go:generate tommy generate
type Config struct {
	Servers []ServerConfig `+"`"+`toml:"servers"`+"`"+`
}

type ServerConfig struct {
	Name        string            `+"`"+`toml:"name"`+"`"+`
	Command     Command           `+"`"+`toml:"command"`+"`"+`
	Annotations *AnnotationFilter `+"`"+`toml:"annotations"`+"`"+`
}

type Command struct {
	parts []string
}

func (c *Command) UnmarshalTOML(data any) error {
	switch v := data.(type) {
	case string:
		c.parts = strings.Fields(v)
		return nil
	case []any:
		c.parts = make([]string, len(v))
		for i, elem := range v {
			s, ok := elem.(string)
			if !ok {
				return fmt.Errorf("element %d not a string", i)
			}
			c.parts[i] = s
		}
		return nil
	default:
		return fmt.Errorf("unsupported type %T", data)
	}
}

func (c Command) MarshalTOML() (any, error) {
	return strings.Join(c.parts, " "), nil
}

func (c Command) String() string {
	return strings.Join(c.parts, " ")
}

type AnnotationFilter struct {
	ReadOnlyHint    *bool `+"`"+`toml:"readOnlyHint"`+"`"+`
	DestructiveHint *bool `+"`"+`toml:"destructiveHint"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "flatkey_test.go", `package flatkey

import "testing"

func TestFlatKeysDecoded(t *testing.T) {
	// Flat annotation keys directly in the server table (no [servers.annotations] sub-table).
	// The codegen should fall back to reading these from the parent container.
	input := []byte("[[servers]]\nname = \"lux\"\ncommand = \"lux\"\nreadOnlyHint = true\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Servers[0].Annotations == nil {
		t.Fatal("expected non-nil Annotations from flat keys, got nil")
	}
	if cfg.Servers[0].Annotations.ReadOnlyHint == nil || *cfg.Servers[0].Annotations.ReadOnlyHint != true {
		t.Fatal("expected ReadOnlyHint true")
	}
	if cfg.Servers[0].Annotations.DestructiveHint != nil {
		t.Fatal("expected DestructiveHint nil (not present)")
	}
}

func TestSubTableTakesPrecedence(t *testing.T) {
	// When both flat keys and a sub-table exist, the sub-table should win.
	input := []byte("[[servers]]\nname = \"lux\"\ncommand = \"lux\"\nreadOnlyHint = false\n\n[servers.annotations]\nreadOnlyHint = true\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Servers[0].Annotations == nil {
		t.Fatal("expected non-nil Annotations")
	}
	if cfg.Servers[0].Annotations.ReadOnlyHint == nil || *cfg.Servers[0].Annotations.ReadOnlyHint != true {
		t.Fatal("expected ReadOnlyHint true from sub-table, not false from flat key")
	}
}

func TestNoFlatKeysNoSubTable(t *testing.T) {
	// No annotation keys at all — Annotations should remain nil.
	input := []byte("[[servers]]\nname = \"lux\"\ncommand = \"lux\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Servers[0].Annotations != nil {
		t.Fatal("expected nil Annotations when no keys present")
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationMapStringString(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/sweatfile",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package sweatfile

//go:generate tommy generate
type Sweatfile struct {
	SystemPrompt *string           `+"`"+`toml:"system-prompt"`+"`"+`
	GitExcludes  []string          `+"`"+`toml:"git-excludes"`+"`"+`
	Env          map[string]string `+"`"+`toml:"env"`+"`"+`
	Hooks        *Hooks            `+"`"+`toml:"hooks"`+"`"+`
}

type Hooks struct {
	Create               *string `+"`"+`toml:"create"`+"`"+`
	Stop                 *string `+"`"+`toml:"stop"`+"`"+`
	DisallowMainWorktree *bool   `+"`"+`toml:"disallow-main-worktree"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "sweatfile_test.go", `package sweatfile

import "testing"

func TestDecodeSweatfile(t *testing.T) {
	input := []byte("system-prompt = \"be helpful\"\ngit-excludes = [\".claude/\", \".direnv/\"]\n\n[env]\nFOO = \"bar\"\nBAZ = \"qux\"\n\n[hooks]\ncreate = \"npm install\"\nstop = \"just test\"\ndisallow-main-worktree = true\n")

	doc, err := DecodeSweatfile(input)
	if err != nil {
		t.Fatal(err)
	}

	sf := doc.Data()
	if sf.SystemPrompt == nil || *sf.SystemPrompt != "be helpful" {
		t.Fatal("expected system-prompt 'be helpful'")
	}
	if len(sf.GitExcludes) != 2 {
		t.Fatalf("expected 2 git-excludes, got %d", len(sf.GitExcludes))
	}
	if sf.Env == nil {
		t.Fatal("expected non-nil Env map")
	}
	if sf.Env["FOO"] != "bar" {
		t.Fatalf("expected Env[FOO] = 'bar', got %q", sf.Env["FOO"])
	}
	if sf.Env["BAZ"] != "qux" {
		t.Fatalf("expected Env[BAZ] = 'qux', got %q", sf.Env["BAZ"])
	}
	if sf.Hooks == nil {
		t.Fatal("expected non-nil Hooks")
	}
	if sf.Hooks.Create == nil || *sf.Hooks.Create != "npm install" {
		t.Fatal("expected hooks.create 'npm install'")
	}
	if sf.Hooks.DisallowMainWorktree == nil || *sf.Hooks.DisallowMainWorktree != true {
		t.Fatal("expected hooks.disallow-main-worktree true")
	}
}

func TestRoundTripSweatfile(t *testing.T) {
	input := []byte("system-prompt = \"be helpful\"\ngit-excludes = [\".claude/\"]\n\n[env]\nFOO = \"bar\"\nBAZ = \"qux\"\n\n[hooks]\ncreate = \"npm install\"\n")

	doc, err := DecodeSweatfile(input)
	if err != nil {
		t.Fatal(err)
	}

	sf := doc.Data()
	sf.Env["NEW_KEY"] = "new_val"

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	// Re-decode to verify the new key survived.
	doc2, err := DecodeSweatfile(out)
	if err != nil {
		t.Fatal(err)
	}
	sf2 := doc2.Data()
	if sf2.Env["FOO"] != "bar" {
		t.Fatalf("expected FOO preserved, got %q", sf2.Env["FOO"])
	}
	if sf2.Env["NEW_KEY"] != "new_val" {
		t.Fatalf("expected NEW_KEY = 'new_val', got %q", sf2.Env["NEW_KEY"])
	}
}

func TestEmptyMapNotAppended(t *testing.T) {
	// No [env] section in input — should not appear in output.
	input := []byte("system-prompt = \"hi\"\ngit-excludes = [\".claude/\"]\n")

	doc, err := DecodeSweatfile(input)
	if err != nil {
		t.Fatal(err)
	}

	sf := doc.Data()
	if sf.Env != nil {
		t.Fatalf("expected nil Env, got %v", sf.Env)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestUndecodedEmpty(t *testing.T) {
	// All keys are known — Undecoded should return nothing.
	input := []byte("system-prompt = \"hi\"\ngit-excludes = [\".claude/\"]\n\n[hooks]\ncreate = \"npm install\"\n")

	doc, err := DecodeSweatfile(input)
	if err != nil {
		t.Fatal(err)
	}

	undecoded := doc.Undecoded()
	if len(undecoded) != 0 {
		t.Fatalf("expected no undecoded keys, got %v", undecoded)
	}
}

func TestUndecodedTypo(t *testing.T) {
	// "sytem-prompt" is a typo — should appear in Undecoded.
	input := []byte("sytem-prompt = \"hi\"\ngit-excludes = [\".claude/\"]\n")

	doc, err := DecodeSweatfile(input)
	if err != nil {
		t.Fatal(err)
	}

	undecoded := doc.Undecoded()
	if len(undecoded) != 1 {
		t.Fatalf("expected 1 undecoded key, got %v", undecoded)
	}
	if undecoded[0] != "sytem-prompt" {
		t.Fatalf("expected undecoded key 'sytem-prompt', got %q", undecoded[0])
	}
}

func TestUndecodedNestedTypo(t *testing.T) {
	// "creat" is a typo inside [hooks] — should appear as "hooks.creat".
	input := []byte("system-prompt = \"hi\"\n\n[hooks]\ncreat = \"npm install\"\n")

	doc, err := DecodeSweatfile(input)
	if err != nil {
		t.Fatal(err)
	}

	undecoded := doc.Undecoded()
	if len(undecoded) != 1 {
		t.Fatalf("expected 1 undecoded key, got %v", undecoded)
	}
	if undecoded[0] != "hooks.creat" {
		t.Fatalf("expected undecoded key 'hooks.creat', got %q", undecoded[0])
	}
}

func TestUndecodedMapKeysAllConsumed(t *testing.T) {
	// [env] is a map[string]string — all keys under it should be consumed.
	input := []byte("system-prompt = \"hi\"\n\n[env]\nFOO = \"bar\"\nANYTHING = \"goes\"\n")

	doc, err := DecodeSweatfile(input)
	if err != nil {
		t.Fatal(err)
	}

	undecoded := doc.Undecoded()
	if len(undecoded) != 0 {
		t.Fatalf("expected no undecoded keys (map accepts all), got %v", undecoded)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationPointerStructEncode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/ptrstruct",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package ptrstruct

//go:generate tommy generate
type Sweatfile struct {
	SystemPrompt *string `+"`"+`toml:"system-prompt"`+"`"+`
	Hooks        *Hooks  `+"`"+`toml:"hooks"`+"`"+`
}

type Hooks struct {
	Create               *string `+"`"+`toml:"create"`+"`"+`
	Stop                 *string `+"`"+`toml:"stop"`+"`"+`
	DisallowMainWorktree *bool   `+"`"+`toml:"disallow-main-worktree"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "ptrstruct_test.go", `package ptrstruct

import (
	"strings"
	"testing"
)

func TestModifyPointerStructField(t *testing.T) {
	input := []byte("system-prompt = \"be helpful\"\n\n[hooks]\ncreate = \"npm install\"\nstop = \"just test\"\ndisallow-main-worktree = true\n")

	doc, err := DecodeSweatfile(input)
	if err != nil {
		t.Fatal(err)
	}

	sf := doc.Data()
	newCreate := "composer install"
	sf.Hooks.Create = &newCreate

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	result := string(out)
	if !strings.Contains(result, "composer install") {
		t.Fatalf("expected modified create hook in output, got:\n%s", result)
	}
	if strings.Contains(result, "npm install") {
		t.Fatalf("expected old create hook replaced, got:\n%s", result)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationTomlDash(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/tomldash",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package tomldash

//go:generate tommy generate
type Config struct {
	Name     string `+"`"+`toml:"name"`+"`"+`
	Internal string `+"`"+`toml:"-"`+"`"+`
	Port     int    `+"`"+`toml:"port"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "dash_test.go", `package tomldash

import (
	"strings"
	"testing"
)

func TestDashFieldExcluded(t *testing.T) {
	input := []byte("name = \"app\"\nport = 8080\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	doc.Data().Internal = "should not appear"

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(out), "Internal") || strings.Contains(string(out), "should not appear") {
		t.Fatalf("toml:\"-\" field leaked into output:\n%s", string(out))
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationOmitempty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/omitempty",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package omitempty

//go:generate tommy generate
type Config struct {
	Name  string   `+"`"+`toml:"name"`+"`"+`
	Tags  []string `+"`"+`toml:"tags,omitempty"`+"`"+`
	Hooks *Hooks   `+"`"+`toml:"hooks,omitempty"`+"`"+`
}

type Hooks struct {
	Create *string `+"`"+`toml:"create"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "omitempty_test.go", `package omitempty

import (
	"strings"
	"testing"
)

func TestNilSliceOmitemptyNotWritten(t *testing.T) {
	// tags is nil and omitempty — should not appear in output.
	input := []byte("name = \"app\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Data().Tags != nil {
		t.Fatal("expected nil Tags")
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestNonEmptySliceOmitemptyWritten(t *testing.T) {
	// tags is set — should appear in output.
	input := []byte("name = \"app\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	doc.Data().Tags = []string{"v1", "v2"}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(out), "tags") {
		t.Fatalf("expected tags in output, got:\n%s", string(out))
	}
}

func TestNilPointerStructOmitemptyNotWritten(t *testing.T) {
	// hooks is nil and omitempty — should not appear in output.
	input := []byte("name = \"app\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Data().Hooks != nil {
		t.Fatal("expected nil Hooks")
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(out), "hooks") || strings.Contains(string(out), "create") {
		t.Fatalf("nil omitempty pointer struct leaked into output:\n%s", string(out))
	}
}

func TestExplicitSliceOmitemptyPreserved(t *testing.T) {
	// tags exists in TOML — should survive round-trip.
	input := []byte("name = \"app\"\ntags = [\"v1\"]\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Regression test for #82: an empty slice field WITHOUT omitempty must encode as
// the explicit-empty form `key = []`, not be omitted. A field without omitempty
// always serializes; dropping it is a wire-format regression invisible to
// round-trip tests (empty → omitted → decodes back to empty). Covers both the
// primitive slice and TextMarshaler slice encode paths (madder's Encryption
// []markl.Id is the latter).
func TestIntegrationEmptySliceWithoutOmitempty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/emptyslice",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package emptyslice

//go:generate tommy generate
type Config struct {
	Name  string `+"`"+`toml:"name"`+"`"+`
	Tags  []string `+"`"+`toml:"tags"`+"`"+`
	Marks []Mark   `+"`"+`toml:"marks"`+"`"+`
}

type Mark struct{ v string }

func (m Mark) MarshalText() ([]byte, error)  { return []byte(m.v), nil }
func (m *Mark) UnmarshalText(b []byte) error { m.v = string(b); return nil }
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "emptyslice_test.go", `package emptyslice

import (
	"strings"
	"testing"
)

// Faithful nil/empty (#21): a non-omitempty slice that was never present in the
// source (nil after decode) is OMITTED on encode — distinct from an explicit
// empty slice, which still emits "key = []" (see the companion test below). This
// is the present-vs-absent distinction TOML's inline arrays can carry.
func TestNilNonOmitemptySlicesOmitted(t *testing.T) {
	doc, err := DecodeConfig([]byte("name = \"app\"\n"))
	if err != nil {
		t.Fatal(err)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "tags") {
		t.Fatalf("nil primitive slice should be omitted, got tags in:\n%s", out)
	}
	if strings.Contains(string(out), "marks") {
		t.Fatalf("nil text-marshaler slice should be omitted, got marks in:\n%s", out)
	}
}

// Explicitly setting an empty (non-nil) slice must also emit "key = []".
func TestExplicitEmptyNonOmitemptySlicesEmitted(t *testing.T) {
	doc, err := DecodeConfig([]byte("name = \"app\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	doc.Data().Tags = []string{}
	doc.Data().Marks = []Mark{}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "tags = []") {
		t.Fatalf("expected \"tags = []\", got:\n%s", out)
	}
	if !strings.Contains(string(out), "marks = []") {
		t.Fatalf("expected \"marks = []\", got:\n%s", out)
	}
}

// Faithful nil/empty (#21): nil and empty primitive slices stay distinct across
// a full encode→decode round-trip — nil omits and re-decodes nil; empty emits
// "key = []" and re-decodes a non-nil empty slice.
func TestNilEmptyPrimitiveSliceRoundTrip(t *testing.T) {
	rt := func(set func(c *Config)) Config {
		doc, err := DecodeConfig([]byte("name = \"app\"\n"))
		if err != nil { t.Fatal(err) }
		set(doc.Data())
		out, err := doc.Encode()
		if err != nil { t.Fatal(err) }
		d2, err := DecodeConfig(out)
		if err != nil { t.Fatalf("re-decode: %v\n%s", err, out) }
		return *d2.Data()
	}

	nilRT := rt(func(c *Config) { c.Tags = nil })
	if nilRT.Tags != nil {
		t.Fatalf("nil Tags should round-trip to nil, got %#v", nilRT.Tags)
	}

	emptyRT := rt(func(c *Config) { c.Tags = []string{} })
	if emptyRT.Tags == nil || len(emptyRT.Tags) != 0 {
		t.Fatalf("empty Tags should round-trip to non-nil empty, got %#v", emptyRT.Tags)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationZeroValuePrimitiveSkip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/zeroval",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package zeroval

//go:generate tommy generate
type Config struct {
	Name    string `+"`"+`toml:"name"`+"`"+`
	Port    int    `+"`"+`toml:"port"`+"`"+`
	Enabled bool   `+"`"+`toml:"enabled"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "zeroval_test.go", `package zeroval

import "testing"

func TestZeroValueNotAppended(t *testing.T) {
	// Only name and port are in the TOML — enabled (bool, zero = false)
	// should NOT be appended on encode.
	input := []byte("name = \"app\"\nport = 8080\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Enabled != false {
		t.Fatal("expected Enabled false")
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestZeroValuePreservedWhenExplicit(t *testing.T) {
	// enabled = false is explicit in the TOML — it should be preserved.
	input := []byte("name = \"app\"\nport = 8080\nenabled = false\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationArrayOfTablesAppend(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/aotappend",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package aotappend

//go:generate tommy generate
type Config struct {
	Servers []Server `+"`"+`toml:"servers"`+"`"+`
}

type Server struct {
	Name    string `+"`"+`toml:"name"`+"`"+`
	Command string `+"`"+`toml:"command"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "append_test.go", `package aotappend

import "testing"

func TestAppendNewEntry(t *testing.T) {
	// Start with one server, append a second, encode.
	input := []byte("# my servers\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	cfg.Servers = append(cfg.Servers, Server{
		Name:    "lux",
		Command: "lux serve",
	})

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "# my servers\n\n[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux serve\"\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}

func TestAppendPreservesExisting(t *testing.T) {
	// Modify existing entry + append new one.
	input := []byte("[[servers]]\nname = \"grit\"\ncommand = \"grit mcp\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	cfg.Servers[0].Command = "grit serve"
	cfg.Servers = append(cfg.Servers, Server{
		Name:    "lux",
		Command: "lux mcp",
	})

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "[[servers]]\nname = \"grit\"\ncommand = \"grit serve\"\n\n[[servers]]\nname = \"lux\"\ncommand = \"lux mcp\"\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestOmitemptyPrimitiveZeroDropped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/omitprim",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package omitprim

//go:generate tommy generate
type Config struct {
	Name    string `+"`"+`toml:"name"`+"`"+`
	Verbose bool   `+"`"+`toml:"verbose,omitempty"`+"`"+`
	Retries int    `+"`"+`toml:"retries,omitempty"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "omitprim_test.go", `package omitprim

import (
	"strings"
	"testing"
)

func TestOmitemptyPrimitiveZeroNotWritten(t *testing.T) {
	// verbose and retries are in TOML but set to zero values.
	// With omitempty, setting them to zero should drop them on encode.
	input := []byte("name = \"app\"\nverbose = true\nretries = 3\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	// Set to zero values — omitempty should cause them to be dropped.
	doc.Data().Verbose = false
	doc.Data().Retries = 0

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(out), "verbose") {
		t.Fatalf("omitempty zero bool should be dropped, got:\n%s", string(out))
	}
	if strings.Contains(string(out), "retries") {
		t.Fatalf("omitempty zero int should be dropped, got:\n%s", string(out))
	}
}

func TestOmitemptyPrimitiveNonZeroPreserved(t *testing.T) {
	// Non-zero values with omitempty should be written normally.
	input := []byte("name = \"app\"\nverbose = true\nretries = 3\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}
`)

	cmd2 := exec.Command("go", "test", "-v", "./...")
	cmd2.Dir = dir
	cmd2.Env = append(os.Environ(), testGoEnv()...)
	output2, err := cmd2.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output2)
	}
}

func TestIntegrationMultiline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/multiline",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package multiline

//go:generate tommy generate
type Config struct {
	Name   string `+"`"+`toml:"name"`+"`"+`
	Script string `+"`"+`toml:"script,multiline"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "multiline_test.go", `package multiline

import (
	"strings"
	"testing"
)

func TestMultilineRoundTrip(t *testing.T) {
	input := []byte("name = \"app\"\nscript = \"\"\"\necho hello\necho world\"\"\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Data().Script != "echo hello\necho world" {
		t.Fatalf("unexpected script value: %q", doc.Data().Script)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestMultilineEncodeNewValue(t *testing.T) {
	// Start with a basic string, set a multiline value — should encode as """.
	input := []byte("name = \"app\"\nscript = \"old\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	doc.Data().Script = "line1\nline2\nline3"

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	s := string(out)
	if !strings.Contains(s, "\"\"\"") {
		t.Fatalf("expected multiline basic string delimiters, got:\n%s", s)
	}
	if !strings.Contains(s, "line1\nline2\nline3") {
		t.Fatalf("expected literal newlines in multiline string, got:\n%s", s)
	}
}
`)

	cmdML := exec.Command("go", "test", "-v", "./...")
	cmdML.Dir = dir
	cmdML.Env = append(os.Environ(), testGoEnv()...)
	outputML, err := cmdML.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", outputML)
	}
}

func TestIntegrationTextMarshaler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/textmarshal",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "types.go", `package textmarshal

import "fmt"

// URI is a custom scalar type implementing encoding.TextMarshaler/TextUnmarshaler.
type URI struct {
	value string
}

func NewURI(s string) URI { return URI{value: s} }
func (u URI) String() string { return u.value }

func (u URI) MarshalText() ([]byte, error) {
	return []byte(u.value), nil
}

func (u *URI) UnmarshalText(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("empty URI")
	}
	u.value = string(data)
	return nil
}
`)

	writeFixture(t, dir, "config.go", `package textmarshal

//go:generate tommy generate
type Config struct {
	Name     string `+"`"+`toml:"name"`+"`"+`
	Homepage URI    `+"`"+`toml:"homepage"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "textmarshal_test.go", `package textmarshal

import "testing"

func TestTextMarshalerRoundTrip(t *testing.T) {
	input := []byte("name = \"myapp\"\nhomepage = \"https://example.com\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Data().Homepage.String() != "https://example.com" {
		t.Fatalf("expected homepage https://example.com, got %q", doc.Data().Homepage.String())
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestTextMarshalerModify(t *testing.T) {
	input := []byte("name = \"myapp\"\nhomepage = \"https://example.com\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	doc.Data().Homepage = NewURI("https://new.example.com")

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "name = \"myapp\"\nhomepage = \"https://new.example.com\"\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}
`)

	cmdTM := exec.Command("go", "test", "-v", "./...")
	cmdTM.Dir = dir
	cmdTM.Env = append(os.Environ(), testGoEnv()...)
	outputTM, err := cmdTM.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", outputTM)
	}
}

func TestIntegrationEmbeddedStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/embedded",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package embedded

type Common struct {
	Version string `+"`"+`toml:"version"`+"`"+`
	Debug   bool   `+"`"+`toml:"debug"`+"`"+`
}

//go:generate tommy generate
type Config struct {
	Common
	Name string `+"`"+`toml:"name"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "embedded_test.go", `package embedded

import "testing"

func TestEmbeddedStructRoundTrip(t *testing.T) {
	input := []byte("version = \"1.0\"\ndebug = true\nname = \"app\"\nport = 8080\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Version != "1.0" {
		t.Fatalf("expected version 1.0, got %q", cfg.Version)
	}
	if cfg.Debug != true {
		t.Fatal("expected debug true")
	}
	if cfg.Name != "app" {
		t.Fatalf("expected name app, got %q", cfg.Name)
	}
	if cfg.Port != 8080 {
		t.Fatalf("expected port 8080, got %d", cfg.Port)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestEmbeddedStructModify(t *testing.T) {
	input := []byte("version = \"1.0\"\ndebug = true\nname = \"app\"\nport = 8080\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	doc.Data().Version = "2.0"
	doc.Data().Debug = false
	doc.Data().Port = 9090

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "version = \"2.0\"\ndebug = false\nname = \"app\"\nport = 9090\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}
`)

	cmdEmb := exec.Command("go", "test", "-v", "./...")
	cmdEmb.Dir = dir
	cmdEmb.Env = append(os.Environ(), testGoEnv()...)
	outputEmb, err := cmdEmb.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", outputEmb)
	}
}

func TestIntegrationMapStringStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/mapstruct",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package mapstruct

//go:generate tommy generate
type Config struct {
	Name    string                `+"`"+`toml:"name"`+"`"+`
	Actions map[string]ActionSpec `+"`"+`toml:"actions"`+"`"+`
}

type ActionSpec struct {
	Command string `+"`"+`toml:"command"`+"`"+`
	Timeout int    `+"`"+`toml:"timeout"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "mapstruct_test.go", `package mapstruct

import "testing"

func TestMapStringStructRoundTrip(t *testing.T) {
	input := []byte("name = \"app\"\n\n[actions.build]\ncommand = \"make\"\ntimeout = 30\n\n[actions.test]\ncommand = \"go test\"\ntimeout = 60\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "app" {
		t.Fatalf("expected name app, got %q", cfg.Name)
	}
	if len(cfg.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(cfg.Actions))
	}
	build := cfg.Actions["build"]
	if build.Command != "make" || build.Timeout != 30 {
		t.Fatalf("unexpected build action: %+v", build)
	}
	test := cfg.Actions["test"]
	if test.Command != "go test" || test.Timeout != 60 {
		t.Fatalf("unexpected test action: %+v", test)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestMapStringStructModify(t *testing.T) {
	input := []byte("name = \"app\"\n\n[actions.build]\ncommand = \"make\"\ntimeout = 30\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	build := doc.Data().Actions["build"]
	build.Command = "cmake"
	build.Timeout = 45
	doc.Data().Actions["build"] = build

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "name = \"app\"\n\n[actions.build]\ncommand = \"cmake\"\ntimeout = 45\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}
`)

	cmdMS := exec.Command("go", "test", "-v", "./...")
	cmdMS.Dir = dir
	cmdMS.Env = append(os.Environ(), testGoEnv()...)
	outputMS, err := cmdMS.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", outputMS)
	}
}

func TestIntegrationNestedMapStringStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/nestedmap",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package nestedmap

//go:generate tommy generate
type Config struct {
	Outer OuterConfig `+"`"+`toml:"outer,omitempty"`+"`"+`
}

type OuterConfig struct {
	Name     string                 `+"`"+`toml:"name"`+"`"+`
	Mappings map[string]EntryConfig `+"`"+`toml:"mappings,omitempty"`+"`"+`
}

type EntryConfig struct {
	Value string `+"`"+`toml:"value"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "nestedmap_test.go", `package nestedmap

import (
	"strings"
	"testing"
)

func TestNestedMapDecode(t *testing.T) {
	input := []byte("[outer]\nname = \"test\"\n\n[outer.mappings.key1]\nvalue = \"hello\"\n\n[outer.mappings.key2]\nvalue = \"world\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Outer.Name != "test" {
		t.Fatalf("expected name test, got %q", cfg.Outer.Name)
	}
	if len(cfg.Outer.Mappings) != 2 {
		t.Fatalf("expected 2 mappings, got %d", len(cfg.Outer.Mappings))
	}
	if cfg.Outer.Mappings["key1"].Value != "hello" {
		t.Fatalf("expected key1=hello, got %q", cfg.Outer.Mappings["key1"].Value)
	}
	if cfg.Outer.Mappings["key2"].Value != "world" {
		t.Fatalf("expected key2=world, got %q", cfg.Outer.Mappings["key2"].Value)
	}
}

func TestNestedMapEncode(t *testing.T) {
	doc, err := DecodeConfig(nil)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	cfg.Outer.Name = "test"
	cfg.Outer.Mappings = map[string]EntryConfig{
		"key1": {Value: "hello"},
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	output := string(out)
	if !strings.Contains(output, "[outer.mappings.key1]") {
		t.Fatalf("expected [outer.mappings.key1] in output, got:\n%s", output)
	}
	if strings.Contains(output, "\n[mappings.key1]") {
		t.Fatalf("mappings should be nested under outer, got:\n%s", output)
	}
}

func TestNestedMapRoundTrip(t *testing.T) {
	input := []byte("[outer]\nname = \"test\"\n\n[outer.mappings.key1]\nvalue = \"hello\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationSliceTextMarshaler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/slicetm",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "types.go", `package slicetm

import "fmt"

type KeyID struct {
	value string
}

func NewKeyID(s string) KeyID { return KeyID{value: s} }
func (k KeyID) String() string { return k.value }

func (k KeyID) MarshalText() ([]byte, error) {
	return []byte(k.value), nil
}

func (k *KeyID) UnmarshalText(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("empty KeyID")
	}
	k.value = string(data)
	return nil
}
`)

	writeFixture(t, dir, "config.go", `package slicetm

//go:generate tommy generate
type Config struct {
	Name       string  `+"`"+`toml:"name"`+"`"+`
	Encryption []KeyID `+"`"+`toml:"encryption"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "slicetm_test.go", `package slicetm

import "testing"

func TestSliceTextMarshalerRoundTrip(t *testing.T) {
	input := []byte("name = \"vault\"\nencryption = [\"key-abc\", \"key-def\"]\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if len(cfg.Encryption) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(cfg.Encryption))
	}
	if cfg.Encryption[0].String() != "key-abc" {
		t.Fatalf("expected key-abc, got %q", cfg.Encryption[0].String())
	}
	if cfg.Encryption[1].String() != "key-def" {
		t.Fatalf("expected key-def, got %q", cfg.Encryption[1].String())
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestSliceTextMarshalerModify(t *testing.T) {
	input := []byte("name = \"vault\"\nencryption = [\"key-abc\"]\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	doc.Data().Encryption = append(doc.Data().Encryption, NewKeyID("key-xyz"))

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "name = \"vault\"\nencryption = [\"key-abc\", \"key-xyz\"]\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}
`)

	cmdST := exec.Command("go", "test", "-v", "./...")
	cmdST.Dir = dir
	cmdST.Env = append(os.Environ(), testGoEnv()...)
	outputST, err := cmdST.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", outputST)
	}
}

func TestIntegrationUint64(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/uint64test",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package uint64test

//go:generate tommy generate
type SelectorConfig struct {
	Type        string `+"`"+`toml:"type"`+"`"+`
	MinBlobSize uint64 `+"`"+`toml:"min-blob-size"`+"`"+`
	MaxBlobSize uint64 `+"`"+`toml:"max-blob-size"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "uint64_test.go", `package uint64test

import "testing"

func TestUint64RoundTrip(t *testing.T) {
	input := []byte("type = \"size\"\nmin-blob-size = 1024\nmax-blob-size = 10485760\n")

	doc, err := DecodeSelectorConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Type != "size" {
		t.Fatalf("expected type size, got %q", cfg.Type)
	}
	if cfg.MinBlobSize != 1024 {
		t.Fatalf("expected min 1024, got %d", cfg.MinBlobSize)
	}
	if cfg.MaxBlobSize != 10485760 {
		t.Fatalf("expected max 10485760, got %d", cfg.MaxBlobSize)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}

func TestUint64Modify(t *testing.T) {
	input := []byte("type = \"size\"\nmin-blob-size = 1024\nmax-blob-size = 10485760\n")

	doc, err := DecodeSelectorConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	doc.Data().MinBlobSize = 2048
	doc.Data().MaxBlobSize = 20971520

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	expected := "type = \"size\"\nmin-blob-size = 2048\nmax-blob-size = 20971520\n"
	if string(out) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(out))
	}
}
`)

	cmdU := exec.Command("go", "test", "-v", "./...")
	cmdU.Dir = dir
	cmdU.Env = append(os.Environ(), testGoEnv()...)
	outputU, err := cmdU.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", outputU)
	}
}

func TestIntegrationNestedArrayOfTablesInStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/nestedaot",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package nestedaot

//go:generate tommy generate
type Config struct {
	Outer OuterConfig `+"`"+`toml:"outer,omitempty"`+"`"+`
}

type OuterConfig struct {
	Name  string       `+"`"+`toml:"name"`+"`"+`
	Items []ItemConfig `+"`"+`toml:"items,omitempty"`+"`"+`
}

type ItemConfig struct {
	URL  string `+"`"+`toml:"url"`+"`"+`
	Type string `+"`"+`toml:"type"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "nested_test.go", `package nestedaot

import "testing"

func TestNestedArrayOfTablesInStructRoundTrip(t *testing.T) {
	input := []byte("[outer]\nname = \"test\"\n\n[[outer.items]]\nurl = \"https://a.com\"\ntype = \"task\"\n\n[[outer.items]]\nurl = \"https://b.com\"\ntype = \"chore\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Outer.Name != "test" {
		t.Fatalf("expected name test, got %q", cfg.Outer.Name)
	}
	if len(cfg.Outer.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(cfg.Outer.Items))
	}
	if cfg.Outer.Items[0].URL != "https://a.com" {
		t.Fatalf("expected url https://a.com, got %q", cfg.Outer.Items[0].URL)
	}
	if cfg.Outer.Items[1].Type != "chore" {
		t.Fatalf("expected type chore, got %q", cfg.Outer.Items[1].Type)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}
`)

	cmdN := exec.Command("go", "test", "-v", "./...")
	cmdN.Dir = dir
	cmdN.Env = append(os.Environ(), testGoEnv()...)
	outputN, err := cmdN.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", outputN)
	}
}

func TestIntegrationNestedArrayOfTablesInSlice(t *testing.T) {
	t.Skip("codegen for doubly-nested array-of-tables not yet implemented — see #6")
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/nestedaot",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package nestedaot

//go:generate tommy generate
type Config struct {
	Name    string   `+"`"+`toml:"name"`+"`"+`
	Servers []Server `+"`"+`toml:"servers"`+"`"+`
}

type Server struct {
	Host    string   `+"`"+`toml:"host"`+"`"+`
	Plugins []Plugin `+"`"+`toml:"plugins"`+"`"+`
}

type Plugin struct {
	Name    string `+"`"+`toml:"name"`+"`"+`
	Enabled bool   `+"`"+`toml:"enabled"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "nested_test.go", `package nestedaot

import "testing"

func TestNestedArrayOfTablesRoundTrip(t *testing.T) {
	input := []byte("name = \"app\"\n\n[[servers]]\nhost = \"alpha\"\n\n[[servers.plugins]]\nname = \"auth\"\nenabled = true\n\n[[servers.plugins]]\nname = \"cache\"\nenabled = false\n\n[[servers]]\nhost = \"beta\"\n\n[[servers.plugins]]\nname = \"log\"\nenabled = true\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "app" {
		t.Fatalf("expected name app, got %q", cfg.Name)
	}
	if len(cfg.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfg.Servers))
	}

	// First server: alpha with 2 plugins
	if cfg.Servers[0].Host != "alpha" {
		t.Fatalf("expected host alpha, got %q", cfg.Servers[0].Host)
	}
	if len(cfg.Servers[0].Plugins) != 2 {
		t.Fatalf("expected 2 plugins for alpha, got %d", len(cfg.Servers[0].Plugins))
	}
	if cfg.Servers[0].Plugins[0].Name != "auth" {
		t.Fatalf("expected plugin auth, got %q", cfg.Servers[0].Plugins[0].Name)
	}
	if cfg.Servers[0].Plugins[1].Name != "cache" {
		t.Fatalf("expected plugin cache, got %q", cfg.Servers[0].Plugins[1].Name)
	}

	// Second server: beta with 1 plugin
	if cfg.Servers[1].Host != "beta" {
		t.Fatalf("expected host beta, got %q", cfg.Servers[1].Host)
	}
	if len(cfg.Servers[1].Plugins) != 1 {
		t.Fatalf("expected 1 plugin for beta, got %d", len(cfg.Servers[1].Plugins))
	}
	if cfg.Servers[1].Plugins[0].Name != "log" {
		t.Fatalf("expected plugin log, got %q", cfg.Servers[1].Plugins[0].Name)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}
`)

	cmdN := exec.Command("go", "test", "-v", "./...")
	cmdN.Dir = dir
	cmdN.Env = append(os.Environ(), testGoEnv()...)
	outputN, err := cmdN.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", outputN)
	}
}

func TestIntegrationCrossPackageEmbedded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// Base package with a struct
	baseDir := filepath.Join(dir, "base")
	writeFixture(t, baseDir, "go.mod", "module example.com/test/base\n\ngo 1.26\n")
	writeFixture(t, baseDir, "base.go", `package base

type Config struct {
	Name   string `+"`"+`toml:"name"`+"`"+`
	Script string `+"`"+`toml:"script,omitempty"`+"`"+`
}
`)

	// Consumer package that embeds cross-package struct
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/base v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/base => ../base",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "consumer.go", `package consumer

import "example.com/test/base"

//go:generate tommy generate
type Extended struct {
	base.Config
	Extra string `+"`"+`toml:"extra"`+"`"+`
}
`)

	if err := Generate(consumerDir, "consumer.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import "testing"

func TestCrossPackageEmbeddedRoundTrip(t *testing.T) {
	input := []byte("name = \"hello\"\nscript = \"echo hi\"\nextra = \"world\"\n")

	doc, err := DecodeExtended(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "hello" {
		t.Fatalf("Name = %q, want \"hello\"", cfg.Name)
	}
	if cfg.Extra != "world" {
		t.Fatalf("Extra = %q, want \"world\"", cfg.Extra)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationCrossPackagePrimitiveWrapper(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// Package with a named type wrapping int
	typesDir := filepath.Join(dir, "types")
	writeFixture(t, typesDir, "go.mod", "module example.com/test/types\n\ngo 1.26\n")
	writeFixture(t, typesDir, "types.go", `package types

type Version int
`)

	// Consumer using the type via embedded struct
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/types v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/types => ../types",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "consumer.go", `package consumer

import "example.com/test/types"

type Common struct {
	Version types.Version `+"`"+`toml:"version"`+"`"+`
	Name    string        `+"`"+`toml:"name"`+"`"+`
}

//go:generate tommy generate
type Config struct {
	Common
	Extra string `+"`"+`toml:"extra"`+"`"+`
}
`)

	if err := Generate(consumerDir, "consumer.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import (
	"bytes"
	"testing"
)

func TestDecodeIntWrapper(t *testing.T) {
	input := []byte("version = 14\nname = \"test\"\nextra = \"ok\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	if int(doc.Data().Version) != 14 {
		t.Errorf("Version = %d, want 14", doc.Data().Version)
	}
	if doc.Data().Name != "test" {
		t.Errorf("Name = %q, want \"test\"", doc.Data().Name)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	// Round-trip: version should still be integer, not quoted string
	if !bytes.Contains(out, []byte("version = 14")) {
		t.Errorf("encoded output should contain integer: %s", out)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationBlankIdentifierField(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/blankfield",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package blankfield

type Common struct {
	_    string `+"`"+`toml:"repo-type"`+"`"+`
	Name string `+"`"+`toml:"name"`+"`"+`
}

//go:generate tommy generate
type Config struct {
	Common
	Extra string `+"`"+`toml:"extra"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "blank_test.go", `package blankfield

import "testing"

func TestBlankFieldRoundTrip(t *testing.T) {
	input := []byte("repo-type = \"legacy\"\nname = \"hello\"\nextra = \"world\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "hello" {
		t.Fatalf("Name = %q, want \"hello\"", cfg.Name)
	}
	if cfg.Extra != "world" {
		t.Fatalf("Extra = %q, want \"world\"", cfg.Extra)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationSliceTextMarshalerCrossPackageImport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// Package with a TextMarshaler type
	typesDir := filepath.Join(dir, "types")
	writeFixture(t, typesDir, "go.mod", "module example.com/test/types\n\ngo 1.26\n")
	writeFixture(t, typesDir, "types.go", `package types

type Tag struct{ value string }

func (t Tag) MarshalText() ([]byte, error)  { return []byte(t.value), nil }
func (t *Tag) UnmarshalText(b []byte) error { t.value = string(b); return nil }
`)

	// Consumer with []types.Tag field
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/types v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/types => ../types",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "consumer.go", `package consumer

import "example.com/test/types"

//go:generate tommy generate
type Config struct {
	Name string      `+"`"+`toml:"name"`+"`"+`
	Tags []types.Tag `+"`"+`toml:"tags"`+"`"+`
}
`)

	if err := Generate(consumerDir, "consumer.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Verify the import was added to the generated file
	generated, err := os.ReadFile(filepath.Join(consumerDir, "consumer_tommy.go"))
	if err != nil {
		t.Fatalf("reading generated file: %v", err)
	}
	if !strings.Contains(string(generated), `"example.com/test/types"`) {
		t.Error("generated file should import the cross-package type's package")
	}

	// Write a round-trip test to verify compilation and runtime behavior
	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import "testing"

func TestSliceCrossPackageRoundTrip(t *testing.T) {
	input := []byte("name = \"test\"\ntags = [\"foo\", \"bar\"]\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Data().Name != "test" {
		t.Fatalf("Name = %q, want \"test\"", doc.Data().Name)
	}
	if len(doc.Data().Tags) != 2 {
		t.Fatalf("Tags len = %d, want 2", len(doc.Data().Tags))
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Regression test for #28: struct with both scalar (ids.TypeStruct) and slice
// ([]ids.TagStruct) of cross-package TextMarshaler types within the same module.
// The generated file must import the cross-package and compile.
func TestIntegrationSliceTextMarshalerCrossPackageImportMixed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// Single module with two packages (like dodder's ids + repo_configs)
	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/test",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	// Cross-package TextMarshaler types (same module, different package)
	idsDir := filepath.Join(dir, "ids")
	writeFixture(t, idsDir, "ids.go", `package ids

type TagStruct struct{ value string }
func (t TagStruct) MarshalText() ([]byte, error)  { return []byte(t.value), nil }
func (t *TagStruct) UnmarshalText(b []byte) error { t.value = string(b); return nil }

type TypeStruct struct{ value string }
func (t TypeStruct) MarshalText() ([]byte, error)  { return []byte(t.value), nil }
func (t *TypeStruct) UnmarshalText(b []byte) error { t.value = string(b); return nil }
`)

	// Same-module struct using both scalar and slice of cross-package TextMarshaler
	configDir := filepath.Join(dir, "config")
	writeFixture(t, configDir, "config.go", `package config

import "example.com/test/ids"

//go:generate tommy generate
type Defaults struct {
	Typ  ids.TypeStruct  `+"`"+`toml:"typ"`+"`"+`
	Tags []ids.TagStruct `+"`"+`toml:"tags"`+"`"+`
}
`)

	if err := Generate(configDir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Verify the import was added to the generated file
	generated, err := os.ReadFile(filepath.Join(configDir, "config_tommy.go"))
	if err != nil {
		t.Fatalf("reading generated file: %v", err)
	}
	if !strings.Contains(string(generated), `"example.com/test/ids"`) {
		t.Errorf("generated file missing import for cross-package type.\nGenerated:\n%s", string(generated))
	}

	// Write a round-trip test
	writeFixture(t, configDir, "config_test.go", `package config

import "testing"

func TestMixedCrossPackageRoundTrip(t *testing.T) {
	input := []byte("typ = \"mytype\"\ntags = [\"foo\", \"bar\"]\n")

	doc, err := DecodeDefaults(input)
	if err != nil {
		t.Fatal(err)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}
`)

	cmdMixed := exec.Command("go", "test", "-v", "./...")
	cmdMixed.Dir = dir
	cmdMixed.Env = append(os.Environ(), testGoEnv()...)
	outputMixed, err := cmdMixed.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", outputMixed)
	}
}

// Regression test for #29: FieldTextMarshaler fields should NOT produce imports
// because the generated code accesses them via promoted field methods
// (d.data.Key.UnmarshalText), not by qualified type name. Only
// FieldSliceTextMarshaler and primitive wrappers need imports.
func TestIntegrationNoUnusedImportsForTextMarshalerFields(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// Single module with cross-package TextMarshaler type
	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/test",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	extDir := filepath.Join(dir, "ext")
	writeFixture(t, extDir, "ext.go", `package ext

type Id struct{ value string }
func (i Id) MarshalText() ([]byte, error)  { return []byte(i.value), nil }
func (i *Id) UnmarshalText(b []byte) error { i.value = string(b); return nil }
`)

	configDir := filepath.Join(dir, "config")
	writeFixture(t, configDir, "config.go", `package config

import "example.com/test/ext"

//go:generate tommy generate
type Config struct {
	Key  ext.Id `+"`"+`toml:"key"`+"`"+`
	Name string `+"`"+`toml:"name"`+"`"+`
}
`)

	if err := Generate(configDir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Generated file must compile (no unused imports)
	cmdBuild := exec.Command("go", "build", ".")
	cmdBuild.Dir = configDir
	cmdBuild.Env = append(os.Environ(), testGoEnv()...)
	buildOutput, err := cmdBuild.CombinedOutput()
	if err != nil {
		t.Fatalf("generated file does not compile (unused import?):\n%s", buildOutput)
	}
}

// Regression test for #28: type aliases (type TagStruct = tagStruct) cause
// obj.Type().(*types.Named) to fail because aliases resolve to the target type.
// The import path must still be extracted for []pkg.AliasType fields.
func TestIntegrationSliceTextMarshalerTypeAlias(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/test",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	// Cross-package types using aliases to unexported structs (dodder pattern)
	idsDir := filepath.Join(dir, "ids")
	writeFixture(t, idsDir, "ids.go", `package ids

type tagStruct struct{ value string }
func (t tagStruct) MarshalText() ([]byte, error)  { return []byte(t.value), nil }
func (t *tagStruct) UnmarshalText(b []byte) error { t.value = string(b); return nil }

type TagStruct = tagStruct

type typeStruct struct{ value string }
func (t typeStruct) MarshalText() ([]byte, error)  { return []byte(t.value), nil }
func (t *typeStruct) UnmarshalText(b []byte) error { t.value = string(b); return nil }

type TypeStruct = typeStruct
`)

	configDir := filepath.Join(dir, "config")
	writeFixture(t, configDir, "config.go", `package config

import "example.com/test/ids"

//go:generate tommy generate
type Defaults struct {
	Typ       ids.TypeStruct  `+"`"+`toml:"typ"`+"`"+`
	Etiketten []ids.TagStruct `+"`"+`toml:"etiketten"`+"`"+`
}
`)

	if err := Generate(configDir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	generated, err := os.ReadFile(filepath.Join(configDir, "config_tommy.go"))
	if err != nil {
		t.Fatalf("reading generated file: %v", err)
	}
	if !strings.Contains(string(generated), `"example.com/test/ids"`) {
		t.Errorf("generated file missing import for cross-package type alias.\nGenerated:\n%s", string(generated))
	}

	// Must compile
	cmdBuild := exec.Command("go", "build", ".")
	cmdBuild.Dir = configDir
	cmdBuild.Env = append(os.Environ(), testGoEnv()...)
	buildOutput, err := cmdBuild.CombinedOutput()
	if err != nil {
		t.Fatalf("generated file does not compile:\n%s", buildOutput)
	}
}

// Regression test for #81: a field whose type is an alias re-exported from a
// public facade over an internal/ package must generate code importing the
// facade, not the underlying internal package. The facade and internal package
// live in their own module; the consumer is a separate module, so an import of
// the internal package would violate Go's internal-package rule and fail to
// compile. An analyze-level ImportPath assertion can't catch that — only an
// end-to-end go build of the generated file does.
func TestIntegrationAliasReExportOverInternalCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// Facade module: an internal package defines a named slice type, and a
	// public pkgs/values package re-exports it through a type alias.
	facadeDir := filepath.Join(dir, "facade")
	writeFixture(t, facadeDir, "go.mod", "module example.com/facade\n\ngo 1.26\n")
	writeFixture(t, filepath.Join(facadeDir, "internal/charlie/values"), "values.go", `package values

type IntSlice []int
`)
	writeFixture(t, filepath.Join(facadeDir, "pkgs/values"), "main.go", `package values

import internal "example.com/facade/internal/charlie/values"

type IntSlice = internal.IntSlice
`)

	// Consumer module (separate from the facade module) using the aliased type.
	mainDir := filepath.Join(dir, "main")
	writeFixture(t, mainDir, "go.mod", strings.Join([]string{
		"module example.com/test",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"require example.com/facade v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"replace example.com/facade => ../facade",
		"",
	}, "\n"))

	configDir := filepath.Join(mainDir, "config")
	writeFixture(t, configDir, "config.go", `package config

import "example.com/facade/pkgs/values"

//go:generate tommy generate
type Config struct {
	HashBuckets values.IntSlice `+"`"+`toml:"hash-buckets"`+"`"+`
}
`)

	if err := Generate(configDir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	generated, err := os.ReadFile(filepath.Join(configDir, "config_tommy.go"))
	if err != nil {
		t.Fatalf("reading generated file: %v", err)
	}
	if strings.Contains(string(generated), `"example.com/facade/internal/charlie/values"`) {
		t.Errorf("generated file imports the internal package (illegal from outside the facade module).\nGenerated:\n%s", string(generated))
	}
	if !strings.Contains(string(generated), `"example.com/facade/pkgs/values"`) {
		t.Errorf("generated file missing import for the public facade.\nGenerated:\n%s", string(generated))
	}

	// The generated file must compile — this is the real downstream check: an
	// internal-package import would be rejected here even though the consumer
	// source itself type-checks fine.
	cmdBuild := exec.Command("go", "build", ".")
	cmdBuild.Dir = configDir
	cmdBuild.Env = append(os.Environ(), testGoEnv()...)
	buildOutput, err := cmdBuild.CombinedOutput()
	if err != nil {
		t.Fatalf("generated file does not compile:\n%s", buildOutput)
	}
}

// Regression test for #81 (follow-up, Class A): a map[string]NamedMap field
// whose value type is a map[string]string alias re-exported from a facade over
// an internal/ package must generate code importing the facade, not the internal
// definition. Same end-to-end guarantee as the scalar case, but exercising the
// AST map-value path.
func TestIntegrationAliasReExportMapOverInternalCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	facadeDir := filepath.Join(dir, "facade")
	writeFixture(t, facadeDir, "go.mod", "module example.com/facade\n\ngo 1.26\n")
	writeFixture(t, filepath.Join(facadeDir, "internal/charlie/values"), "values.go", `package values

type Labels map[string]string
`)
	writeFixture(t, filepath.Join(facadeDir, "pkgs/values"), "main.go", `package values

import internal "example.com/facade/internal/charlie/values"

type Labels = internal.Labels
`)

	mainDir := filepath.Join(dir, "main")
	writeFixture(t, mainDir, "go.mod", strings.Join([]string{
		"module example.com/test",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"require example.com/facade v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"replace example.com/facade => ../facade",
		"",
	}, "\n"))

	configDir := filepath.Join(mainDir, "config")
	writeFixture(t, configDir, "config.go", `package config

import "example.com/facade/pkgs/values"

//go:generate tommy generate
type Config struct {
	Groups map[string]values.Labels `+"`"+`toml:"groups"`+"`"+`
}
`)

	if err := Generate(configDir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	generated, err := os.ReadFile(filepath.Join(configDir, "config_tommy.go"))
	if err != nil {
		t.Fatalf("reading generated file: %v", err)
	}
	if strings.Contains(string(generated), `"example.com/facade/internal/charlie/values"`) {
		t.Errorf("generated file imports the internal package (illegal from outside the facade module).\nGenerated:\n%s", string(generated))
	}
	if !strings.Contains(string(generated), `"example.com/facade/pkgs/values"`) {
		t.Errorf("generated file missing import for the public facade.\nGenerated:\n%s", string(generated))
	}

	cmdBuild := exec.Command("go", "build", ".")
	cmdBuild.Dir = configDir
	cmdBuild.Env = append(os.Environ(), testGoEnv()...)
	buildOutput, err := cmdBuild.CombinedOutput()
	if err != nil {
		t.Fatalf("generated file does not compile:\n%s", buildOutput)
	}
}

// Sub-case 1: Cross-package named struct fields (#22)
func TestIntegrationCrossPackageNamedStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// "other" package with a simple struct (also tommy-generated for delegation)
	otherDir := filepath.Join(dir, "other")
	writeFixture(t, otherDir, "go.mod", strings.Join([]string{
		"module example.com/test/other",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, otherDir, "config.go", `package other

//go:generate tommy generate
type Config struct {
	Host string `+"`"+`toml:"host"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}
`)

	if err := Generate(otherDir, "config.go"); err != nil {
		t.Fatalf("Generate other: %v", err)
	}

	// Consumer using other.Config as a named field
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/other v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/other => ../other",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "consumer.go", `package consumer

import "example.com/test/other"

//go:generate tommy generate
type Server struct {
	Name     string       `+"`"+`toml:"name"`+"`"+`
	Settings other.Config `+"`"+`toml:"settings"`+"`"+`
}
`)

	if err := Generate(consumerDir, "consumer.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import "testing"

func TestCrossPackageNamedStructRoundTrip(t *testing.T) {
	input := []byte("name = \"web\"\n\n[settings]\nhost = \"localhost\"\nport = 8080\n")

	doc, err := DecodeServer(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "web" {
		t.Fatalf("Name = %q, want \"web\"", cfg.Name)
	}
	if cfg.Settings.Host != "localhost" {
		t.Fatalf("Settings.Host = %q, want \"localhost\"", cfg.Settings.Host)
	}
	if cfg.Settings.Port != 8080 {
		t.Fatalf("Settings.Port = %d, want 8080", cfg.Settings.Port)
	}

	cfg.Settings.Port = 9090

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeServer(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if d2.Settings.Port != 9090 {
		t.Fatalf("re-decoded Port = %d, want 9090", d2.Settings.Port)
	}
	if d2.Settings.Host != "localhost" {
		t.Fatalf("re-decoded Host = %q, want \"localhost\"", d2.Settings.Host)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Sub-case 2: map[string]CrossPackageStruct (#22)
func TestIntegrationMapStringCrossPackageStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// "other" package with a struct (must also be generated for delegation)
	otherDir := filepath.Join(dir, "other")
	writeFixture(t, otherDir, "go.mod", strings.Join([]string{
		"module example.com/test/other",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, otherDir, "action.go", `package other

//go:generate tommy generate
type Action struct {
	Command string `+"`"+`toml:"command"`+"`"+`
	Timeout int    `+"`"+`toml:"timeout"`+"`"+`
}
`)

	if err := Generate(otherDir, "action.go"); err != nil {
		t.Fatalf("Generate other: %v", err)
	}

	// Consumer using map[string]other.Action
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/other v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/other => ../other",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "consumer.go", `package consumer

import "example.com/test/other"

//go:generate tommy generate
type Config struct {
	Name    string                    `+"`"+`toml:"name"`+"`"+`
	Actions map[string]other.Action   `+"`"+`toml:"actions,omitempty"`+"`"+`
}
`)

	if err := Generate(consumerDir, "consumer.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import "testing"

func TestMapCrossPackageStructRoundTrip(t *testing.T) {
	input := []byte("name = \"myapp\"\n\n[actions.build]\ncommand = \"make\"\ntimeout = 30\n\n[actions.test]\ncommand = \"go test\"\ntimeout = 60\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "myapp" {
		t.Fatalf("Name = %q, want \"myapp\"", cfg.Name)
	}
	if len(cfg.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(cfg.Actions))
	}
	build := cfg.Actions["build"]
	if build.Command != "make" {
		t.Fatalf("Actions[build].Command = %q, want \"make\"", build.Command)
	}
	if build.Timeout != 30 {
		t.Fatalf("Actions[build].Timeout = %d, want 30", build.Timeout)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if len(d2.Actions) != 2 {
		t.Fatalf("re-decoded actions count = %d, want 2", len(d2.Actions))
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Regression: *types.Alias not unwrapped in cross-package recursive struct field resolution (#32)
func TestIntegrationCrossPackageTypeAlias(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// "other" package with a type alias to an unexported struct (also tommy-generated)
	otherDir := filepath.Join(dir, "other")
	writeFixture(t, otherDir, "go.mod", strings.Join([]string{
		"module example.com/test/other",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, otherDir, "types.go", `package other

type inner struct {
	Value string `+"`"+`toml:"value"`+"`"+`
}

type Alias = inner

//go:generate tommy generate
type Wrapper struct {
	Item Alias `+"`"+`toml:"item"`+"`"+`
}
`)

	if err := Generate(otherDir, "types.go"); err != nil {
		t.Fatalf("Generate other: %v", err)
	}

	// Consumer using other.Wrapper (which contains an alias field)
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/other v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/other => ../other",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "consumer.go", `package consumer

import "example.com/test/other"

//go:generate tommy generate
type Config struct {
	Name    string        `+"`"+`toml:"name"`+"`"+`
	Wrapper other.Wrapper `+"`"+`toml:"wrapper"`+"`"+`
}
`)

	if err := Generate(consumerDir, "consumer.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import "testing"

func TestCrossPackageTypeAliasRoundTrip(t *testing.T) {
	input := []byte("name = \"app\"\n\n[wrapper]\n\n[wrapper.item]\nvalue = \"hello\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "app" {
		t.Fatalf("Name = %q, want \"app\"", cfg.Name)
	}
	if cfg.Wrapper.Item.Value != "hello" {
		t.Fatalf("Wrapper.Item.Value = %q, want \"hello\"", cfg.Wrapper.Item.Value)
	}

	cfg.Wrapper.Item.Value = "world"

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if doc2.Data().Wrapper.Item.Value != "world" {
		t.Fatalf("re-decoded Value = %q, want \"world\"", doc2.Data().Wrapper.Item.Value)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Regression: map[string]NamedMapType fails when value type is a named map[string]string (#33)
func TestIntegrationMapStringNamedMapType(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/namedmap",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package namedmap

type UTIGroup map[string]string

//go:generate tommy generate
type Config struct {
	Name   string                `+"`"+`toml:"name"`+"`"+`
	Groups map[string]UTIGroup   `+"`"+`toml:"groups"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "config_test.go", `package namedmap

import "testing"

func TestMapStringNamedMapTypeRoundTrip(t *testing.T) {
	input := []byte("name = \"types\"\n\n[groups.editors]\nvim = \"text/plain\"\nemacs = \"text/plain\"\n\n[groups.compilers]\ngcc = \"application/x-executable\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "types" {
		t.Fatalf("Name = %q, want \"types\"", cfg.Name)
	}
	if len(cfg.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(cfg.Groups))
	}
	editors := cfg.Groups["editors"]
	if editors["vim"] != "text/plain" {
		t.Fatalf("Groups[editors][vim] = %q, want \"text/plain\"", editors["vim"])
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if len(doc2.Data().Groups) != 2 {
		t.Fatalf("re-decoded groups count = %d, want 2", len(doc2.Data().Groups))
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Regression: []*Struct (slice of pointer-to-struct) not supported (#34)
func TestIntegrationSlicePointerToStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/sliceptr",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package sliceptr

type Inner struct {
	Name string `+"`"+`toml:"name"`+"`"+`
}

//go:generate tommy generate
type Config struct {
	Title string    `+"`"+`toml:"title"`+"`"+`
	Items []*Inner  `+"`"+`toml:"items"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "config_test.go", `package sliceptr

import "testing"

func TestSlicePointerToStructRoundTrip(t *testing.T) {
	input := []byte("title = \"test\"\n\n[[items]]\nname = \"first\"\n\n[[items]]\nname = \"second\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Title != "test" {
		t.Fatalf("Title = %q, want \"test\"", cfg.Title)
	}
	if len(cfg.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(cfg.Items))
	}
	if cfg.Items[0] == nil || cfg.Items[0].Name != "first" {
		t.Fatalf("Items[0].Name = %q, want \"first\"", cfg.Items[0].Name)
	}
	if cfg.Items[1] == nil || cfg.Items[1].Name != "second" {
		t.Fatalf("Items[1].Name = %q, want \"second\"", cfg.Items[1].Name)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if len(d2.Items) != 2 {
		t.Fatalf("re-decoded items count = %d, want 2", len(d2.Items))
	}
	if d2.Items[1].Name != "second" {
		t.Fatalf("re-decoded Items[1].Name = %q, want \"second\"", d2.Items[1].Name)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Sub-case 3: Cross-package slice alias and non-TextMarshaler struct (#22)
func TestIntegrationCrossPackageSliceAlias(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// "other" package with a slice alias and a non-TextMarshaler struct
	otherDir := filepath.Join(dir, "other")
	writeFixture(t, otherDir, "go.mod", "module example.com/test/other\n\ngo 1.26\n")
	writeFixture(t, otherDir, "types.go", `package other

type IntSlice []int
`)

	// Consumer using other.IntSlice
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/other v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/other => ../other",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "consumer.go", `package consumer

import "example.com/test/other"

//go:generate tommy generate
type Config struct {
	Name    string          `+"`"+`toml:"name"`+"`"+`
	Buckets other.IntSlice  `+"`"+`toml:"buckets"`+"`"+`
}
`)

	if err := Generate(consumerDir, "consumer.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import "testing"

func TestCrossPackageSliceAliasRoundTrip(t *testing.T) {
	input := []byte("name = \"store\"\nbuckets = [1, 2, 4, 8]\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()
	if cfg.Name != "store" {
		t.Fatalf("Name = %q, want \"store\"", cfg.Name)
	}
	if len(cfg.Buckets) != 4 {
		t.Fatalf("expected 4 buckets, got %d", len(cfg.Buckets))
	}
	if cfg.Buckets[0] != 1 || cfg.Buckets[3] != 8 {
		t.Fatalf("unexpected bucket values: %v", cfg.Buckets)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(input) {
		t.Fatalf("expected byte-identical output.\nexpected:\n%s\ngot:\n%s", string(input), string(out))
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/validation",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package validation

import "fmt"

//go:generate tommy generate
type Config struct {
	Port int    `+"`"+`toml:"port"`+"`"+`
	Name string `+"`"+`toml:"name"`+"`"+`
}

func (c Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port must be 1-65535, got %d", c.Port)
	}
	return nil
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "validation_test.go", `package validation

import (
	"strings"
	"testing"
)

func TestDecodeValidInput(t *testing.T) {
	input := []byte("port = 8080\nname = \"myapp\"\n")
	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if doc.Data().Port != 8080 {
		t.Fatalf("Port = %d, want 8080", doc.Data().Port)
	}
}

func TestDecodeInvalidInput(t *testing.T) {
	input := []byte("port = 0\nname = \"myapp\"\n")
	_, err := DecodeConfig(input)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "port must be 1-65535") {
		t.Fatalf("expected port validation error, got: %v", err)
	}
}

func TestEncodeInvalidState(t *testing.T) {
	input := []byte("port = 8080\nname = \"myapp\"\n")
	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	doc.Data().Port = 0
	_, err = doc.Encode()
	if err == nil {
		t.Fatal("expected validation error on encode, got nil")
	}
	if !strings.Contains(err.Error(), "port must be 1-65535") {
		t.Fatalf("expected port validation error, got: %v", err)
	}
}
`)

	cmdVal := exec.Command("go", "test", "-v", "./...")
	cmdVal.Dir = dir
	cmdVal.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmdVal.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

func TestIntegrationDecodeIntoEncodeFrom(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/inttest",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package inttest

//go:generate tommy generate
type Settings struct {
	Host string `+"`"+`toml:"host"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "inttest_test.go", `package inttest

import (
	"testing"

	"github.com/amarbel-llc/tommy/pkg/cst"
	"github.com/amarbel-llc/tommy/pkg/document"
)

func decompose(t *testing.T, src []byte) *cst.Value {
	t.Helper()
	doc, err := document.Parse(src)
	if err != nil { t.Fatal(err) }
	m, err := cst.Decompose(doc.Root())
	if err != nil { t.Fatal(err) }
	return m
}

func TestDecodeIntoRoundTrip(t *testing.T) {
	input := []byte("host = \"localhost\"\nport = 8080\n")

	var data Settings
	if err := DecodeSettingsInto(&data, decompose(t, input)); err != nil {
		t.Fatalf("DecodeSettingsInto: %v", err)
	}

	if data.Host != "localhost" {
		t.Fatalf("Host = %q, want \"localhost\"", data.Host)
	}
	if data.Port != 8080 {
		t.Fatalf("Port = %d, want 8080", data.Port)
	}

	// EncodeFrom still edits a CST node; round-trip the encode path on a fresh doc.
	doc, err := document.Parse(input)
	if err != nil { t.Fatal(err) }
	data.Port = 9090
	if err := EncodeSettingsFrom(&data, doc, doc.Root()); err != nil {
		t.Fatalf("EncodeSettingsFrom: %v", err)
	}

	out := doc.Bytes()
	var data2 Settings
	if err := DecodeSettingsInto(&data2, decompose(t, out)); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if data2.Port != 9090 {
		t.Fatalf("re-decoded Port = %d, want 9090", data2.Port)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Core #35 test: cross-package struct with unexported nested type, delegated via DecodeInto/EncodeFrom
// Regression for #114: a standalone dotted sub-header ([sub.env] with no bare
// [sub]) defines the parent implicitly (#113), but the delegated renderers —
// compDelStruct (value + pointer), compScopedDelStruct, and compDelMap — only
// found the parent via an exact [sub] header, so the cross-package field stayed
// zero and the dotted header was reported undecoded. Each now falls back to
// cst.FindImplicitChildTable and passes the synthetic node to the target
// package's DecodeInto, which resolves its sub-tables via ChildScope.
func TestIntegrationDelegatedImplicitParent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	extDir := filepath.Join(dir, "options")
	writeFixture(t, extDir, "go.mod", strings.Join([]string{
		"module example.com/test/options",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, extDir, "options.go", `package options

//go:generate tommy generate
type Sub struct {
	Env map[string]string `+"`"+`toml:"env"`+"`"+`
}

//go:generate tommy generate
type Action struct {
	Env map[string]string `+"`"+`toml:"env"`+"`"+`
}
`)
	if err := Generate(extDir, "options.go"); err != nil {
		t.Fatalf("Generate options: %v", err)
	}

	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/options v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/options => ../options",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "config.go", `package consumer

import "example.com/test/options"

//go:generate tommy generate
type Config struct {
	Sub     options.Sub               `+"`"+`toml:"sub"`+"`"+`
	PSub    *options.Sub              `+"`"+`toml:"psub"`+"`"+`
	Actions map[string]options.Action `+"`"+`toml:"actions"`+"`"+`
}
`)
	if err := Generate(consumerDir, "config.go"); err != nil {
		t.Fatalf("Generate consumer: %v", err)
	}
	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import "testing"

// Value delegated struct: only [sub.env] present, no bare [sub] (compDelStruct).
func TestDelegatedImplicitParentValue(t *testing.T) {
	doc, err := DecodeConfig([]byte("[sub.env]\nK = \"v\"\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Sub.Env["K"] != "v" { t.Fatalf("Sub.Env=%+v", d.Sub.Env) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// Pointer delegated struct (compDelStruct, Ptr branch).
func TestDelegatedImplicitParentPtr(t *testing.T) {
	doc, err := DecodeConfig([]byte("[psub.env]\nK = \"v\"\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.PSub == nil || d.PSub.Env["K"] != "v" { t.Fatalf("PSub=%+v", d.PSub) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// Delegated map whose only header for an entry is the deeper [actions.build.env]
// (compDelMap).
func TestDelegatedMapImplicitParent(t *testing.T) {
	doc, err := DecodeConfig([]byte("[actions.build.env]\nK = \"v\"\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Actions["build"].Env["K"] != "v" { t.Fatalf("Actions=%+v", d.Actions) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// The canonical bare-header spelling must still decode unchanged.
func TestDelegatedExplicitParentStillWorks(t *testing.T) {
	doc, err := DecodeConfig([]byte("[sub]\n[sub.env]\nK = \"v\"\n\n[actions.build]\n[actions.build.env]\nK = \"w\"\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Sub.Env["K"] != "v" { t.Fatalf("Sub.Env=%+v", d.Sub.Env) }
	if d.Actions["build"].Env["K"] != "w" { t.Fatalf("Actions=%+v", d.Actions) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
`)
	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, out)
	}
}

func TestIntegrationCrossPackageDelegation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// External package with unexported nested struct
	extDir := filepath.Join(dir, "options")
	writeFixture(t, extDir, "go.mod", strings.Join([]string{
		"module example.com/test/options",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, extDir, "options.go", `package options

type abbreviations struct {
	ZettelIds *bool `+"`"+`toml:"zettel_ids"`+"`"+`
	MarkIds   *bool `+"`"+`toml:"mark_ids"`+"`"+`
}

//go:generate tommy generate
type PrintOptions struct {
	Abbreviations *abbreviations `+"`"+`toml:"abbreviations"`+"`"+`
	PrintColors   *bool          `+"`"+`toml:"print-colors"`+"`"+`
}
`)

	if err := Generate(extDir, "options.go"); err != nil {
		t.Fatalf("Generate options: %v", err)
	}

	// Consumer package
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/options v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/options => ../options",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "config.go", `package consumer

import "example.com/test/options"

//go:generate tommy generate
type Config struct {
	Name         string               `+"`"+`toml:"name"`+"`"+`
	PrintOptions options.PrintOptions `+"`"+`toml:"cli-output"`+"`"+`
}
`)

	if err := Generate(consumerDir, "config.go"); err != nil {
		t.Fatalf("Generate consumer: %v", err)
	}

	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import "testing"

func TestCrossPackageDelegationRoundTrip(t *testing.T) {
	input := []byte("name = \"myapp\"\n\n[cli-output]\nprint-colors = true\n\n[cli-output.abbreviations]\nzettel_ids = true\nmark_ids = false\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}

	cfg := doc.Data()
	if cfg.Name != "myapp" {
		t.Fatalf("Name = %q, want \"myapp\"", cfg.Name)
	}
	if cfg.PrintOptions.PrintColors == nil || !*cfg.PrintOptions.PrintColors {
		t.Fatal("PrintColors should be true")
	}
	if cfg.PrintOptions.Abbreviations == nil {
		t.Fatal("Abbreviations should not be nil")
	}
	if cfg.PrintOptions.Abbreviations.ZettelIds == nil || !*cfg.PrintOptions.Abbreviations.ZettelIds {
		t.Fatal("ZettelIds should be true")
	}

	v := false
	cfg.PrintOptions.Abbreviations.ZettelIds = &v

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if d2.PrintOptions.Abbreviations.ZettelIds == nil || *d2.PrintOptions.Abbreviations.ZettelIds {
		t.Fatal("re-decoded ZettelIds should be false")
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Regression #38: pointer-to-cross-package-struct delegation should not produce **T
func TestIntegrationPointerCrossPackageDelegation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// External package with a struct
	extDir := filepath.Join(dir, "scriptcfg")
	writeFixture(t, extDir, "go.mod", strings.Join([]string{
		"module example.com/test/scriptcfg",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, extDir, "script.go", `package scriptcfg

//go:generate tommy generate
type ScriptConfig struct {
	Description string            `+"`"+`toml:"description"`+"`"+`
	Shell       []string          `+"`"+`toml:"shell,omitempty"`+"`"+`
	Script      string            `+"`"+`toml:"script,omitempty,multiline"`+"`"+`
	Env         map[string]string `+"`"+`toml:"env,omitempty"`+"`"+`
}
`)

	if err := Generate(extDir, "script.go"); err != nil {
		t.Fatalf("Generate scriptcfg: %v", err)
	}

	// Consumer with *scriptcfg.ScriptConfig field
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/scriptcfg v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/scriptcfg => ../scriptcfg",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "blob.go", `package consumer

import "example.com/test/scriptcfg"

//go:generate tommy generate
type Blob struct {
	Name        string                      `+"`"+`toml:"name"`+"`"+`
	ExecCommand *scriptcfg.ScriptConfig     `+"`"+`toml:"exec-command,omitempty"`+"`"+`
}
`)

	if err := Generate(consumerDir, "blob.go"); err != nil {
		t.Fatalf("Generate consumer: %v", err)
	}

	writeFixture(t, consumerDir, "blob_test.go", `package consumer

import "testing"

func TestPointerDelegationRoundTrip(t *testing.T) {
	input := []byte("name = \"mybuild\"\n\n[exec-command]\ndescription = \"run build\"\nscript = \"make all\"\n")

	doc, err := DecodeBlob(input)
	if err != nil {
		t.Fatalf("DecodeBlob: %v", err)
	}

	cfg := doc.Data()
	if cfg.Name != "mybuild" {
		t.Fatalf("Name = %q, want \"mybuild\"", cfg.Name)
	}
	if cfg.ExecCommand == nil {
		t.Fatal("ExecCommand should not be nil")
	}
	if cfg.ExecCommand.Description != "run build" {
		t.Fatalf("Description = %q, want \"run build\"", cfg.ExecCommand.Description)
	}
	if cfg.ExecCommand.Script != "make all" {
		t.Fatalf("Script = %q, want \"make all\"", cfg.ExecCommand.Script)
	}

	cfg.ExecCommand.Script = "make test"

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeBlob(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if doc2.Data().ExecCommand.Script != "make test" {
		t.Fatalf("re-decoded Script = %q, want \"make test\"", doc2.Data().ExecCommand.Script)
	}
}

func TestPointerDelegationNilOmitted(t *testing.T) {
	input := []byte("name = \"simple\"\n")

	doc, err := DecodeBlob(input)
	if err != nil {
		t.Fatalf("DecodeBlob: %v", err)
	}

	cfg := doc.Data()
	if cfg.ExecCommand != nil {
		t.Fatal("ExecCommand should be nil")
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Regression #39: delegation should not emit unused imports from delegated struct's inner fields
func TestIntegrationDelegationNoUnusedImports(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// "ids" package with a TextMarshaler type
	idsDir := filepath.Join(dir, "ids")
	writeFixture(t, idsDir, "go.mod", "module example.com/test/ids\n\ngo 1.26\n")
	writeFixture(t, idsDir, "tag.go", `package ids

type tagStruct struct{ value string }
type TagStruct = tagStruct

func (t tagStruct) MarshalText() ([]byte, error) { return []byte(t.value), nil }
func (t *tagStruct) UnmarshalText(b []byte) error { t.value = string(b); return nil }
`)

	// "defaults" package that uses ids.TagStruct
	defaultsDir := filepath.Join(dir, "defaults")
	writeFixture(t, defaultsDir, "go.mod", strings.Join([]string{
		"module example.com/test/defaults",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/ids v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/ids => ../ids",
		")",
		"",
	}, "\n"))
	writeFixture(t, defaultsDir, "defaults.go", `package defaults

import "example.com/test/ids"

//go:generate tommy generate
type Defaults struct {
	Tags []ids.TagStruct `+"`"+`toml:"tags,omitempty"`+"`"+`
}
`)

	if err := Generate(defaultsDir, "defaults.go"); err != nil {
		t.Fatalf("Generate defaults: %v", err)
	}

	// Consumer that delegates to defaults — should NOT import "ids"
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/defaults v0.0.0",
		"\texample.com/test/ids v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/defaults => ../defaults",
		"\texample.com/test/ids => ../ids",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "config.go", `package consumer

import "example.com/test/defaults"

//go:generate tommy generate
type Config struct {
	Name     string            `+"`"+`toml:"name"`+"`"+`
	Defaults defaults.Defaults `+"`"+`toml:"defaults"`+"`"+`
}
`)

	if err := Generate(consumerDir, "config.go"); err != nil {
		t.Fatalf("Generate consumer: %v", err)
	}

	// The generated file should compile without "imported and not used" errors
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed (likely unused import):\n%s", output)
	}
}

// Regression #40: regeneration over existing _tommy.go files must be idempotent
func TestIntegrationRegenerateOverExistingTommyFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// Leaf package
	leafDir := filepath.Join(dir, "leaf")
	writeFixture(t, leafDir, "go.mod", strings.Join([]string{
		"module example.com/test/leaf",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, leafDir, "config.go", `package leaf

//go:generate tommy generate
type Config struct {
	Host string `+"`"+`toml:"host"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}
`)

	// Generate once
	if err := Generate(leafDir, "config.go"); err != nil {
		t.Fatalf("Generate leaf (first): %v", err)
	}

	firstOutput, err := os.ReadFile(filepath.Join(leafDir, "config_tommy.go"))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(firstOutput), "func DecodeConfigInto(") {
		t.Fatal("first generation missing DecodeConfigInto")
	}

	// Generate again over existing _tommy.go (simulates go generate ./... re-run)
	if err := Generate(leafDir, "config.go"); err != nil {
		t.Fatalf("Generate leaf (second): %v", err)
	}

	secondOutput, err := os.ReadFile(filepath.Join(leafDir, "config_tommy.go"))
	if err != nil {
		t.Fatal(err)
	}

	if string(firstOutput) != string(secondOutput) {
		t.Fatalf("regeneration over existing _tommy.go produced different output.\nFirst length: %d\nSecond length: %d", len(firstOutput), len(secondOutput))
	}
}

// Regression #40: multi-package regeneration via go generate ./...
func TestIntegrationGoGenerateAllMultiPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// Monorepo with two packages: leaf and consumer
	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/test",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	leafDir := filepath.Join(dir, "leaf")
	writeFixture(t, leafDir, "config.go", `package leaf

//go:generate tommy generate
type Config struct {
	Host string `+"`"+`toml:"host"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}
`)

	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "app.go", `package consumer

import "example.com/test/leaf"

//go:generate tommy generate
type App struct {
	Name   string      `+"`"+`toml:"name"`+"`"+`
	Config leaf.Config `+"`"+`toml:"config"`+"`"+`
}
`)

	// Generate individually in dependency order
	if err := Generate(leafDir, "config.go"); err != nil {
		t.Fatalf("Generate leaf: %v", err)
	}
	if err := Generate(consumerDir, "app.go"); err != nil {
		t.Fatalf("Generate consumer: %v", err)
	}

	leafIndividual, _ := os.ReadFile(filepath.Join(leafDir, "config_tommy.go"))
	consumerIndividual, _ := os.ReadFile(filepath.Join(consumerDir, "app_tommy.go"))

	// Build tommy binary from current source. CGO_ENABLED=0: cmd/tommy imports
	// net (internal/stats UDP telemetry), which pulls in net's cgo DNS resolver
	// under the default CGO_ENABLED=1; the offline go-generate sandbox has no C
	// compiler, so a cgo build fails. tommy needs no cgo (statsd is pure-Go UDP),
	// so force the pure-Go build.
	tommyBin := filepath.Join(t.TempDir(), "tommy")
	buildCmd := exec.Command("go", "build", "-o", tommyBin, "./cmd/tommy")
	buildCmd.Dir = repoRoot
	buildCmd.Env = append(append(os.Environ(), testGoEnv()...), "CGO_ENABLED=0")
	if buildOut, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build tommy: %v\n%s", err, buildOut)
	}

	// Regenerate via go generate ./...
	cmd := exec.Command("go", "generate", "./...")
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), testGoEnv()...), "PATH="+filepath.Dir(tommyBin)+":"+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go generate ./... failed: %v\n%s", err, output)
	}

	leafAll, _ := os.ReadFile(filepath.Join(leafDir, "config_tommy.go"))
	consumerAll, _ := os.ReadFile(filepath.Join(consumerDir, "app_tommy.go"))

	if !strings.Contains(string(leafAll), "func DecodeConfigInto(") {
		t.Fatalf("go generate ./... dropped leaf DecodeConfigInto.\nOutput:\n%s", string(leafAll))
	}
	if !strings.Contains(string(consumerAll), "func DecodeAppInto(") {
		t.Fatalf("go generate ./... dropped consumer DecodeAppInto.\nOutput:\n%s", string(consumerAll))
	}

	if string(leafIndividual) != string(leafAll) {
		t.Fatalf("leaf: go generate ./... produced different output than individual generate")
	}
	if string(consumerIndividual) != string(consumerAll) {
		t.Fatalf("consumer: go generate ./... produced different output than individual generate")
	}

	// Verify the full project compiles and tests pass
	writeFixture(t, consumerDir, "app_test.go", `package consumer

import "testing"

func TestAppRoundTrip(t *testing.T) {
	input := []byte("name = \"myapp\"\n\n[config]\nhost = \"localhost\"\nport = 8080\n")
	doc, err := DecodeApp(input)
	if err != nil {
		t.Fatal(err)
	}
	cfg := doc.Data()
	if cfg.Config.Host != "localhost" {
		t.Fatalf("Host = %q, want \"localhost\"", cfg.Config.Host)
	}
}
`)

	testCmd := exec.Command("go", "test", "./...")
	testCmd.Dir = dir
	testCmd.Env = append(os.Environ(), testGoEnv()...)
	if testOut, err := testCmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed:\n%s", testOut)
	}
}

func TestIntegrationCommentAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/commentapi",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package commentapi

//go:generate tommy generate
type Config struct {
	Name string `+"`"+`toml:"name"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "comment_test.go", `package commentapi

import (
	"strings"
	"testing"
)

func TestCommentGetSet(t *testing.T) {
	input := []byte("# Server name\nname = \"myapp\"\nport = 8080 # default port\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	// Read above-key comment
	if got := doc.Comment("name"); got != "# Server name" {
		t.Fatalf("Comment(name) = %q, want %q", got, "# Server name")
	}

	// Read inline comment
	if got := doc.InlineComment("port"); got != "# default port" {
		t.Fatalf("InlineComment(port) = %q, want %q", got, "# default port")
	}

	// Set a new above-key comment
	doc.SetComment("port", "# HTTP port")

	// Set a new inline comment
	doc.SetInlineComment("name", "# app identifier")

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	result := string(out)

	if !strings.Contains(result, "# HTTP port") {
		t.Fatalf("SetComment not reflected in output:\n%s", result)
	}
	if !strings.Contains(result, "# app identifier") {
		t.Fatalf("SetInlineComment not reflected in output:\n%s", result)
	}

	// Round-trip: decode the output and verify comments persist
	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := doc2.Comment("port"); got != "# HTTP port" {
		t.Fatalf("after round-trip Comment(port) = %q, want %q", got, "# HTTP port")
	}
	if got := doc2.InlineComment("name"); got != "# app identifier" {
		t.Fatalf("after round-trip InlineComment(name) = %q, want %q", got, "# app identifier")
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test failed:\n%s", out)
	}
	t.Log(string(out))
}

func TestIntegrationEncodeFromEmptyDocument(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/emptydoc",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package emptydoc

//go:generate tommy generate
type AppConfig struct {
	Name     string   `+"`"+`toml:"name"`+"`"+`
	Tags     []string `+"`"+`toml:"tags"`+"`"+`
	Defaults Defaults `+"`"+`toml:"defaults"`+"`"+`
	Output   Output   `+"`"+`toml:"output"`+"`"+`
}

type Defaults struct {
	Type    string `+"`"+`toml:"type"`+"`"+`
	Enabled bool   `+"`"+`toml:"enabled"`+"`"+`
}

type Output struct {
	Format  string `+"`"+`toml:"format"`+"`"+`
	Verbose bool   `+"`"+`toml:"verbose"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "emptydoc_test.go", `package emptydoc

import (
	"strings"
	"testing"
)

func TestEncodeFromEmptyDocumentPreservesNestedTables(t *testing.T) {
	// Issue #42: Encoding a struct into a document parsed from empty bytes
	// silently drops all nested [table] sections because FindTable returns nil
	// when no table exists in the CST.
	doc, err := DecodeAppConfig([]byte{})
	if err != nil {
		t.Fatalf("DecodeAppConfig(empty): %v", err)
	}

	*doc.Data() = AppConfig{
		Name: "myapp",
		Tags: []string{"alpha", "beta"},
		Defaults: Defaults{
			Type:    "!md",
			Enabled: true,
		},
		Output: Output{
			Format:  "json",
			Verbose: true,
		},
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	result := string(out)

	// Top-level fields must be present.
	if !strings.Contains(result, "name = \"myapp\"") {
		t.Fatalf("top-level 'name' missing from output:\n%s", result)
	}

	// Nested [defaults] table must be present — this is the core of #42.
	if !strings.Contains(result, "[defaults]") {
		t.Fatalf("[defaults] table silently dropped when encoding from empty document:\n%s", result)
	}
	if !strings.Contains(result, "type = \"!md\"") {
		t.Fatalf("defaults.type field missing from output:\n%s", result)
	}
	if !strings.Contains(result, "enabled = true") {
		t.Fatalf("defaults.enabled field missing from output:\n%s", result)
	}

	// Second nested table [output] must also be present.
	if !strings.Contains(result, "[output]") {
		t.Fatalf("[output] table silently dropped when encoding from empty document:\n%s", result)
	}
	if !strings.Contains(result, "format = \"json\"") {
		t.Fatalf("output.format field missing from output:\n%s", result)
	}
	if !strings.Contains(result, "verbose = true") {
		t.Fatalf("output.verbose field missing from output:\n%s", result)
	}

	// Verify the output can be decoded back correctly.
	doc2, err := DecodeAppConfig(out)
	if err != nil {
		t.Fatalf("re-decode failed: %v", err)
	}
	d := doc2.Data()
	if d.Name != "myapp" {
		t.Fatalf("re-decoded Name = %q, want \"myapp\"", d.Name)
	}
	if d.Defaults.Type != "!md" {
		t.Fatalf("re-decoded Defaults.Type = %q, want \"!md\"", d.Defaults.Type)
	}
	if d.Defaults.Enabled != true {
		t.Fatal("re-decoded Defaults.Enabled = false, want true")
	}
	if d.Output.Format != "json" {
		t.Fatalf("re-decoded Output.Format = %q, want \"json\"", d.Output.Format)
	}
	if d.Output.Verbose != true {
		t.Fatal("re-decoded Output.Verbose = false, want true")
	}
}

func TestEncodeFromEmptyDocumentPointerStruct(t *testing.T) {
	// Same issue but for *Struct fields — FieldPointerStruct uses
	// FindTableInContainer which also returns nil on empty documents.
	doc, err := DecodeAppConfig([]byte("name = \"x\"\n"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// Set nested struct fields — the document has no [defaults] or [output] tables.
	d := doc.Data()
	d.Defaults = Defaults{Type: "txt", Enabled: true}
	d.Output = Output{Format: "yaml", Verbose: false}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	result := string(out)
	if !strings.Contains(result, "[defaults]") {
		t.Fatalf("[defaults] table dropped when not in original input:\n%s", result)
	}
	if !strings.Contains(result, "type = \"txt\"") {
		t.Fatalf("defaults.type missing:\n%s", result)
	}
	if !strings.Contains(result, "[output]") {
		t.Fatalf("[output] table dropped when not in original input:\n%s", result)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestEncodeFromEmpty", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEncodeFromEmptyPointerStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/emptyptr",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package emptyptr

//go:generate tommy generate
type Config struct {
	Name  string  `+"`"+`toml:"name"`+"`"+`
	Hooks *Hooks  `+"`"+`toml:"hooks"`+"`"+`
}

type Hooks struct {
	Create *string `+"`"+`toml:"create"`+"`"+`
	Stop   *string `+"`"+`toml:"stop"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "emptyptr_test.go", `package emptyptr

import (
	"strings"
	"testing"
)

func TestEncodePointerStructFromEmptyDocument(t *testing.T) {
	// Issue #42: pointer-struct variant. When the document is empty,
	// FindTableInContainer returns nil and the entire *Hooks is dropped.
	doc, err := DecodeConfig([]byte{})
	if err != nil {
		t.Fatalf("DecodeConfig(empty): %v", err)
	}

	create := "npm install"
	stop := "just test"
	*doc.Data() = Config{
		Name: "myapp",
		Hooks: &Hooks{
			Create: &create,
			Stop:   &stop,
		},
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	result := string(out)
	if !strings.Contains(result, "[hooks]") {
		t.Fatalf("[hooks] table silently dropped when encoding *Hooks from empty document:\n%s", result)
	}
	if !strings.Contains(result, "create = \"npm install\"") {
		t.Fatalf("hooks.create missing from output:\n%s", result)
	}
	if !strings.Contains(result, "stop = \"just test\"") {
		t.Fatalf("hooks.stop missing from output:\n%s", result)
	}

	// Round-trip verification.
	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode failed: %v", err)
	}
	d := doc2.Data()
	if d.Hooks == nil {
		t.Fatal("re-decoded Hooks is nil")
	}
	if d.Hooks.Create == nil || *d.Hooks.Create != "npm install" {
		t.Fatalf("re-decoded Hooks.Create = %v, want \"npm install\"", d.Hooks.Create)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestEncodePointerStruct", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEncodeFromEmptyDelegatedStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// External package with a struct
	extDir := filepath.Join(dir, "dbcfg")
	writeFixture(t, extDir, "go.mod", strings.Join([]string{
		"module example.com/test/dbcfg",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, extDir, "db.go", `package dbcfg

//go:generate tommy generate
type Database struct {
	Host string `+"`"+`toml:"host"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}
`)

	if err := Generate(extDir, "db.go"); err != nil {
		t.Fatalf("Generate dbcfg: %v", err)
	}

	// Consumer with dbcfg.Database (non-pointer, delegated)
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/dbcfg v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/dbcfg => ../dbcfg",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "app.go", `package consumer

import "example.com/test/dbcfg"

//go:generate tommy generate
type App struct {
	Name string          `+"`"+`toml:"name"`+"`"+`
	DB   dbcfg.Database  `+"`"+`toml:"database"`+"`"+`
}
`)

	if err := Generate(consumerDir, "app.go"); err != nil {
		t.Fatalf("Generate consumer: %v", err)
	}

	writeFixture(t, consumerDir, "app_test.go", `package consumer

import (
	"strings"
	"testing"
	"example.com/test/dbcfg"
)

func TestDelegatedStructFromEmptyDocument(t *testing.T) {
	// Issue #42: FieldDelegatedStruct uses FindTable which returns nil
	// on an empty document, silently dropping the delegated table.
	doc, err := DecodeApp([]byte{})
	if err != nil {
		t.Fatalf("DecodeApp(empty): %v", err)
	}

	*doc.Data() = App{
		Name: "webapp",
		DB: dbcfg.Database{
			Host: "db.example.com",
			Port: 5432,
		},
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	result := string(out)
	if !strings.Contains(result, "[database]") {
		t.Fatalf("[database] table silently dropped for delegated struct from empty document:\n%s", result)
	}
	if !strings.Contains(result, "host = \"db.example.com\"") {
		t.Fatalf("database.host missing:\n%s", result)
	}
	if !strings.Contains(result, "port = 5432") {
		t.Fatalf("database.port missing:\n%s", result)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestDelegatedStruct", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEncodeFromEmptyPointerDelegatedStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// External package
	extDir := filepath.Join(dir, "logcfg")
	writeFixture(t, extDir, "go.mod", strings.Join([]string{
		"module example.com/test/logcfg",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, extDir, "log.go", `package logcfg

//go:generate tommy generate
type LogConfig struct {
	Level  string `+"`"+`toml:"level"`+"`"+`
	Format string `+"`"+`toml:"format"`+"`"+`
}
`)

	if err := Generate(extDir, "log.go"); err != nil {
		t.Fatalf("Generate logcfg: %v", err)
	}

	// Consumer with *logcfg.LogConfig (pointer, delegated)
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/logcfg v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/logcfg => ../logcfg",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "svc.go", `package consumer

import "example.com/test/logcfg"

//go:generate tommy generate
type Service struct {
	Name    string             `+"`"+`toml:"name"`+"`"+`
	Logging *logcfg.LogConfig  `+"`"+`toml:"logging"`+"`"+`
}
`)

	if err := Generate(consumerDir, "svc.go"); err != nil {
		t.Fatalf("Generate consumer: %v", err)
	}

	writeFixture(t, consumerDir, "svc_test.go", `package consumer

import (
	"strings"
	"testing"
	"example.com/test/logcfg"
)

func TestPointerDelegatedStructFromEmptyDocument(t *testing.T) {
	// Issue #42: FieldPointerDelegatedStruct uses FindTableInContainer
	// which returns nil on an empty document.
	doc, err := DecodeService([]byte{})
	if err != nil {
		t.Fatalf("DecodeService(empty): %v", err)
	}

	*doc.Data() = Service{
		Name: "api",
		Logging: &logcfg.LogConfig{
			Level:  "debug",
			Format: "json",
		},
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	result := string(out)
	if !strings.Contains(result, "[logging]") {
		t.Fatalf("[logging] table silently dropped for pointer-delegated struct from empty document:\n%s", result)
	}
	if !strings.Contains(result, "level = \"debug\"") {
		t.Fatalf("logging.level missing:\n%s", result)
	}
	if !strings.Contains(result, "format = \"json\"") {
		t.Fatalf("logging.format missing:\n%s", result)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestPointerDelegated", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEncodeFromEmptyNestedStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/nested",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package nested

//go:generate tommy generate
type Root struct {
	Name  string `+"`"+`toml:"name"`+"`"+`
	Mid   Middle `+"`"+`toml:"mid"`+"`"+`
}

type Middle struct {
	Label string `+"`"+`toml:"label"`+"`"+`
	Inner Leaf   `+"`"+`toml:"inner"`+"`"+`
}

type Leaf struct {
	Value string `+"`"+`toml:"value"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}

	writeFixture(t, dir, "nested_test.go", `package nested

import (
	"strings"
	"testing"
)

func TestDeeplyNestedStructFromEmptyDocument(t *testing.T) {
	// Issue #42: struct-within-struct-within-struct from empty document.
	// The outer FindTable returns nil, so the inner struct is also unreachable.
	doc, err := DecodeRoot([]byte{})
	if err != nil {
		t.Fatalf("DecodeRoot(empty): %v", err)
	}

	*doc.Data() = Root{
		Name: "root",
		Mid: Middle{
			Label: "middle",
			Inner: Leaf{
				Value: "deep",
			},
		},
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	result := string(out)
	if !strings.Contains(result, "[mid]") {
		t.Fatalf("[mid] table dropped from empty document:\n%s", result)
	}
	if !strings.Contains(result, "label = \"middle\"") {
		t.Fatalf("mid.label missing:\n%s", result)
	}
	if !strings.Contains(result, "[mid.inner]") {
		t.Fatalf("[mid.inner] nested table dropped from empty document:\n%s", result)
	}
	if !strings.Contains(result, "value = \"deep\"") {
		t.Fatalf("mid.inner.value missing:\n%s", result)
	}

	// Round-trip.
	doc2, err := DecodeRoot(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d := doc2.Data()
	if d.Mid.Inner.Value != "deep" {
		t.Fatalf("re-decoded Mid.Inner.Value = %q, want \"deep\"", d.Mid.Inner.Value)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestDeeplyNested", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Issue #43: cross-package slice-of-structs where the external struct has a
// pointer-to-unexported nested struct.  FieldSliceStruct inlines inner field
// decode/encode, so the generated consumer code must NOT reference unexported
// types from the external package.
func TestIntegrationSliceOfCrossPackageStructWithUnexportedNested(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// External package with an exported struct containing *unexported
	extDir := filepath.Join(dir, "options_print")
	writeFixture(t, extDir, "go.mod", strings.Join([]string{
		"module example.com/test/options_print",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, extDir, "options.go", `package options_print

//go:generate tommy generate
type abbreviationsV1 struct {
	ZettelIds *bool `+"`"+`toml:"zettel-ids"`+"`"+`
	ShaIds    *bool `+"`"+`toml:"shas"`+"`"+`
}

//go:generate tommy generate
type V1 struct {
	Abbreviations *abbreviationsV1 `+"`"+`toml:"abbreviations"`+"`"+`
	PrintColors   *bool            `+"`"+`toml:"print-colors"`+"`"+`
}
`)

	if err := Generate(extDir, "options.go"); err != nil {
		t.Fatalf("Generate options_print: %v", err)
	}

	// Consumer package using []options_print.V1 (slice triggers inlined inner field code)
	consumerDir := filepath.Join(dir, "repo_configs")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/repo_configs",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/options_print v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/options_print => ../options_print",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "config.go", `package repo_configs

import "example.com/test/options_print"

//go:generate tommy generate
type V0 struct {
	Outputs []options_print.V1 `+"`"+`toml:"outputs"`+"`"+`
}
`)

	// Generation may succeed but produce code referencing unexported types
	if err := Generate(consumerDir, "config.go"); err != nil {
		t.Fatalf("Generate repo_configs: %v", err)
	}

	// Verify the generated file does NOT reference the unexported type
	genPath := filepath.Join(consumerDir, "config_tommy.go")
	genBytes, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	genCode := string(genBytes)
	if strings.Contains(genCode, "abbreviationsV1") {
		t.Fatalf("generated consumer code references unexported type abbreviationsV1:\n%s", genCode)
	}

	// The generated code should compile and the round-trip should work
	writeFixture(t, consumerDir, "consumer_test.go", `package repo_configs

import "testing"

func TestSliceCrossPackageUnexportedRoundTrip(t *testing.T) {
	input := []byte("[[outputs]]\nprint-colors = true\n\n[outputs.abbreviations]\nzettel-ids = true\nshas = false\n\n[[outputs]]\nprint-colors = false\n")

	doc, err := DecodeV0(input)
	if err != nil {
		t.Fatalf("DecodeV0: %v", err)
	}

	cfg := doc.Data()
	if len(cfg.Outputs) != 2 {
		t.Fatalf("len(Outputs) = %d, want 2", len(cfg.Outputs))
	}
	if cfg.Outputs[0].Abbreviations == nil {
		t.Fatal("Outputs[0].Abbreviations should not be nil")
	}
	if cfg.Outputs[0].Abbreviations.ZettelIds == nil || !*cfg.Outputs[0].Abbreviations.ZettelIds {
		t.Fatal("ZettelIds should be true")
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeV0(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if len(d2.Outputs) != 2 {
		t.Fatalf("re-decoded len(Outputs) = %d, want 2", len(d2.Outputs))
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Acceptance test for #99: a delegated struct used as an array-table element
// ([]options_print.V1) whose definition has a nested *table* field
// (Abbreviations *abbreviationsV1) must decode that sub-table scoped to ITS OWN
// [[outputs]] entry. Entry 1 here has no [outputs.abbreviations], so its
// Abbreviations must stay nil — and a round-trip must NOT manufacture a second
// [outputs.abbreviations] under the second entry.
func TestIntegrationDelegatedNestedTableScoping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}
	extDir := filepath.Join(dir, "options_print")
	writeFixture(t, extDir, "go.mod", "module example.com/test99/options_print\n\ngo 1.26\n\nrequire github.com/amarbel-llc/tommy v0.0.0\n\nreplace github.com/amarbel-llc/tommy => "+repoRoot+"\n")
	writeFixture(t, extDir, "options.go", `package options_print

//go:generate tommy generate
type abbreviationsV1 struct {
	ZettelIds *bool `+"`"+`toml:"zettel-ids"`+"`"+`
}

//go:generate tommy generate
type V1 struct {
	Abbreviations *abbreviationsV1 `+"`"+`toml:"abbreviations"`+"`"+`
	PrintColors   *bool            `+"`"+`toml:"print-colors"`+"`"+`
}
`)
	if err := Generate(extDir, "options.go"); err != nil {
		t.Fatalf("Generate options_print: %v", err)
	}
	consumerDir := filepath.Join(dir, "repo_configs")
	writeFixture(t, consumerDir, "go.mod", "module example.com/test99/repo_configs\n\ngo 1.26\n\nrequire (\n\tgithub.com/amarbel-llc/tommy v0.0.0\n\texample.com/test99/options_print v0.0.0\n)\n\nreplace (\n\tgithub.com/amarbel-llc/tommy => "+repoRoot+"\n\texample.com/test99/options_print => ../options_print\n)\n")
	writeFixture(t, consumerDir, "config.go", `package repo_configs

import "example.com/test99/options_print"

//go:generate tommy generate
type V0 struct {
	Outputs []options_print.V1 `+"`"+`toml:"outputs"`+"`"+`
}
`)
	if err := Generate(consumerDir, "config.go"); err != nil {
		t.Fatalf("Generate repo_configs: %v", err)
	}
	writeFixture(t, consumerDir, "scope_test.go", `package repo_configs

import (
	"strings"
	"testing"
)

func TestDelegatedScope(t *testing.T) {
	// Only the FIRST [[outputs]] has an [outputs.abbreviations] sub-table.
	input := []byte("[[outputs]]\nprint-colors = true\n\n[outputs.abbreviations]\nzettel-ids = true\n\n[[outputs]]\nprint-colors = false\n")
	doc, err := DecodeV0(input)
	if err != nil { t.Fatalf("DecodeV0: %v", err) }
	cfg := doc.Data()
	if len(cfg.Outputs) != 2 {
		t.Fatalf("len(Outputs) = %d, want 2", len(cfg.Outputs))
	}
	if cfg.Outputs[0].Abbreviations == nil {
		t.Fatal("Outputs[0].Abbreviations should be set")
	}
	if cfg.Outputs[1].Abbreviations != nil {
		t.Fatalf("Outputs[1].Abbreviations should be nil (entry 1 has no [outputs.abbreviations]), got %#v", cfg.Outputs[1].Abbreviations)
	}

	out, err := doc.Encode()
	if err != nil { t.Fatalf("Encode: %v", err) }
	if n := strings.Count(string(out), "[outputs.abbreviations]"); n != 1 {
		t.Fatalf("want exactly one [outputs.abbreviations] in output, got %d:\n%s", n, out)
	}

	doc2, err := DecodeV0(out)
	if err != nil { t.Fatalf("re-decode: %v\n%s", err, out) }
	if doc2.Data().Outputs[1].Abbreviations != nil {
		t.Fatal("re-decoded Outputs[1].Abbreviations should still be nil")
	}
}
`)
	cmd := exec.Command("go", "test", "-run", "TestDelegatedScope", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed:\n%s", out)
	}
}

// Issue #44: codegen fails on slice fields whose element type is a type alias
// to an unexported struct from another package.
// Variant A: the struct with the slice is generated directly (AST path).
func TestIntegrationSliceOfAliasedUnexportedStructDirect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// Package with type alias to unexported struct
	idsDir := filepath.Join(dir, "ids")
	writeFixture(t, idsDir, "go.mod", "module example.com/test/ids\n\ngo 1.26\n\n"+
		"require github.com/amarbel-llc/tommy v0.0.0\n\n"+
		"replace github.com/amarbel-llc/tommy => "+repoRoot+"\n")
	writeFixture(t, idsDir, "ids.go", `package ids

type tagStruct struct {
	Value string `+"`"+`toml:"value"`+"`"+`
}

// TagStruct is an exported alias for the unexported tagStruct.
type TagStruct = tagStruct

//go:generate tommy generate
type TagWrapper struct {
	Value string `+"`"+`toml:"value"`+"`"+`
}
`)

	if err := Generate(idsDir, "ids.go"); err != nil {
		t.Fatalf("Generate ids: %v", err)
	}

	// Consumer package that directly has a []ids.TagStruct field
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/ids v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/ids => ../ids",
		")",
		"",
	}, "\n"))
	writeFixture(t, consumerDir, "config.go", `package consumer

import "example.com/test/ids"

//go:generate tommy generate
type Config struct {
	Name string          `+"`"+`toml:"name"`+"`"+`
	Tags []ids.TagStruct `+"`"+`toml:"tags"`+"`"+`
}
`)

	// This should not error — the alias should be followed to the underlying struct
	if err := Generate(consumerDir, "config.go"); err != nil {
		t.Fatalf("Generate consumer (direct slice of aliased type): %v", err)
	}

	// The generated code should compile and round-trip
	writeFixture(t, consumerDir, "consumer_test.go", `package consumer

import "testing"

func TestSliceAliasRoundTrip(t *testing.T) {
	input := []byte("name = \"test\"\n\n[[tags]]\nvalue = \"hello\"\n\n[[tags]]\nvalue = \"world\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}

	cfg := doc.Data()
	if cfg.Name != "test" {
		t.Fatalf("Name = %q, want \"test\"", cfg.Name)
	}
	if len(cfg.Tags) != 2 {
		t.Fatalf("len(Tags) = %d, want 2", len(cfg.Tags))
	}
	if cfg.Tags[0].Value != "hello" {
		t.Fatalf("Tags[0].Value = %q, want \"hello\"", cfg.Tags[0].Value)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if len(d2.Tags) != 2 || d2.Tags[1].Value != "world" {
		t.Fatalf("re-decoded Tags mismatch: %+v", d2.Tags)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEmbeddedNonStructWithTomlIgnoreTag(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/embeddedskip",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	// An interface type embedded in a struct with toml:"-" should be
	// silently skipped. Before the fix, resolveEmbeddedFields is called
	// before the tag is checked, causing "not a struct" error.
	writeFixture(t, dir, "config.go", `package embeddedskip

type Pool interface {
	Acquire() error
	Release()
}

//go:generate tommy generate
type Config struct {
	Pool `+"`"+`toml:"-"`+"`"+`
	Name string `+"`"+`toml:"name"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate should succeed when embedded non-struct has toml:\"-\" tag, got: %v", err)
	}

	if _, err := os.ReadFile(filepath.Join(dir, "config_tommy.go")); err != nil {
		t.Fatalf("generated file not found: %v", err)
	}
}

// Regression #47 case 2: map[string]CrossPackageStruct where the struct has
// unexported nested fields should delegate to DecodeInto/EncodeFrom rather than
// inlining fields (which would fail because the consumer can't access unexported types).
func TestIntegrationMapStringCrossPackageStructWithUnexported(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// pkga: struct with unexported nested type
	pkgaDir := filepath.Join(dir, "pkga")
	writeFixture(t, pkgaDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkga",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, pkgaDir, "pkga.go", `package pkga

type outputFormat struct {
	Extension string `+"`"+`toml:"extension"`+"`"+`
	MimeType  string `+"`"+`toml:"mime_type"`+"`"+`
}

//go:generate tommy generate
type ScriptConfig struct {
	Command string        `+"`"+`toml:"command"`+"`"+`
	Output  *outputFormat `+"`"+`toml:"output,omitempty"`+"`"+`
}
`)

	if err := Generate(pkgaDir, "pkga.go"); err != nil {
		t.Fatalf("Generate pkga: %v", err)
	}

	// pkgb: consumer with map[string]pkga.ScriptConfig
	pkgbDir := filepath.Join(dir, "pkgb")
	writeFixture(t, pkgbDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkgb",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/pkga v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/pkga => ../pkga",
		")",
		"",
	}, "\n"))
	writeFixture(t, pkgbDir, "pkgb.go", `package pkgb

import "example.com/test/pkga"

//go:generate tommy generate
type Config struct {
	Actions map[string]pkga.ScriptConfig `+"`"+`toml:"actions,omitempty"`+"`"+`
}
`)

	if err := Generate(pkgbDir, "pkgb.go"); err != nil {
		t.Fatalf("Generate pkgb: %v", err)
	}

	writeFixture(t, pkgbDir, "pkgb_test.go", `package pkgb

import "testing"

func TestMapCrossPackageUnexportedRoundTrip(t *testing.T) {
	input := []byte("[actions.build]\ncommand = \"make\"\n\n[actions.build.output]\nextension = \".bin\"\nmime_type = \"application/octet-stream\"\n\n[actions.test]\ncommand = \"go test\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}

	cfg := doc.Data()
	if len(cfg.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(cfg.Actions))
	}
	build := cfg.Actions["build"]
	if build.Command != "make" {
		t.Fatalf("build.Command = %q, want \"make\"", build.Command)
	}
	if build.Output == nil {
		t.Fatal("build.Output should not be nil")
	}
	if build.Output.Extension != ".bin" {
		t.Fatalf("build.Output.Extension = %q, want \".bin\"", build.Output.Extension)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if len(d2.Actions) != 2 {
		t.Fatalf("re-decoded actions count = %d, want 2", len(d2.Actions))
	}
	if d2.Actions["build"].Output == nil || d2.Actions["build"].Output.Extension != ".bin" {
		t.Fatal("re-decoded build.Output.Extension mismatch")
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = pkgbDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Regression #47 case 3a: cross-package named slice type (type IntSlice []int)
// should be unwrapped to its underlying []int and classified as FieldSlicePrimitive.
func TestIntegrationCrossPackageSliceNamedType(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// pkga: external package with a slice type alias
	pkgaDir := filepath.Join(dir, "pkga")
	writeFixture(t, pkgaDir, "go.mod", "module example.com/test/pkga\n\ngo 1.26\n")
	writeFixture(t, pkgaDir, "pkga.go", `package pkga

type IntSlice []int
`)

	// pkgb: consumer using pkga.IntSlice
	pkgbDir := filepath.Join(dir, "pkgb")
	writeFixture(t, pkgbDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkgb",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/pkga v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/pkga => ../pkga",
		")",
		"",
	}, "\n"))
	writeFixture(t, pkgbDir, "pkgb.go", `package pkgb

import "example.com/test/pkga"

//go:generate tommy generate
type Stats struct {
	Buckets pkga.IntSlice `+"`"+`toml:"buckets"`+"`"+`
}
`)

	if err := Generate(pkgbDir, "pkgb.go"); err != nil {
		t.Fatalf("Generate pkgb: %v", err)
	}

	writeFixture(t, pkgbDir, "pkgb_test.go", `package pkgb

import "testing"

func TestCrossPackageSliceAliasRoundTrip(t *testing.T) {
	input := []byte("buckets = [10, 20, 30, 40]\n")

	doc, err := DecodeStats(input)
	if err != nil {
		t.Fatalf("DecodeStats: %v", err)
	}

	s := doc.Data()
	if len(s.Buckets) != 4 {
		t.Fatalf("Buckets length = %d, want 4", len(s.Buckets))
	}
	if s.Buckets[0] != 10 || s.Buckets[3] != 40 {
		t.Fatalf("Buckets = %v, want [10 20 30 40]", s.Buckets)
	}

	s.Buckets = append(s.Buckets, 50)
	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeStats(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if len(d2.Buckets) != 5 {
		t.Fatalf("re-decoded Buckets length = %d, want 5", len(d2.Buckets))
	}
	if d2.Buckets[4] != 50 {
		t.Fatalf("re-decoded Buckets[4] = %d, want 50", d2.Buckets[4])
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = pkgbDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Regression #47: map[string]CrossPackageStruct with all-exported fields should
// delegate to DecodeInto/EncodeFrom. This test verifies delegation compiles and
// round-trips correctly when the struct has a Validate() method (which would cause
// the generated code to reference the method if inlined incorrectly).
func TestIntegrationMapStringCrossPackageStructDelegation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// pkga: struct with Validate()
	pkgaDir := filepath.Join(dir, "pkga")
	writeFixture(t, pkgaDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkga",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, pkgaDir, "pkga.go", `package pkga

import "fmt"

//go:generate tommy generate
type Action struct {
	Command string `+"`"+`toml:"command"`+"`"+`
	Timeout int    `+"`"+`toml:"timeout"`+"`"+`
}

func (a Action) Validate() error {
	if a.Command == "" {
		return fmt.Errorf("command must not be empty")
	}
	return nil
}
`)

	if err := Generate(pkgaDir, "pkga.go"); err != nil {
		t.Fatalf("Generate pkga: %v", err)
	}

	// pkgb: consumer with map[string]pkga.Action
	pkgbDir := filepath.Join(dir, "pkgb")
	writeFixture(t, pkgbDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkgb",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/pkga v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/pkga => ../pkga",
		")",
		"",
	}, "\n"))
	writeFixture(t, pkgbDir, "pkgb.go", `package pkgb

import "example.com/test/pkga"

//go:generate tommy generate
type Config struct {
	Actions map[string]pkga.Action `+"`"+`toml:"actions,omitempty"`+"`"+`
}
`)

	if err := Generate(pkgbDir, "pkgb.go"); err != nil {
		t.Fatalf("Generate pkgb: %v", err)
	}

	writeFixture(t, pkgbDir, "pkgb_test.go", `package pkgb

import (
	"strings"
	"testing"
)

func TestMapCrossPackageDelegationRoundTrip(t *testing.T) {
	input := []byte("[actions.build]\ncommand = \"make\"\ntimeout = 30\n\n[actions.test]\ncommand = \"go test\"\ntimeout = 60\n")
	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}

	cfg := doc.Data()
	if len(cfg.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(cfg.Actions))
	}
	build := cfg.Actions["build"]
	if build.Command != "make" || build.Timeout != 30 {
		t.Fatalf("build = %+v, want {make 30}", build)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if len(doc2.Data().Actions) != 2 {
		t.Fatalf("re-decoded actions count = %d, want 2", len(doc2.Data().Actions))
	}
}

func TestMapCrossPackageDelegationValidates(t *testing.T) {
	// An empty command violates pkga.Action.Validate(); the delegated decode
	// (DecodeActionInto) must run Validate and surface the error rather than
	// silently accept the invalid nested cross-package value.
	_, err := DecodeConfig([]byte("[actions.build]\ntimeout = 30\n"))
	if err == nil {
		t.Fatal("expected validation error for empty command, got nil")
	}
	if !strings.Contains(err.Error(), "command must not be empty") {
		t.Fatalf("error = %v, want validation failure", err)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = pkgbDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Regression #105: a delegated map nested inside a same-package map-struct entry
// (map[string]Wrapper where Wrapper has map[string]pkga.Thing) must generate
// compilable code. The map-struct entry decoder and the inner delegated-map
// decoder both used a hardcoded `entry`/`_mk` local; nested, the inner `entry`
// shadowed the outer, so the inner map's target expression (entry.Things)
// rebound to the wrong type and the generated code failed to compile. Surfaced
// by the cross-package round-trip fuzzer.
// Regression #105 (bug #2): a nil *pointer-struct field whose own field is a
// delegated slice, sitting beside a sibling delegated slice that DOES have
// entries, must stay nil on decode — the sibling's array-table entries must not
// be mis-scooped into the absent pointer-struct's slice (which would also flip
// the nil-guard to non-nil). Surfaced by the cross-package round-trip fuzzer at
// depth-4 nesting.
func TestIntegrationNilPtrDelegatedSliceNoPhantom(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	pkgaDir := filepath.Join(dir, "pkga")
	writeFixture(t, pkgaDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkga", "", "go 1.26", "",
		"require github.com/amarbel-llc/tommy v0.0.0", "",
		"replace github.com/amarbel-llc/tommy => " + repoRoot, "",
	}, "\n"))
	writeFixture(t, pkgaDir, "pkga.go", `package pkga

//go:generate tommy generate
type Thing struct {
	Name string `+"`"+`toml:"name"`+"`"+`
}
`)
	if err := Generate(pkgaDir, "pkga.go"); err != nil {
		t.Fatalf("Generate pkga: %v", err)
	}

	pkgbDir := filepath.Join(dir, "pkgb")
	writeFixture(t, pkgbDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkgb", "", "go 1.26", "",
		"require (", "\tgithub.com/amarbel-llc/tommy v0.0.0", "\texample.com/test/pkga v0.0.0", ")", "",
		"replace (", "\tgithub.com/amarbel-llc/tommy => " + repoRoot, "\texample.com/test/pkga => ../pkga", ")", "",
	}, "\n"))
	writeFixture(t, pkgbDir, "pkgb.go", `package pkgb

import "example.com/test/pkga"

//go:generate tommy generate
type Config struct {
	Outers []Outer `+"`"+`toml:"outers"`+"`"+`
}

type Outer struct {
	Items []pkga.Thing `+"`"+`toml:"items"`+"`"+`
	Extra *Extra       `+"`"+`toml:"extra,omitempty"`+"`"+`
}

type Extra struct {
	Items []pkga.Thing `+"`"+`toml:"items"`+"`"+`
}
`)
	if err := Generate(pkgbDir, "pkgb.go"); err != nil {
		t.Fatalf("Generate pkgb: %v", err)
	}

	writeFixture(t, pkgbDir, "pkgb_test.go", `package pkgb

import (
	"reflect"
	"testing"

	"example.com/test/pkga"
)

func TestNilPtrDelegatedSliceNoPhantom(t *testing.T) {
	d, err := DecodeConfig([]byte(""))
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	want := []Outer{{
		Items: []pkga.Thing{{Name: "a"}, {Name: "b"}},
		Extra: nil,
	}}
	d.Data().Outers = want
	out, err := d.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	d2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v\n%s", err, out)
	}
	if !reflect.DeepEqual(d2.Data().Outers, want) {
		t.Fatalf("round-trip mismatch:\ngot:  %#v\nwant: %#v\ntoml:\n%s", d2.Data().Outers, want, out)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = pkgbDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationDelegatedMapInStructMap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	pkgaDir := filepath.Join(dir, "pkga")
	writeFixture(t, pkgaDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkga", "", "go 1.26", "",
		"require github.com/amarbel-llc/tommy v0.0.0", "",
		"replace github.com/amarbel-llc/tommy => " + repoRoot, "",
	}, "\n"))
	writeFixture(t, pkgaDir, "pkga.go", `package pkga

//go:generate tommy generate
type Thing struct {
	Name string `+"`"+`toml:"name"`+"`"+`
}
`)
	if err := Generate(pkgaDir, "pkga.go"); err != nil {
		t.Fatalf("Generate pkga: %v", err)
	}

	pkgbDir := filepath.Join(dir, "pkgb")
	writeFixture(t, pkgbDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkgb", "", "go 1.26", "",
		"require (", "\tgithub.com/amarbel-llc/tommy v0.0.0", "\texample.com/test/pkga v0.0.0", ")", "",
		"replace (", "\tgithub.com/amarbel-llc/tommy => " + repoRoot, "\texample.com/test/pkga => ../pkga", ")", "",
	}, "\n"))
	writeFixture(t, pkgbDir, "pkgb.go", `package pkgb

import "example.com/test/pkga"

//go:generate tommy generate
type Config struct {
	Wrappers map[string]Wrapper `+"`"+`toml:"wrappers"`+"`"+`
}

type Wrapper struct {
	Things map[string]pkga.Thing `+"`"+`toml:"things"`+"`"+`
}
`)
	if err := Generate(pkgbDir, "pkgb.go"); err != nil {
		t.Fatalf("Generate pkgb: %v", err)
	}

	writeFixture(t, pkgbDir, "pkgb_test.go", `package pkgb

import (
	"reflect"
	"testing"

	"example.com/test/pkga"
)

func TestDelegatedMapInStructMapRoundTrip(t *testing.T) {
	// Round-trip from a value (as the fuzzer does) so the encoder emits the
	// intermediate [wrappers.<k>] headers; hand-written TOML omitting them is a
	// separate implicit-super-table concern, not what #105 is about.
	d, err := DecodeConfig([]byte(""))
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	want := map[string]Wrapper{
		"w1": {Things: map[string]pkga.Thing{"t1": {Name: "a"}, "t2": {Name: "b"}}},
		"w2": {Things: map[string]pkga.Thing{"t3": {Name: "c"}}},
	}
	d.Data().Wrappers = want
	out, err := d.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	d2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v\n%s", err, out)
	}
	if !reflect.DeepEqual(d2.Data().Wrappers, want) {
		t.Fatalf("round-trip mismatch:\ngot:  %#v\nwant: %#v\ntoml:\n%s", d2.Data().Wrappers, want, out)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = pkgbDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Regression #103: a map key needing TOML quoting (a dot or a space) used as a
// delegated map[string]CrossPackageStruct entry must serialize its sub-table
// header quoted ([actions."build.fast"]) and decode back as one segment — not
// nest as actions→build→fast. The round-trip fuzzer covers same-package maps but
// not cross-package delegation, so this pins the delegated path explicitly.
func TestIntegrationMapStringCrossPackageStructQuotedKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	pkgaDir := filepath.Join(dir, "pkga")
	writeFixture(t, pkgaDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkga",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, pkgaDir, "pkga.go", `package pkga

//go:generate tommy generate
type Action struct {
	Command string `+"`"+`toml:"command"`+"`"+`
	Timeout int    `+"`"+`toml:"timeout"`+"`"+`
}
`)

	if err := Generate(pkgaDir, "pkga.go"); err != nil {
		t.Fatalf("Generate pkga: %v", err)
	}

	pkgbDir := filepath.Join(dir, "pkgb")
	writeFixture(t, pkgbDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkgb",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/pkga v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/pkga => ../pkga",
		")",
		"",
	}, "\n"))
	writeFixture(t, pkgbDir, "pkgb.go", `package pkgb

import "example.com/test/pkga"

//go:generate tommy generate
type Config struct {
	Actions map[string]pkga.Action `+"`"+`toml:"actions,omitempty"`+"`"+`
}
`)

	if err := Generate(pkgbDir, "pkgb.go"); err != nil {
		t.Fatalf("Generate pkgb: %v", err)
	}

	writeFixture(t, pkgbDir, "pkgb_test.go", `package pkgb

import (
	"strings"
	"testing"
)

func TestMapCrossPackageQuotedKeyRoundTrip(t *testing.T) {
	// Two keys that require quoting: one with a dot, one with a space.
	input := []byte("[actions.\"build.fast\"]\ncommand = \"make\"\ntimeout = 30\n\n[actions.\"run all\"]\ncommand = \"go test\"\ntimeout = 60\n")
	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}

	cfg := doc.Data()
	if len(cfg.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d: %v", len(cfg.Actions), cfg.Actions)
	}
	if got := cfg.Actions["build.fast"]; got.Command != "make" || got.Timeout != 30 {
		t.Fatalf("actions[\"build.fast\"] = %+v, want {make 30}", got)
	}
	if got := cfg.Actions["run all"]; got.Command != "go test" || got.Timeout != 60 {
		t.Fatalf("actions[\"run all\"] = %+v, want {go test 60}", got)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// The dotted key must be re-emitted quoted as one segment, not flattened.
	if !strings.Contains(string(out), "[actions.\"build.fast\"]") {
		t.Fatalf("encoded output missing quoted header [actions.\"build.fast\"]:\n%s", out)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if got := doc2.Data().Actions["build.fast"]; got.Command != "make" {
		t.Fatalf("re-decoded actions[\"build.fast\"] = %+v, want command=make", got)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = pkgbDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// Regression #47: cross-package named map type alias (type ScriptMap map[string]Struct)
// used as a direct field goes through classifyType → *types.Map, which has no
// struct value handling.
func TestIntegrationCrossPackageMapTypeAlias(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// pkga: named map type alias
	pkgaDir := filepath.Join(dir, "pkga")
	writeFixture(t, pkgaDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkga",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, pkgaDir, "pkga.go", `package pkga

//go:generate tommy generate
type Script struct {
	Command string `+"`"+`toml:"command"`+"`"+`
}

type ScriptMap map[string]Script
`)

	if err := Generate(pkgaDir, "pkga.go"); err != nil {
		t.Fatalf("Generate pkga: %v", err)
	}

	// pkgb: consumer using pkga.ScriptMap as a field type
	pkgbDir := filepath.Join(dir, "pkgb")
	writeFixture(t, pkgbDir, "go.mod", strings.Join([]string{
		"module example.com/test/pkgb",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/pkga v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/pkga => ../pkga",
		")",
		"",
	}, "\n"))
	writeFixture(t, pkgbDir, "pkgb.go", `package pkgb

import "example.com/test/pkga"

//go:generate tommy generate
type Config struct {
	Actions pkga.ScriptMap `+"`"+`toml:"actions,omitempty"`+"`"+`
}
`)

	if err := Generate(pkgbDir, "pkgb.go"); err != nil {
		t.Fatalf("Generate pkgb: %v", err)
	}

	writeFixture(t, pkgbDir, "pkgb_test.go", `package pkgb

import "testing"

func TestCrossPackageMapTypeAliasRoundTrip(t *testing.T) {
	input := []byte("[actions.build]\ncommand = \"make\"\n\n[actions.test]\ncommand = \"go test\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}

	cfg := doc.Data()
	if len(cfg.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(cfg.Actions))
	}
	if cfg.Actions["build"].Command != "make" {
		t.Fatalf("Actions[build].Command = %q, want \"make\"", cfg.Actions["build"].Command)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if len(doc2.Data().Actions) != 2 {
		t.Fatalf("re-decoded actions count = %d, want 2", len(doc2.Data().Actions))
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = pkgbDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// gh#48: omitempty lost when inlining cross-package struct fields.
// When Inner has a TextMarshaler field with omitempty and the zero value,
// the encoder should omit the key. Instead it writes an empty string,
// which then fails on decode via UnmarshalText("").
func TestIntegrationCrossPackageOmitemptyTextMarshaler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	// "inner" package: struct with a TextMarshaler field tagged omitempty
	innerDir := filepath.Join(dir, "inner")
	writeFixture(t, innerDir, "go.mod", strings.Join([]string{
		"module example.com/test/inner",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, innerDir, "inner.go", `package inner

import "fmt"

//go:generate tommy generate
type Inner struct {
	Name CustomType `+"`"+`toml:"name,omitempty"`+"`"+`
}

type CustomType struct{ Val string }

func (c CustomType) MarshalText() ([]byte, error)  { return []byte(c.Val), nil }
func (c *CustomType) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		return fmt.Errorf("empty value not allowed")
	}
	c.Val = string(b)
	return nil
}
`)

	if err := Generate(innerDir, "inner.go"); err != nil {
		t.Fatalf("Generate inner: %v", err)
	}

	// "outer" package: uses inner.Inner as a named field
	outerDir := filepath.Join(dir, "outer")
	writeFixture(t, outerDir, "go.mod", strings.Join([]string{
		"module example.com/test/outer",
		"",
		"go 1.26",
		"",
		"require (",
		"\tgithub.com/amarbel-llc/tommy v0.0.0",
		"\texample.com/test/inner v0.0.0",
		")",
		"",
		"replace (",
		"\tgithub.com/amarbel-llc/tommy => " + repoRoot,
		"\texample.com/test/inner => ../inner",
		")",
		"",
	}, "\n"))
	writeFixture(t, outerDir, "outer.go", `package outer

import "example.com/test/inner"

//go:generate tommy generate
type Outer struct {
	Label string      `+"`"+`toml:"label"`+"`"+`
	Data  inner.Inner `+"`"+`toml:"data"`+"`"+`
}
`)

	if err := Generate(outerDir, "outer.go"); err != nil {
		t.Fatalf("Generate outer: %v", err)
	}

	writeFixture(t, outerDir, "outer_test.go", `package outer

import (
	"strings"
	"testing"
)

func TestCrossPackageOmitemptyZeroValue(t *testing.T) {
	// Only label is set; data.name is zero-valued and omitempty.
	// The encoder should omit data.name entirely.
	input := []byte("label = \"hello\"\n\n[data]\n")

	doc, err := DecodeOuter(input)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Data().Label != "hello" {
		t.Fatalf("Label = %q, want \"hello\"", doc.Data().Label)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	if strings.Contains(string(out), "name") {
		t.Fatalf("zero-value omitempty TextMarshaler field leaked into output:\n%s", string(out))
	}

	// Round-trip: re-decode should succeed (no UnmarshalText("") error)
	doc2, err := DecodeOuter(out)
	if err != nil {
		t.Fatalf("re-decode failed (omitempty not respected): %v", err)
	}
	if doc2.Data().Label != "hello" {
		t.Fatalf("re-decoded Label = %q, want \"hello\"", doc2.Data().Label)
	}
}

func TestCrossPackageOmitemptyNonZeroValue(t *testing.T) {
	// data.name is set — should survive round-trip.
	input := []byte("label = \"hello\"\n\n[data]\nname = \"world\"\n")

	doc, err := DecodeOuter(input)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Data().Data.Name.Val != "world" {
		t.Fatalf("Data.Name.Val = %q, want \"world\"", doc.Data().Data.Name.Val)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(out), "name = \"world\"") {
		t.Fatalf("expected name in output, got:\n%s", string(out))
	}

	doc2, err := DecodeOuter(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if doc2.Data().Data.Name.Val != "world" {
		t.Fatalf("re-decoded Name.Val = %q, want \"world\"", doc2.Data().Data.Name.Val)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = outerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// gh#48: same-package TextMarshaler with omitempty.
// Proves the bug is in emitEncodeField, not the cross-package delegation path.
func TestIntegrationSamePackageOmitemptyTextMarshaler(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/test/samepkg",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))
	writeFixture(t, dir, "config.go", `package samepkg

import "fmt"

//go:generate tommy generate
type Config struct {
	Label string     `+"`"+`toml:"label"`+"`"+`
	Kind  CustomType `+"`"+`toml:"kind,omitempty"`+"`"+`
}

type CustomType struct{ Val string }

func (c CustomType) MarshalText() ([]byte, error)  { return []byte(c.Val), nil }
func (c *CustomType) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		return fmt.Errorf("empty value not allowed")
	}
	c.Val = string(b)
	return nil
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "config_test.go", `package samepkg

import (
	"strings"
	"testing"
)

func TestOmitemptyTextMarshalerZeroValue(t *testing.T) {
	// kind is zero-valued and omitempty — should not appear in output.
	input := []byte("label = \"hello\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	if strings.Contains(string(out), "kind") {
		t.Fatalf("zero-value omitempty TextMarshaler field leaked into output:\n%s", string(out))
	}

	// Round-trip: re-decode should succeed (no UnmarshalText("") error)
	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode failed (omitempty not respected): %v", err)
	}
	if doc2.Data().Label != "hello" {
		t.Fatalf("re-decoded Label = %q, want \"hello\"", doc2.Data().Label)
	}
}

func TestOmitemptyTextMarshalerNonZeroValue(t *testing.T) {
	// kind is set — should survive round-trip.
	input := []byte("label = \"hello\"\nkind = \"widget\"\n")

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Data().Kind.Val != "widget" {
		t.Fatalf("Kind.Val = %q, want \"widget\"", doc.Data().Kind.Val)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(out), "kind = \"widget\"") {
		t.Fatalf("expected kind in output, got:\n%s", string(out))
	}

	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if doc2.Data().Kind.Val != "widget" {
		t.Fatalf("re-decoded Kind.Val = %q, want \"widget\"", doc2.Data().Kind.Val)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEncodeFromNilPointerStruct(t *testing.T) {
	// Issue #49: pointer-to-struct fields silently dropped when encoding from
	// scratch (nil input). Verifies that DecodeX(nil) followed by populating
	// *SubStruct and Encode() round-trips correctly.
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/issue49",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "v2.go", `package issue49

//go:generate tommy generate
type V2 struct {
	Abbreviations    *abbreviationsV2 `+"`"+`toml:"abbreviations"`+"`"+`
	PrintBlobDigests *bool            `+"`"+`toml:"print-blob_digests"`+"`"+`
}

type abbreviationsV2 struct {
	ZettelIds *bool `+"`"+`toml:"zettel_ids"`+"`"+`
	MarklIds  *bool `+"`"+`toml:"merkle_ids"`+"`"+`
}
`)

	if err := Generate(dir, "v2.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "issue49_test.go", `package issue49

import (
	"testing"
)

func TestEncodeFromScratchPointerToStruct(t *testing.T) {
	// Issue #49: Decode(nil) creates an empty document. Populating a
	// pointer-to-struct field and encoding should produce the sub-table,
	// not silently drop it.
	doc, err := DecodeV2(nil)
	if err != nil {
		t.Fatalf("DecodeV2(nil): %v", err)
	}

	zettelIds := true
	marklIds := true
	printDigests := false
	doc.Data().Abbreviations = &abbreviationsV2{
		ZettelIds: &zettelIds,
		MarklIds:  &marklIds,
	}
	doc.Data().PrintBlobDigests = &printDigests

	encoded, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Round-trip verification
	doc2, err := DecodeV2(encoded)
	if err != nil {
		t.Fatalf("re-decode failed: %v", err)
	}

	d := doc2.Data()
	if d.Abbreviations == nil {
		t.Fatal("Abbreviations lost during encode from scratch")
	}
	if d.Abbreviations.ZettelIds == nil || !*d.Abbreviations.ZettelIds {
		t.Fatal("ZettelIds lost or wrong")
	}
	if d.Abbreviations.MarklIds == nil || !*d.Abbreviations.MarklIds {
		t.Fatal("MarklIds lost or wrong")
	}
	if d.PrintBlobDigests == nil {
		t.Fatal("PrintBlobDigests pointer lost during encode from scratch")
	}
	if *d.PrintBlobDigests != false {
		t.Fatalf("PrintBlobDigests wrong: got %v, want false", *d.PrintBlobDigests)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestEncodeFromScratch", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEncodeFromScratchPrimitiveAfterStruct(t *testing.T) {
	// Root-level primitive fields placed after a non-pointer struct field
	// should not end up inside the struct's table section.
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/primafterstruct",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package primafterstruct

//go:generate tommy generate
type Config struct {
	Defaults Defaults `+"`"+`toml:"defaults"`+"`"+`
	Name     string   `+"`"+`toml:"name"`+"`"+`
	Version  int      `+"`"+`toml:"version"`+"`"+`
}

type Defaults struct {
	Type    string `+"`"+`toml:"type"`+"`"+`
	Enabled bool   `+"`"+`toml:"enabled"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "config_test.go", `package primafterstruct

import (
	"testing"
)

func TestEncodeFromScratchPrimitiveAfterStruct(t *testing.T) {
	doc, err := DecodeConfig(nil)
	if err != nil {
		t.Fatal(err)
	}

	d := doc.Data()
	d.Defaults = Defaults{Type: "txt", Enabled: true}
	d.Name = "myapp"
	d.Version = 3

	encoded, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeConfig(encoded)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}

	d2 := doc2.Data()
	if d2.Name != "myapp" {
		t.Fatalf("Name = %q, want \"myapp\" (may have landed inside [defaults] table)", d2.Name)
	}
	if d2.Version != 3 {
		t.Fatalf("Version = %d, want 3 (may have landed inside [defaults] table)", d2.Version)
	}
	if d2.Defaults.Type != "txt" {
		t.Fatalf("Defaults.Type = %q, want \"txt\"", d2.Defaults.Type)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestEncodeFromScratch", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEncodeFromScratchPrimitiveAfterSliceStruct(t *testing.T) {
	// Root-level primitive after an array-of-tables (slice of structs).
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/primafterslice",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package primafterslice

//go:generate tommy generate
type Config struct {
	Servers []Server `+"`"+`toml:"servers"`+"`"+`
	Owner   string   `+"`"+`toml:"owner"`+"`"+`
}

type Server struct {
	Name string `+"`"+`toml:"name"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "config_test.go", `package primafterslice

import (
	"testing"
)

func TestEncodeFromScratchPrimitiveAfterSliceStruct(t *testing.T) {
	doc, err := DecodeConfig(nil)
	if err != nil {
		t.Fatal(err)
	}

	d := doc.Data()
	d.Servers = []Server{{Name: "alpha", Port: 8080}}
	d.Owner = "admin"

	encoded, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeConfig(encoded)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}

	if doc2.Data().Owner != "admin" {
		t.Fatalf("Owner = %q, want \"admin\" (may have landed inside [[servers]])", doc2.Data().Owner)
	}
	if len(doc2.Data().Servers) != 1 || doc2.Data().Servers[0].Name != "alpha" {
		t.Fatalf("Servers lost or corrupted: %+v", doc2.Data().Servers)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestEncodeFromScratch", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEncodeFromScratchMultipleStructsThenPrimitive(t *testing.T) {
	// Two struct fields followed by a primitive — primitive must not land
	// inside the second struct's table.
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/multistructprim",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package multistructprim

//go:generate tommy generate
type Config struct {
	Database Database `+"`"+`toml:"database"`+"`"+`
	Logging  *Logging `+"`"+`toml:"logging"`+"`"+`
	Debug    bool     `+"`"+`toml:"debug"`+"`"+`
}

type Database struct {
	Host string `+"`"+`toml:"host"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}

type Logging struct {
	Level  string `+"`"+`toml:"level"`+"`"+`
	Format string `+"`"+`toml:"format"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "config_test.go", `package multistructprim

import (
	"testing"
)

func TestEncodeFromScratchMultipleStructsThenPrimitive(t *testing.T) {
	doc, err := DecodeConfig(nil)
	if err != nil {
		t.Fatal(err)
	}

	d := doc.Data()
	d.Database = Database{Host: "localhost", Port: 5432}
	d.Logging = &Logging{Level: "debug", Format: "json"}
	d.Debug = true

	encoded, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeConfig(encoded)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}

	d2 := doc2.Data()
	if !d2.Debug {
		t.Fatal("Debug lost (may have landed inside [logging] table)")
	}
	if d2.Database.Host != "localhost" {
		t.Fatalf("Database.Host = %q, want \"localhost\"", d2.Database.Host)
	}
	if d2.Logging == nil || d2.Logging.Level != "debug" {
		t.Fatal("Logging lost or corrupted")
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestEncodeFromScratch", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

func TestIntegrationEncodeFromScratchNestedSubTableOrdering(t *testing.T) {
	// Nested struct inside a struct: inner key-values must not leak into a
	// sub-sub-table section.
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/nestedorder",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package nestedorder

//go:generate tommy generate
type Config struct {
	Server Server `+"`"+`toml:"server"`+"`"+`
}

type Server struct {
	TLS  TLS    `+"`"+`toml:"tls"`+"`"+`
	Name string `+"`"+`toml:"name"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}

type TLS struct {
	Cert string `+"`"+`toml:"cert"`+"`"+`
	Key  string `+"`"+`toml:"key"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "config_test.go", `package nestedorder

import (
	"testing"
)

func TestEncodeFromScratchNestedSubTableOrdering(t *testing.T) {
	doc, err := DecodeConfig(nil)
	if err != nil {
		t.Fatal(err)
	}

	d := doc.Data()
	d.Server = Server{
		TLS:  TLS{Cert: "/path/cert.pem", Key: "/path/key.pem"},
		Name: "prod",
		Port: 443,
	}

	encoded, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}

	doc2, err := DecodeConfig(encoded)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}

	d2 := doc2.Data()
	if d2.Server.Name != "prod" {
		t.Fatalf("Server.Name = %q, want \"prod\" (may have landed inside [server.tls])", d2.Server.Name)
	}
	if d2.Server.Port != 443 {
		t.Fatalf("Server.Port = %d, want 443 (may have landed inside [server.tls])", d2.Server.Port)
	}
	if d2.Server.TLS.Cert != "/path/cert.pem" {
		t.Fatalf("Server.TLS.Cert = %q, want \"/path/cert.pem\"", d2.Server.TLS.Cert)
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "-run", "TestEncodeFromScratch", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test failed:\n%s", output)
	}
}

// --- Compositional nesting matrix tests (issue #56) ---

func nestingSetup(t *testing.T) (string, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}
	return dir, repoRoot
}

func nestingGoMod(t *testing.T, dir, repoRoot, mod string) {
	t.Helper()
	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/" + mod, "", "go 1.26", "",
		"require github.com/amarbel-llc/tommy v0.0.0", "",
		"replace github.com/amarbel-llc/tommy => " + repoRoot, "",
	}, "\n"))
}

func nestingRun(t *testing.T, dir string) {
	t.Helper()
	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, out)
	}
}

// Struct > []Struct > primitive + map[string]string
func TestNestingStructSliceStructLeaves(t *testing.T) {
	dir, root := nestingSetup(t)
	nestingGoMod(t, dir, root, "n1")
	writeFixture(t, dir, "config.go", `package n1
//go:generate tommy generate
type Config struct {
	Database Database `+"`toml:\"database\"`"+`
}
type Database struct {
	Replicas []Replica `+"`toml:\"replicas\"`"+`
}
type Replica struct {
	Host   string            `+"`toml:\"host\"`"+`
	Port   int               `+"`toml:\"port\"`"+`
	Labels map[string]string `+"`toml:\"labels\"`"+`
}
`)
	writeFixture(t, dir, "config_test.go", `package n1
import "testing"
func TestDecode(t *testing.T) {
	input := []byte("[database]\n\n[[database.replicas]]\nhost = \"primary\"\nport = 5432\n\n[database.replicas.labels]\nrole = \"primary\"\nregion = \"us\"\n\n[[database.replicas]]\nhost = \"secondary\"\nport = 5433\n\n[database.replicas.labels]\nrole = \"secondary\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if len(d.Database.Replicas) != 2 { t.Fatalf("len=%d", len(d.Database.Replicas)) }
	if d.Database.Replicas[0].Host != "primary" { t.Fatalf("host=%q", d.Database.Replicas[0].Host) }
	if d.Database.Replicas[0].Port != 5432 { t.Fatalf("port=%d", d.Database.Replicas[0].Port) }
	if d.Database.Replicas[0].Labels["role"] != "primary" { t.Fatalf("label=%q", d.Database.Replicas[0].Labels["role"]) }
	if d.Database.Replicas[1].Labels["role"] != "secondary" { t.Fatalf("label=%q", d.Database.Replicas[1].Labels["role"]) }
	out, err := doc.Encode()
	if err != nil { t.Fatal(err) }
	doc2, err := DecodeConfig(out)
	if err != nil { t.Fatal(err) }
	if doc2.Data().Database.Replicas[0].Labels["region"] != "us" { t.Fatal("round-trip lost region") }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
`)
	nestingRun(t, dir)
}

// *Struct > []Struct > primitive + map[string]string (explicit + implicit parent)
func TestNestingPointerStructSliceStructLeaves(t *testing.T) {
	dir, root := nestingSetup(t)
	nestingGoMod(t, dir, root, "n2")
	writeFixture(t, dir, "config.go", `package n2
//go:generate tommy generate
type Config struct {
	Exec *ExecConfig `+"`toml:\"exec\"`"+`
}
type ExecConfig struct {
	Allow []Rule `+"`toml:\"allow\"`"+`
	Deny  []Rule `+"`toml:\"deny\"`"+`
}
type Rule struct {
	Binary string            `+"`toml:\"binary\"`"+`
	Env    map[string]string `+"`toml:\"env\"`"+`
}
`)
	writeFixture(t, dir, "config_test.go", `package n2
import "testing"
func TestExplicitTable(t *testing.T) {
	input := []byte("[exec]\n\n[[exec.allow]]\nbinary = \"go\"\n\n[exec.allow.env]\nGOPATH = \"/go\"\n\n[[exec.deny]]\nbinary = \"rm\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Exec == nil { t.Fatal("nil") }
	if len(d.Exec.Allow) != 1 { t.Fatalf("allow=%d", len(d.Exec.Allow)) }
	if d.Exec.Allow[0].Binary != "go" { t.Fatalf("binary=%q", d.Exec.Allow[0].Binary) }
	if d.Exec.Allow[0].Env["GOPATH"] != "/go" { t.Fatalf("env=%q", d.Exec.Allow[0].Env["GOPATH"]) }
	if len(d.Exec.Deny) != 1 { t.Fatalf("deny=%d", len(d.Exec.Deny)) }
	out, err := doc.Encode()
	if err != nil { t.Fatal(err) }
	doc2, err := DecodeConfig(out)
	if err != nil { t.Fatal(err) }
	if doc2.Data().Exec.Allow[0].Env["GOPATH"] != "/go" { t.Fatal("round-trip env lost") }
}
func TestImplicitTable(t *testing.T) {
	input := []byte("[[exec.allow]]\nbinary = \"git\"\n\n[[exec.allow]]\nbinary = \"go\"\n\n[[exec.deny]]\nbinary = \"sudo\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Exec == nil { t.Fatal("nil with implicit table") }
	if len(d.Exec.Allow) != 2 { t.Fatalf("allow=%d", len(d.Exec.Allow)) }
	if len(d.Exec.Deny) != 1 { t.Fatalf("deny=%d", len(d.Exec.Deny)) }
}
`)
	nestingRun(t, dir)
}

// []Struct > Struct > primitive
func TestNestingSliceStructStruct(t *testing.T) {
	dir, root := nestingSetup(t)
	nestingGoMod(t, dir, root, "n3")
	writeFixture(t, dir, "config.go", `package n3
//go:generate tommy generate
type Config struct {
	Servers []Server `+"`toml:\"servers\"`"+`
}
type Server struct {
	Name     string   `+"`toml:\"name\"`"+`
	Settings Settings `+"`toml:\"settings\"`"+`
}
type Settings struct {
	MaxConns int    `+"`toml:\"max_conns\"`"+`
	Mode     string `+"`toml:\"mode\"`"+`
}
`)
	writeFixture(t, dir, "config_test.go", `package n3
import "testing"
func TestDecode(t *testing.T) {
	input := []byte("[[servers]]\nname = \"alpha\"\n\n[servers.settings]\nmax_conns = 100\nmode = \"rw\"\n\n[[servers]]\nname = \"beta\"\n\n[servers.settings]\nmax_conns = 50\nmode = \"ro\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if len(d.Servers) != 2 { t.Fatalf("len=%d", len(d.Servers)) }
	if d.Servers[0].Settings.MaxConns != 100 { t.Fatalf("max=%d", d.Servers[0].Settings.MaxConns) }
	if d.Servers[1].Settings.Mode != "ro" { t.Fatalf("mode=%q", d.Servers[1].Settings.Mode) }
	out, err := doc.Encode()
	if err != nil { t.Fatal(err) }
	doc2, err := DecodeConfig(out)
	if err != nil { t.Fatal(err) }
	if doc2.Data().Servers[0].Settings.MaxConns != 100 { t.Fatal("round-trip MaxConns") }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
`)
	nestingRun(t, dir)
}

// []Struct > *Struct > primitive
func TestNestingSliceStructPointerStruct(t *testing.T) {
	dir, root := nestingSetup(t)
	nestingGoMod(t, dir, root, "n4")
	writeFixture(t, dir, "config.go", `package n4
//go:generate tommy generate
type Config struct {
	Jobs []Job `+"`toml:\"jobs\"`"+`
}
type Job struct {
	Name    string   `+"`toml:\"name\"`"+`
	Timeout *Timeout `+"`toml:\"timeout\"`"+`
}
type Timeout struct {
	Seconds int  `+"`toml:\"seconds\"`"+`
	Retry   bool `+"`toml:\"retry\"`"+`
}
`)
	writeFixture(t, dir, "config_test.go", `package n4
import "testing"
func TestDecode(t *testing.T) {
	input := []byte("[[jobs]]\nname = \"build\"\n\n[jobs.timeout]\nseconds = 300\nretry = true\n\n[[jobs]]\nname = \"lint\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if len(d.Jobs) != 2 { t.Fatalf("len=%d", len(d.Jobs)) }
	if d.Jobs[0].Timeout == nil { t.Fatal("nil") }
	if d.Jobs[0].Timeout.Seconds != 300 { t.Fatalf("sec=%d", d.Jobs[0].Timeout.Seconds) }
	if !d.Jobs[0].Timeout.Retry { t.Fatal("retry") }
	if d.Jobs[1].Timeout != nil { t.Fatal("should be nil") }
	out, err := doc.Encode()
	if err != nil { t.Fatal(err) }
	doc2, err := DecodeConfig(out)
	if err != nil { t.Fatal(err) }
	if doc2.Data().Jobs[0].Timeout.Seconds != 300 { t.Fatal("round-trip") }
	if doc2.Data().Jobs[1].Timeout != nil { t.Fatal("round-trip nil") }
}
`)
	nestingRun(t, dir)
}

// []Struct > map[string]string
func TestNestingSliceStructMapStringString(t *testing.T) {
	dir, root := nestingSetup(t)
	nestingGoMod(t, dir, root, "n5")
	writeFixture(t, dir, "config.go", `package n5
//go:generate tommy generate
type Config struct {
	Services []Service `+"`toml:\"services\"`"+`
}
type Service struct {
	Name   string            `+"`toml:\"name\"`"+`
	Labels map[string]string `+"`toml:\"labels\"`"+`
}
`)
	writeFixture(t, dir, "config_test.go", `package n5
import "testing"
func TestDecode(t *testing.T) {
	input := []byte("[[services]]\nname = \"api\"\n\n[services.labels]\nenv = \"prod\"\nteam = \"backend\"\n\n[[services]]\nname = \"worker\"\n\n[services.labels]\nenv = \"staging\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if len(d.Services) != 2 { t.Fatalf("len=%d", len(d.Services)) }
	if d.Services[0].Labels["env"] != "prod" { t.Fatalf("env=%q", d.Services[0].Labels["env"]) }
	if d.Services[0].Labels["team"] != "backend" { t.Fatalf("team=%q", d.Services[0].Labels["team"]) }
	if d.Services[1].Labels["env"] != "staging" { t.Fatalf("env=%q", d.Services[1].Labels["env"]) }
	out, err := doc.Encode()
	if err != nil { t.Fatal(err) }
	doc2, err := DecodeConfig(out)
	if err != nil { t.Fatal(err) }
	if doc2.Data().Services[0].Labels["team"] != "backend" { t.Fatal("round-trip") }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
`)
	nestingRun(t, dir)
}

// map[string]Struct > []Struct > primitive
func TestNestingMapStringStructSliceStruct(t *testing.T) {
	dir, root := nestingSetup(t)
	nestingGoMod(t, dir, root, "n6")
	writeFixture(t, dir, "config.go", `package n6
//go:generate tommy generate
type Config struct {
	Pipelines map[string]Pipeline `+"`toml:\"pipelines\"`"+`
}
type Pipeline struct {
	Steps []Step `+"`toml:\"steps\"`"+`
}
type Step struct {
	Name    string `+"`toml:\"name\"`"+`
	Command string `+"`toml:\"command\"`"+`
}
`)
	writeFixture(t, dir, "config_test.go", `package n6
import "testing"
func TestDecode(t *testing.T) {
	input := []byte("[pipelines.ci]\n\n[[pipelines.ci.steps]]\nname = \"build\"\ncommand = \"make\"\n\n[[pipelines.ci.steps]]\nname = \"test\"\ncommand = \"make test\"\n\n[pipelines.deploy]\n\n[[pipelines.deploy.steps]]\nname = \"push\"\ncommand = \"docker push\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if len(d.Pipelines) != 2 { t.Fatalf("len=%d", len(d.Pipelines)) }
	ci := d.Pipelines["ci"]
	if len(ci.Steps) != 2 { t.Fatalf("ci.steps=%d", len(ci.Steps)) }
	if ci.Steps[0].Name != "build" { t.Fatalf("name=%q", ci.Steps[0].Name) }
	deploy := d.Pipelines["deploy"]
	if len(deploy.Steps) != 1 { t.Fatalf("deploy.steps=%d", len(deploy.Steps)) }
	out, err := doc.Encode()
	if err != nil { t.Fatal(err) }
	doc2, err := DecodeConfig(out)
	if err != nil { t.Fatal(err) }
	if doc2.Data().Pipelines["ci"].Steps[1].Command != "make test" { t.Fatal("round-trip") }
}
`)
	nestingRun(t, dir)
}

// map[string]Struct > *Struct > primitive
func TestNestingMapStringStructPointerStruct(t *testing.T) {
	dir, root := nestingSetup(t)
	nestingGoMod(t, dir, root, "n7")
	writeFixture(t, dir, "config.go", `package n7
//go:generate tommy generate
type Config struct {
	Targets map[string]Target `+"`toml:\"targets\"`"+`
}
type Target struct {
	Host string      `+"`toml:\"host\"`"+`
	Auth *AuthConfig `+"`toml:\"auth\"`"+`
}
type AuthConfig struct {
	User  string `+"`toml:\"user\"`"+`
	Token string `+"`toml:\"token\"`"+`
}
`)
	writeFixture(t, dir, "config_test.go", `package n7
import "testing"
func TestDecode(t *testing.T) {
	input := []byte("[targets.prod]\nhost = \"prod.example.com\"\n\n[targets.prod.auth]\nuser = \"deploy\"\ntoken = \"secret\"\n\n[targets.staging]\nhost = \"staging.example.com\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if len(d.Targets) != 2 { t.Fatalf("len=%d", len(d.Targets)) }
	prod := d.Targets["prod"]
	if prod.Host != "prod.example.com" { t.Fatalf("host=%q", prod.Host) }
	if prod.Auth == nil { t.Fatal("nil") }
	if prod.Auth.User != "deploy" { t.Fatalf("user=%q", prod.Auth.User) }
	staging := d.Targets["staging"]
	if staging.Auth != nil { t.Fatal("should be nil") }
	out, err := doc.Encode()
	if err != nil { t.Fatal(err) }
	doc2, err := DecodeConfig(out)
	if err != nil { t.Fatal(err) }
	if doc2.Data().Targets["prod"].Auth.Token != "secret" { t.Fatal("round-trip") }
}
`)
	nestingRun(t, dir)
}

// []Struct > []Struct > Struct (#87): container sub-fields inside a doubly-nested
// array-table entry must decode, and the inner array index must not collide with
// the outer one. Uses 2 outers with 2/1 inners to expose both the dropped-container
// and index-collision bugs at once.
func TestNestingSliceStructSliceStructStruct(t *testing.T) {
	dir, root := nestingSetup(t)
	nestingGoMod(t, dir, root, "n8")
	writeFixture(t, dir, "config.go", `package n8
//go:generate tommy generate
type Config struct {
	Outers []Outer `+"`toml:\"outers\"`"+`
}
type Outer struct {
	Name   string  `+"`toml:\"name\"`"+`
	Inners []Inner `+"`toml:\"inners\"`"+`
}
type Inner struct {
	ID   string `+"`toml:\"id\"`"+`
	Meta Meta   `+"`toml:\"meta\"`"+`
}
type Meta struct {
	Key string `+"`toml:\"key\"`"+`
}
`)
	writeFixture(t, dir, "config_test.go", `package n8
import "testing"
func TestDecode(t *testing.T) {
	input := []byte("[[outers]]\nname = \"o0\"\n\n[[outers.inners]]\nid = \"i00\"\n\n[outers.inners.meta]\nkey = \"m00\"\n\n[[outers.inners]]\nid = \"i01\"\n\n[outers.inners.meta]\nkey = \"m01\"\n\n[[outers]]\nname = \"o1\"\n\n[[outers.inners]]\nid = \"i10\"\n\n[outers.inners.meta]\nkey = \"m10\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if len(d.Outers) != 2 { t.Fatalf("outers=%d", len(d.Outers)) }
	if len(d.Outers[0].Inners) != 2 { t.Fatalf("o0 inners=%d", len(d.Outers[0].Inners)) }
	if len(d.Outers[1].Inners) != 1 { t.Fatalf("o1 inners=%d", len(d.Outers[1].Inners)) }
	if d.Outers[0].Inners[0].ID != "i00" { t.Fatalf("o0i0 id=%q", d.Outers[0].Inners[0].ID) }
	if d.Outers[0].Inners[1].ID != "i01" { t.Fatalf("o0i1 id=%q", d.Outers[0].Inners[1].ID) }
	if d.Outers[0].Inners[0].Meta.Key != "m00" { t.Fatalf("o0i0 meta=%q", d.Outers[0].Inners[0].Meta.Key) }
	if d.Outers[0].Inners[1].Meta.Key != "m01" { t.Fatalf("o0i1 meta=%q", d.Outers[0].Inners[1].Meta.Key) }
	if d.Outers[1].Inners[0].Meta.Key != "m10" { t.Fatalf("o1i0 meta=%q", d.Outers[1].Inners[0].Meta.Key) }
	out, err := doc.Encode()
	if err != nil { t.Fatal(err) }
	doc2, err := DecodeConfig(out)
	if err != nil { t.Fatal(err) }
	d2 := doc2.Data()
	if d2.Outers[0].Inners[1].Meta.Key != "m01" { t.Fatalf("round-trip o0i1 meta=%q", d2.Outers[0].Inners[1].Meta.Key) }
	if d2.Outers[1].Inners[0].ID != "i10" { t.Fatalf("round-trip o1i0 id=%q", d2.Outers[1].Inners[0].ID) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
`)
	nestingRun(t, dir)
}

// []Struct > map[string]Struct: a map of structs nested inside an array-table
// entry must scope its sub-tables to the entry (exercises the scoped map path).
func TestNestingSliceStructMapStringStruct(t *testing.T) {
	dir, root := nestingSetup(t)
	nestingGoMod(t, dir, root, "n9")
	writeFixture(t, dir, "config.go", `package n9
//go:generate tommy generate
type Config struct {
	Hosts []Host `+"`toml:\"hosts\"`"+`
}
type Host struct {
	Name  string          `+"`toml:\"name\"`"+`
	Disks map[string]Disk `+"`toml:\"disks\"`"+`
}
type Disk struct {
	Size int `+"`toml:\"size\"`"+`
}
`)
	writeFixture(t, dir, "config_test.go", `package n9
import "testing"
func TestDecode(t *testing.T) {
	input := []byte("[[hosts]]\nname = \"h0\"\n\n[hosts.disks.sda]\nsize = 100\n\n[hosts.disks.sdb]\nsize = 200\n\n[[hosts]]\nname = \"h1\"\n\n[hosts.disks.sda]\nsize = 50\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if len(d.Hosts) != 2 { t.Fatalf("hosts=%d", len(d.Hosts)) }
	if d.Hosts[0].Disks["sda"].Size != 100 { t.Fatalf("h0 sda=%d", d.Hosts[0].Disks["sda"].Size) }
	if d.Hosts[0].Disks["sdb"].Size != 200 { t.Fatalf("h0 sdb=%d", d.Hosts[0].Disks["sdb"].Size) }
	if len(d.Hosts[1].Disks) != 1 { t.Fatalf("h1 disks=%d", len(d.Hosts[1].Disks)) }
	if d.Hosts[1].Disks["sda"].Size != 50 { t.Fatalf("h1 sda=%d", d.Hosts[1].Disks["sda"].Size) }
	out, err := doc.Encode()
	if err != nil { t.Fatal(err) }
	doc2, err := DecodeConfig(out)
	if err != nil { t.Fatal(err) }
	if doc2.Data().Hosts[0].Disks["sdb"].Size != 200 { t.Fatalf("rt h0 sdb=%d", doc2.Data().Hosts[0].Disks["sdb"].Size) }
	if doc2.Data().Hosts[1].Disks["sda"].Size != 50 { t.Fatalf("rt h1 sda=%d", doc2.Data().Hosts[1].Disks["sda"].Size) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
`)
	nestingRun(t, dir)
}

// []Struct with a delegated (cross-package) sub-field (#86): the delegated table
// for the i-th array entry must be located within that entry's scope, not via a
// document-root scan that grabs the first match.
func TestNestingSliceStructDelegatedField(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	extDir := filepath.Join(dir, "srvext")
	writeFixture(t, extDir, "go.mod", strings.Join([]string{
		"module example.com/test/srvext", "", "go 1.26", "",
		"require github.com/amarbel-llc/tommy v0.0.0", "",
		"replace github.com/amarbel-llc/tommy => " + repoRoot, "",
	}, "\n"))
	writeFixture(t, extDir, "settings.go", `package srvext
//go:generate tommy generate
type Settings struct {
	Timeout int    `+"`toml:\"timeout\"`"+`
	Mode    string `+"`toml:\"mode\"`"+`
}
`)
	if err := Generate(extDir, "settings.go"); err != nil {
		t.Fatalf("Generate srvext: %v", err)
	}

	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer", "", "go 1.26", "",
		"require (", "\tgithub.com/amarbel-llc/tommy v0.0.0", "\texample.com/test/srvext v0.0.0", ")", "",
		"replace (", "\tgithub.com/amarbel-llc/tommy => " + repoRoot, "\texample.com/test/srvext => ../srvext", ")", "",
	}, "\n"))
	writeFixture(t, consumerDir, "app.go", `package consumer
import "example.com/test/srvext"
//go:generate tommy generate
type Config struct {
	Servers []Server `+"`toml:\"servers\"`"+`
}
type Server struct {
	Name     string          `+"`toml:\"name\"`"+`
	Settings srvext.Settings `+"`toml:\"settings\"`"+`
}
`)
	if err := Generate(consumerDir, "app.go"); err != nil {
		t.Fatalf("Generate consumer: %v", err)
	}

	writeFixture(t, consumerDir, "app_test.go", `package consumer
import "testing"
func TestDecode(t *testing.T) {
	input := []byte("[[servers]]\nname = \"alpha\"\n\n[servers.settings]\ntimeout = 30\nmode = \"fast\"\n\n[[servers]]\nname = \"beta\"\n\n[servers.settings]\ntimeout = 60\nmode = \"slow\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if len(d.Servers) != 2 { t.Fatalf("servers=%d", len(d.Servers)) }
	if d.Servers[0].Settings.Timeout != 30 { t.Fatalf("s0 timeout=%d", d.Servers[0].Settings.Timeout) }
	if d.Servers[0].Settings.Mode != "fast" { t.Fatalf("s0 mode=%q", d.Servers[0].Settings.Mode) }
	if d.Servers[1].Settings.Timeout != 60 { t.Fatalf("s1 timeout=%d", d.Servers[1].Settings.Timeout) }
	if d.Servers[1].Settings.Mode != "slow" { t.Fatalf("s1 mode=%q", d.Servers[1].Settings.Mode) }
	out, err := doc.Encode()
	if err != nil { t.Fatal(err) }
	doc2, err := DecodeConfig(out)
	if err != nil { t.Fatal(err) }
	if doc2.Data().Servers[1].Settings.Timeout != 60 { t.Fatalf("round-trip s1 timeout=%d", doc2.Data().Servers[1].Settings.Timeout) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
`)
	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("test failed:\n%s", out)
	}
}

// []Struct whose element has a delegated []ext.T and a map[string]ext.T (#88):
// both must be scoped to the i-th entry on decode and encode.
func TestNestingSliceStructDelegatedSliceAndMap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	extDir := filepath.Join(dir, "srvext")
	writeFixture(t, extDir, "go.mod", strings.Join([]string{
		"module example.com/test/srvext", "", "go 1.26", "",
		"require github.com/amarbel-llc/tommy v0.0.0", "",
		"replace github.com/amarbel-llc/tommy => " + repoRoot, "",
	}, "\n"))
	writeFixture(t, extDir, "ext.go", `package srvext
//go:generate tommy generate
type Backend struct {
	URL string `+"`toml:\"url\"`"+`
}

//go:generate tommy generate
type Meta struct {
	Note string `+"`toml:\"note\"`"+`
}
`)
	if err := Generate(extDir, "ext.go"); err != nil {
		t.Fatalf("Generate srvext: %v", err)
	}

	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", strings.Join([]string{
		"module example.com/test/consumer", "", "go 1.26", "",
		"require (", "\tgithub.com/amarbel-llc/tommy v0.0.0", "\texample.com/test/srvext v0.0.0", ")", "",
		"replace (", "\tgithub.com/amarbel-llc/tommy => " + repoRoot, "\texample.com/test/srvext => ../srvext", ")", "",
	}, "\n"))
	writeFixture(t, consumerDir, "app.go", `package consumer
import "example.com/test/srvext"
//go:generate tommy generate
type Config struct {
	Servers []Server `+"`toml:\"servers\"`"+`
}
type Server struct {
	Name     string                     `+"`toml:\"name\"`"+`
	Backends []srvext.Backend           `+"`toml:\"backends\"`"+`
	Metas    map[string]srvext.Meta     `+"`toml:\"metas\"`"+`
}
`)
	if err := Generate(consumerDir, "app.go"); err != nil {
		t.Fatalf("Generate consumer: %v", err)
	}

	writeFixture(t, consumerDir, "app_test.go", `package consumer
import "testing"
func TestDecode(t *testing.T) {
	input := []byte("[[servers]]\nname = \"alpha\"\n\n[[servers.backends]]\nurl = \"u1\"\n\n[[servers.backends]]\nurl = \"u2\"\n\n[servers.metas.a]\nnote = \"na\"\n\n[[servers]]\nname = \"beta\"\n\n[[servers.backends]]\nurl = \"u3\"\n\n[servers.metas.x]\nnote = \"nx\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if len(d.Servers) != 2 { t.Fatalf("servers=%d", len(d.Servers)) }
	if len(d.Servers[0].Backends) != 2 { t.Fatalf("s0 backends=%d", len(d.Servers[0].Backends)) }
	if d.Servers[0].Backends[0].URL != "u1" || d.Servers[0].Backends[1].URL != "u2" { t.Fatalf("s0 backends=%v", d.Servers[0].Backends) }
	if len(d.Servers[1].Backends) != 1 || d.Servers[1].Backends[0].URL != "u3" { t.Fatalf("s1 backends=%v", d.Servers[1].Backends) }
	if d.Servers[0].Metas["a"].Note != "na" { t.Fatalf("s0 metas=%v", d.Servers[0].Metas) }
	if d.Servers[1].Metas["x"].Note != "nx" { t.Fatalf("s1 metas=%v", d.Servers[1].Metas) }
	out, err := doc.Encode()
	if err != nil { t.Fatal(err) }
	doc2, err := DecodeConfig(out)
	if err != nil { t.Fatalf("re-decode: %v\n%s", err, out) }
	d2 := doc2.Data()
	if len(d2.Servers[1].Backends) != 1 || d2.Servers[1].Backends[0].URL != "u3" { t.Fatalf("round-trip s1 backends=%v\n%s", d2.Servers[1].Backends, out) }
	if d2.Servers[0].Metas["a"].Note != "na" { t.Fatalf("round-trip s0 metas=%v\n%s", d2.Servers[0].Metas, out) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
`)
	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = consumerDir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("test failed:\n%s", out)
	}
}

// Growing a nested inner slice and re-encoding must append the new entry within
// the right outer (#88: scoped AppendChildArrayTableEntry reuse+append path).
func TestNestingSliceStructSliceStructAppendGrow(t *testing.T) {
	dir, root := nestingSetup(t)
	nestingGoMod(t, dir, root, "ng")
	writeFixture(t, dir, "config.go", `package ng
//go:generate tommy generate
type Config struct {
	Outers []Outer `+"`toml:\"outers\"`"+`
}
type Outer struct {
	Name   string  `+"`toml:\"name\"`"+`
	Inners []Inner `+"`toml:\"inners\"`"+`
}
type Inner struct {
	ID string `+"`toml:\"id\"`"+`
}
`)
	writeFixture(t, dir, "config_test.go", `package ng
import "testing"
func TestDecode(t *testing.T) {
	input := []byte("[[outers]]\nname = \"o0\"\n\n[[outers.inners]]\nid = \"i00\"\n\n[[outers]]\nname = \"o1\"\n\n[[outers.inners]]\nid = \"i10\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if len(d.Outers) != 2 { t.Fatalf("outers=%d", len(d.Outers)) }
	// Grow outer 0's inner slice by one.
	d.Outers[0].Inners = append(d.Outers[0].Inners, Inner{ID: "i01"})
	out, err := doc.Encode()
	if err != nil { t.Fatal(err) }
	doc2, err := DecodeConfig(out)
	if err != nil { t.Fatalf("re-decode: %v\n%s", err, out) }
	d2 := doc2.Data()
	if len(d2.Outers[0].Inners) != 2 { t.Fatalf("o0 inners=%d, want 2\n%s", len(d2.Outers[0].Inners), out) }
	if d2.Outers[0].Inners[0].ID != "i00" || d2.Outers[0].Inners[1].ID != "i01" { t.Fatalf("o0 inners=%v\n%s", d2.Outers[0].Inners, out) }
	if len(d2.Outers[1].Inners) != 1 || d2.Outers[1].Inners[0].ID != "i10" { t.Fatalf("o1 inners=%v (append leaked across outer)\n%s", d2.Outers[1].Inners, out) }
}
`)
	nestingRun(t, dir)
}

// Regression test for #60: map[string]Struct with nested sub-tables should not
// produce phantom map entries from grandchild tables.
func TestIntegrationMapStringStructNestedSubTable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/issue60", "", "go 1.26", "",
		"require github.com/amarbel-llc/tommy v0.0.0", "",
		"replace github.com/amarbel-llc/tommy => " + repoRoot, "",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package issue60

//go:generate tommy generate
type Config struct {
	Calendars map[string]Calendar `+"`toml:\"calendars\"`"+`
}

type Calendar struct {
	URL        string            `+"`toml:\"url\"`"+`
	Type       string            `+"`toml:\"type\"`"+`
	StatusTags map[string]string `+"`toml:\"status-tags\"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "config_test.go", `package issue60

import "testing"

func TestMapStructNestedSubTable(t *testing.T) {
	input := []byte(`+"`"+`[calendars.tasks]
url = "https://example.com/tasks"
type = "!task"

[calendars.tasks.status-tags]
COMPLETED = "done"
IN_PROGRESS = "wip"

[calendars.chores]
url = "https://example.com/chores"
type = "!chore"

[calendars.chores.status-tags]
COMPLETED = "archived"
`+"`"+`)

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatal(err)
	}

	cfg := doc.Data()

	// Should have exactly 2 calendar entries, not 4
	if len(cfg.Calendars) != 2 {
		var keys []string
		for k := range cfg.Calendars {
			keys = append(keys, k)
		}
		t.Fatalf("expected 2 calendars, got %d: %v", len(cfg.Calendars), keys)
	}

	tasks := cfg.Calendars["tasks"]
	if tasks.URL != "https://example.com/tasks" {
		t.Fatalf("tasks.URL = %q", tasks.URL)
	}
	if tasks.StatusTags["COMPLETED"] != "done" {
		t.Fatalf("tasks.StatusTags[COMPLETED] = %q", tasks.StatusTags["COMPLETED"])
	}

	chores := cfg.Calendars["chores"]
	if chores.URL != "https://example.com/chores" {
		t.Fatalf("chores.URL = %q", chores.URL)
	}

	// Round-trip
	out, err := doc.Encode()
	if err != nil {
		t.Fatal(err)
	}
	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc2.Data().Calendars) != 2 {
		t.Fatalf("round-trip: expected 2 calendars, got %d", len(doc2.Data().Calendars))
	}
	if doc2.Data().Calendars["tasks"].StatusTags["COMPLETED"] != "done" {
		t.Fatal("round-trip lost status-tags")
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	out, errCmd := cmd.CombinedOutput()
	if errCmd != nil {
		t.Fatalf("test failed:\n%s", out)
	}
}

// Reproducer for #62: when a parent table exists but an optional nested pointer
// struct sub-table is missing, the generated else branch shadows tableNode with
// nil and panics trying to read flat-key fallback fields from it.
func TestIntegrationOptionalNestedTableMissing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/optmissing",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package optmissing

//go:generate tommy generate
type Config struct {
	Haustoria *HaustoriaConfig `+"`"+`toml:"haustoria"`+"`"+`
}

type HaustoriaConfig struct {
	Type   string       `+"`"+`toml:"type"`+"`"+`
	CalDAV *CalDAVConfig `+"`"+`toml:"caldav"`+"`"+`
	Orgmode *OrgmodeConfig `+"`"+`toml:"orgmode"`+"`"+`
}

type CalDAVConfig struct {
	URL string `+"`"+`toml:"url"`+"`"+`
}

type OrgmodeConfig struct {
	Transport string `+"`"+`toml:"transport"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "optmissing_test.go", `package optmissing

import "testing"

func TestOptionalNestedTableMissing(t *testing.T) {
	// Parent [haustoria] exists with [haustoria.orgmode], but
	// [haustoria.caldav] is absent. The generated else branch for
	// CalDAV must not panic.
	input := []byte(`+"`"+`[haustoria]
type = "orgmode"

[haustoria.orgmode]
transport = "sftp"
`+"`"+`)

	doc, err := DecodeConfig(input)
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	d := doc.Data()
	if d.Haustoria == nil {
		t.Fatal("Haustoria is nil")
	}
	if d.Haustoria.Type != "orgmode" {
		t.Fatalf("Type = %q, want %q", d.Haustoria.Type, "orgmode")
	}
	if d.Haustoria.CalDAV != nil {
		t.Fatalf("CalDAV should be nil, got %+v", d.Haustoria.CalDAV)
	}
	if d.Haustoria.Orgmode == nil {
		t.Fatal("Orgmode is nil")
	}
	if d.Haustoria.Orgmode.Transport != "sftp" {
		t.Fatalf("Transport = %q, want %q", d.Haustoria.Orgmode.Transport, "sftp")
	}

	// Round-trip: encode then re-decode should not lose data.
	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	doc2, err := DecodeConfig(out)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	d2 := doc2.Data()
	if d2.Haustoria.CalDAV != nil {
		t.Fatalf("re-decoded CalDAV should be nil")
	}
	if d2.Haustoria.Orgmode == nil || d2.Haustoria.Orgmode.Transport != "sftp" {
		t.Fatalf("re-decoded Orgmode lost data")
	}
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

// #108 axis 1: a nested struct / *struct field written as an inline table
// (`inner = { name = "a" }`) must decode and be marked consumed, exactly like the
// sub-table form (`[inner]`). Covers a value struct (cdInTable), a pointer struct
// (cdNilGuard) at the top level, and a struct nested inside an array-table entry
// (the scoped cdInTable/compScopedBody path). Leaf structs only.
func TestIntegrationInlineTableStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/inlinestruct",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package inlinestruct

//go:generate tommy generate
type Config struct {
	Val      Inner     `+"`"+`toml:"val"`+"`"+`
	Ptr      *Inner    `+"`"+`toml:"ptr"`+"`"+`
	Services []Service `+"`"+`toml:"services"`+"`"+`
}

type Inner struct {
	Name string `+"`"+`toml:"name"`+"`"+`
	Port int    `+"`"+`toml:"port"`+"`"+`
}

type Service struct {
	Host string `+"`"+`toml:"host"`+"`"+`
	Meta Inner  `+"`"+`toml:"meta"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "inlinestruct_test.go", `package inlinestruct

import "testing"

// Value struct (cdInTable), inline form.
func TestInlineValueStruct(t *testing.T) {
	doc, err := DecodeConfig([]byte("val = { name = \"a\", port = 8080 }\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Val.Name != "a" || d.Val.Port != 8080 { t.Fatalf("Val=%+v", d.Val) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// Pointer struct (cdNilGuard), inline form.
func TestInlinePtrStruct(t *testing.T) {
	doc, err := DecodeConfig([]byte("ptr = { name = \"b\", port = 90 }\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Ptr == nil || d.Ptr.Name != "b" || d.Ptr.Port != 90 { t.Fatalf("Ptr=%+v", d.Ptr) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// Struct nested in an array-table entry (scoped path), inline form.
func TestInlineScopedStruct(t *testing.T) {
	doc, err := DecodeConfig([]byte("[[services]]\nhost = \"h\"\nmeta = { name = \"m\", port = 1 }\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if len(d.Services) != 1 { t.Fatalf("services=%+v", d.Services) }
	if d.Services[0].Host != "h" || d.Services[0].Meta.Name != "m" || d.Services[0].Meta.Port != 1 { t.Fatalf("svc=%+v", d.Services[0]) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// The sub-table form must still work and produce the same value.
func TestSubTableStructStillWorks(t *testing.T) {
	doc, err := DecodeConfig([]byte("[val]\nname = \"a\"\nport = 8080\n\n[ptr]\nname = \"b\"\nport = 90\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Val.Name != "a" || d.Ptr == nil || d.Ptr.Name != "b" { t.Fatalf("d=%+v", d) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

// #108 axis 1: a map[string]struct field written as a nested inline table
// (`actions = { build = { command = "make" } }`) must decode and be consumed,
// exactly like the sub-table form (`[actions.build]`). Covers the top-level map
// (compMapStruct) and a map nested in an array-table entry (compScopedMapStruct).
func TestIntegrationInlineTableMapStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/inlinemapstruct",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package inlinemapstruct

//go:generate tommy generate
type Config struct {
	Actions map[string]ActionSpec `+"`"+`toml:"actions"`+"`"+`
	Hosts   []Host                `+"`"+`toml:"hosts"`+"`"+`
}

type ActionSpec struct {
	Command string `+"`"+`toml:"command"`+"`"+`
	Timeout int    `+"`"+`toml:"timeout"`+"`"+`
}

type Host struct {
	Name  string                `+"`"+`toml:"name"`+"`"+`
	Tasks map[string]ActionSpec `+"`"+`toml:"tasks"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "inlinemapstruct_test.go", `package inlinemapstruct

import "testing"

// Top-level map[string]struct, inline form.
func TestInlineMapStruct(t *testing.T) {
	doc, err := DecodeConfig([]byte("actions = { build = { command = \"make\", timeout = 30 }, test = { command = \"go test\", timeout = 60 } }\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Actions["build"].Command != "make" || d.Actions["build"].Timeout != 30 { t.Fatalf("build=%+v", d.Actions["build"]) }
	if d.Actions["test"].Command != "go test" || d.Actions["test"].Timeout != 60 { t.Fatalf("test=%+v", d.Actions["test"]) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// map[string]struct nested in an array-table entry (scoped), inline form.
func TestInlineScopedMapStruct(t *testing.T) {
	doc, err := DecodeConfig([]byte("[[hosts]]\nname = \"h\"\ntasks = { a = { command = \"x\", timeout = 1 } }\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if len(d.Hosts) != 1 || d.Hosts[0].Name != "h" { t.Fatalf("hosts=%+v", d.Hosts) }
	if d.Hosts[0].Tasks["a"].Command != "x" || d.Hosts[0].Tasks["a"].Timeout != 1 { t.Fatalf("tasks=%+v", d.Hosts[0].Tasks) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// The sub-table form must still work.
func TestSubTableMapStructStillWorks(t *testing.T) {
	doc, err := DecodeConfig([]byte("[actions.build]\ncommand = \"make\"\ntimeout = 30\n"))
	if err != nil { t.Fatal(err) }
	if doc.Data().Actions["build"].Command != "make" { t.Fatalf("actions=%+v", doc.Data().Actions) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

// #108 axis 1: a map[string]NamedMap field (map of a named map[string]string
// alias) written as a nested inline table (`groups = { editors = { vim = "x" } }`)
// must decode and be consumed, like the sub-table form (`[groups.editors]`).
// Covers top-level (compMapMap) and scoped (compScopedMapMap, in a [[hosts]] entry).
func TestIntegrationInlineTableMapMap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/inlinemapmap",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package inlinemapmap

type Group map[string]string

//go:generate tommy generate
type Config struct {
	Groups map[string]Group `+"`"+`toml:"groups"`+"`"+`
	Hosts  []Host           `+"`"+`toml:"hosts"`+"`"+`
}

type Host struct {
	Name string           `+"`"+`toml:"name"`+"`"+`
	Tags map[string]Group `+"`"+`toml:"tags"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "inlinemapmap_test.go", `package inlinemapmap

import "testing"

// Top-level map[string]NamedMap, inline form.
func TestInlineMapMap(t *testing.T) {
	doc, err := DecodeConfig([]byte("groups = { editors = { vim = \"text/plain\", emacs = \"text/plain\" }, compilers = { gcc = \"x\" } }\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Groups["editors"]["vim"] != "text/plain" || d.Groups["editors"]["emacs"] != "text/plain" { t.Fatalf("editors=%+v", d.Groups["editors"]) }
	if d.Groups["compilers"]["gcc"] != "x" { t.Fatalf("compilers=%+v", d.Groups["compilers"]) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// map[string]NamedMap nested in an array-table entry (scoped), inline form.
func TestInlineScopedMapMap(t *testing.T) {
	doc, err := DecodeConfig([]byte("[[hosts]]\nname = \"h\"\ntags = { env = { region = \"us\" } }\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if len(d.Hosts) != 1 || d.Hosts[0].Name != "h" { t.Fatalf("hosts=%+v", d.Hosts) }
	if d.Hosts[0].Tags["env"]["region"] != "us" { t.Fatalf("tags=%+v", d.Hosts[0].Tags) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// The sub-table form must still work.
func TestSubTableMapMapStillWorks(t *testing.T) {
	doc, err := DecodeConfig([]byte("[groups.editors]\nvim = \"text/plain\"\n"))
	if err != nil { t.Fatal(err) }
	if doc.Data().Groups["editors"]["vim"] != "text/plain" { t.Fatalf("groups=%+v", doc.Data().Groups) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

// #108 axis 1: a FULLY-inline nested struct (`val = { name = "a", inner = { x = 5 } }`)
// must decode the nested struct field too, not just the leaf fields. The body of
// an inline-table fallback is decoded scope-relative to the inline node, so a
// nested struct/map field resolves WITHIN the inline node via the scoped inline
// fallback — composing recursively. Before this, the nested field was searched at
// the document root, silently dropped, and not even flagged by Undecoded().
func TestIntegrationInlineTableNestedStruct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/inlinenest",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package inlinenest

//go:generate tommy generate
type Config struct {
	Val Outer  `+"`"+`toml:"val"`+"`"+`
	Ptr *Outer `+"`"+`toml:"ptr"`+"`"+`
}

type Outer struct {
	Name  string `+"`"+`toml:"name"`+"`"+`
	Inner Inner  `+"`"+`toml:"inner"`+"`"+`
	Env   map[string]string `+"`"+`toml:"env"`+"`"+`
}

type Inner struct {
	X int `+"`"+`toml:"x"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "inlinenest_test.go", `package inlinenest

import "testing"

// Fully-inline value struct with a nested struct and a nested map.
func TestInlineNestedValue(t *testing.T) {
	doc, err := DecodeConfig([]byte("val = { name = \"a\", inner = { x = 5 }, env = { K = \"v\" } }\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Val.Name != "a" || d.Val.Inner.X != 5 || d.Val.Env["K"] != "v" { t.Fatalf("Val=%+v", d.Val) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// Fully-inline pointer struct with a nested struct.
func TestInlineNestedPtr(t *testing.T) {
	doc, err := DecodeConfig([]byte("ptr = { name = \"b\", inner = { x = 9 } }\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Ptr == nil || d.Ptr.Name != "b" || d.Ptr.Inner.X != 9 { t.Fatalf("Ptr=%+v", d.Ptr) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// Mixed: outer inline, inner as a sub-table — the inner [val.inner] still works
// when val itself is a header table (canonical path unchanged).
func TestSubTableNestedStillWorks(t *testing.T) {
	doc, err := DecodeConfig([]byte("[val]\nname = \"a\"\n\n[val.inner]\nx = 5\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Val.Name != "a" || d.Val.Inner.X != 5 { t.Fatalf("Val=%+v", d.Val) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

// Regression for #115: the "header-outer + inline-inner" spelling. A child field
// of a struct that DOES have its bare [parent] header, written as an inline table
// (`session = { ... }` under `[exec]`), parses to a NodeKeyValue child of the
// [exec] table node — not the document root. The root-relative container
// renderers (compNilGuard / compInTable / compMapStruct / compMapMap) ran their
// #106/#108 inline fallback against doc.Root() rather than the found parent node
// (cv), so every inline-inner child was missed: the field stayed zero AND the
// drop was silent (also see #109). compMapScalar (map[string]string) already
// searched cv (#106); this aligns the other three plus the nested-struct paths.
func TestIntegrationInlineUnderHeader(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/inlineunderheader",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package inlineunderheader

type Labels map[string]string

//go:generate tommy generate
type Config struct {
	Exec *Exec `+"`"+`toml:"exec"`+"`"+`
}

type Exec struct {
	Session *Session          `+"`"+`toml:"session"`+"`"+` // *struct -> compNilGuard
	Window  Window            `+"`"+`toml:"window"`+"`"+`  // struct  -> compInTable
	Actions map[string]Action `+"`"+`toml:"actions"`+"`"+` // map[string]struct -> compMapStruct
	Groups  map[string]Labels `+"`"+`toml:"groups"`+"`"+`  // map[string]NamedMap -> compMapMap
	Env     map[string]string `+"`"+`toml:"env"`+"`"+`     // map[string]string -> compMapScalar (#106 baseline)
}

type Session struct {
	Name string `+"`"+`toml:"name"`+"`"+`
}

type Window struct {
	Title string `+"`"+`toml:"title"`+"`"+`
}

type Action struct {
	Command string `+"`"+`toml:"command"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "inlineunderheader_test.go", `package inlineunderheader

import "testing"

// All five field kinds, written as inline-inner tables under the bare [exec]
// header. Before #115 every one of these was silently dropped except env
// (map[string]string, fixed by #106).
func TestInlineInnerUnderHeader(t *testing.T) {
	const in = "[exec]\n" +
		"session = { name = \"sess\" }\n" +
		"window = { title = \"win\" }\n" +
		"actions = { build = { command = \"make\" } }\n" +
		"groups = { editors = { vim = \"on\" } }\n" +
		"env = { FOO = \"bar\" }\n"
	doc, err := DecodeConfig([]byte(in))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Exec == nil { t.Fatal("Exec is nil") }
	if d.Exec.Session == nil || d.Exec.Session.Name != "sess" { t.Fatalf("Session=%+v", d.Exec.Session) }
	if d.Exec.Window.Title != "win" { t.Fatalf("Window=%+v", d.Exec.Window) }
	if d.Exec.Actions["build"].Command != "make" { t.Fatalf("Actions=%+v", d.Exec.Actions) }
	if d.Exec.Groups["editors"]["vim"] != "on" { t.Fatalf("Groups=%+v", d.Exec.Groups) }
	if d.Exec.Env["FOO"] != "bar" { t.Fatalf("Env=%+v", d.Exec.Env) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// The canonical sub-table spelling under the same header must still decode.
func TestSubTableInnerUnderHeaderStillWorks(t *testing.T) {
	const in = "[exec]\n" +
		"[exec.session]\nname = \"sess\"\n" +
		"[exec.window]\ntitle = \"win\"\n" +
		"[exec.actions.build]\ncommand = \"make\"\n" +
		"[exec.groups.editors]\nvim = \"on\"\n" +
		"[exec.env]\nFOO = \"bar\"\n"
	doc, err := DecodeConfig([]byte(in))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Exec == nil || d.Exec.Session == nil || d.Exec.Session.Name != "sess" { t.Fatalf("Session=%+v", d.Exec) }
	if d.Exec.Window.Title != "win" { t.Fatalf("Window=%+v", d.Exec.Window) }
	if d.Exec.Actions["build"].Command != "make" { t.Fatalf("Actions=%+v", d.Exec.Actions) }
	if d.Exec.Groups["editors"]["vim"] != "on" { t.Fatalf("Groups=%+v", d.Exec.Groups) }
	if d.Exec.Env["FOO"] != "bar" { t.Fatalf("Env=%+v", d.Exec.Env) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

// Regression for #110: a key defined twice in the inline-table spelling
// (`mytable = { ... }` twice) must be rejected, like the header form
// (`[mytable]` twice, #92). The localized decoder guards missed it — the second
// inline key was silently dropped. The generated decoder now runs
// cst.CheckNoDuplicateKeys after parse, rejecting duplicate keys in every
// spelling. Verifies both the outer container-key case and that the canonical
// single-spelling decode still succeeds.
func TestIntegrationDuplicateInlineKeyRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/dupinline",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package dupinline

//go:generate tommy generate
type Config struct {
	Sess *Sess             `+"`"+`toml:"sess"`+"`"+`
	Env  map[string]string `+"`"+`toml:"env"`+"`"+`
}

type Sess struct {
	Name string `+"`"+`toml:"name"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "dupinline_test.go", `package dupinline

import (
	"strings"
	"testing"
)

// Duplicate outer inline key -> error (the #110 gap).
func TestDuplicateInlineOuterKey(t *testing.T) {
	_, err := DecodeConfig([]byte("sess = { name = \"a\" }\nsess = { name = \"b\" }\n"))
	if err == nil {
		t.Fatal("expected error for duplicate inline key, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("error should mention duplicate key, got: %v", err)
	}
}

// Duplicate inner inline key -> error.
func TestDuplicateInlineInnerKey(t *testing.T) {
	_, err := DecodeConfig([]byte("env = { FOO = \"a\", FOO = \"b\" }\n"))
	if err == nil {
		t.Fatal("expected error for duplicate inner inline key, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("error should mention duplicate key, got: %v", err)
	}
}

// A single, valid spelling still decodes.
func TestDuplicateGuardAllowsValid(t *testing.T) {
	doc, err := DecodeConfig([]byte("sess = { name = \"a\" }\nenv = { FOO = \"x\" }\n"))
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Sess == nil || d.Sess.Name != "a" || d.Env["FOO"] != "x" { t.Fatalf("data=%+v", d) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

// Regression: a stale generated file from an older tommy — one that still calls
// a cst symbol this version removed (e.g. the deleted FindChildTable) — must not
// block regeneration. tommy generate ignores its own *_tommy.go output during
// analysis, so `go generate` recovers in place for an isolated codegen package
// (the #93 bootstrap catch-22 proud-mulberry hit during the rewrite migration).
func TestIntegrationStaleGeneratedFileDoesNotBlockRegen(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/staleregen",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package staleregen

//go:generate tommy generate
type Config struct {
	Name string `+"`"+`toml:"name"`+"`"+`
}
`)

	// A stale generated file from a prior tommy that calls a cst finder this
	// version deleted: it does not compile and must not block regen.
	writeFixture(t, dir, "config_tommy.go", `package staleregen

import "github.com/amarbel-llc/tommy/pkg/cst"

var _ = cst.FindChildTable
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate must ignore the stale generated file, got: %v", err)
	}

	out, err := os.ReadFile(filepath.Join(dir, "config_tommy.go"))
	if err != nil {
		t.Fatalf("read regenerated file: %v", err)
	}
	if strings.Contains(string(out), "FindChildTable") {
		t.Fatal("regenerated file still references the removed FindChildTable — it was not overwritten")
	}

	// The regenerated package must compile cleanly.
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if buildOut, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("regenerated package failed to build: %v\n%s", err, buildOut)
	}
}

// Regression for #113: a standalone dotted sub-table header ([direnv.dotenv]
// with no bare [direnv] header) is valid TOML — the parent table is implicit —
// but the generated decoder only ran the dotted-header consumption scan inside
// the branch entered when the bare parent header was found. A document
// containing ONLY the dotted form left the header unconsumed: Undecoded()
// reported it and the struct field stayed nil. Covers both parent shapes
// (pointer struct / value struct) and both generated codepaths (the receiver
// DecodeConfig and the keyPrefix-parameterized DecodeConfigInto).
func TestIntegrationStandaloneDottedHeader(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/dottedhdr",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package dottedhdr

//go:generate tommy generate
type Config struct {
	Direnv *Direnv `+"`"+`toml:"direnv"`+"`"+`
	Tools  Tools   `+"`"+`toml:"tools"`+"`"+`
	Apps   []App   `+"`"+`toml:"apps"`+"`"+`
	Exec   *Exec   `+"`"+`toml:"exec"`+"`"+`
}

type Exec struct {
	Session *Session `+"`"+`toml:"session"`+"`"+`
}

type Session struct {
	Env string `+"`"+`toml:"env"`+"`"+`
}

type Direnv struct {
	Dotenv map[string]string `+"`"+`toml:"dotenv"`+"`"+`
}

type Tools struct {
	Env map[string]string `+"`"+`toml:"env"`+"`"+`
}

type App struct {
	Name   string  `+"`"+`toml:"name"`+"`"+`
	Direnv *Direnv `+"`"+`toml:"direnv"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "dottedhdr_test.go", `package dottedhdr

import (
	"testing"

	"github.com/amarbel-llc/tommy/pkg/cst"
	"github.com/amarbel-llc/tommy/pkg/document"
)

// Pointer-struct parent (cdNilGuard), standalone dotted header — the spinclass
// [direnv.dotenv] case from #113.
func TestStandaloneDottedPointerStruct(t *testing.T) {
	input := []byte("[direnv.dotenv]\nFOO = \"bar\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Direnv == nil { t.Fatal("Direnv is nil") }
	if d.Direnv.Dotenv["FOO"] != "bar" { t.Fatalf("Dotenv[FOO]=%q, want bar", d.Direnv.Dotenv["FOO"]) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// Value-struct parent (cdInTable), standalone dotted header.
func TestStandaloneDottedValueStruct(t *testing.T) {
	input := []byte("[tools.env]\nPATH = \"/bin\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Tools.Env["PATH"] != "/bin" { t.Fatalf("Env[PATH]=%q, want /bin", d.Tools.Env["PATH"]) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// The DecodeInto variant decodes from an already-normalized sub-value; the
// standalone dotted header is materialized into the model by Decompose, so the
// implicit parent resolves with no special handling in DecodeInto.
func TestStandaloneDottedDecodeInto(t *testing.T) {
	input := []byte("[direnv.dotenv]\nFOO = \"bar\"\n")
	doc, err := document.Parse(input)
	if err != nil { t.Fatal(err) }
	model, err := cst.Decompose(doc.Root())
	if err != nil { t.Fatal(err) }
	var data Config
	if err := DecodeConfigInto(&data, model); err != nil {
		t.Fatalf("DecodeConfigInto: %v", err)
	}
	if data.Direnv == nil { t.Fatal("Direnv is nil") }
	if data.Direnv.Dotenv["FOO"] != "bar" { t.Fatalf("Dotenv[FOO]=%q, want bar", data.Direnv.Dotenv["FOO"]) }
	if u := model.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// The explicit-parent spelling must keep working and produce the same value.
func TestExplicitParentStillWorks(t *testing.T) {
	input := []byte("[direnv]\n[direnv.dotenv]\nFOO = \"bar\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Direnv == nil || d.Direnv.Dotenv["FOO"] != "bar" { t.Fatalf("explicit form lost data: %+v", d.Direnv) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// An absent field must stay nil — the implicit-parent synthesis must not
// materialize a pointer struct out of nothing (#101 still holds).
func TestAbsentStaysNil(t *testing.T) {
	input := []byte("[tools.env]\nPATH = \"/bin\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	if doc.Data().Direnv != nil { t.Fatalf("Direnv = %+v, want nil", doc.Data().Direnv) }
}

// The #64 repro: a pointer-struct chain where the inner struct's table is
// spelled with a standalone dotted header ([exec.session] with no [exec]) and
// carries a scalar leaf. The implicit parent materializes Exec; the inner
// [exec.session] header is explicit, so Session decodes its leaves normally.
func TestStandaloneDottedPointerChain(t *testing.T) {
	input := []byte("[exec.session]\nenv = \"MY_SESSION\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Exec == nil { t.Fatal("Exec is nil") }
	if d.Exec.Session == nil { t.Fatal("Session is nil") }
	if d.Exec.Session.Env != "MY_SESSION" { t.Fatalf("Env = %q", d.Exec.Session.Env) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// Standalone dotted headers inside array-table entries: each entry's implicit
// [apps.direnv] must resolve its own sub-table, not a sibling entry's
// identically-headed one (the evidence anchor bounds the scope per entry).
func TestStandaloneDottedInArrayEntries(t *testing.T) {
	input := []byte("[[apps]]\nname = \"a0\"\n\n[apps.direnv.dotenv]\nK = \"v0\"\n\n[[apps]]\nname = \"a1\"\n\n[apps.direnv.dotenv]\nK = \"v1\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if len(d.Apps) != 2 { t.Fatalf("apps = %d, want 2", len(d.Apps)) }
	for i, want := range []string{"v0", "v1"} {
		if d.Apps[i].Direnv == nil { t.Fatalf("apps[%d].Direnv is nil", i) }
		if got := d.Apps[i].Direnv.Dotenv["K"]; got != want {
			t.Fatalf("apps[%d].Dotenv[K] = %q, want %q (cross-entry leak)", i, got, want)
		}
	}
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// An explicit empty array "apps = []" for a []struct field decodes to an empty
// (non-nil) slice and is consumed. Decompose keeps an empty array a leaf (it
// can't tell an empty array-of-tables from an empty scalar array, #94), so the
// generated array-table decoder must accept the empty-array leaf rather than
// skip it — which left the field nil AND reported "apps" as undecoded.
func TestEmptyArrayOfTables(t *testing.T) {
	doc, err := DecodeConfig([]byte("apps = []\n"))
	if err != nil { t.Fatalf("DecodeConfig: %v", err) }
	d := doc.Data()
	if d.Apps == nil { t.Fatal("Apps = nil, want empty non-nil slice") }
	if len(d.Apps) != 0 { t.Fatalf("len(Apps) = %d, want 0", len(d.Apps)) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}

// Regression for #106: a map[string]string field written as an inline table
// (`dotenv = { FOO = "bar" }`) must decode and be marked consumed, exactly like
// the semantically-equivalent sub-table form (`[parent.dotenv]`). Before the fix
// the generated decoder resolved the field only via FindChildTable (NodeTable
// header match), so the inline-table NodeKeyValue child was never matched: the
// field stayed nil and showed up in Undecoded(). Covers both a top-level map and
// a map nested under a parent struct (the spinclass [direnv.dotenv] case).
func TestIntegrationInlineTableMapStringString(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dir := t.TempDir()

	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/inlinemap",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "config.go", `package inlinemap

//go:generate tommy generate
type Config struct {
	Env    map[string]string `+"`"+`toml:"env"`+"`"+`
	Direnv *Direnv           `+"`"+`toml:"direnv"`+"`"+`
}

type Direnv struct {
	Dotenv map[string]string `+"`"+`toml:"dotenv"`+"`"+`
}
`)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	writeFixture(t, dir, "inlinemap_test.go", `package inlinemap

import "testing"

// Top-level map[string]string written inline.
func TestInlineTopLevel(t *testing.T) {
	input := []byte("env = { FOO = \"bar\", BAZ = \"qux\" }\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Env["FOO"] != "bar" { t.Fatalf("Env[FOO]=%q, want bar", d.Env["FOO"]) }
	if d.Env["BAZ"] != "qux" { t.Fatalf("Env[BAZ]=%q, want qux", d.Env["BAZ"]) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// map[string]string nested under a parent struct, written inline. This is the
// spinclass [direnv.dotenv] case from #106.
func TestInlineNested(t *testing.T) {
	input := []byte("[direnv]\ndotenv = { FOO = \"bar\" }\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Direnv == nil { t.Fatal("Direnv is nil") }
	if d.Direnv.Dotenv["FOO"] != "bar" { t.Fatalf("Dotenv[FOO]=%q, want bar", d.Direnv.Dotenv["FOO"]) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}

// The sub-table form must still work — and produce the same decoded value as
// the inline form.
func TestSubTableStillWorks(t *testing.T) {
	input := []byte("[direnv]\n[direnv.dotenv]\nFOO = \"bar\"\n")
	doc, err := DecodeConfig(input)
	if err != nil { t.Fatal(err) }
	d := doc.Data()
	if d.Direnv == nil || d.Direnv.Dotenv["FOO"] != "bar" { t.Fatalf("sub-table form lost data: %+v", d.Direnv) }
	if u := doc.Undecoded(); len(u) != 0 { t.Fatalf("undecoded: %v", u) }
}
`)

	cmd := exec.Command("go", "test", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed: %v\n%s", err, output)
	}
}
