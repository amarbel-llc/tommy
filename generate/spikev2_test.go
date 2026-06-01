package generate

// COMPOSITIONAL-IR PROOF-OF-CONCEPT — ADR step #2 (option B), DECODE + ENCODE
// (ADR docs/decisions/2026-06-01-compositional-codegen.md, option B).
//
// Unlike the equivalence harness (spike_compositional_test.go), this does NOT
// reproduce the current ~32-op IR. It defines SMALL compositional decode and
// encode IRs (~4 nodes each) and NEW renderers over them, then validates by
// compiling and RUNNING the generated decode/encode against real TOML (byte-diff
// is gone; behaviour and comment-preserving round-trip are the oracle).
//
// The shrink it demonstrates:
//   - Ptr(Struct) = v2NilGuard wrapping a SINGLE v2Table — no TableFields/
//     FlatFields duplication (compare InPointerTable in ir.go).
//   - Slice(Struct)/Slice(Ptr(Struct)) = one v2ArrayTable; the pointer is a
//     structural flag, not a distinct op.
//   - One renderer walk per direction handles every node; both folds reuse the
//     algebra's fieldType from the equivalence harness.
//
// SCOPE: Scalar (incl. *scalar), Slice(scalar), Struct, Ptr(Struct),
// Slice(Struct), Slice(Ptr(Struct)). Deliberately omitted (and panicked on):
//   - the InPointerTable flat-key fallback (#55) — a behaviour the redesign
//     must consciously decide to keep; the fixture uses explicit [tables].
//   - nested containers INSIDE []struct entries — needs entry-relative table
//     matching (the jenPosOp path).
//   - maps, delegation, custom/text codecs, omitempty-delete, multiline,
//     handle-tracking — later coverage.
// No undecoded-key (consumed) tracking — irrelevant to the round-trip oracle.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	jen "github.com/dave/jennifer/jen"
)

// --- Compositional decode IR ---

type v2node interface{ isV2() }

// v2Leaf: a key-value scanned in the current container (Scalar / *Scalar /
// Slice(Scalar)).
type v2Leaf struct {
	Tgt     TargetPath
	Key     string
	Codec   extractInfo // reused from the real renderer
	Ptr     bool        // *scalar -> assign &v
	Slice   bool        // []scalar -> inline array
	SliceFn string      // cst.ExtractXSlice
}

// v2Table: Struct — find [Dotted] from the document root, recurse children
// scoped to the found node.
type v2Table struct {
	Dotted   string
	Children []v2node
}

// v2NilGuard: Ptr(Struct) — the pointer concern as ONE node wrapping a single
// table body. No duplicated field lists.
type v2NilGuard struct {
	Tgt      TargetPath
	TypeName string
	Dotted   string
	LocalVar string
	Children []v2node
}

// v2ArrayTable: Slice(Struct) / Slice(Ptr(Struct)) — iterate [[Dotted]].
type v2ArrayTable struct {
	Tgt      TargetPath
	TypeName string
	Dotted   string
	SlicePtr bool
	Children []v2node // leaf-only in this spike
}

func (v2Leaf) isV2()       {}
func (v2Table) isV2()      {}
func (v2NilGuard) isV2()   {}
func (v2ArrayTable) isV2() {}

// --- The decode fold (over the shared TypeExpr algebra) ---

type v2pos struct {
	dotted string
	tgt    TargetPath
}

func (p v2pos) child(tomlKey, goName string) (string, TargetPath) {
	d := tomlKey
	if p.dotted != "" {
		d = p.dotted + "." + tomlKey
	}
	return d, p.tgt.Dot(goName)
}

func foldV2DecodeStruct(si *StructInfo, pos v2pos) []v2node {
	var out []v2node
	for _, fi := range si.Fields {
		out = append(out, foldV2DecodeField(fi, pos))
	}
	return out
}

