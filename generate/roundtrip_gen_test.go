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
	pkg    string // struct: import qualifier ("" = local pkg, "dep" = cross-pkg)
	fields []tdField
}

// qualify prefixes a type name with its package selector for a cross-package
// reference (dep.Dep0). The type *definition* always uses the bare name; only
// references from another package are qualified.
func qualify(pkg, name string) string {
	if pkg == "" {
		return name
	}
	return pkg + "." + name
}

// maybeEmitValidate appends a no-op Validate() to a generated type ~1/3 of the
// time. A struct implementing Validate() gets the call injected into its
// generated receiver Decode/Encode (the Validatable path); returning nil keeps
// the round-trip green while exercising that call site. Only types with a
// //go:generate directive (Case structs, dep structs) get a receiver
// Decode/Encode, so only those are passed here.
func (g *shapeGen) maybeEmitValidate(buf *strings.Builder, name string) {
	if g.rng.Intn(3) == 0 {
		fmt.Fprintf(buf, "func (%s) Validate() error { return nil }\n\n", name)
	}
}

type tdField struct {
	name, tomlKey string
	t             *td
	omitEmpty     bool
}

type shapeGen struct {
	rng      *rand.Rand
	typeDefs *strings.Builder
	depDefs  *strings.Builder // cross-package (dep) type defs, when fuzzing delegation
	subN     int

	// delegating, when set, lets genType inject delegated dep fields at any
	// depth (the cross-package fuzzer, #105). A delegated field nested inside a
	// same-package slice/map container decodes through compScopedBody → the
	// compScopedDel* paths, which the flat top-level-only variant never reached.
	delegating bool
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
	// Cross-package fuzzer (#105): with delegation enabled, ~1/3 of fields are a
	// delegated dep target. dep structs are self-terminating (flat fields), so
	// this adds no recursion; placing it before the depth gate lets delegated
	// fields appear at leaf depth too. The other 2/3 keep producing same-package
	// structs/slices/maps that WRAP these delegated fields, yielding the scoped
	// delegation shapes (compScopedDel*).
	if g.delegating && g.rng.Intn(3) == 0 {
		return g.genDelegatedField()
	}
	// Same-package TextMarshaler coverage (a leaf): ~1/8 of fields are a
	// TextMarshaler value or a []TextMarshaler slice.
	if g.rng.Intn(8) == 0 {
		txt := g.genTextType(g.typeDefs, "")
		if g.rng.Intn(2) == 0 {
			return txt
		}
		return &td{kind: "slice", elem: txt}
	}
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

// genTextType emits a TextMarshaler/TextUnmarshaler type (a string newtype with
// an identity text codec, so it round-trips) into buf, and returns it as a
// "text" shape in package pkg. Covers FieldTextMarshaler (and, as a slice
// element, FieldSliceTextMarshaler) — including the cross-package import paths
// when pkg="dep". The type needs no //go:generate (it has no Decode/Encode).
func (g *shapeGen) genTextType(buf *strings.Builder, pkg string) *td {
	name := fmt.Sprintf("Text%d", g.subN)
	g.subN++
	fmt.Fprintf(buf, "type %s string\n\n", name)
	fmt.Fprintf(buf, "func (t %s) MarshalText() ([]byte, error) { return []byte(t), nil }\n\n", name)
	fmt.Fprintf(buf, "func (t *%s) UnmarshalText(b []byte) error { *t = %s(b); return nil }\n\n", name, name)
	return &td{kind: "text", stName: name, pkg: pkg}
}

// goType renders a Go type as referenced from the MAIN package: a dep struct
// becomes dep.DepN. For emitting a type INSIDE the dep package (where dep types
// are bare), use goTypeIn(t, "dep").
func (g *shapeGen) goType(t *td) string {
	return g.goTypeIn(t, "")
}

// goTypeIn renders t's Go type as written from package cur ("" = main, "dep" =
// the dep subpackage). A struct in a different package than cur is qualified
// (dep.DepN); a struct in cur itself stays bare (DepN) — so a nested dep struct
// is bare in dep.go but dep.-qualified in a main-package value literal (#105).
func (g *shapeGen) goTypeIn(t *td, cur string) string {
	switch t.kind {
	case "scalar":
		return t.scalar
	case "ptr":
		return "*" + g.goTypeIn(t.elem, cur)
	case "slice":
		return "[]" + g.goTypeIn(t.elem, cur)
	case "map":
		return "map[string]" + g.goTypeIn(t.elem, cur)
	case "mapmap":
		if t.pkg == cur {
			return "map[string]" + t.stName
		}
		return "map[string]" + qualify(t.pkg, t.stName)
	case "struct", "namedmapstruct", "text":
		// All render as a (possibly qualified) named type: a struct, a named
		// map[string]struct alias used as a direct field (#105), or a
		// TextMarshaler type.
		if t.pkg == cur {
			return t.stName
		}
		return qualify(t.pkg, t.stName)
	}
	panic("bad td kind: " + t.kind)
}

// genValue emits a Go composite literal populating t. Every slice/map gets two
// distinct entries so a swapped index or mis-scoped entry is caught by DeepEqual.
func (g *shapeGen) genValue(t *td) string {
	// nil/empty generation is confined to representable positions, all injected by
	// the struct case below (~1/4 each). As a STRUCT FIELD, a nil pointer / nil map
	// / nil slice round-trips cleanly (the key is simply absent). A present-but-
	// empty PRIMITIVE slice (`= []`) and an empty map[string]string (an empty
	// `[table]`) are distinct TOML forms, so both nil AND empty are generated for
	// them. A map[string]Struct has no present-empty form, so only its nil variant
	// is generated, never `{}`. An array-of-tables ([]struct / []*struct) generates
	// NEITHER nil nor empty: a nil one as a struct's only emitting field makes that
	// struct encode to nothing — unrepresentable behind a pointer (it round-trips
	// to a nil pointer, cf. #89) — so it stays populated. A nil pointer as a
	// slice/map ELEMENT is never generated either: TOML arrays/sub-tables have no
	// null, so []*T{nil} / map[string]*T{k:nil} are unrepresentable.
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
			return qualify(t.pkg, t.stName) + "{\"ik.0\": " + g.scalarValue("string") + ", \"ik 1\": " + g.scalarValue("string") + "}"
		}
		return g.goType(t) + "{\"k.0\": " + inner() + ", \"k 1\": " + inner() + "}"
	case "namedmapstruct":
		// A named map[string]struct alias (dep.NamedMapStructN). Two quote-
		// requiring keys, each a struct value, so a swap/mis-scope is caught (#105).
		lit := qualify(t.pkg, t.stName)
		return lit + "{\"k.0\": " + g.genValue(t.elem) + ", \"k 1\": " + g.genValue(t.elem) + "}"
	case "text":
		// A TextMarshaler value: a typed conversion from an (alphanumeric, so
		// escape-safe) string literal that round-trips through MarshalText.
		return qualify(t.pkg, t.stName) + "(" + g.scalarValue("string") + ")"
	case "struct":
		var b strings.Builder
		b.WriteString(qualify(t.pkg, t.stName) + "{")
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
			case "mapmap", "ptr", "namedmapstruct":
				// namedmapstruct is a map[string]struct alias — no present-empty
				// form (like map[string]Struct), so nil-only, never `{}`.
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
					// A primitive slice distinguishes nil (key omitted) from empty
					// (`= []`) — exercise both.
					switch r := g.rng.Intn(4); {
					case r == 0:
						fv = "nil"
					case r == 1 && !f.omitEmpty:
						fv = g.goType(f.t) + "{}"
					}
				}
				// An array-of-tables ([]struct / []*struct) stays populated: it has
				// no present-empty TOML form, and a nil one that is a struct's only
				// emitting field makes that struct encode to nothing — which is
				// unrepresentable behind a pointer (it round-trips to a nil pointer,
				// cf. #89's no-spurious-bare-section rule). So neither nil nor empty
				// is generated for it.
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
		g.maybeEmitValidate(g.typeDefs, name)
		value := g.genValue(&td{kind: "struct", stName: name, fields: fields})
		testBodies.WriteString(roundTripCaseBody(name, value))
	}

	configSrc := "package fuzz\n\n" + g.typeDefs.String()
	testSrc := fuzzTestSource(testBodies.String())

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

