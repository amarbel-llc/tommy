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

// v2Leaf: a key-value scanned in the current container. Covers primitive
// scalars/*scalars/[]scalars and the custom/text codecs.
type v2Leaf struct {
	Tgt          TargetPath
	Key          string
	CodecKind    spkCodec    // codecPrim | codecCustom | codecText
	Codec        extractInfo // primitive extract fn/cast (codecPrim only)
	Ptr          bool        // *scalar -> assign &v
	Slice        bool        // []scalar / []text
	SliceFn      string      // cst.ExtractXSlice (codecPrim slice)
	ElemTypeName string      // element type for []text make()
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

// v2MapScalar: map[string]string — find [Dotted] table, ExtractStringMap.
type v2MapScalar struct {
	Tgt    TargetPath
	Dotted string
}

// v2MapMap: map[string]map[string]string — iterate [Dotted.*] tables.
type v2MapMap struct {
	Tgt    TargetPath
	Dotted string
}

// v2MapStruct: map[string]Struct / map[string]*Struct — iterate [Dotted.<key>]
// sub-tables; entry fields are leaf-only here.
type v2MapStruct struct {
	Tgt      TargetPath
	Dotted   string
	TypeName string
	Ptr      bool
	Children []v2node
}

func (v2Leaf) isV2()       {}
func (v2Table) isV2()      {}
func (v2NilGuard) isV2()   {}
func (v2ArrayTable) isV2() {}
func (v2MapScalar) isV2()  {}
func (v2MapMap) isV2()     {}
func (v2MapStruct) isV2()  {}

// v2Del* delegate to another package's Decode<St>Into. St is the short type
// name; Import is the package path; Dotted is the table/array key from root.
type v2DelStruct struct {
	Tgt    TargetPath
	Dotted string
	Import string
	St     string
	Ptr    bool
}
type v2DelSlice struct {
	Tgt      TargetPath
	Dotted   string
	Import   string
	St       string
	SlicePtr bool
}
type v2DelMap struct {
	Tgt    TargetPath
	Dotted string
	Import string
	St     string
}

func (v2DelStruct) isV2() {}
func (v2DelSlice) isV2()  {}
func (v2DelMap) isV2()    {}

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
		switch te.Codec {
		case codecPrim:
			return v2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, CodecKind: codecPrim, Codec: cstExtract(fi.TypeName)}
		case codecCustom:
			return v2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, CodecKind: codecCustom}
		case codecText:
			return v2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, CodecKind: codecText}
		}

	case spkPtr:
		switch te.Elem.(type) {
		case spkScalar:
			return v2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, CodecKind: codecPrim, Codec: cstExtract(fi.TypeName), Ptr: true}
		case spkStruct:
			lv := toLowerFirst(fi.GoName) + "Val"
			return v2NilGuard{
				Tgt: fieldTgt, TypeName: fi.TypeName, Dotted: dotted, LocalVar: lv,
				Children: foldV2DecodeStruct(fi.InnerInfo, v2pos{dotted: dotted, tgt: LocalTarget(lv)}),
			}
		case spkDelegated:
			_, st := delegateParts(fi.TypeName)
			return v2DelStruct{Tgt: fieldTgt, Dotted: dotted, Import: fi.ImportPath, St: st, Ptr: true}
		}

	case spkSlice:
		switch elem := te.Elem.(type) {
		case spkScalar:
			if elem.Codec == codecText {
				return v2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, Slice: true, CodecKind: codecText, ElemTypeName: fi.TypeName}
			}
			return v2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, Slice: true, CodecKind: codecPrim, SliceFn: cstSliceExtractFunc(fi.ElemType)}
		case spkStruct:
			return v2ArrayTableNode(fi, fieldTgt, dotted, false)
		case spkDelegated:
			_, st := delegateParts(fi.TypeName)
			return v2DelSlice{Tgt: fieldTgt, Dotted: dotted, Import: fi.ImportPath, St: st, SlicePtr: false}
		case spkPtr:
			switch elem.Elem.(type) {
			case spkStruct:
				return v2ArrayTableNode(fi, fieldTgt, dotted, true)
			case spkDelegated:
				_, st := delegateParts(fi.TypeName)
				return v2DelSlice{Tgt: fieldTgt, Dotted: dotted, Import: fi.ImportPath, St: st, SlicePtr: true}
			}
		}

	case spkMap:
		switch elem := te.Elem.(type) {
		case spkScalar:
			return v2MapScalar{Tgt: fieldTgt, Dotted: dotted}
		case spkMap:
			return v2MapMap{Tgt: fieldTgt, Dotted: dotted}
		case spkStruct:
			return v2MapStructNode(fi, fieldTgt, dotted, false)
		case spkDelegated:
			_, st := delegateParts(fi.ElemType)
			return v2DelMap{Tgt: fieldTgt, Dotted: dotted, Import: fi.ImportPath, St: st}
		case spkPtr:
			if _, ok := elem.Elem.(spkStruct); ok {
				return v2MapStructNode(fi, fieldTgt, dotted, true)
			}
		}

	case spkStruct:
		return v2Table{Dotted: dotted, Children: foldV2DecodeStruct(fi.InnerInfo, v2pos{dotted: dotted, tgt: fieldTgt})}

	case spkDelegated:
		_, st := delegateParts(fi.TypeName)
		return v2DelStruct{Tgt: fieldTgt, Dotted: dotted, Import: fi.ImportPath, St: st, Ptr: false}
	}
	panic("v2 spike: field shape out of scope: " + spikeKindName(fi.Kind))
}