func foldV2DecodeField(fi FieldInfo, pos v2pos) v2node {
	dotted, fieldTgt := pos.child(fi.TomlKey, fi.GoName)

	switch te := fieldType(fi).(type) {
	case spkScalar:
		if te.Codec != codecPrim {
			panic("v2 spike: non-primitive scalar codec out of scope")
		}
		return v2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, Codec: cstExtract(fi.TypeName)}

	case spkPtr:
		switch te.Elem.(type) {
		case spkScalar:
			return v2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, Codec: cstExtract(fi.TypeName), Ptr: true}
		case spkStruct:
			lv := toLowerFirst(fi.GoName) + "Val"
			return v2NilGuard{
				Tgt: fieldTgt, TypeName: fi.TypeName, Dotted: dotted, LocalVar: lv,
				Children: foldV2DecodeStruct(fi.InnerInfo, v2pos{dotted: dotted, tgt: LocalTarget(lv)}),
			}
		}

	case spkSlice:
		switch elem := te.Elem.(type) {
		case spkScalar:
			return v2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, Slice: true, SliceFn: cstSliceExtractFunc(fi.ElemType)}
		case spkStruct:
			return v2ArrayTableNode(fi, fieldTgt, dotted, false)
		case spkPtr:
			if _, ok := elem.Elem.(spkStruct); ok {
				return v2ArrayTableNode(fi, fieldTgt, dotted, true)
			}
		}

	case spkStruct:
		return v2Table{Dotted: dotted, Children: foldV2DecodeStruct(fi.InnerInfo, v2pos{dotted: dotted, tgt: fieldTgt})}
	}
	panic("v2 spike: field shape out of scope: " + spikeKindName(fi.Kind))
}

func v2ArrayTableNode(fi FieldInfo, fieldTgt TargetPath, dotted string, slicePtr bool) v2node {
	children := foldV2DecodeStruct(fi.InnerInfo, v2pos{dotted: dotted, tgt: fieldTgt.Index("i")})
	for _, c := range children {
		if _, ok := c.(v2Leaf); !ok {
			panic("v2 spike: nested container inside []struct out of scope")
		}
	}
	return v2ArrayTable{Tgt: fieldTgt, TypeName: fi.TypeName, Dotted: dotted, SlicePtr: slicePtr, Children: children}
}

// --- The new renderer ---

func v2RootChildren() *jen.Statement {
	return jen.Id("d").Dot("cstDoc").Dot("Root").Call().Dot("Children")
}

func v2TableMatch(dotted string) *jen.Statement {
	return jen.Id("_ch").Dot("Kind").Op("==").Qual(cstPkg, "NodeTable").
		Op("&&").Qual(cstPkg, "TableHeaderKey").Call(jen.Id("_ch")).Op("==").Lit(dotted)
}

func v2LeafCase(l v2Leaf) jen.Code {
	if l.Slice {
		return jen.Case(jen.Lit(l.Key)).Block(
			jen.If(jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Qual(cstPkg, l.SliceFn).Call(jen.Id("_kv")), jen.Id("ok")).Block(
				l.Tgt.Jen().Clone().Op("=").Id("v"),
			),
		)
	}
	if l.Ptr {
		if l.Codec.cast != "" {
			return jen.Case(jen.Lit(l.Key)).Block(
				jen.If(jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Qual(cstPkg, l.Codec.fn).Call(jen.Id("_kv")), jen.Id("ok")).Block(
					jen.Id("_cv").Op(":=").Id(l.Codec.cast).Call(jen.Id("v")),
					l.Tgt.Jen().Clone().Op("=").Op("&").Id("_cv"),
				),
			)
		}
		return jen.Case(jen.Lit(l.Key)).Block(
			jen.If(jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Qual(cstPkg, l.Codec.fn).Call(jen.Id("_kv")), jen.Id("ok")).Block(
				l.Tgt.Jen().Clone().Op("=").Op("&").Id("v"),
			),
		)
	}
	var assign *jen.Statement
	if l.Codec.cast != "" {
		assign = l.Tgt.Jen().Clone().Op("=").Id(l.Codec.cast).Call(jen.Id("v"))
	} else {
		assign = l.Tgt.Jen().Clone().Op("=").Id("v")
	}
	return jen.Case(jen.Lit(l.Key)).Block(
		jen.If(jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Qual(cstPkg, l.Codec.fn).Call(jen.Id("_kv")), jen.Id("ok")).Block(assign),
	)
}

