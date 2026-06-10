package generate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Conformance harness for the representability fold (ADR 2026-06-08): every
// claim the model makes about a value-space boundary cell is asserted against
// COMPILED GENERATED CODE, so the static model and the renderer cannot drift
// apart silently — the failure names the cell. Cells cover both faithful and
// lossy regions: lossy cells are pinned (encode is silent, decode collapses to
// the absent default), so closing one later (e.g. teaching the encoder a
// present-empty witness) consciously flips a cell here AND the model together.
//
// Each cell records: the TypeExpr shape (for the fold), which model axis
// decides it (predict), and the empirically verified outcome (faithful). The
// generate-side check `predict(reprOf(shape)) == faithful` fails fast on model
// drift; the generated test then verifies `faithful` against reality.

type confCell struct {
	name      string
	shape     spkType
	omitEmpty bool
	// predict picks the model axis that decides this cell's value class.
	predict func(repr) bool
	// faithful is the empirically verified truth: does the cell's value
	// round-trip? When false the value collapses to the absent default (the
	// zero Config field), which the generated assertion encodes.
	faithful bool
	// setLit populates the cell's value on *d.Data() (variable c).
	setLit string
	// wantLit is the full expected Config literal when faithful.
	wantLit string
	// wire, when non-empty, must appear in the encoded TOML (a presence
	// witness); wireSilent instead requires the encoded TOML to be empty
	// (the value is in the encoder's silent region).
	wire       string
	wireSilent bool
}

func TestRepresentabilityConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Shapes mirroring the fixture package below.
	server := &StructInfo{Name: "Server", Fields: []FieldInfo{
		{GoName: "Host", TomlKey: "host", Type: spkScalar{Codec: codecPrim, TypeName: "string"}},
	}}
	serverT := spkStruct{TypeName: "Server", InnerInfo: server}
	wrapper := &StructInfo{Name: "Wrapper", Fields: []FieldInfo{
		{GoName: "S", TomlKey: "s", Type: spkPtr{Elem: serverT}},
	}}
	allTables := &StructInfo{Name: "AllTables", Fields: []FieldInfo{
		{GoName: "Xs", TomlKey: "xs", Type: spkSlice{Elem: serverT}},
	}}
	boolT := spkScalar{Codec: codecPrim, TypeName: "bool"}
	stringT := spkScalar{Codec: codecPrim, TypeName: "string"}

	cells := []confCell{
		{
			// A non-omitempty zero scalar is suppressed (no `b = false` in an
			// empty doc) but decodes back to zero: silent AND faithful.
			name: "zero-scalar-silent-faithful", shape: boolT,
			predict: func(r repr) bool { return r.MayBeSilent && r.SilentFaithful },
			faithful: true, setLit: `c.B = false`, wantLit: `Config{}`, wireSilent: true,
		},
		{
			// A non-nil pointer scalar always witnesses, zero included.
			name: "ptr-zero-scalar-witnesses", shape: spkPtr{Elem: boolT},
			predict:  func(r repr) bool { return r.FullyFaithful },
			faithful: true, setLit: `v := false; c.PB = &v`, wantLit: `Config{PB: ptr(false)}`, wire: "pb = false",
		},
		{
			// #121: empty []scalar witnesses with `= []`.
			name: "empty-prim-slice", shape: spkSlice{Elem: stringT},
			predict:  func(r repr) bool { return r.EncodeWitnessesEmpty },
			faithful: true, setLit: `c.Tags = []string{}`, wantLit: `Config{Tags: []string{}}`, wire: "tags = []",
		},
		{
			// omitempty collapses empty to absent: lossy at empty, by request.
			name: "empty-prim-slice-omitempty", shape: spkSlice{Elem: stringT}, omitEmpty: true,
			predict:  func(r repr) bool { return r.EncodeWitnessesEmpty },
			faithful: false, setLit: `c.TagsOE = []string{}`, wireSilent: true,
		},
		{
			// #1/#94: the array-of-tables encoder is entry-driven — empty is
			// silent and collapses to nil (decode DOES read `= []`; see the
			// decode-side cells below — the asymmetry is encode-only).
			name: "empty-slice-struct", shape: spkSlice{Elem: serverT},
			predict:  func(r repr) bool { return r.EncodeWitnessesEmpty },
			faithful: false, setLit: `c.Servers = []Server{}`, wireSilent: true,
		},
		{
			// map[string]scalar witnesses present-empty with its bare [table].
			name: "empty-map-scalar", shape: spkMap{Elem: stringT},
			predict:  func(r repr) bool { return r.EncodeWitnessesEmpty },
			faithful: true, setLit: `c.Mc = map[string]string{}`, wantLit: `Config{Mc: map[string]string{}}`, wire: "[mc]",
		},
		{
			// omitempty is a LEAF-only option: ceMapScalar never carries it
			// (comp_build threads OmitEmpty into ceLeaf only), so an omitempty
			// scalar map still witnesses present-empty.
			name: "empty-map-scalar-omitempty-ignored", shape: spkMap{Elem: stringT}, omitEmpty: true,
			predict:  func(r repr) bool { return r.EncodeWitnessesEmpty },
			faithful: true, setLit: `c.McOE = map[string]string{}`, wantLit: `Config{McOE: map[string]string{}}`, wire: "[mc_oe]",
		},
		{
			// map[string]struct is entry-driven: empty is silent → nil.
			name: "empty-map-struct", shape: spkMap{Elem: serverT},
			predict:  func(r repr) bool { return r.EncodeWitnessesEmpty },
			faithful: false, setLit: `c.MS = map[string]Server{}`, wireSilent: true,
		},
		{
			// map[string]NamedMap likewise (compEncMapMap gates on len > 0).
			name: "empty-mapmap", shape: spkMap{Elem: spkMap{Elem: stringT}},
			predict:  func(r repr) bool { return r.EncodeWitnessesEmpty },
			faithful: false, setLit: `c.MM = map[string]Named{}`, wireSilent: true,
		},
		{
			// A pointer struct with a non-array-table body always witnesses via
			// its [table] header: &Wrapper{nil} round-trips. (This cell disproved
			// the first prototype's AND-over-fields silence rule.)
			name: "ptr-struct-header-witnesses", shape: spkPtr{Elem: spkStruct{TypeName: "Wrapper", InnerInfo: wrapper}},
			predict:  func(r repr) bool { return r.FullyFaithful },
			faithful: true, setLit: `c.W = &Wrapper{}`, wantLit: `Config{W: &Wrapper{}}`, wire: "[w]",
		},
		{
			// #122: all fields array-tables → #89 skips the header → a non-nil
			// pointer with an all-silent body collapses to nil.
			name: "ptr-all-array-tables-silent", shape: spkPtr{Elem: spkStruct{TypeName: "AllTables", InnerInfo: allTables}},
			predict:  func(r repr) bool { return r.SilentFaithful },
			faithful: false, setLit: `c.AT = &AllTables{}`, wireSilent: true,
		},
	}

	// Model ↔ empirical-table agreement, before compiling anything: a model
	// change that flips a cell's prediction fails here with the cell named.
	for _, c := range cells {
		r := reprOf(c.shape, c.omitEmpty, false)
		if got := c.predict(r); got != c.faithful {
			t.Fatalf("model drift on cell %q: fold predicts %v, empirical truth is %v (repr=%+v)",
				c.name, got, c.faithful, r)
		}
	}

	// Generate the fixture package and assert each cell against reality.
	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/reprconf", "", "go 1.26", "",
		"require github.com/amarbel-llc/tommy v0.0.0", "",
		"replace github.com/amarbel-llc/tommy => " + repoRoot, "",
	}, "\n"))
	writeFixture(t, dir, "config.go", reprConfFixtureSrc)

	if err := Generate(dir, "config.go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var body strings.Builder
	for _, c := range cells {
		expectLit := c.wantLit
		if !c.faithful {
			// A lossy cell collapses to the absent default — the zero Config.
			expectLit = "Config{}"
		}
		var wireCheck string
		switch {
		case c.wireSilent:
			wireCheck = `	if len(out) != 0 { t.Fatalf("expected a silent encode, got:\n%s", out) }` + "\n"
		case c.wire != "":
			wireCheck = fmt.Sprintf(`	if !strings.Contains(string(out), %q) { t.Fatalf("missing witness %%q in:\n%%s", %q, out) }`+"\n", c.wire, c.wire)
		}
		fmt.Fprintf(&body, `	t.Run(%q, func(t *testing.T) {
	d, err := DecodeConfig([]byte(""))
	if err != nil { t.Fatal(err) }
	c := d.Data()
	%s
	out, err := d.Encode()
	if err != nil { t.Fatalf("encode: %%v", err) }
%s	d2, err := DecodeConfig(out)
	if err != nil { t.Fatalf("re-decode: %%v\ntoml:\n%%s", err, out) }
	expect := %s
	if !reflect.DeepEqual(*d2.Data(), expect) {
		t.Fatalf("cell outcome changed\nexpect: %%s\ngot:    %%s\ntoml:\n%%s", dump(expect), dump(*d2.Data()), out)
	}
	})
`, c.name, c.setLit, wireCheck, expectLit)
	}

	// Cells the per-field repr table doesn't express:
	body.WriteString(reprConfExtraCells)

	testSrc := "package reprconf\n\nimport (\n\t\"fmt\"\n\t\"reflect\"\n\t\"sort\"\n\t\"strings\"\n\t\"testing\"\n)\n\n" +
		"func ptr[T any](v T) *T { return &v }\n\n" + dumpHelperSrc +
		"func TestConformance(t *testing.T) {\n" + body.String() + "}\n"
	writeFixture(t, dir, "conformance_test.go", testSrc)

	cmd := exec.Command("go", "test", "-count=1", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), testGoEnv()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("conformance failed:\n%s\n--- conformance_test.go ---\n%s", out, testSrc)
	}
}

