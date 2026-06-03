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
	omitEmpty     bool
}

type shapeGen struct {
	rng      *rand.Rand
	typeDefs *strings.Builder
	subN     int
}

// fuzzableScalars is the fuzzer's scalar universe, DERIVED from the canonical
// scalarTypes registry (scalars.go) rather than hardcoded — so adding a scalar to
// the registry auto-expands fuzz coverage (#24). Every registry row is fuzzed now
// that the sized rows (int8/16/32, uint/8/16/32, float32) have their per-element
// casting decode loop + encode widening (#96). Every name here must have a
// (range-clamped, for sized types) scalarValue case below.
func fuzzableScalars() []string {
	out := make([]string, len(scalarTypes))
	for i, s := range scalarTypes {
		out[i] = s.goName
	}
	return out
}

func (g *shapeGen) scalarType() string {
	fs := fuzzableScalars()
	return fs[g.rng.Intn(len(fs))]
}

func (g *shapeGen) scalarValue(typ string) string {
	// int/bool/float64/string emit a literal whose untyped-constant default type
	// already matches the field, so `ptr(...)` infers correctly. Every other
	// scalar is emitted as a typed conversion (else `*int8` / `[]*uint32` literals
	// fail to compile), and each sized sample is clamped to its type's range so the
	// value survives the round-trip exactly.
	switch typ {
	case "string":
		return strconv.Quote(fmt.Sprintf("s%d", g.rng.Intn(1_000_000)))
	case "bool":
		return strconv.FormatBool(g.rng.Intn(2) == 1)
	case "int":
		return strconv.Itoa(g.rng.Intn(1_000_000) - 500_000)
	case "int8":
		return "int8(" + strconv.Itoa(g.rng.Intn(256)-128) + ")"
	case "int16":
		return "int16(" + strconv.Itoa(g.rng.Intn(65536)-32768) + ")"
	case "int32":
		return "int32(" + strconv.Itoa(g.rng.Intn(1_000_000)-500_000) + ")"
	case "int64":
		return "int64(" + strconv.Itoa(g.rng.Intn(1_000_000)-500_000) + ")"
	case "uint":
		return "uint(" + strconv.Itoa(g.rng.Intn(1_000_000)) + ")"
	case "uint8":
		return "uint8(" + strconv.Itoa(g.rng.Intn(256)) + ")"
	case "uint16":
		return "uint16(" + strconv.Itoa(g.rng.Intn(65536)) + ")"
	case "uint32":
		return "uint32(" + strconv.Itoa(g.rng.Intn(1_000_000)) + ")"
	case "uint64":
		return "uint64(" + strconv.Itoa(g.rng.Intn(1_000_000)) + ")"
	case "float32":
		return fmt.Sprintf("float32(%d.5)", g.rng.Intn(10_000))
	case "float64":
		return fmt.Sprintf("%d.5", g.rng.Intn(10_000))
	}
	return `""`
}

// sliceScalarType is the element type for []scalar / []*scalar — the same
// registry-derived universe as scalarType (every cast-free row has a working
// direct slice extractor + encoder).
func (g *shapeGen) sliceScalarType() string {
	return g.scalarType()
}