// v2RenderBody walks the IR. Leaves are batched into one scan over `container`;
// each container node is its own block. This single walk replaces the per-op
// jenContOp/jenLeafCase/jenIT/jenIPT/jenFAT dispatch in the current renderer.
func v2RenderBody(g *jen.Group, container *jen.Statement, children []v2node) {
	var leaves []v2Leaf
	var conts []v2node
	for _, c := range children {
		if l, ok := c.(v2Leaf); ok {
			leaves = append(leaves, l)
		} else {
			conts = append(conts, c)
		}
	}

	if len(leaves) > 0 {
		g.For(jen.List(jen.Id("_"), jen.Id("_kv")).Op(":=").Range().Add(container.Clone()).Dot("Children")).Block(
			jen.If(jen.Id("_kv").Dot("Kind").Op("!=").Qual(cstPkg, "NodeKeyValue")).Block(jen.Continue()),
			jen.Switch(jen.Qual(cstPkg, "KeyValueName").Call(jen.Id("_kv"))).BlockFunc(func(sw *jen.Group) {
				for _, l := range leaves {
					sw.Add(v2LeafCase(l))
				}
			}),
		)
	}

	for _, c := range conts {
		switch n := c.(type) {
		case v2Table:
			g.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(v2RootChildren())).Block(
				jen.If(v2TableMatch(n.Dotted)).BlockFunc(func(b *jen.Group) {
					v2RenderBody(b, jen.Id("_ch"), n.Children)
					b.Break()
				}),
			)

		case v2NilGuard:
			g.BlockFunc(func(b *jen.Group) {
				b.Var().Id("_ft").Op("*").Qual(cstPkg, "Node")
				b.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(v2RootChildren())).Block(
					jen.If(v2TableMatch(n.Dotted)).Block(jen.Id("_ft").Op("=").Id("_ch"), jen.Break()),
				)
				b.If(jen.Id("_ft").Op("!=").Nil()).BlockFunc(func(ib *jen.Group) {
					ib.Id(n.LocalVar).Op(":=").Op("&").Id(n.TypeName).Values()
					v2RenderBody(ib, jen.Id("_ft"), n.Children)
					ib.Add(n.Tgt.Jen().Clone()).Op("=").Id(n.LocalVar)
				})
			})

		case v2ArrayTable:
			g.BlockFunc(func(b *jen.Group) {
				b.Var().Id("_nodes").Index().Op("*").Qual(cstPkg, "Node")
				b.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(v2RootChildren())).Block(
					jen.If(jen.Id("_ch").Dot("Kind").Op("==").Qual(cstPkg, "NodeArrayTable").
						Op("&&").Qual(cstPkg, "TableHeaderKey").Call(jen.Id("_ch")).Op("==").Lit(n.Dotted)).Block(
						jen.Id("_nodes").Op("=").Append(jen.Id("_nodes"), jen.Id("_ch")),
					),
				)
				jt := jen.Id(n.TypeName)
				if n.SlicePtr {
					b.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Index().Op("*").Add(jt.Clone()), jen.Len(jen.Id("_nodes")))
				} else {
					b.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Index().Add(jt.Clone()), jen.Len(jen.Id("_nodes")))
				}
				b.For(jen.List(jen.Id("i"), jen.Id("_node")).Op(":=").Range().Id("_nodes")).BlockFunc(func(eb *jen.Group) {
					if n.SlicePtr {
						eb.Add(n.Tgt.Jen().Clone()).Index(jen.Id("i")).Op("=").Op("&").Add(jt.Clone()).Values()
					}
					v2RenderBody(eb, jen.Id("_node"), n.Children)
				})
			})
		}
	}
}

