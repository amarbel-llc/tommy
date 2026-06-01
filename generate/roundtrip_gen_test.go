package generate

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Generative round-trip property test (#84 ADR: "a small generative grammar can
// be fuzzed (decode∘encode == id)"). It builds random *legal* nested struct
// shapes — every composition the current classifier accepts, with structs,
// slices and maps recursing — generates code for each, then asserts
// decode(encode(value)) == value starting from an empty document. This catches
// unhandled shape *combinations* (the #50/#55/#62/#86/#87 class) in CI rather
// than in a downstream migration.
//
// Scope: structural combinators + primitive scalars, where TOML's value/table
// duality and nested-scoping bugs live. The text/custom codecs and cross-package
// delegation are covered by hand fixtures + the delegation matrix; they are not
// the nesting-scoping locus and need extra type impls / a second module.
//
// Determinism: a fixed seed (override TOMMY_FUZZ_SEED) and case count
// (TOMMY_FUZZ_CASES) so CI runs the same shapes every time and failures
// reproduce. On failure the generated config.go is printed.

// td describes a generated type: enough to emit both its Go type text and a
// matching value literal.
type td struct {
	kind   string // scalar | ptr | slice | map | mapmap | struct
	scalar string // scalar: base type
	elem   *td    // ptr/slice/map: element
	stName string // struct: type name
	fields []tdField
}

type tdField struct {
	name, tomlKey string
	t             *td
}

type shapeGen struct {
	rng      *rand.Rand
	typeDefs *strings.Builder
	subN     int
}

func (g *shapeGen) scalarType() string {
	return []string{"string", "int", "bool", "float64"}[g.rng.Intn(4)]
}

func (g *shapeGen) scalarValue(typ string) string {
	switch typ {
	case "string":
		return strconv.Quote(fmt.Sprintf("s%d", g.rng.Intn(1_000_000)))
	case "int":
		return strconv.Itoa(g.rng.Intn(1_000_000) - 500_000)
	case "bool":
		return strconv.FormatBool(g.rng.Intn(2) == 1)
	case "float64":
		return fmt.Sprintf("%d.5", g.rng.Intn(10_000))
	}
	return `""`
}

// sliceScalarType is the element type for []scalar / []*scalar. The renderer's
// cstSliceExtractFunc only implements string and int slices, so the fuzzer stays
// within that surface; other element slices are a separate coverage gap tracked
// out-of-band.
func (g *shapeGen) sliceScalarType() string {
	return []string{"string", "int"}[g.rng.Intn(2)]
}

// genType picks a random field shape from the surface the codegen actually
// supports: scalars {string,int,bool,float64}, []scalar/[]*scalar over
// {string,int}, map[string]string, structs/pointers/slices/maps of structs
// recursing. (Bare map[string]map[string]string, map[string]<non-string>, and
// non-string/int element slices are not supported by the current classifier +
// renderer and are excluded — see the tracked coverage gaps.) At depth<=0 only
// non-struct shapes are produced so recursion terminates.
func (g *shapeGen) genType(depth int) *td {
	const (
		shScalar = iota
		shPtrScalar
		shSliceScalar
		shSlicePtrScalar
		shMapScalar
		shStruct
		shPtrStruct
		shSliceStruct
		shSlicePtrStruct
		shMapStruct
		shMapPtrStruct
	)
	leaf := []int{shScalar, shPtrScalar, shSliceScalar, shSlicePtrScalar, shMapScalar}
	all := append(append([]int{}, leaf...), shStruct, shPtrStruct, shSliceStruct, shSlicePtrStruct, shMapStruct, shMapPtrStruct)

	choices := all
	if depth <= 0 {
		choices = leaf
	}
	scal := func() *td { return &td{kind: "scalar", scalar: g.scalarType()} }
	sliceScal := func() *td { return &td{kind: "scalar", scalar: g.sliceScalarType()} }
	switch choices[g.rng.Intn(len(choices))] {
	case shScalar:
		return scal()
	case shPtrScalar:
		return &td{kind: "ptr", elem: scal()}
	case shSliceScalar:
		return &td{kind: "slice", elem: sliceScal()}
	case shSlicePtrScalar:
		return &td{kind: "slice", elem: &td{kind: "ptr", elem: sliceScal()}}
	case shMapScalar:
		return &td{kind: "map", elem: &td{kind: "scalar", scalar: "string"}}
	case shStruct:
		return g.genStruct(depth)
	case shPtrStruct:
		return &td{kind: "ptr", elem: g.genStruct(depth)}
	case shSliceStruct:
		return &td{kind: "slice", elem: g.genStruct(depth)}
	case shSlicePtrStruct:
		return &td{kind: "slice", elem: &td{kind: "ptr", elem: g.genStruct(depth)}}
	case shMapStruct:
		return &td{kind: "map", elem: g.genStruct(depth)}
	case shMapPtrStruct:
		return &td{kind: "map", elem: &td{kind: "ptr", elem: g.genStruct(depth)}}
	}
	panic("unreachable")
}

// genFields generates 1..3 fields, emitting any nested struct type defs as a side
// effect. Returns the field descriptors and the struct-body source.
func (g *shapeGen) genFields(depth int) ([]tdField, string) {
	k := 1 + g.rng.Intn(3)
	var fields []tdField
	var body strings.Builder
	for i := 0; i < k; i++ {
		ft := g.genType(depth - 1)
		f := tdField{name: fmt.Sprintf("F%d", i), tomlKey: fmt.Sprintf("f%d", i), t: ft}
		fields = append(fields, f)
		fmt.Fprintf(&body, "\t%s %s `toml:%q`\n", f.name, g.goType(ft), f.tomlKey)
	}
	return fields, body.String()
}

func (g *shapeGen) genStruct(depth int) *td {
	name := fmt.Sprintf("Sub%d", g.subN)
	g.subN++
	fields, body := g.genFields(depth)
	fmt.Fprintf(g.typeDefs, "type %s struct {\n%s}\n\n", name, body)
	return &td{kind: "struct", stName: name, fields: fields}
}

func (g *shapeGen) goType(t *td) string {
	switch t.kind {
	case "scalar":
		return t.scalar
	case "ptr":
		return "*" + g.goType(t.elem)
	case "slice":
		return "[]" + g.goType(t.elem)
	case "map":
		return "map[string]" + g.goType(t.elem)
	case "struct":
		return t.stName
	}
	panic("bad td kind: " + t.kind)
}

// genValue emits a Go composite literal populating t. Every slice/map gets two
// distinct entries so a swapped index or mis-scoped entry is caught by DeepEqual.
func (g *shapeGen) genValue(t *td) string {
	switch t.kind {
	case "scalar":
		return g.scalarValue(t.scalar)
	case "ptr":
		if t.elem.kind == "struct" {
			return "&" + g.genValue(t.elem)
		}
		return "ptr(" + g.genValue(t.elem) + ")"
	case "slice":
		return g.goType(t) + "{" + g.genValue(t.elem) + ", " + g.genValue(t.elem) + "}"
	case "map":
		return g.goType(t) + "{\"k0\": " + g.genValue(t.elem) + ", \"k1\": " + g.genValue(t.elem) + "}"
	case "struct":
		var b strings.Builder
		b.WriteString(t.stName + "{")
		for i, f := range t.fields {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(f.name + ": " + g.genValue(f.t))
		}
		b.WriteString("}")
		return b.String()
	}
	panic("bad td kind: " + t.kind)
}

func fuzzEnvInt(name string, def int) int {
	if s := os.Getenv(name); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			return v
		}
	}
	return def
}