const reprConfFixtureSrc = `package reprconf

//go:generate tommy generate
type Config struct {
	B       bool               ` + "`toml:\"b\"`" + `
	PB      *bool              ` + "`toml:\"pb\"`" + `
	Tags    []string           ` + "`toml:\"tags\"`" + `
	TagsOE  []string           ` + "`toml:\"tags_oe,omitempty\"`" + `
	Servers []Server           ` + "`toml:\"servers\"`" + `
	PServers []*Server         ` + "`toml:\"pservers\"`" + `
	Mc      map[string]string  ` + "`toml:\"mc\"`" + `
	McOE    map[string]string  ` + "`toml:\"mc_oe,omitempty\"`" + `
	MS      map[string]Server  ` + "`toml:\"ms\"`" + `
	MSP     map[string]*Server ` + "`toml:\"msp\"`" + `
	MM      map[string]Named   ` + "`toml:\"mm\"`" + `
	W       *Wrapper           ` + "`toml:\"w\"`" + `
	AT      *AllTables         ` + "`toml:\"at\"`" + `
}

type Named map[string]string

type Server struct {
	Host string ` + "`toml:\"host\"`" + `
}

type Wrapper struct {
	S *Server ` + "`toml:\"s\"`" + `
}

type AllTables struct {
	Xs []Server ` + "`toml:\"xs\"`" + `
}
`

// reprConfExtraCells covers behavior outside the per-field repr table:
// decode-side present-empty reading (the asymmetry's other half) and the
// nil-element policies (TOML has no null; encode must not panic).
const reprConfExtraCells = `	t.Run("decode-reads-empty-array-of-tables", func(t *testing.T) {
	// DecodeReadsEmpty: the decoder maps servers = [] to a NON-nil empty
	// slice — only the encoder lacks the present-empty witness.
	d, err := DecodeConfig([]byte("servers = []\n"))
	if err != nil { t.Fatal(err) }
	if d.Data().Servers == nil || len(d.Data().Servers) != 0 {
		t.Fatalf("expected non-nil empty, got %s", dump(d.Data().Servers))
	}
	})
	t.Run("decode-reads-empty-map-struct-table", func(t *testing.T) {
	d, err := DecodeConfig([]byte("[ms]\n"))
	if err != nil { t.Fatal(err) }
	if d.Data().MS == nil || len(d.Data().MS) != 0 {
		t.Fatalf("expected non-nil empty, got %s", dump(d.Data().MS))
	}
	})
	t.Run("nil-slice-element-skipped", func(t *testing.T) {
	// A nil []*Struct element has no TOML representation: encode skips it
	// (and must not panic — it dereferenced nil before 2026-06-09).
	d, err := DecodeConfig([]byte(""))
	if err != nil { t.Fatal(err) }
	d.Data().PServers = []*Server{nil, {Host: "h"}}
	out, err := d.Encode()
	if err != nil { t.Fatalf("encode: %v", err) }
	d2, err := DecodeConfig(out)
	if err != nil { t.Fatalf("re-decode: %v\ntoml:\n%s", err, out) }
	expect := []*Server{{Host: "h"}}
	if !reflect.DeepEqual(d2.Data().PServers, expect) {
		t.Fatalf("expect %s got %s\ntoml:\n%s", dump(expect), dump(d2.Data().PServers), out)
	}
	})
	t.Run("nil-map-element-becomes-empty-entry", func(t *testing.T) {
	// The map encoder creates the [msp.k] sub-table BEFORE its nil check, so a
	// nil entry round-trips as a non-nil empty struct rather than dropping —
	// pinned here as the (inconsistent with slices) current policy.
	d, err := DecodeConfig([]byte(""))
	if err != nil { t.Fatal(err) }
	d.Data().MSP = map[string]*Server{"k": nil}
	out, err := d.Encode()
	if err != nil { t.Fatalf("encode: %v", err) }
	d2, err := DecodeConfig(out)
	if err != nil { t.Fatalf("re-decode: %v\ntoml:\n%s", err, out) }
	expect := map[string]*Server{"k": {}}
	if !reflect.DeepEqual(d2.Data().MSP, expect) {
		t.Fatalf("expect %s got %s\ntoml:\n%s", dump(expect), dump(d2.Data().MSP), out)
	}
	})
`