func v2RenderDecodeFile(pkg, structName string, body []v2node) (string, error) {
	dt := structName + "Document"
	f := jen.NewFile(pkg)
	f.HeaderComment("Code generated by tommy V2 decode spike; DO NOT EDIT.")
	f.ImportName(cstPkg, "cst")
	f.ImportName(docPkg, "document")

	f.Type().Id(dt).Struct(
		jen.Id("data").Id(structName),
		jen.Id("cstDoc").Op("*").Qual(docPkg, "Document"),
	)

	f.Func().Id("Decode"+structName).Params(jen.Id("input").Index().Byte()).Params(jen.Op("*").Id(dt), jen.Error()).BlockFunc(func(g *jen.Group) {
		g.List(jen.Id("doc"), jen.Err()).Op(":=").Qual(docPkg, "Parse").Call(jen.Id("input"))
		g.If(jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Err()))
		g.Id("d").Op(":=").Op("&").Id(dt).Values(jen.Dict{jen.Id("cstDoc"): jen.Id("doc")})
		v2RenderBody(g, jen.Id("d").Dot("cstDoc").Dot("Root").Call(), body)
		g.Return(jen.Id("d"), jen.Nil())
	})

	f.Func().Params(jen.Id("d").Op("*").Id(dt)).Id("Data").Params().Op("*").Id(structName).Block(
		jen.Return(jen.Op("&").Id("d").Dot("data")),
	)

	var b strings.Builder
	if err := f.Render(&b); err != nil {
		return "", err
	}
	return b.String(), nil
}

// --- Compile-and-round-trip validation ---

func TestSpikeV2DecodeCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping compile-and-run spike in short mode")
	}

	// Fixture: StructInfo (drives the fold) + matching Go source (compiled).
	sub := &StructInfo{Name: "Sub", Fields: []FieldInfo{
		{GoName: "Level", TomlKey: "level", Kind: FieldPrimitive, TypeName: "int"},
		{GoName: "Note", TomlKey: "note", Kind: FieldPrimitive, TypeName: "string"},
	}}
	tlsc := &StructInfo{Name: "TLSc", Fields: []FieldInfo{
		{GoName: "Cert", TomlKey: "cert", Kind: FieldPrimitive, TypeName: "string"},
	}}
	host := &StructInfo{Name: "Host", Fields: []FieldInfo{
		{GoName: "Addr", TomlKey: "addr", Kind: FieldPrimitive, TypeName: "string"},
		{GoName: "Tags", TomlKey: "tags", Kind: FieldSlicePrimitive, ElemType: "string"},
	}}
	cfg := StructInfo{Name: "Cfg", Fields: []FieldInfo{
		{GoName: "Name", TomlKey: "name", Kind: FieldPrimitive, TypeName: "string"},
		{GoName: "Port", TomlKey: "port", Kind: FieldPrimitive, TypeName: "int"},
		{GoName: "On", TomlKey: "on", Kind: FieldPrimitive, TypeName: "bool"},
		{GoName: "Tags", TomlKey: "tags", Kind: FieldSlicePrimitive, ElemType: "string"},
		{GoName: "Sub", TomlKey: "sub", Kind: FieldStruct, TypeName: "Sub", InnerInfo: sub},
		{GoName: "TLS", TomlKey: "tls", Kind: FieldPointerStruct, TypeName: "TLSc", InnerInfo: tlsc},
		{GoName: "Hosts", TomlKey: "hosts", Kind: FieldSliceStruct, TypeName: "Host", InnerInfo: host},
	}}

	body := foldV2DecodeStruct(&cfg, v2pos{tgt: ReceiverTarget("d", "data")})
	generated, err := v2RenderDecodeFile("rt", "Cfg", body)
	if err != nil {
		t.Fatalf("V2 render: %v", err)
	}
	t.Logf("generated decode:\n%s", generated)

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/v2dec",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "cfg.go", `package rt

type Cfg struct {
	Name  string   `+"`"+`toml:"name"`+"`"+`
	Port  int      `+"`"+`toml:"port"`+"`"+`
	On    bool     `+"`"+`toml:"on"`+"`"+`
	Tags  []string `+"`"+`toml:"tags"`+"`"+`
	Sub   Sub      `+"`"+`toml:"sub"`+"`"+`
	TLS   *TLSc    `+"`"+`toml:"tls"`+"`"+`
	Hosts []Host   `+"`"+`toml:"hosts"`+"`"+`
}

type Sub struct {
	Level int    `+"`"+`toml:"level"`+"`"+`
	Note  string `+"`"+`toml:"note"`+"`"+`
}

type TLSc struct {
	Cert string `+"`"+`toml:"cert"`+"`"+`
}