func v2MapStructNode(fi FieldInfo, fieldTgt TargetPath, dotted string, ptr bool) v2node {
	children := foldV2DecodeStruct(fi.InnerInfo, v2pos{tgt: LocalTarget("entry")})
	for _, c := range children {
		if _, ok := c.(v2Leaf); !ok {
			panic("v2 spike: nested container inside map[string]struct out of scope")
		}
	}
	return v2MapStruct{Tgt: fieldTgt, Dotted: dotted, TypeName: fi.TypeName, Ptr: ptr, Children: children}
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
	switch l.CodecKind {
	case codecCustom: // ExtractRaw + UnmarshalTOML
		return jen.Case(jen.Lit(l.Key)).Block(
			jen.If(jen.List(jen.Id("raw"), jen.Id("ok")).Op(":=").Qual(cstPkg, "ExtractRaw").Call(jen.Id("_kv")), jen.Id("ok")).Block(
				jen.If(jen.Err().Op(":=").Add(l.Tgt.Jen().Clone()).Dot("UnmarshalTOML").Call(jen.Id("raw")), jen.Err().Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(l.Key+": %w"), jen.Err())),
				),
			),
		)
	case codecText:
		if l.Slice { // ExtractStringSlice + per-elem UnmarshalText
			return jen.Case(jen.Lit(l.Key)).Block(
				jen.If(jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Qual(cstPkg, "ExtractStringSlice").Call(jen.Id("_kv")), jen.Id("ok")).Block(
					l.Tgt.Jen().Clone().Op("=").Make(jen.Index().Id(l.ElemTypeName), jen.Len(jen.Id("v"))),
					jen.For(jen.List(jen.Id("_i"), jen.Id("_s")).Op(":=").Range().Id("v")).Block(
						jen.If(jen.Err().Op(":=").Add(l.Tgt.Jen().Clone()).Index(jen.Id("_i")).Dot("UnmarshalText").Call(jen.Index().Byte().Call(jen.Id("_s"))), jen.Err().Op("!=").Nil()).Block(
							jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(l.Key+"[%d]: %w"), jen.Id("_i"), jen.Err())),
						),
					),
				),
			)
		}
		return jen.Case(jen.Lit(l.Key)).Block( // ExtractString + UnmarshalText
			jen.If(jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Qual(cstPkg, "ExtractString").Call(jen.Id("_kv")), jen.Id("ok")).Block(
				jen.If(jen.Err().Op(":=").Add(l.Tgt.Jen().Clone()).Dot("UnmarshalText").Call(jen.Index().Byte().Call(jen.Id("v"))), jen.Err().Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(l.Key+": %w"), jen.Err())),
				),
			),
		)
	}

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

		case v2MapScalar: // map[string]string: find [Dotted] table, ExtractStringMap
			g.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(v2RootChildren())).Block(
				jen.If(v2TableMatch(n.Dotted)).Block(
					n.Tgt.Jen().Clone().Op("=").Qual(cstPkg, "ExtractStringMap").Call(jen.Id("_ch")),
					jen.Break(),
				),
			)

		case v2MapMap: // map[string]map[string]string: iterate [Dotted.*] sub-tables
			g.BlockFunc(func(b *jen.Group) {
				b.Var().Id("_mr").Map(jen.String()).Map(jen.String()).String()
				b.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(v2RootChildren())).BlockFunc(func(lb *jen.Group) {
					lb.If(jen.Id("_ch").Dot("Kind").Op("!=").Qual(cstPkg, "NodeTable")).Block(jen.Continue())
					lb.Id("_hdr").Op(":=").Qual(cstPkg, "TableHeaderKey").Call(jen.Id("_ch"))
					lb.If(jen.Op("!").Qual("strings", "HasPrefix").Call(jen.Id("_hdr"), jen.Lit(n.Dotted+"."))).Block(jen.Continue())
					lb.Id("_mk").Op(":=").Id("_hdr").Index(jen.Lit(len(n.Dotted) + 1).Op(":"))
					lb.If(jen.Id("_mr").Op("==").Nil()).Block(jen.Id("_mr").Op("=").Make(jen.Map(jen.String()).Map(jen.String()).String()))
					lb.Id("_mr").Index(jen.Id("_mk")).Op("=").Qual(cstPkg, "ExtractStringMap").Call(jen.Id("_ch"))
				})
				b.If(jen.Id("_mr").Op("!=").Nil()).Block(n.Tgt.Jen().Clone().Op("=").Id("_mr"))
			})

		case v2MapStruct: // map[string]Struct / map[string]*Struct: iterate [Dotted.<key>] sub-tables
			g.BlockFunc(func(b *jen.Group) {
				mt := jen.Id(n.TypeName)
				if n.Ptr {
					b.Var().Id("_mr").Map(jen.String()).Op("*").Add(mt.Clone())
				} else {
					b.Var().Id("_mr").Map(jen.String()).Add(mt.Clone())
				}
				b.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(v2RootChildren())).BlockFunc(func(lb *jen.Group) {
					lb.If(jen.Id("_ch").Dot("Kind").Op("!=").Qual(cstPkg, "NodeTable")).Block(jen.Continue())
					lb.Id("_hdr").Op(":=").Qual(cstPkg, "TableHeaderKey").Call(jen.Id("_ch"))
					lb.If(jen.Op("!").Qual("strings", "HasPrefix").Call(jen.Id("_hdr"), jen.Lit(n.Dotted+"."))).Block(jen.Continue())
					lb.Id("_mk").Op(":=").Id("_hdr").Index(jen.Lit(len(n.Dotted) + 1).Op(":"))
					lb.If(jen.Qual("strings", "Contains").Call(jen.Id("_mk"), jen.Lit("."))).Block(jen.Continue())
					if n.Ptr {
						lb.If(jen.Id("_mr").Op("==").Nil()).Block(jen.Id("_mr").Op("=").Make(jen.Map(jen.String()).Op("*").Add(mt.Clone())))
					} else {
						lb.If(jen.Id("_mr").Op("==").Nil()).Block(jen.Id("_mr").Op("=").Make(jen.Map(jen.String()).Add(mt.Clone())))
					}
					lb.Var().Id("entry").Add(mt.Clone())
					v2RenderBody(lb, jen.Id("_ch"), n.Children)
					if n.Ptr {
						lb.Id("_mr").Index(jen.Id("_mk")).Op("=").Op("&").Id("entry")
					} else {
						lb.Id("_mr").Index(jen.Id("_mk")).Op("=").Id("entry")
					}
				})
				b.If(jen.Id("_mr").Op("!=").Nil()).Block(n.Tgt.Jen().Clone().Op("=").Id("_mr"))
			})

		case v2DelStruct: // delegate to pkg.Decode<St>Into
			decFn := "Decode" + n.St + "Into"
			consumed := jen.Map(jen.String()).Bool().Values()
			pk := jen.Lit(n.Dotted + ".")
			if n.Ptr {
				g.BlockFunc(func(b *jen.Group) {
					b.Var().Id("_tbl").Op("*").Qual(cstPkg, "Node")
					b.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(v2RootChildren())).Block(
						jen.If(v2TableMatch(n.Dotted)).Block(jen.Id("_tbl").Op("=").Id("_ch"), jen.Break()),
					)
					b.If(jen.Id("_tbl").Op("!=").Nil()).BlockFunc(func(ib *jen.Group) {
						lv := toLowerFirst(n.St) + "Val"
						ib.Id(lv).Op(":=").Op("&").Qual(n.Import, n.St).Values()
						ib.If(jen.Err().Op(":=").Qual(n.Import, decFn).Call(jen.Id(lv), jen.Id("d").Dot("cstDoc"), jen.Id("_tbl"), consumed, pk), jen.Err().Op("!=").Nil()).Block(
							jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(n.Dotted+": %w"), jen.Err())),
						)
						ib.Add(n.Tgt.Jen().Clone()).Op("=").Id(lv)
					})
				})
			} else {
				g.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(v2RootChildren())).Block(
					jen.If(v2TableMatch(n.Dotted)).BlockFunc(func(b *jen.Group) {
						b.If(jen.Err().Op(":=").Qual(n.Import, decFn).Call(jen.Op("&").Add(n.Tgt.Jen().Clone()), jen.Id("d").Dot("cstDoc"), jen.Id("_ch"), consumed, pk), jen.Err().Op("!=").Nil()).Block(
							jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(n.Dotted+": %w"), jen.Err())),
						)
						b.Break()
					}),
				)
			}

		case v2DelSlice: // delegate []pkg.St per array-table entry
			g.BlockFunc(func(b *jen.Group) {
				decFn := "Decode" + n.St + "Into"
				st := jen.Qual(n.Import, n.St)
				b.Var().Id("_nodes").Index().Op("*").Qual(cstPkg, "Node")
				b.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(v2RootChildren())).Block(
					jen.If(jen.Id("_ch").Dot("Kind").Op("==").Qual(cstPkg, "NodeArrayTable").
						Op("&&").Qual(cstPkg, "TableHeaderKey").Call(jen.Id("_ch")).Op("==").Lit(n.Dotted)).Block(
						jen.Id("_nodes").Op("=").Append(jen.Id("_nodes"), jen.Id("_ch")),
					),
				)
				if n.SlicePtr {
					b.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Index().Op("*").Add(st.Clone()), jen.Len(jen.Id("_nodes")))
				} else {
					b.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Index().Add(st.Clone()), jen.Len(jen.Id("_nodes")))
				}
				b.For(jen.List(jen.Id("i"), jen.Id("_node")).Op(":=").Range().Id("_nodes")).BlockFunc(func(eb *jen.Group) {
					consumed := jen.Map(jen.String()).Bool().Values()
					pk := jen.Lit(n.Dotted + ".")
					if n.SlicePtr {
						eb.Add(n.Tgt.Jen().Clone()).Index(jen.Id("i")).Op("=").Op("&").Add(st.Clone()).Values()
						eb.If(jen.Err().Op(":=").Qual(n.Import, decFn).Call(n.Tgt.Jen().Clone().Index(jen.Id("i")), jen.Id("d").Dot("cstDoc"), jen.Id("_node"), consumed, pk), jen.Err().Op("!=").Nil()).Block(
							jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(n.Dotted+"[%d]: %w"), jen.Id("i"), jen.Err())),
						)
					} else {
						eb.If(jen.Err().Op(":=").Qual(n.Import, decFn).Call(jen.Op("&").Add(n.Tgt.Jen().Clone()).Index(jen.Id("i")), jen.Id("d").Dot("cstDoc"), jen.Id("_node"), consumed, pk), jen.Err().Op("!=").Nil()).Block(
							jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(n.Dotted+"[%d]: %w"), jen.Id("i"), jen.Err())),
						)
					}
				})
			})

		case v2DelMap: // delegate map[string]pkg.St per [Dotted.<key>] sub-table
			g.BlockFunc(func(b *jen.Group) {
				decFn := "Decode" + n.St + "Into"
				st := jen.Qual(n.Import, n.St)
				b.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(v2RootChildren())).BlockFunc(func(lb *jen.Group) {
					lb.If(jen.Id("_ch").Dot("Kind").Op("!=").Qual(cstPkg, "NodeTable")).Block(jen.Continue())
					lb.Id("_hdr").Op(":=").Qual(cstPkg, "TableHeaderKey").Call(jen.Id("_ch"))
					lb.If(jen.Op("!").Qual("strings", "HasPrefix").Call(jen.Id("_hdr"), jen.Lit(n.Dotted+"."))).Block(jen.Continue())
					lb.Id("_mk").Op(":=").Id("_hdr").Index(jen.Lit(len(n.Dotted) + 1).Op(":"))
					lb.If(jen.Qual("strings", "Contains").Call(jen.Id("_mk"), jen.Lit("."))).Block(jen.Continue())
					lb.If(n.Tgt.Jen().Clone().Op("==").Nil()).Block(n.Tgt.Jen().Clone().Op("=").Make(jen.Map(jen.String()).Add(st.Clone())))
					lb.Var().Id("entry").Add(st.Clone())
					consumed := jen.Map(jen.String()).Bool().Values()
					pk := jen.Lit(n.Dotted + ".").Op("+").Id("_mk").Op("+").Lit(".")
					lb.If(jen.Err().Op(":=").Qual(n.Import, decFn).Call(jen.Op("&").Id("entry"), jen.Id("d").Dot("cstDoc"), jen.Id("_ch"), consumed, pk), jen.Err().Op("!=").Nil()).Block(
						jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(n.Dotted+".%s: %w"), jen.Id("_mk"), jen.Err())),
					)
					lb.Add(n.Tgt.Jen().Clone()).Index(jen.Id("_mk")).Op("=").Id("entry")
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

// TestSpikeV2Wide exercises the expanded coverage: extended scalar types,
// *primitive, multiline + omitempty, 3-level struct nesting (explicit parent
// tables — implicit-parent #55 is out of scope), all four map shapes, and
// []*struct — through one compiled round-trip.
func TestSpikeV2Wide(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping compile-and-run spike in short mode")
	}

	l2 := &StructInfo{Name: "L2", Fields: []FieldInfo{
		{GoName: "Val", TomlKey: "val", Kind: FieldPrimitive, TypeName: "int"},
	}}
	l1 := &StructInfo{Name: "L1", Fields: []FieldInfo{
		{GoName: "L2", TomlKey: "l2", Kind: FieldStruct, TypeName: "L2", InnerInfo: l2},
	}}
	svc := &StructInfo{Name: "Svc", Fields: []FieldInfo{
		{GoName: "Image", TomlKey: "image", Kind: FieldPrimitive, TypeName: "string"},
	}}
	rt := &StructInfo{Name: "Rt", Fields: []FieldInfo{
		{GoName: "Path", TomlKey: "path", Kind: FieldPrimitive, TypeName: "string"},
	}}
	wide := StructInfo{Name: "Wide", Fields: []FieldInfo{
		{GoName: "Big", TomlKey: "big", Kind: FieldPrimitive, TypeName: "int64"},
		{GoName: "Ratio", TomlKey: "ratio", Kind: FieldPrimitive, TypeName: "float64"},
		{GoName: "Count", TomlKey: "count", Kind: FieldPrimitive, TypeName: "uint64"},
		{GoName: "Ptr", TomlKey: "ptr", Kind: FieldPointerPrimitive, TypeName: "int"},
		{GoName: "Desc", TomlKey: "desc", Kind: FieldPrimitive, TypeName: "string", Multiline: true},
		{GoName: "Opt", TomlKey: "opt", Kind: FieldPrimitive, TypeName: "string", OmitEmpty: true},
		{GoName: "Deep", TomlKey: "deep", Kind: FieldStruct, TypeName: "L1", InnerInfo: l1},
		{GoName: "Env", TomlKey: "env", Kind: FieldMapStringString},
		{GoName: "Svcs", TomlKey: "svcs", Kind: FieldMapStringStruct, TypeName: "Svc", InnerInfo: svc},
		{GoName: "PSvcs", TomlKey: "psvcs", Kind: FieldMapStringStruct, TypeName: "Svc", InnerInfo: svc, SlicePointer: true},
		{GoName: "Groups", TomlKey: "groups", Kind: FieldMapStringMapStringString},
		{GoName: "PR", TomlKey: "pr", Kind: FieldSliceStruct, TypeName: "Rt", InnerInfo: rt, SlicePointer: true},
	}}

	dec := foldV2DecodeStruct(&wide, v2pos{tgt: ReceiverTarget("d", "data")})
	enc := foldV2EncodeStruct(&wide, v2pos{tgt: ReceiverTarget("d", "data")})
	generated, err := v2RenderFullFile("rt", "Wide", dec, enc)
	if err != nil {
		t.Fatalf("V2 render: %v", err)
	}
	t.Logf("generated wide decode+encode:\n%s", generated)

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/v2wide",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "wide.go", `package rt

type Wide struct {
	Big    int64                        `+"`"+`toml:"big"`+"`"+`
	Ratio  float64                      `+"`"+`toml:"ratio"`+"`"+`
	Count  uint64                       `+"`"+`toml:"count"`+"`"+`
	Ptr    *int                         `+"`"+`toml:"ptr"`+"`"+`
	Desc   string                       `+"`"+`toml:"desc,multiline"`+"`"+`
	Opt    string                       `+"`"+`toml:"opt,omitempty"`+"`"+`
	Deep   L1                           `+"`"+`toml:"deep"`+"`"+`
	Env    map[string]string            `+"`"+`toml:"env"`+"`"+`
	Svcs   map[string]Svc               `+"`"+`toml:"svcs"`+"`"+`
	PSvcs  map[string]*Svc              `+"`"+`toml:"psvcs"`+"`"+`
	Groups map[string]map[string]string `+"`"+`toml:"groups"`+"`"+`
	PR     []*Rt                        `+"`"+`toml:"pr"`+"`"+`
}

type L1 struct {
	L2 L2 `+"`"+`toml:"l2"`+"`"+`
}
type L2 struct {
	Val int `+"`"+`toml:"val"`+"`"+`
}
type Svc struct {
	Image string `+"`"+`toml:"image"`+"`"+`
}
type Rt struct {
	Path string `+"`"+`toml:"path"`+"`"+`
}
`)

	writeFixture(t, dir, "wide_tommy.go", generated)

	writeFixture(t, dir, "roundtrip_test.go", `package rt

import "testing"

const in = `+"`"+`# wide comment
big = 9000000000
ratio = 2.5
count = 42
ptr = 7
desc = "line one"
opt = "present"

[deep]

[deep.l2]
val = 11

[env]
a = "1"
b = "2"

[svcs.web]
image = "nginx"

[psvcs.db]
image = "postgres"

[groups.team]
alice = "admin"

[[pr]]
path = "/x"

[[pr]]
path = "/y"
`+"`"+`

func TestV2Wide(t *testing.T) {
	doc, err := DecodeWide([]byte(in))
	if err != nil {
		t.Fatalf("DecodeWide: %v", err)
	}
	doc.Data().Big = 12345

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	d, err := DecodeWide(out)
	if err != nil {
		t.Fatalf("re-DecodeWide: %v\n%s", err, out)
	}
	w := d.Data()

	if w.Big != 12345 || w.Ratio != 2.5 || w.Count != 42 {
		t.Fatalf("scalars wrong: %+v", w)
	}
	if w.Ptr == nil || *w.Ptr != 7 {
		t.Fatalf("ptr wrong: %v", w.Ptr)
	}
	if w.Desc != "line one" || w.Opt != "present" {
		t.Fatalf("multiline/omitempty wrong: desc=%q opt=%q", w.Desc, w.Opt)
	}
	if w.Deep.L2.Val != 11 {
		t.Fatalf("3-level nesting wrong: %+v", w.Deep)
	}
	if w.Env["a"] != "1" || w.Env["b"] != "2" {
		t.Fatalf("map[string]string wrong: %v", w.Env)
	}
	if w.Svcs["web"].Image != "nginx" {
		t.Fatalf("map[string]struct wrong: %v", w.Svcs)
	}
	if w.PSvcs["db"] == nil || w.PSvcs["db"].Image != "postgres" {
		t.Fatalf("map[string]*struct wrong: %v", w.PSvcs)
	}
	if w.Groups["team"]["alice"] != "admin" {
		t.Fatalf("map[string]map[string]string wrong: %v", w.Groups)
	}
	if len(w.PR) != 2 || w.PR[0].Path != "/x" || w.PR[1].Path != "/y" {
		t.Fatalf("[]*struct wrong: %+v", w.PR)
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
	Tgt       TargetPath
	Key       string
	CodecKind spkCodec // codecPrim | codecCustom | codecText
	ZeroType  string   // jenZeroLit input; "" for slices
	Slice     bool
	Ptr       bool
	OmitEmpty bool
	Multiline bool
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
type e2MapScalar struct {
	Tgt TargetPath
	Bk  string
}
type e2MapMap struct {
	Tgt TargetPath
	Bk  string
}
type e2MapStruct struct {
	Tgt      TargetPath
	Bk       string
	TypeName string
	Ptr      bool
	Children []e2node
}

func (e2Leaf) isE2()       {}
func (e2Table) isE2()      {}
func (e2NilGuard) isE2()   {}
func (e2ArrayTable) isE2() {}
func (e2MapScalar) isE2()  {}
func (e2MapMap) isE2()     {}
func (e2MapStruct) isE2()  {}

type e2DelStruct struct {
	Tgt    TargetPath
	Bk     string
	Import string
	St     string
	Ptr    bool
}
type e2DelSlice struct {
	Tgt      TargetPath
	Bk       string
	Dotted   string
	Import   string
	St       string
	SlicePtr bool
}
type e2DelMap struct {
	Tgt    TargetPath
	Bk     string
	Import string
	St     string
}

func (e2DelStruct) isE2() {}
func (e2DelSlice) isE2()  {}
func (e2DelMap) isE2()    {}

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
		switch te.Codec {
		case codecPrim:
			return e2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, CodecKind: codecPrim, ZeroType: fi.TypeName, OmitEmpty: fi.OmitEmpty, Multiline: fi.Multiline}
		case codecCustom:
			return e2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, CodecKind: codecCustom}
		case codecText:
			return e2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, CodecKind: codecText, OmitEmpty: fi.OmitEmpty}
		}

	case spkPtr:
		switch te.Elem.(type) {
		case spkScalar:
			return e2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, CodecKind: codecPrim, Ptr: true}
		case spkStruct:
			return e2NilGuard{Tgt: fieldTgt, Bk: fi.TomlKey, Children: foldV2EncodeStruct(fi.InnerInfo, v2pos{dotted: dotted, tgt: fieldTgt})}
		case spkDelegated:
			_, st := delegateParts(fi.TypeName)
			return e2DelStruct{Tgt: fieldTgt, Bk: fi.TomlKey, Import: fi.ImportPath, St: st, Ptr: true}
		}

	case spkSlice:
		switch elem := te.Elem.(type) {
		case spkScalar:
			if elem.Codec == codecText {
				return e2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, Slice: true, CodecKind: codecText, OmitEmpty: fi.OmitEmpty}
			}
			return e2Leaf{Tgt: fieldTgt, Key: fi.TomlKey, Slice: true, CodecKind: codecPrim, OmitEmpty: fi.OmitEmpty}
		case spkStruct:
			return e2ArrayEncode(fi, fieldTgt, dotted, false)
		case spkDelegated:
			_, st := delegateParts(fi.TypeName)
			return e2DelSlice{Tgt: fieldTgt, Bk: fi.TomlKey, Dotted: dotted, Import: fi.ImportPath, St: st, SlicePtr: false}
		case spkPtr:
			switch elem.Elem.(type) {
			case spkStruct:
				return e2ArrayEncode(fi, fieldTgt, dotted, true)
			case spkDelegated:
				_, st := delegateParts(fi.TypeName)
				return e2DelSlice{Tgt: fieldTgt, Bk: fi.TomlKey, Dotted: dotted, Import: fi.ImportPath, St: st, SlicePtr: true}
			}
		}

	case spkMap:
		switch elem := te.Elem.(type) {
		case spkScalar:
			return e2MapScalar{Tgt: fieldTgt, Bk: fi.TomlKey}
		case spkMap:
			return e2MapMap{Tgt: fieldTgt, Bk: fi.TomlKey}
		case spkStruct:
			return e2MapStructNode(fi, fieldTgt, false)
		case spkDelegated:
			_, st := delegateParts(fi.ElemType)
			return e2DelMap{Tgt: fieldTgt, Bk: fi.TomlKey, Import: fi.ImportPath, St: st}
		case spkPtr:
			if _, ok := elem.Elem.(spkStruct); ok {
				return e2MapStructNode(fi, fieldTgt, true)
			}
		}

	case spkStruct:
		return e2Table{Bk: fi.TomlKey, Children: foldV2EncodeStruct(fi.InnerInfo, v2pos{dotted: dotted, tgt: fieldTgt})}

	case spkDelegated:
		_, st := delegateParts(fi.TypeName)
		return e2DelStruct{Tgt: fieldTgt, Bk: fi.TomlKey, Import: fi.ImportPath, St: st, Ptr: false}
	}
	panic("v2 encode spike: field shape out of scope: " + spikeKindName(fi.Kind))
}