func TestRoundTripFuzz(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	seed := int64(fuzzEnvInt("TOMMY_FUZZ_SEED", 1))
	cases := fuzzEnvInt("TOMMY_FUZZ_CASES", 48)
	const depth = 4
	t.Logf("round-trip fuzz: seed=%d cases=%d depth=%d", seed, cases, depth)

	g := &shapeGen{rng: rand.New(rand.NewSource(seed)), typeDefs: &strings.Builder{}}
	var testBodies strings.Builder
	for i := 0; i < cases; i++ {
		name := fmt.Sprintf("Case%d", i)
		fields, body := g.genFields(depth - 1)
		fmt.Fprintf(g.typeDefs, "//go:generate tommy generate\ntype %s struct {\n%s}\n\n", name, body)
		value := g.genValue(&td{kind: "struct", stName: name, fields: fields})
		fmt.Fprintf(&testBodies, `	t.Run(%q, func(t *testing.T) {
		want := %s
		d, err := Decode%s([]byte(""))
		if err != nil { t.Fatalf("decode empty: %%v", err) }
		*d.Data() = want
		out, err := d.Encode()
		if err != nil { t.Fatalf("encode: %%v", err) }
		d2, err := Decode%s(out)
		if err != nil { t.Fatalf("re-decode: %%v\ntoml:\n%%s", err, out) }
		if !reflect.DeepEqual(*d2.Data(), want) {
			t.Fatalf("round-trip mismatch\nwant: %%#v\ngot:  %%#v\ntoml:\n%%s", want, *d2.Data(), out)
		}
		if u := d2.Undecoded(); len(u) != 0 {
			t.Fatalf("undecoded keys %%v\ntoml:\n%%s", u, out)
		}
	})
`, name, value, name, name)
	}

	configSrc := "package fuzz\n\n" + g.typeDefs.String()
	testSrc := "package fuzz\n\nimport (\n\t\"reflect\"\n\t\"testing\"\n)\n\n" +
		"func ptr[T any](v T) *T { return &v }\n\n" +
		"func TestRoundTrip(t *testing.T) {\n" + testBodies.String() + "}\n"

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/fuzz", "", "go 1.26", "",
		"require github.com/amarbel-llc/tommy v0.0.0", "",
		"replace github.com/amarbel-llc/tommy => " + repoRoot, "",
	}, "\n"))
	writeFixture(t, dir, "config.go", configSrc)
	writeFixture(t, dir, "fuzz_test.go", testSrc)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate (seed=%d): %v\n--- config.go ---\n%s", seed, err, configSrc)
	}

	cmd := exec.Command("go", "test", "-count=1", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("round-trip fuzz failed (seed=%d):\n%s\n--- config.go ---\n%s", seed, out, configSrc)
	}
}