type Host struct {
	Addr string   `+"`"+`toml:"addr"`+"`"+`
	Tags []string `+"`"+`toml:"tags"`+"`"+`
}
`)

	writeFixture(t, dir, "cfg_tommy.go", generated)

	writeFixture(t, dir, "roundtrip_test.go", `package rt

import "testing"

const in = `+"`"+`name = "app"
port = 8080
on = true
tags = ["a", "b"]

[sub]
level = 3
note = "hi"

[tls]
cert = "abc"

[[hosts]]
addr = "h1"
tags = ["x"]

[[hosts]]
addr = "h2"
tags = ["y", "z"]
`+"`"+`

func TestV2Decode(t *testing.T) {
	doc, err := DecodeCfg([]byte(in))
	if err != nil {
		t.Fatalf("DecodeCfg: %v", err)
	}
	d := doc.Data()
	if d.Name != "app" || d.Port != 8080 || !d.On {
		t.Fatalf("scalars wrong: %+v", d)
	}
	if len(d.Tags) != 2 || d.Tags[0] != "a" || d.Tags[1] != "b" {
		t.Fatalf("tags wrong: %v", d.Tags)
	}
	if d.Sub.Level != 3 || d.Sub.Note != "hi" {
		t.Fatalf("nested struct wrong: %+v", d.Sub)
	}
	if d.TLS == nil || d.TLS.Cert != "abc" {
		t.Fatalf("pointer struct wrong: %+v", d.TLS)
	}
	if len(d.Hosts) != 2 {
		t.Fatalf("hosts len = %d, want 2", len(d.Hosts))
	}
	if d.Hosts[0].Addr != "h1" || d.Hosts[1].Addr != "h2" {
		t.Fatalf("host addrs wrong: %+v", d.Hosts)
	}
	if len(d.Hosts[0].Tags) != 1 || d.Hosts[0].Tags[0] != "x" {
		t.Fatalf("host[0].tags wrong: %v", d.Hosts[0].Tags)
	}
	if len(d.Hosts[1].Tags) != 2 || d.Hosts[1].Tags[1] != "z" {
		t.Fatalf("host[1].tags wrong: %v", d.Hosts[1].Tags)
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

// =========================================================================
// Encode (step #2, second half)
//
// Encode walks the same TypeExpr and writes into the retained CST via the real
// document API (cst.SetAny / EnsureChildTable / FindArrayTableNodes /
// AppendArrayTableEntryAfter), updating existing nodes in place so comments and
// layout round-trip. EnsureChildTable's parent is always the current container,
// so the decode/encode folds thread position identically.
// =========================================================================

type e2node interface{ isE2() }

type e2Leaf struct {
	Tgt      TargetPath
	Key      string
	ZeroType string // jenZeroLit input; "" for slices
	Slice    bool
	Ptr      bool
}
type e2Table struct {
	Bk       string
	Children []e2node
}
type e2NilGuard struct {
	Tgt      TargetPath
	Bk       string
	Children []e2node
}
type e2ArrayTable struct {
	Tgt      TargetPath
	Dotted   string
	SlicePtr bool
	Children []e2node // leaf-only
}

func (e2Leaf) isE2()       {}
func (e2Table) isE2()      {}
func (e2NilGuard) isE2()   {}
func (e2ArrayTable) isE2() {}

func foldV2EncodeStruct(si *StructInfo, pos v2pos) []e2node {
	var out []e2node
	for _, fi := range si.Fields {
		out = append(out, foldV2EncodeField(fi, pos))
	}
	return out
}

func foldV2EncodeField(fi FieldInfo, pos v2pos) e2node {
	dotted, fieldTgt := pos.child(fi.TomlKey, fi.GoName)

	switch te := fieldType(fi).(type) {
	case spkScalar:
		if te.Codec != codecPrim {
			panic("v2 encode spike: non-primitive scalar codec out of scope")
		}
		return e2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, ZeroType: fi.TypeName}

	case spkPtr:
		switch te.Elem.(type) {
		case spkScalar:
			return e2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, Ptr: true}
		case spkStruct:
			return e2NilGuard{Tgt: fieldTgt, Bk: fi.TomlKey, Children: foldV2EncodeStruct(fi.InnerInfo, v2pos{dotted: dotted, tgt: fieldTgt})}
		}

	case spkSlice:
		switch elem := te.Elem.(type) {
		case spkScalar:
			return e2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, Slice: true}
		case spkStruct:
			return e2ArrayEncode(fi, fieldTgt, dotted, false)
		case spkPtr:
			if _, ok := elem.Elem.(spkStruct); ok {
				return e2ArrayEncode(fi, fieldTgt, dotted, true)
			}
		}

	case spkStruct:
		return e2Table{Bk: fi.TomlKey, Children: foldV2EncodeStruct(fi.InnerInfo, v2pos{dotted: dotted, tgt: fieldTgt})}
	}
	panic("v2 encode spike: field shape out of scope: " + spikeKindName(fi.Kind))
}