// genType picks a random field shape from the surface the codegen actually
// supports: scalars {string,int,bool,float64}, []scalar/[]*scalar over
// {string,int}, map[string]string, map[string]NamedMap (a named map[string]string
// alias, the FieldMapStringMapStringString kind), structs/pointers/slices/maps of
// structs recursing. (Bare map[string]map[string]string, map[string]<non-string>,
// and non-string/int element slices are not supported by the current classifier +
// renderer and are excluded — see the tracked coverage gaps.) At depth<=0 only
// non-struct shapes are produced so recursion terminates.
func (g *shapeGen) genType(depth int) *td {
	const (
		shScalar = iota
		shPtrScalar
		shSliceScalar
		shSlicePtrScalar
		shMapScalar
		shMapMap
		shStruct
		shPtrStruct
		shSliceStruct
		shSlicePtrStruct
		shMapStruct
		shMapPtrStruct
	)
	leaf := []int{shScalar, shPtrScalar, shSliceScalar, shSlicePtrScalar, shMapScalar, shMapMap}
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
	case shMapMap:
		return g.genMapMap()
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
		// #97: randomly tag fields with ,omitempty and (string scalars only)
		// ,multiline. omitempty omits zero/nil/empty, so the value generator must
		// not emit the empty-collection variant for an omitempty field (it would
		// round-trip to nil, not empty) — tracked via tdField.omitEmpty.
		omitEmpty := g.rng.Intn(3) == 0
		multiline := ft.kind == "scalar" && ft.scalar == "string" && g.rng.Intn(3) == 0
		opts := ""
		if omitEmpty {
			opts += ",omitempty"
		}
		if multiline {
			opts += ",multiline"
		}
		f := tdField{name: fmt.Sprintf("F%d", i), tomlKey: fmt.Sprintf("f%d", i), t: ft, omitEmpty: omitEmpty}
		fields = append(fields, f)
		fmt.Fprintf(&body, "\t%s %s `toml:%q`\n", f.name, g.goType(ft), f.tomlKey+opts)
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

// genMapMap emits a named map[string]string alias and returns a
// map[string]<alias> shape — the FieldMapStringMapStringString kind. The alias is
// required: the classifier only accepts the nested-map form through a named map
// type, not bare map[string]map[string]string. stName carries the alias name.
func (g *shapeGen) genMapMap() *td {
	name := fmt.Sprintf("Mapalias%d", g.subN)
	g.subN++
	fmt.Fprintf(g.typeDefs, "type %s map[string]string\n\n", name)
	return &td{kind: "mapmap", stName: name}
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
	case "mapmap":
		return "map[string]" + t.stName
	case "struct":
		return t.stName
	}
	panic("bad td kind: " + t.kind)
}