// roundTripCaseBody renders the per-case t.Run(...) block shared by both
// fuzzers: build want, decode the empty doc, set, encode, re-decode, and assert
// DeepEqual + no undecoded keys.
func roundTripCaseBody(name, value string) string {
	return fmt.Sprintf(`	t.Run(%q, func(t *testing.T) {
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

// fuzzTestSource wraps the accumulated t.Run case bodies into a complete
// fuzz_test.go (package + imports + the ptr/dump helpers + TestRoundTrip).
// extraImports adds non-stdlib imports the case bodies reference — the dep
// subpackage for the delegation fuzzer, whose value literals name dep.DepN.
func fuzzTestSource(testBodies string, extraImports ...string) string {
	var imp strings.Builder
	imp.WriteString("import (\n\t\"fmt\"\n\t\"reflect\"\n\t\"sort\"\n\t\"strings\"\n\t\"testing\"\n")
	for _, p := range extraImports {
		fmt.Fprintf(&imp, "\t%q\n", p)
	}
	imp.WriteString(")\n\n")
	return "package fuzz\n\n" + imp.String() +
		"func ptr[T any](v T) *T { return &v }\n\n" +
		dumpHelperSrc +
		"func TestRoundTrip(t *testing.T) {\n" + testBodies + "}\n"
}

// --- Cross-package delegation fuzzing (#105) ---
//
// The same-package fuzzer never exercises the delegated codegen paths
// (compDelStruct / compDelSlice / compDelMap and their compScopedDel* duals):
// with every generated type in one package the classifier always picks the
// inline kinds. This variant emits a `dep` SUBPACKAGE of
// `//go:generate tommy generate` target structs and references them from the
// main package as the delegated field shapes, forcing delegation. A subpackage
// suffices — delegation triggers on the package (import-path) boundary, not the
// module boundary. The #103 compDelMap dotted-key bug lived exactly here and was
// invisible to the same-package sweep, which is what motivated this (#105).

// depFieldType returns a field shape for a delegated-target struct. At
// depth<=0 (or 2/3 of the time) it is a flat leaf (scalar, []scalar/[]*scalar,
// map[string]string). Otherwise it nests another dep struct in one of the
// supported shapes — exercising the delegated decoder's handling of its OWN
// nested same-package structs/slices/maps (#105). All dep types stay in the dep
// package, so a nested dep struct is inline within the delegated DecodeInto.
func (g *shapeGen) depFieldType(depth int) *td {
	if depth > 0 && g.rng.Intn(3) == 0 {
		sub := g.genDepStruct(depth - 1)
		switch g.rng.Intn(4) {
		case 0:
			return sub
		case 1:
			return &td{kind: "ptr", elem: sub}
		case 2:
			return &td{kind: "slice", elem: sub}
		default:
			return &td{kind: "map", elem: sub}
		}
	}
	switch g.rng.Intn(4) {
	case 0:
		return &td{kind: "slice", elem: &td{kind: "scalar", scalar: g.sliceScalarType()}}
	case 1:
		return &td{kind: "slice", elem: &td{kind: "ptr", elem: &td{kind: "scalar", scalar: g.sliceScalarType()}}}
	case 2:
		return &td{kind: "map", elem: &td{kind: "scalar", scalar: "string"}}
	default:
		return &td{kind: "scalar", scalar: g.scalarType()}
	}
}

// genDepStruct emits a delegated-target struct into the dep package buffer and
// returns its descriptor (pkg="dep", so references from the main package are
// qualified dep.DepN while its own definition stays bare). depth bounds nested
// dep structs. Field types are rendered with the "dep" view so a nested dep
// struct is bare (Dep5) in the definition, not dep.Dep5.
func (g *shapeGen) genDepStruct(depth int) *td {
	name := fmt.Sprintf("Dep%d", g.subN)
	g.subN++
	k := 1 + g.rng.Intn(3)
	var fields []tdField
	var body strings.Builder
	for i := 0; i < k; i++ {
		ft := g.depFieldType(depth)
		f := tdField{name: fmt.Sprintf("F%d", i), tomlKey: fmt.Sprintf("f%d", i), t: ft}
		fields = append(fields, f)
		fmt.Fprintf(&body, "\t%s %s `toml:%q`\n", f.name, g.goTypeIn(ft, "dep"), f.tomlKey)
	}
	fmt.Fprintf(g.depDefs, "//go:generate tommy generate\ntype %s struct {\n%s}\n\n", name, body.String())
	g.maybeEmitValidate(g.depDefs, name)
	return &td{kind: "struct", stName: name, pkg: "dep", fields: fields}
}

// genDepMapMap emits a named map[string]string alias into the dep package and
// returns a map[string]<alias> shape qualified to dep (map[string]dep.NamedMapN).
// This is the FieldMapStringMapStringString kind reached through a CROSS-package
// alias — classified as a nested-string-map with an import, decoded/encoded via
// compMapMap/compScopedMapMap casting to the dep alias (#105). The alias needs no
// //go:generate (a bare map type has no Decode/Encode), only to exist in dep.
func (g *shapeGen) genDepMapMap() *td {
	name := fmt.Sprintf("NamedMap%d", g.subN)
	g.subN++
	fmt.Fprintf(g.depDefs, "type %s map[string]string\n\n", name)
	return &td{kind: "mapmap", stName: name, pkg: "dep"}
}

// genDepNamedMapStruct emits a named map[string]<dep struct> alias into the dep
// package and returns it as a direct-field shape (Actions dep.NamedMapStructN).
// This is the hand-fixture shape (type ScriptMap map[string]Script used directly)
// the algebra previously couldn't generate (#105): a named map alias whose value
// is a struct, classified as a delegated map carrying the alias type name. The
// alias itself needs no //go:generate; its value struct (emitted by genDepStruct)
// does.
func (g *shapeGen) genDepNamedMapStruct() *td {
	val := g.genDepStruct(1)
	name := fmt.Sprintf("NamedMapStruct%d", g.subN)
	g.subN++
	fmt.Fprintf(g.depDefs, "type %s map[string]%s\n\n", name, g.goTypeIn(val, "dep"))
	return &td{kind: "namedmapstruct", stName: name, pkg: "dep", elem: val}
}

// genDelegatedField returns a cross-package field shape: a map[string]dep.NamedMap
// (named string-map alias), a dep.NamedMapStructN (named struct-map alias used as
// a direct field), or a dep target struct wrapped in one of the five delegated
// shapes the codegen supports. map[string]*T is excluded: the classifier rejects
// cross-package pointer-struct map values (analyze.go).
func (g *shapeGen) genDelegatedField() *td {
	switch g.rng.Intn(7) {
	case 0:
		return g.genDepMapMap()
	case 1:
		return g.genDepNamedMapStruct()
	case 2:
		// cross-package TextMarshaler value or []TextMarshaler slice (import +
		// codec edge cases the hand fixtures cover heavily).
		txt := g.genTextType(g.depDefs, "dep")
		if g.rng.Intn(2) == 0 {
			return txt
		}
		return &td{kind: "slice", elem: txt}
	}
	const depDepth = 2
	dep := g.genDepStruct(depDepth)
	switch g.rng.Intn(5) {
	case 0:
		return dep
	case 1:
		return &td{kind: "ptr", elem: dep}
	case 2:
		return &td{kind: "slice", elem: dep}
	case 3:
		return &td{kind: "slice", elem: &td{kind: "ptr", elem: dep}}
	default:
		return &td{kind: "map", elem: dep}
	}
}

func TestRoundTripFuzzDelegation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	seed := int64(fuzzEnvInt("TOMMY_FUZZ_SEED", 1))
	cases := fuzzEnvInt("TOMMY_FUZZ_CASES", 48)
	const depth = 4
	t.Logf("delegation round-trip fuzz: seed=%d cases=%d depth=%d", seed, cases, depth)

	// Same recursive generator as the same-package fuzzer, but with delegation
	// enabled: fields may be delegated dep targets at any depth, so a delegated
	// field can land inside a same-package slice/map and exercise the scoped
	// delegation paths (compScopedDel*), not just the top-level compDel* ones.
	g := &shapeGen{rng: rand.New(rand.NewSource(seed)), typeDefs: &strings.Builder{}, depDefs: &strings.Builder{}, delegating: true}
	var testBodies strings.Builder
	for i := 0; i < cases; i++ {
		name := fmt.Sprintf("Case%d", i)
		fields, body := g.genFields(depth - 1)
		fmt.Fprintf(g.typeDefs, "//go:generate tommy generate\ntype %s struct {\n%s}\n\n", name, body)
		g.maybeEmitValidate(g.typeDefs, name)
		value := g.genValue(&td{kind: "struct", stName: name, fields: fields})
		testBodies.WriteString(roundTripCaseBody(name, value))
	}

	// A seed may, in principle, generate no delegated field; only wire the dep
	// subpackage + its import when one was actually emitted, so the fixture never
	// carries an unused import.
	hasDep := g.depDefs.Len() > 0
	configHeader := "package fuzz\n\n"
	var extraImports []string
	if hasDep {
		configHeader += "import \"example.com/fuzz/dep\"\n\n"
		extraImports = []string{"example.com/fuzz/dep"}
	}
	configSrc := configHeader + g.typeDefs.String()
	testSrc := fuzzTestSource(testBodies.String(), extraImports...)

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

	// dep must be generated before the consumer compiles (its delegated
	// Decode*Into/Encode*From are what config's generated code calls), but
	// analysis order is irrelevant: the main package resolves dep's types from
	// source, not its generated methods.
	if hasDep {
		depSrc := "package dep\n\n" + g.depDefs.String()
		writeFixture(t, filepath.Join(dir, "dep"), "dep.go", depSrc)
		if err := Generate(filepath.Join(dir, "dep"), "dep.go"); err != nil {
			t.Fatalf("Generate dep (seed=%d): %v\n--- dep.go ---\n%s", seed, err, depSrc)
		}
	}
	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate (seed=%d): %v\n--- config.go ---\n%s", seed, err, configSrc)
	}

	cmd := exec.Command("go", "test", "-count=1", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		// On a codegen *compile* failure the source alone isn't enough — dump the
		// generated config_tommy.go too. (CI captures the full output; locally,
		// reduce TOMMY_FUZZ_CASES to shrink it.)
		gen, _ := os.ReadFile(filepath.Join(dir, "config_tommy.go"))
		t.Fatalf("delegation round-trip fuzz failed (seed=%d):\n%s\n--- config.go ---\n%s\n--- config_tommy.go ---\n%s\n--- dep.go ---\n%s", seed, out, configSrc, gen, g.depDefs.String())
	}
}