func e2ArrayEncode(fi FieldInfo, fieldTgt TargetPath, dotted string, slicePtr bool) e2node {
	children := foldV2EncodeStruct(fi.InnerInfo, v2pos{dotted: dotted, tgt: fieldTgt.Index("i")})
	for _, c := range children {
		if _, ok := c.(e2Leaf); !ok {
			panic("v2 encode spike: nested container inside []struct out of scope")
		}
	}
	return e2ArrayTable{Tgt: fieldTgt, Dotted: dotted, SlicePtr: slicePtr, Children: children}
}

func v2EncRoot() *jen.Statement { return jen.Id("d").Dot("cstDoc").Dot("Root").Call() }

func v2SetAny(cv *jen.Statement, key string, val *jen.Statement) jen.Code {
	return jen.If(jen.Err().Op(":=").Qual(cstPkg, "SetAny").Call(cv.Clone(), jen.Lit(key), val), jen.Err().Op("!=").Nil()).Block(
		jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("%w"), jen.Err())),
	)
}

func v2RenderEncodeBody(g *jen.Group, cv *jen.Statement, children []e2node) {
	for _, c := range children {
		switch n := c.(type) {
		case e2Leaf:
			switch {
			case n.Ptr:
				g.If(n.Tgt.Jen().Clone().Op("!=").Nil()).Block(
					v2SetAny(cv, n.Key, jen.Op("*").Add(n.Tgt.Jen().Clone())),
				)
			case n.Slice:
				g.Add(v2SetAny(cv, n.Key, n.Tgt.Jen().Clone()))
			default:
				g.If(n.Tgt.Jen().Clone().Op("!=").Add(jenZeroLit(n.ZeroType)).
					Op("||").Qual(cstPkg, "HasValue").Call(cv.Clone(), jen.Lit(n.Key))).Block(
					v2SetAny(cv, n.Key, n.Tgt.Jen().Clone()),
				)
			}

		case e2Table:
			g.BlockFunc(func(b *jen.Group) {
				b.Id("tableNode").Op(":=").Qual(cstPkg, "EnsureChildTable").Call(v2EncRoot(), cv.Clone(), jen.Lit(n.Bk))
				v2RenderEncodeBody(b, jen.Id("tableNode"), n.Children)
			})

		case e2NilGuard:
			g.If(n.Tgt.Jen().Clone().Op("!=").Nil()).BlockFunc(func(b *jen.Group) {
				b.Id("tableNode").Op(":=").Qual(cstPkg, "EnsureChildTable").Call(v2EncRoot(), cv.Clone(), jen.Lit(n.Bk))
				v2RenderEncodeBody(b, jen.Id("tableNode"), n.Children)
			})

		case e2ArrayTable:
			g.BlockFunc(func(b *jen.Group) {
				b.Id("_exist").Op(":=").Qual(cstPkg, "FindArrayTableNodes").Call(v2EncRoot(), jen.Lit(n.Dotted))
				b.For(jen.Id("i").Op(":=").Range().Add(n.Tgt.Jen().Clone())).BlockFunc(func(lb *jen.Group) {
					lb.Var().Id("container").Op("*").Qual(cstPkg, "Node")
					lb.If(jen.Id("i").Op("<").Len(jen.Id("_exist"))).Block(
						jen.Id("container").Op("=").Id("_exist").Index(jen.Id("i")),
					).Else().Block(
						jen.Id("container").Op("=").Qual(cstPkg, "AppendArrayTableEntryAfter").Call(v2EncRoot(), jen.Lit(n.Dotted)),
					)
					if n.SlicePtr {
						lb.If(n.Tgt.Jen().Clone().Index(jen.Id("i")).Op("==").Nil()).Block(jen.Continue())
					}
					v2RenderEncodeBody(lb, jen.Id("container"), n.Children)
				})
			})
		}
	}
}