func e2MapStructNode(fi FieldInfo, fieldTgt TargetPath, ptr bool) e2node {
	base := "mapVal"
	if ptr {
		base = "(*mapVal)"
	}
	children := foldV2EncodeStruct(fi.InnerInfo, v2pos{tgt: LocalTarget(base)})
	for _, c := range children {
		if _, ok := c.(e2Leaf); !ok {
			panic("v2 encode spike: nested container inside map[string]struct out of scope")
		}
	}
	return e2MapStruct{Tgt: fieldTgt, Bk: fi.TomlKey, TypeName: fi.TypeName, Ptr: ptr, Children: children}
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

func v2SetMultiline(cv *jen.Statement, key string, val *jen.Statement) jen.Code {
	return jen.If(jen.Err().Op(":=").Qual(cstPkg, "SetMultilineString").Call(cv.Clone(), jen.Lit(key), val), jen.Err().Op("!=").Nil()).Block(
		jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("%w"), jen.Err())),
	)
}

func v2DeleteValue(cv *jen.Statement, key string) jen.Code {
	return jen.Qual(cstPkg, "DeleteValue").Call(cv.Clone(), jen.Lit(key))
}

func v2RenderEncodeBody(g *jen.Group, cv *jen.Statement, children []e2node) {
	for _, c := range children {
		switch n := c.(type) {
		case e2Leaf:
			switch n.CodecKind {
			case codecCustom: // MarshalTOML + SetAny
				g.BlockFunc(func(b *jen.Group) {
					b.List(jen.Id("v"), jen.Err()).Op(":=").Add(n.Tgt.Jen().Clone()).Dot("MarshalTOML").Call()
					b.If(jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(n.Key+": %w"), jen.Err())))
					b.Add(v2SetAny(cv, n.Key, jen.Id("v")))
				})
			case codecText:
				if n.Slice { // MarshalText each -> []string
					g.BlockFunc(func(b *jen.Group) {
						b.Id("vals").Op(":=").Make(jen.Index().String(), jen.Len(n.Tgt.Jen().Clone()))
						b.For(jen.List(jen.Id("_i"), jen.Id("_item")).Op(":=").Range().Add(n.Tgt.Jen().Clone())).Block(
							jen.List(jen.Id("v"), jen.Err()).Op(":=").Id("_item").Dot("MarshalText").Call(),
							jen.If(jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(n.Key+"[%d]: %w"), jen.Id("_i"), jen.Err()))),
							jen.Id("vals").Index(jen.Id("_i")).Op("=").String().Call(jen.Id("v")),
						)
						b.Add(v2SetAny(cv, n.Key, jen.Id("vals")))
					})
				} else { // MarshalText + SetAny(string(v))
					g.BlockFunc(func(b *jen.Group) {
						b.List(jen.Id("v"), jen.Err()).Op(":=").Add(n.Tgt.Jen().Clone()).Dot("MarshalText").Call()
						b.If(jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(n.Key+": %w"), jen.Err())))
						if n.OmitEmpty {
							b.If(jen.Len(jen.Id("v")).Op(">").Lit(0)).Block(v2SetAny(cv, n.Key, jen.String().Call(jen.Id("v")))).Else().Block(v2DeleteValue(cv, n.Key))
						} else {
							b.Add(v2SetAny(cv, n.Key, jen.String().Call(jen.Id("v"))))
						}
					})
				}
			default: // codecPrim
				switch {
				case n.Ptr:
					g.If(n.Tgt.Jen().Clone().Op("!=").Nil()).Block(
						v2SetAny(cv, n.Key, jen.Op("*").Add(n.Tgt.Jen().Clone())),
					)
				case n.Slice:
					if n.OmitEmpty {
						g.If(jen.Len(n.Tgt.Jen().Clone()).Op(">").Lit(0).
							Op("||").Qual(cstPkg, "HasValue").Call(cv.Clone(), jen.Lit(n.Key))).Block(
							v2SetAny(cv, n.Key, n.Tgt.Jen().Clone()),
						)
					} else {
						// Non-omitempty: SetAny emits the explicit "key = []" for an
						// empty slice rather than dropping it. See #82.
						g.Add(v2SetAny(cv, n.Key, n.Tgt.Jen().Clone()))
					}
				default:
					setStmt := v2SetAny(cv, n.Key, n.Tgt.Jen().Clone())
					if n.Multiline && n.ZeroType == "string" {
						setStmt = v2SetMultiline(cv, n.Key, n.Tgt.Jen().Clone())
					}
					if n.OmitEmpty {
						g.If(n.Tgt.Jen().Clone().Op("!=").Add(jenZeroLit(n.ZeroType))).Block(setStmt).Else().Block(
							v2DeleteValue(cv, n.Key),
						)
					} else {
						g.If(n.Tgt.Jen().Clone().Op("!=").Add(jenZeroLit(n.ZeroType)).
							Op("||").Qual(cstPkg, "HasValue").Call(cv.Clone(), jen.Lit(n.Key))).Block(setStmt)
					}
				}
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

		case e2MapScalar: // map[string]string: EnsureChildTable + DeleteAllValues + loop
			g.If(jen.Len(n.Tgt.Jen().Clone()).Op(">").Lit(0)).BlockFunc(func(b *jen.Group) {
				b.Id("tableNode").Op(":=").Qual(cstPkg, "EnsureChildTable").Call(v2EncRoot(), cv.Clone(), jen.Lit(n.Bk))
				b.Qual(cstPkg, "DeleteAllValues").Call(jen.Id("tableNode"))
				b.For(jen.List(jen.Id("k"), jen.Id("v")).Op(":=").Range().Add(n.Tgt.Jen().Clone())).Block(
					jen.If(jen.Err().Op(":=").Qual(cstPkg, "SetAny").Call(jen.Id("tableNode"), jen.Id("k"), jen.Id("v")), jen.Err().Op("!=").Nil()).Block(
						jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("%w"), jen.Err())),
					),
				)
			})

		case e2MapStruct: // map[string]Struct / map[string]*Struct: EnsureChildSubTable per key
			g.If(jen.Len(n.Tgt.Jen().Clone()).Op(">").Lit(0)).BlockFunc(func(b *jen.Group) {
				b.For(jen.List(jen.Id("mapKey"), jen.Id("mapVal")).Op(":=").Range().Add(n.Tgt.Jen().Clone())).BlockFunc(func(lb *jen.Group) {
					lb.Id("subTable").Op(":=").Qual(cstPkg, "EnsureChildSubTable").Call(v2EncRoot(), cv.Clone(), jen.Lit(n.Bk), jen.Id("mapKey"))
					if n.Ptr {
						lb.If(jen.Id("mapVal").Op("==").Nil()).Block(jen.Continue())
					}
					v2RenderEncodeBody(lb, jen.Id("subTable"), n.Children)
				})
			})

		case e2MapMap: // map[string]map[string]string: EnsureChildSubTable + DeleteAllValues + loop
			g.If(jen.Len(n.Tgt.Jen().Clone()).Op(">").Lit(0)).BlockFunc(func(b *jen.Group) {
				b.For(jen.List(jen.Id("mapKey"), jen.Id("mapVal")).Op(":=").Range().Add(n.Tgt.Jen().Clone())).Block(
					jen.Id("subTable").Op(":=").Qual(cstPkg, "EnsureChildSubTable").Call(v2EncRoot(), v2EncRoot(), jen.Lit(n.Bk), jen.Id("mapKey")),
					jen.Qual(cstPkg, "DeleteAllValues").Call(jen.Id("subTable")),
					jen.For(jen.List(jen.Id("k"), jen.Id("v")).Op(":=").Range().Map(jen.String()).String().Call(jen.Id("mapVal"))).Block(
						jen.If(jen.Err().Op(":=").Qual(cstPkg, "SetAny").Call(jen.Id("subTable"), jen.Id("k"), jen.Id("v")), jen.Err().Op("!=").Nil()).Block(
							jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("%w"), jen.Err())),
						),
					),
				)
			})

		case e2DelStruct: // EnsureChildTable + pkg.Encode<St>From
			encFn := "Encode" + n.St + "From"
			doc := jen.Id("d").Dot("cstDoc")
			if n.Ptr {
				g.If(n.Tgt.Jen().Clone().Op("!=").Nil()).BlockFunc(func(b *jen.Group) {
					b.Id("tableNode").Op(":=").Qual(cstPkg, "EnsureChildTable").Call(v2EncRoot(), cv.Clone(), jen.Lit(n.Bk))
					b.If(jen.Err().Op(":=").Qual(n.Import, encFn).Call(n.Tgt.Jen().Clone(), doc.Clone(), jen.Id("tableNode")), jen.Err().Op("!=").Nil()).Block(
						jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(n.Bk+": %w"), jen.Err())),
					)
				})
			} else {
				g.BlockFunc(func(b *jen.Group) {
					b.Id("tableNode").Op(":=").Qual(cstPkg, "EnsureChildTable").Call(v2EncRoot(), cv.Clone(), jen.Lit(n.Bk))
					b.If(jen.Err().Op(":=").Qual(n.Import, encFn).Call(jen.Op("&").Add(n.Tgt.Jen().Clone()), doc.Clone(), jen.Id("tableNode")), jen.Err().Op("!=").Nil()).Block(
						jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(n.Bk+": %w"), jen.Err())),
					)
				})
			}

		case e2DelSlice: // array-of-tables, pkg.Encode<St>From per entry
			g.BlockFunc(func(b *jen.Group) {
				encFn := "Encode" + n.St + "From"
				doc := jen.Id("d").Dot("cstDoc")
				b.Id("_exist").Op(":=").Qual(cstPkg, "FindArrayTableNodes").Call(v2EncRoot(), jen.Lit(n.Dotted))
				b.For(jen.Id("i").Op(":=").Range().Add(n.Tgt.Jen().Clone())).BlockFunc(func(lb *jen.Group) {
					lb.Var().Id("container").Op("*").Qual(cstPkg, "Node")
					lb.If(jen.Id("i").Op("<").Len(jen.Id("_exist"))).Block(
						jen.Id("container").Op("=").Id("_exist").Index(jen.Id("i")),
					).Else().Block(
						jen.Id("container").Op("=").Qual(cstPkg, "AppendArrayTableEntryAfter").Call(v2EncRoot(), jen.Lit(n.Bk)),
					)
					arg := jen.Op("&").Add(n.Tgt.Jen().Clone()).Index(jen.Id("i"))
					if n.SlicePtr {
						arg = n.Tgt.Jen().Clone().Index(jen.Id("i"))
					}
					lb.If(jen.Err().Op(":=").Qual(n.Import, encFn).Call(arg, doc.Clone(), jen.Id("container")), jen.Err().Op("!=").Nil()).Block(
						jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(n.Bk+"[%d]: %w"), jen.Id("i"), jen.Err())),
					)
				})
			})

		case e2DelMap: // EnsureChildSubTable + pkg.Encode<St>From per key
			g.If(jen.Len(n.Tgt.Jen().Clone()).Op(">").Lit(0)).BlockFunc(func(b *jen.Group) {
				encFn := "Encode" + n.St + "From"
				doc := jen.Id("d").Dot("cstDoc")
				b.For(jen.List(jen.Id("mapKey"), jen.Id("mapVal")).Op(":=").Range().Add(n.Tgt.Jen().Clone())).BlockFunc(func(lb *jen.Group) {
					lb.Id("subTable").Op(":=").Qual(cstPkg, "EnsureChildSubTable").Call(v2EncRoot(), cv.Clone(), jen.Lit(n.Bk), jen.Id("mapKey"))
					lb.If(jen.Err().Op(":=").Qual(n.Import, encFn).Call(jen.Op("&").Id("mapVal"), doc.Clone(), jen.Id("subTable")), jen.Err().Op("!=").Nil()).Block(
						jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit(n.Bk+".%s: %w"), jen.Id("mapKey"), jen.Err())),
					)
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

// TestSpikeV2Codecs exercises the custom (TOMLMarshaler/Unmarshaler) and text
// (encoding.TextMarshaler/Unmarshaler) leaf codecs, incl. []text, via a
// compiled round-trip with fixture types implementing the interfaces.
func TestSpikeV2Codecs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping compile-and-run spike in short mode")
	}

	cod := StructInfo{Name: "Codecs", Fields: []FieldInfo{
		{GoName: "Name", TomlKey: "name", Kind: FieldCustom, TypeName: "Tag"},
		{GoName: "Lang", TomlKey: "lang", Kind: FieldTextMarshaler, TypeName: "Up"},
		{GoName: "Langs", TomlKey: "langs", Kind: FieldSliceTextMarshaler, TypeName: "Up"},
	}}

	dec := foldV2DecodeStruct(&cod, v2pos{tgt: ReceiverTarget("d", "data")})
	enc := foldV2EncodeStruct(&cod, v2pos{tgt: ReceiverTarget("d", "data")})
	generated, err := v2RenderFullFile("rt", "Codecs", dec, enc)
	if err != nil {
		t.Fatalf("V2 render: %v", err)
	}
	t.Logf("generated codecs decode+encode:\n%s", generated)

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/v2codecs",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	writeFixture(t, dir, "codecs.go", `package rt

import "strings"

// Tag implements tommy's custom codec (TOMLMarshaler/TOMLUnmarshaler): it
// round-trips through a "tag:"-prefixed string.
type Tag string

func (t *Tag) UnmarshalTOML(data any) error {
	s, _ := data.(string)
	*t = Tag(strings.TrimPrefix(s, "tag:"))
	return nil
}
func (t Tag) MarshalTOML() (any, error) { return "tag:" + string(t), nil }

// Up implements encoding.TextMarshaler/TextUnmarshaler: uppercased on the wire,
// lowercased in memory.
type Up string

func (u *Up) UnmarshalText(b []byte) error { *u = Up(strings.ToLower(string(b))); return nil }
func (u Up) MarshalText() ([]byte, error)  { return []byte(strings.ToUpper(string(u))), nil }

type Codecs struct {
	Name  Tag  `+"`"+`toml:"name"`+"`"+`
	Lang  Up   `+"`"+`toml:"lang"`+"`"+`
	Langs []Up `+"`"+`toml:"langs"`+"`"+`
}
`)

	writeFixture(t, dir, "codecs_tommy.go", generated)

	writeFixture(t, dir, "roundtrip_test.go", `package rt

import (
	"strings"
	"testing"
)

const in = `+"`"+`# c
name = "tag:alpha"
lang = "GO"
langs = ["GO", "RUST"]
`+"`"+`

func TestV2Codecs(t *testing.T) {
	doc, err := DecodeCodecs([]byte(in))
	if err != nil {
		t.Fatalf("DecodeCodecs: %v", err)
	}
	d := doc.Data()
	if d.Name != "alpha" {
		t.Fatalf("custom decode wrong: %q", d.Name)
	}
	if d.Lang != "go" {
		t.Fatalf("text decode wrong: %q", d.Lang)
	}
	if len(d.Langs) != 2 || d.Langs[0] != "go" || d.Langs[1] != "rust" {
		t.Fatalf("[]text decode wrong: %v", d.Langs)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "# c") {
		t.Fatalf("comment lost:\n%s", s)
	}
	for _, want := range []string{"tag:alpha", "GO", "RUST"} {
		if !strings.Contains(s, want) {
			t.Fatalf("encode missing %q:\n%s", want, s)
		}
	}

	d2, err := DecodeCodecs(out)
	if err != nil {
		t.Fatalf("re-DecodeCodecs: %v\n%s", err, s)
	}
	w := d2.Data()
	if w.Name != "alpha" || w.Lang != "go" || len(w.Langs) != 2 || w.Langs[1] != "rust" {
		t.Fatalf("re-decoded codecs wrong: %+v", w)
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

// TestSpikeV2Delegation exercises cross-package delegation: Delegated,
// Ptr(Delegated), Slice(Delegated), Map(Delegated). The temp module gets a
// hand-written `dele` package providing DecodeSettingsInto / EncodeSettingsFrom
// (standing in for what tommy would generate for the delegated package), and
// the main struct's generated code delegates to it.
func TestSpikeV2Delegation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping compile-and-run spike in short mode")
	}

	const imp = "example.com/v2del/dele"
	mn := StructInfo{Name: "Main", Fields: []FieldInfo{
		{GoName: "Solo", TomlKey: "solo", Kind: FieldDelegatedStruct, TypeName: "dele.Settings", ImportPath: imp},
		{GoName: "Opt", TomlKey: "opt", Kind: FieldPointerDelegatedStruct, TypeName: "dele.Settings", ImportPath: imp},
		{GoName: "Many", TomlKey: "many", Kind: FieldSliceDelegatedStruct, TypeName: "dele.Settings", ImportPath: imp},
		{GoName: "Keyed", TomlKey: "keyed", Kind: FieldMapStringDelegatedStruct, ElemType: "dele.Settings", ImportPath: imp},
	}}

	dec := foldV2DecodeStruct(&mn, v2pos{tgt: ReceiverTarget("d", "data")})
	enc := foldV2EncodeStruct(&mn, v2pos{tgt: ReceiverTarget("d", "data")})
	generated, err := v2RenderFullFile("rt", "Main", dec, enc)
	if err != nil {
		t.Fatalf("V2 render: %v", err)
	}
	t.Logf("generated delegation decode+encode:\n%s", generated)

	dir := t.TempDir()
	repoRoot, err := filepath.Abs(filepath.Join("..", "."))
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, dir, "go.mod", strings.Join([]string{
		"module example.com/v2del",
		"",
		"go 1.26",
		"",
		"require github.com/amarbel-llc/tommy v0.0.0",
		"",
		"replace github.com/amarbel-llc/tommy => " + repoRoot,
		"",
	}, "\n"))

	// The delegated package — what tommy would generate for Settings, here by hand.
	writeFixture(t, filepath.Join(dir, "dele"), "settings.go", `package dele

import (
	"github.com/amarbel-llc/tommy/pkg/cst"
	"github.com/amarbel-llc/tommy/pkg/document"
)

type Settings struct {
	Mode  string `+"`"+`toml:"mode"`+"`"+`
	Level int    `+"`"+`toml:"level"`+"`"+`
}

func DecodeSettingsInto(data *Settings, doc *document.Document, container *cst.Node, consumed map[string]bool, keyPrefix string) error {
	_, _, _ = doc, consumed, keyPrefix
	for _, kv := range container.Children {
		if kv.Kind != cst.NodeKeyValue {
			continue
		}
		switch cst.KeyValueName(kv) {
		case "mode":
			if v, ok := cst.ExtractString(kv); ok {
				data.Mode = v
			}
		case "level":
			if v, ok := cst.ExtractInt(kv); ok {
				data.Level = v
			}
		}
	}
	return nil
}

func EncodeSettingsFrom(data *Settings, doc *document.Document, container *cst.Node) error {
	_ = doc
	if err := cst.SetAny(container, "mode", data.Mode); err != nil {
		return err
	}
	if err := cst.SetAny(container, "level", data.Level); err != nil {
		return err
	}
	return nil
}
`)

	writeFixture(t, dir, "main.go", `package rt

import "example.com/v2del/dele"

type Main struct {
	Solo  dele.Settings            `+"`"+`toml:"solo"`+"`"+`
	Opt   *dele.Settings           `+"`"+`toml:"opt"`+"`"+`
	Many  []dele.Settings          `+"`"+`toml:"many"`+"`"+`
	Keyed map[string]dele.Settings `+"`"+`toml:"keyed"`+"`"+`
}
`)

	writeFixture(t, dir, "main_tommy.go", generated)

	writeFixture(t, dir, "roundtrip_test.go", `package rt

import "testing"

const in = `+"`"+`[solo]
mode = "a"
level = 1

[opt]
mode = "b"
level = 2

[[many]]
mode = "m1"
level = 10

[[many]]
mode = "m2"
level = 20

[keyed.x]
mode = "kx"
level = 99
`+"`"+`

func TestV2Delegation(t *testing.T) {
	doc, err := DecodeMain([]byte(in))
	if err != nil {
		t.Fatalf("DecodeMain: %v", err)
	}
	d := doc.Data()
	if d.Solo.Mode != "a" || d.Solo.Level != 1 {
		t.Fatalf("delegated struct wrong: %+v", d.Solo)
	}
	if d.Opt == nil || d.Opt.Mode != "b" || d.Opt.Level != 2 {
		t.Fatalf("delegated *struct wrong: %+v", d.Opt)
	}
	if len(d.Many) != 2 || d.Many[0].Mode != "m1" || d.Many[1].Level != 20 {
		t.Fatalf("delegated []struct wrong: %+v", d.Many)
	}
	if d.Keyed["x"].Mode != "kx" || d.Keyed["x"].Level != 99 {
		t.Fatalf("delegated map wrong: %+v", d.Keyed)
	}

	out, err := doc.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	d2, err := DecodeMain(out)
	if err != nil {
		t.Fatalf("re-DecodeMain: %v\n%s", err, out)
	}
	w := d2.Data()
	if w.Solo.Mode != "a" || w.Opt == nil || w.Opt.Mode != "b" ||
		len(w.Many) != 2 || w.Many[1].Level != 20 || w.Keyed["x"].Mode != "kx" {
		t.Fatalf("re-decoded delegation wrong: %+v", w)
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