// genValue emits a Go composite literal populating t. Every slice/map gets two
// distinct entries so a swapped index or mis-scoped entry is caught by DeepEqual.
func (g *shapeGen) genValue(t *td) string {
	// nil generation is confined to representable positions. A nil pointer or nil
	// map as a STRUCT FIELD round-trips cleanly (the key is simply absent), and is
	// emitted ~1/4 of the time by the struct case below. A nil pointer as a
	// slice/map ELEMENT is NOT generated: TOML arrays/sub-tables have no null, so
	// []*T{nil} / map[string]*T{k:nil} are unrepresentable. nil *slices* are also
	// not generated — their decode normalizes inconsistently across element types
	// (nil []string -> nil, but nil []*int/[]Struct -> empty), a fidelity gap
	// tracked separately. Empty non-nil collections normalize to nil and are not
	// generated either.
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
		// Quote-requiring keys (a dot and a space) are the #103 canary: they
		// exercise quoted sub-table header keys (map[string]Struct/NamedMap) at
		// every nesting level. A dotted key in a [m."k.0"] header must round-trip
		// as one segment, not nest as m→k→0.
		return g.goType(t) + "{\"k.0\": " + g.genValue(t.elem) + ", \"k 1\": " + g.genValue(t.elem) + "}"
	case "mapmap":
		// Two outer keys, each a two-entry inner map, so a swapped or mis-scoped
		// entry is caught by DeepEqual. Values are random, so they differ. Keys
		// require quoting (#103 canary).
		inner := func() string {
			return t.stName + "{\"ik.0\": " + g.scalarValue("string") + ", \"ik 1\": " + g.scalarValue("string") + "}"
		}
		return g.goType(t) + "{\"k.0\": " + inner() + ", \"k 1\": " + inner() + "}"
	case "struct":
		var b strings.Builder
		b.WriteString(t.stName + "{")
		for i, f := range t.fields {
			if i > 0 {
				b.WriteString(", ")
			}
			fv := g.genValue(f.t)
			// Emit nil/empty variants where they round-trip faithfully (#21). A nil
			// *scalar / *struct / map / mapmap field is simply an absent key/[table].
			// A primitive slice distinguishes nil (key omitted) from empty `= []`, so
			// generate both. An array-table slice ([]struct / []*struct) has no
			// present-empty TOML form (an array-of-tables exists only with ≥1 entry),
			// so nil≡empty there — keep those populated. Nil collection ELEMENTS stay
			// excluded (TOML has no null).
			// An ,omitempty field omits empty AND nil → both decode nil, so the
			// empty-collection variant (which would round-trip to nil, not empty) is
			// suppressed for omitempty fields; nil and populated still round-trip.
			switch f.t.kind {
			case "mapmap", "ptr":
				if g.rng.Intn(4) == 0 {
					fv = "nil"
				}
			case "map":
				// map[string]string (scalar elem) has a present-empty form (an empty
				// `[table]`), so it's faithful: generate nil AND empty. map[string]Struct
				// uses `[m.key]` sub-tables with no distinct empty representation, so it
				// normalizes nil≡empty — only nil there.
				if f.t.elem.kind == "scalar" {
					switch r := g.rng.Intn(4); {
					case r == 0:
						fv = "nil"
					case r == 1 && !f.omitEmpty:
						fv = g.goType(f.t) + "{}"
					}
				} else if g.rng.Intn(4) == 0 {
					fv = "nil"
				}
			case "slice":
				primElem := f.t.elem.kind == "scalar" ||
					(f.t.elem.kind == "ptr" && f.t.elem.elem.kind == "scalar")
				if primElem {
					switch r := g.rng.Intn(4); {
					case r == 0:
						fv = "nil"
					case r == 1 && !f.omitEmpty:
						fv = g.goType(f.t) + "{}"
					}
				}
			}
			b.WriteString(f.name + ": " + fv)
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

// dumpHelperSrc is injected into the generated fuzz test harness. The default
// %#v rendering prints pointer ADDRESSES (so two structurally-identical values
// that differ only behind a pointer look the same in a mismatch report). dump
// recursively dereferences pointers (showing nil vs &value), sorts map keys, and
// renders slices/structs, so a round-trip mismatch shows the real value diff.
const dumpHelperSrc = `func dump(v any) string {
	var b strings.Builder
	var rec func(rv reflect.Value)
	rec = func(rv reflect.Value) {
		switch rv.Kind() {
		case reflect.Ptr:
			if rv.IsNil() {
				b.WriteString("nil")
				return
			}
			b.WriteString("&")
			rec(rv.Elem())
		case reflect.Interface:
			if rv.IsNil() {
				b.WriteString("nil")
				return
			}
			rec(rv.Elem())
		case reflect.Slice:
			if rv.IsNil() {
				b.WriteString("nil")
				return
			}
			b.WriteString("[")
			for i := 0; i < rv.Len(); i++ {
				if i > 0 {
					b.WriteString(", ")
				}
				rec(rv.Index(i))
			}
			b.WriteString("]")
		case reflect.Map:
			if rv.IsNil() {
				b.WriteString("nil")
				return
			}
			keys := rv.MapKeys()
			sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })
			b.WriteString("{")
			for i, k := range keys {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(fmt.Sprintf("%v: ", k))
				rec(rv.MapIndex(k))
			}
			b.WriteString("}")
		case reflect.Struct:
			b.WriteString(rv.Type().Name() + "{")
			for i := 0; i < rv.NumField(); i++ {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(rv.Type().Field(i).Name + ": ")
				rec(rv.Field(i))
			}
			b.WriteString("}")
		default:
			b.WriteString(fmt.Sprintf("%#v", rv.Interface()))
		}
	}
	rec(reflect.ValueOf(v))
	return b.String()
}

`

func TestRoundTripFuzz(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	seed := int64(fuzzEnvInt("TOMMY_FUZZ_SEED", 1))
	cases := fuzzEnvInt("TOMMY_FUZZ_CASES", 96)
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
			t.Fatalf("round-trip mismatch\nwant: %%s\ngot:  %%s\ntoml:\n%%s", dump(want), dump(*d2.Data()), out)
		}
		if u := d2.Undecoded(); len(u) != 0 {
			t.Fatalf("undecoded keys %%v\ntoml:\n%%s", u, out)
		}
	})
`, name, value, name, name)
	}

	configSrc := "package fuzz\n\n" + g.typeDefs.String()
	testSrc := "package fuzz\n\nimport (\n\t\"fmt\"\n\t\"reflect\"\n\t\"sort\"\n\t\"strings\"\n\t\"testing\"\n)\n\n" +
		"func ptr[T any](v T) *T { return &v }\n\n" +
		dumpHelperSrc +
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