// v2RenderFullFile emits Document + Decode + Data + Encode from the two folds.
func v2RenderFullFile(pkg, structName string, dec []v2node, enc []e2node) (string, error) {
	dt := structName + "Document"
	f := jen.NewFile(pkg)
	f.HeaderComment("Code generated by tommy V2 spike; DO NOT EDIT.")
	f.ImportName(cstPkg, "cst")
	f.ImportName(docPkg, "document")

	f.Type().Id(dt).Struct(
		jen.Id("data").Id(structName),
		jen.Id("cstDoc").Op("*").Qual(docPkg, "Document"),
	)

	f.Func().Id("Decode"+structName).Params(jen.Id("input").Index().Byte()).Params(jen.Op("*").Id(dt), jen.Error()).BlockFunc(func(g *jen.Group) {
		g.List(jen.Id("doc"), jen.Err()).Op(":=").Qual(docPkg, "Parse").Call(jen.Id("input"))
		g.If(jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Err()))
		g.Id("d").Op(":=").Op("&").Id(dt).Values(jen.Dict{jen.Id("cstDoc"): jen.Id("doc")})
		v2RenderBody(g, jen.Id("d").Dot("cstDoc").Dot("Root").Call(), dec)
		g.Return(jen.Id("d"), jen.Nil())
	})

	f.Func().Params(jen.Id("d").Op("*").Id(dt)).Id("Data").Params().Op("*").Id(structName).Block(
		jen.Return(jen.Op("&").Id("d").Dot("data")),
	)

	f.Func().Params(jen.Id("d").Op("*").Id(dt)).Id("Encode").Params().Params(jen.Index().Byte(), jen.Error()).BlockFunc(func(g *jen.Group) {
		v2RenderEncodeBody(g, jen.Id("d").Dot("cstDoc").Dot("Root").Call(), enc)
		g.Return(jen.Id("d").Dot("cstDoc").Dot("Bytes").Call(), jen.Nil())
	})

	var b strings.Builder
	if err := f.Render(&b); err != nil {
		return "", err
	}
	return b.String(), nil
}

func TestSpikeV2RoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping compile-and-run spike in short mode")
	}

	sub := &StructInfo{Name: "Sub", Fields: []FieldInfo{
		{GoName: "Level", TomlKey: "level", Kind: FieldPrimitive, TypeName: "int"},
		{GoName: "Note", TomlKey: "note", Kind: FieldPrimitive, TypeName: "string"},
	}}
	tlsc := &StructInfo{Name: "TLSc", Fields: []FieldInfo{
		{GoName: "Cert", TomlKey: "cert", Kind: FieldPrimitive, TypeName: "string"},
	}}
	host := &StructInfo{Name: "Host", Fields: []FieldInfo{
		{GoName: "Addr", TomlKey: "addr", Kind: FieldPrimitive, TypeName: "string"},
		{GoName: "Tags", TomlKey: "tags", Kind: FieldSlicePrimitive, ElemType: "string"},
	}}
	cfg := StructInfo{Name: "Cfg", Fields: []FieldInfo{
		{GoName: "Name", TomlKey: "name", Kind: FieldPrimitive, TypeName: "string"},
		{GoName: "Port", TomlKey: "port", Kind: FieldPrimitive, TypeName: "int"},
		{GoName: "On", TomlKey: "on", Kind: FieldPrimitive, TypeName: "bool"},
		{GoName: "Tags", TomlKey: "tags", Kind: FieldSlicePrimitive, ElemType: "string"},
		{GoName: "Sub", TomlKey: "sub", Kind: FieldStruct, TypeName: "Sub", InnerInfo: sub},
		{GoName: "TLS", TomlKey: "tls", Kind: FieldPointerStruct, TypeName: "TLSc", InnerInfo: tlsc},
		{GoName: "Hosts", TomlKey: "hosts", Kind: FieldSliceStruct, TypeName: "Host", InnerInfo: host},
	}}

	dec := foldV2DecodeStruct(&cfg, v2pos{tgt: ReceiverTarget("d", "data")})
	enc := foldV2EncodeStruct(&cfg, v2pos{tgt: ReceiverTarget("d", "data")})
	generated, err := v2RenderFullFile("rt", "Cfg", dec, enc)
	if err != nil {
		t.Fatalf("V2 render: %v", err)
	}
	t.Logf("generated decode+encode:\n%s", generated)

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/v2rt",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "cfg.go", `package rt

type Cfg struct {
	Name  string   `+"`"+`toml:"name"`+"`"+`
	Port  int      `+"`"+`toml:"port"`+"`"+`
	On    bool     `+"`"+`toml:"on"`+"`"+`
	Tags  []string `+"`"+`toml:"tags"`+"`"+`
	Sub   Sub      `+"`"+`toml:"sub"`+"`"+`
	TLS   *TLSc    `+"`"+`toml:"tls"`+"`"+`
	Hosts []Host   `+"`"+`toml:"hosts"`+"`"+`
}

type Sub struct {
	Level int    `+"`"+`toml:"level"`+"`"+`
	Note  string `+"`"+`toml:"note"`+"`"+`
}

type TLSc struct {
	Cert string `+"`"+`toml:"cert"`+"`"+`
}

type Host struct {
	Addr string   `+"`"+`toml:"addr"`+"`"+`
	Tags []string `+"`"+`toml:"tags"`+"`"+`
}
`)

	writeFixture(t, dir, "cfg_tommy.go", generated)

	writeFixture(t, dir, "roundtrip_test.go", `package rt

import (
	"strings"
	"testing"
)

const in = `+"`"+`# top comment
name = "app"
port = 8080
on = true
tags = ["a", "b"]

[sub]
level = 3
note = "hi"

[tls]
cert = "abc"

[[hosts]]
addr = "h1"
tags = ["x"]

[[hosts]]
addr = "h2"
tags = ["y", "z"]
`+"`"+`

func TestV2RoundTrip(t *testing.T) {
	doc, err := DecodeCfg([]byte(in))
	if err != nil {
		t.Fatalf("DecodeCfg: %v", err)
	}
	doc.Data().Port = 9090
	doc.Data().Sub.Note = "changed"

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	s := string(out)

	if !strings.Contains(s, "# top comment") {
		t.Fatalf("comment lost:\n%s", s)
	}
	if !strings.Contains(s, "9090") || strings.Contains(s, "8080") {
		t.Fatalf("port not updated in place:\n%s", s)
	}
	if !strings.Contains(s, "changed") {
		t.Fatalf("nested edit not applied:\n%s", s)
	}
	for _, want := range []string{"[sub]", "[tls]", "h1", "h2"} {
		if !strings.Contains(s, want) {
			t.Fatalf("expected %q preserved in:\n%s", want, s)
		}
	}

	// Re-decode the emitted bytes and confirm structural fidelity.
	doc2, err := DecodeCfg(out)
	if err != nil {
		t.Fatalf("re-DecodeCfg: %v\n%s", err, s)
	}
	d2 := doc2.Data()
	if d2.Port != 9090 || d2.Name != "app" || !d2.On {
		t.Fatalf("re-decoded scalars wrong: %+v", d2)
	}
	if d2.Sub.Level != 3 || d2.Sub.Note != "changed" {
		t.Fatalf("re-decoded sub wrong: %+v", d2.Sub)
	}
	if d2.TLS == nil || d2.TLS.Cert != "abc" {
		t.Fatalf("re-decoded tls wrong: %+v", d2.TLS)
	}
	if len(d2.Hosts) != 2 || d2.Hosts[0].Addr != "h1" || d2.Hosts[1].Addr != "h2" {
		t.Fatalf("re-decoded hosts wrong: %+v", d2.Hosts)
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
