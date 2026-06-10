package generate

import (
	"io"
	"strings"

	jen "github.com/dave/jennifer/jen"
)

// Compositional renderer (#84). RenderFile walks the cd*/ce* node trees
// (comp_build.go) and emits the generated *_tommy.go. It uses the decode/encode
// contexts (jenCtx/encCtx) and the shared jennifer helpers (jenType,
// jenZeroLit, delegateParts, jenSetCall, cst/doc package consts) from
// comp_support.go. The four behaviors the proof-of-concept spike
// deferred — consumed/undecoded tracking, same-package []struct handle tracking,
// positional nesting (#10), and the flat-key fallback (#55) — are all handled
// here.

// ==========================================================================
// Encode walk
// ==========================================================================

func compRenderEncodeBody(ctx encCtx, children []ceNode, cv *jen.Statement) []jen.Code {
	var out []jen.Code
	for _, c := range children {
		out = append(out, compEncodeNode(ctx, c, cv)...)
	}
	return out
}

// compEncodeNeedsContainer reports whether the parent table/sub-table node will
// be referenced by any child — i.e. whether to bind it to a variable rather than
// `_`. Most child kinds write through the container (now including ceMapMap,
// which nests its sub-tables under it). The exceptions operate on the document
// root: array-tables / delegated slices when NOT scoped (top-level /
// struct-nested, found root-wide by their unique dotted key). A scoped
// array-table/delegated-slice does use the container (find/append within scope).
func compEncodeNeedsContainer(children []ceNode) bool {
	for _, c := range children {
		switch n := c.(type) {
		case ceArrayTable:
			if n.Scoped {
				return true
			}
		case ceDelSlice:
			if n.Scoped {
				return true
			}
		case ceTable:
			if structRefsContainer(n.Children) {
				return true
			}
		case ceNilGuard:
			if structRefsContainer(n.Children) {
				return true
			}
		default:
			return true
		}
	}
	return false
}

// structRefsContainer reports whether a nested struct's encode references its
// parent container node. compEncTable/compEncNilGuard emit
// EnsureChildTable(root, cv, key) — referencing cv — in both the needsTable and
// the `_ =` branches, EXCEPT when the struct is an all-array-tables struct whose
// header is omitted (#89): there neither branch fires, so cv is untouched.
// Recursing here keeps compEncodeNeedsContainer's prediction in step with what's
// rendered; without it a parent binds an unused `tableNode` when its only
// container-needing child is such a header-omitting struct (#105).
func structRefsContainer(children []ceNode) bool {
	return compEncodeNeedsContainer(children) || !compEncodeAllArrayTables(children)
}

func compEncodeNode(ctx encCtx, c ceNode, cv *jen.Statement) []jen.Code {
	switch n := c.(type) {
	case ceLeaf:
		return compEncodeLeaf(ctx, n, cv)
	case ceMapScalar:
		return compSetMapScalar(ctx, n, cv)
	case ceMapMap:
		return compEncMapMap(ctx, n, cv)
	case ceTable:
		return compEncTable(ctx, n, cv)
	case ceNilGuard:
		return compEncNilGuard(ctx, n, cv)
	case ceArrayTable:
		return compEncArrayTable(ctx, n, cv)
	case ceMapStruct:
		return compEncMapStruct(ctx, n, cv)
	case ceDelStruct:
		return compEncDelStruct(ctx, n, cv)
	case ceDelSlice:
		return compEncDelSlice(ctx, n, cv)
	case ceDelMap:
		return compEncDelMap(ctx, n, cv)
	}
	return nil
}

func compEncodeLeaf(ctx encCtx, n ceLeaf, cv *jen.Statement) []jen.Code {
	switch n.Kind {
	case ceLeafPrim:
		return compSetPrimitive(ctx, n, cv)
	case ceLeafPtrPrim:
		bk := n.TKey.BareKey()
		return []jen.Code{
			jen.If(n.Tgt.Jen().Clone().Op("!=").Nil()).Block(
				jenSetCall(ctx, cv, StaticKey(bk), jen.Op("*").Add(n.Tgt.Jen().Clone())),
			),
		}
	case ceLeafCustom:
		bk := n.TKey.BareKey()
		return []jen.Code{jen.BlockFunc(func(g *jen.Group) {
			g.List(jen.Id("v"), jen.Err()).Op(":=").Add(n.Tgt.Jen().Clone()).Dot("MarshalTOML").Call()
			g.If(jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+": %w", jen.Err()))
			g.Add(jenSetCall(ctx, cv, StaticKey(bk), jen.Id("v")))
		})}
	case ceLeafText:
		bk := n.TKey.BareKey()
		return []jen.Code{jen.BlockFunc(func(g *jen.Group) {
			g.List(jen.Id("v"), jen.Err()).Op(":=").Add(n.Tgt.Jen().Clone()).Dot("MarshalText").Call()
			g.If(jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+": %w", jen.Err()))
			if n.OmitEmpty {
				g.If(jen.Len(jen.Id("v")).Op(">").Lit(0)).Block(
					jenSetCall(ctx, cv, StaticKey(bk), jen.String().Call(jen.Id("v"))),
				).Else().Block(
					jen.Qual(cstPkg, "DeleteValue").Call(cv.Clone(), jen.Lit(bk)),
				)
			} else {
				g.Add(jenSetCall(ctx, cv, StaticKey(bk), jen.String().Call(jen.Id("v"))))
			}
		})}
	case ceLeafSlicePrim:
		return compSetSlicePrimitive(ctx, n, cv)
	case ceLeafSliceText:
		return compSetSliceTextMarshaler(ctx, n, cv)
	}
	return nil
}

func compSetPrimitive(ctx encCtx, n ceLeaf, cv *jen.Statement) []jen.Code {
	src := n.Tgt.Jen()
	bk := n.TKey.BareKey()
	zv := jenZeroLit(n.TypeName)
	encSrc := src.Clone()
	zvCmp := src.Clone()
	if n.ElemType != "" {
		encSrc = jen.Id(n.TypeName).Call(src.Clone())
		zvCmp = src.Clone()
		zv = jenType(n.ElemType, n.ImportPath).Call(jenZeroLit(n.TypeName))
	}

	if n.OmitEmpty {
		var setStmt jen.Code
		if n.Multiline && n.TypeName == "string" {
			setStmt = jenSetMultilineCall(ctx, cv, StaticKey(bk), encSrc)
		} else {
			setStmt = jenSetCall(ctx, cv, StaticKey(bk), encSrc)
		}
		return []jen.Code{
			jen.If(zvCmp.Op("!=").Add(zv)).Block(setStmt).Else().Block(
				jen.Qual(cstPkg, "DeleteValue").Call(cv.Clone(), jen.Lit(bk)),
			),
		}
	}

	var setStmt jen.Code
	if n.Multiline && n.TypeName == "string" {
		setStmt = jenSetMultilineCall(ctx, cv, StaticKey(bk), encSrc)
	} else {
		setStmt = jenSetCall(ctx, cv, StaticKey(bk), encSrc)
	}
	return []jen.Code{
		jen.If(zvCmp.Op("!=").Add(zv).Op("||").Qual(cstPkg, "HasValue").Call(cv.Clone(), jen.Lit(bk))).Block(setStmt),
	}
}

func compSetSlicePrimitive(ctx encCtx, n ceLeaf, cv *jen.Statement) []jen.Code {
	bk := n.TKey.BareKey()
	if !n.OmitEmpty {
		// Faithful nil/empty (#21): a nil slice omits the key entirely; a non-nil
		// slice — including an empty one — emits `key = [...]` / `key = []`. (An
		// inline array, unlike an array-of-tables, has a present-empty TOML form.)
		return []jen.Code{jen.BlockFunc(func(g *jen.Group) {
			g.If(n.Tgt.Jen().Op("!=").Nil()).BlockFunc(func(g *jen.Group) {
				g.Add(compSlicePrimSet(ctx, n, cv, bk))
			})
		})}
	}
	src := n.Tgt.Jen()
	return []jen.Code{jen.BlockFunc(func(g *jen.Group) {
		g.If(jen.Len(src.Clone()).Op(">").Lit(0).Op("||").Qual(cstPkg, "HasValue").Call(cv.Clone(), jen.Lit(bk))).BlockFunc(func(g *jen.Group) {
			g.Add(compSlicePrimSet(ctx, n, cv, bk))
		})
	})}
}

func compSlicePrimSet(ctx encCtx, n ceLeaf, cv *jen.Statement, bk string) jen.Code {
	src := n.Tgt.Jen()
	if n.SlicePointer {
		tmpVar := "tmp" + n.Tgt.Segs[len(n.Tgt.Segs)-1].Name
		return jen.BlockFunc(func(g *jen.Group) {
			g.Id(tmpVar).Op(":=").Make(jen.Index().Id(n.ElemType), jen.Lit(0), jen.Len(src.Clone()))
			g.For(jen.List(jen.Id("_"), jen.Id("p")).Op(":=").Range().Add(src.Clone())).Block(
				jen.If(jen.Id("p").Op("!=").Nil()).Block(
					jen.Id(tmpVar).Op("=").Append(jen.Id(tmpVar), jen.Op("*").Id("p")),
				),
			)
			g.Add(jenSetCall(ctx, cv, StaticKey(bk), jen.Id(tmpVar)))
		})
	}
	var encSrc *jen.Statement
	if n.TypeName != "" {
		encSrc = jen.Index().Id(n.ElemType).Call(src.Clone())
	} else {
		encSrc = src.Clone()
	}
	return jenSetCall(ctx, cv, StaticKey(bk), encSrc)
}

func compSetSliceTextMarshaler(ctx encCtx, n ceLeaf, cv *jen.Statement) []jen.Code {
	bk := n.TKey.BareKey()
	src := n.Tgt.Jen()
	emit := func(g *jen.Group) {
		g.Id("vals").Op(":=").Make(jen.Index().String(), jen.Len(src.Clone()))
		g.For(jen.List(jen.Id("i"), jen.Id("item")).Op(":=").Range().Add(src.Clone())).Block(
			jen.List(jen.Id("v"), jen.Err()).Op(":=").Id("item").Dot("MarshalText").Call(),
			jen.If(jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+"[%d]: %w", jen.Id("i"), jen.Err())),
			jen.Id("vals").Index(jen.Id("i")).Op("=").String().Call(jen.Id("v")),
		)
		g.Add(jenSetCall(ctx, cv, StaticKey(bk), jen.Id("vals")))
	}
	if !n.OmitEmpty {
		// Faithful nil/empty (#21): nil omits, non-nil (incl. empty) emits.
		return []jen.Code{jen.BlockFunc(func(g *jen.Group) {
			g.If(src.Clone().Op("!=").Nil()).BlockFunc(emit)
		})}
	}
	return []jen.Code{jen.BlockFunc(func(g *jen.Group) {
		g.If(jen.Len(src.Clone()).Op(">").Lit(0).Op("||").Qual(cstPkg, "HasValue").Call(cv.Clone(), jen.Lit(bk))).BlockFunc(emit)
	})}
}

func compSetMapScalar(ctx encCtx, n ceMapScalar, cv *jen.Statement) []jen.Code {
	bk := n.TKey.BareKey()
	src := n.Tgt.Jen()
	// Faithful nil/empty (#21): a nil map omits the [table]; a non-nil map —
	// including an empty one — emits the `[table]` header (EnsureChildTable creates
	// it even with no entries), so a present-empty map round-trips as non-nil
	// rather than collapsing to an absent-table nil.
	return []jen.Code{
		jen.If(src.Clone().Op("!=").Nil()).BlockFunc(func(g *jen.Group) {
			g.Id("tableNode").Op(":=").Qual(cstPkg, "EnsureChildTable").Call(ctx.rootVar.Clone(), cv.Clone(), jen.Lit(bk))
			g.Qual(cstPkg, "DeleteAllValues").Call(jen.Id("tableNode"))
			g.For(jen.List(jen.Id("k"), jen.Id("v")).Op(":=").Range().Add(src.Clone())).Block(
				jen.If(jen.Err().Op(":=").Qual(cstPkg, "SetAny").Call(
					jen.Id("tableNode"), jen.Id("k"), jen.Id("v"),
				), jen.Err().Op("!=").Nil()).Block(ctx.retErr("%w", jen.Err())),
			)
		}),
	}
}

func compEncMapMap(ctx encCtx, n ceMapMap, cv *jen.Statement) []jen.Code {
	bk := n.TKey.BareKey()
	src := n.Tgt.Jen()
	// cv is the parent container (document root at top level, the enclosing
	// struct/array-entry node when nested). Nesting the sub-tables under it —
	// like ceMapStruct — keeps map[string]NamedMap scoped to its parent (#86/#87
	// for the mapmap kind), rather than always writing at the document root.
	return []jen.Code{
		jen.If(jen.Len(src.Clone()).Op(">").Lit(0)).BlockFunc(func(g *jen.Group) {
			g.For(jen.List(jen.Id("mapKey"), jen.Id("mapVal")).Op(":=").Range().Add(src.Clone())).Block(
				jen.Id("subTable").Op(":=").Qual(cstPkg, "EnsureChildSubTable").Call(ctx.rootVar.Clone(), cv.Clone(), jen.Lit(bk), jen.Id("mapKey")),
				jen.Qual(cstPkg, "DeleteAllValues").Call(jen.Id("subTable")),
				jen.For(jen.List(jen.Id("k"), jen.Id("v")).Op(":=").Range().Map(jen.String()).String().Call(jen.Id("mapVal"))).Block(
					jen.If(jen.Err().Op(":=").Qual(cstPkg, "SetAny").Call(
						jen.Id("subTable"), jen.Id("k"), jen.Id("v"),
					), jen.Err().Op("!=").Nil()).Block(ctx.retErr("%w", jen.Err())),
				),
			)
		}),
	}
}

func compEncTable(ctx encCtx, n ceTable, cv *jen.Statement) []jen.Code {
	bk := n.TKey.BareKey()
	needsTable := compEncodeNeedsContainer(n.Children)
	skip := compEncodeAllArrayTables(n.Children)
	return []jen.Code{jen.BlockFunc(func(g *jen.Group) {
		if needsTable {
			g.Id("tableNode").Op(":=").Qual(cstPkg, "EnsureChildTable").Call(ctx.rootVar.Clone(), cv.Clone(), jen.Lit(bk))
		} else if !skip {
			g.Id("_").Op("=").Qual(cstPkg, "EnsureChildTable").Call(ctx.rootVar.Clone(), cv.Clone(), jen.Lit(bk))
		}
		// When every child is a document-root-relative array-table (#89), no
		// parent table is created — the array-table headers imply it, and decode
		// falls back to the dotted-key search (cdInTable.FlatChildren).
		for _, s := range compRenderEncodeBody(ctx, n.Children, jen.Id("tableNode")) {
			g.Add(s)
		}
	})}
}

// compEncodeAllArrayTables reports whether every child is a document-root-
// relative array-table or delegated-slice. Such a struct needs no parent table
// node on encode: the [[a.b]] headers already name the parent, and decode falls
// back to the dotted-key search when the [a] header is absent (#89). A nested-map
// child (which compEncodeNeedsContainer also treats as root-relative) or any
// container-writing child returns false, so those keep their parent table.
func compEncodeAllArrayTables(children []ceNode) bool {
	if len(children) == 0 {
		return false
	}
	for _, c := range children {
		switch n := c.(type) {
		case ceArrayTable:
			if n.Scoped {
				return false
			}
		case ceDelSlice:
			if n.Scoped {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func compEncNilGuard(ctx encCtx, n ceNilGuard, cv *jen.Statement) []jen.Code {
	bk := n.TKey.BareKey()
	needsTable := compEncodeNeedsContainer(n.Children)
	skip := compEncodeAllArrayTables(n.Children)
	return []jen.Code{
		jen.If(n.Tgt.Jen().Clone().Op("!=").Nil()).BlockFunc(func(g *jen.Group) {
			// See compEncTable / #89: a struct of only array-tables needs no
			// parent table node; other root-relative children keep theirs.
			if needsTable {
				g.Id("tableNode").Op(":=").Qual(cstPkg, "EnsureChildTable").Call(ctx.rootVar.Clone(), cv.Clone(), jen.Lit(bk))
			} else if !skip {
				g.Id("_").Op("=").Qual(cstPkg, "EnsureChildTable").Call(ctx.rootVar.Clone(), cv.Clone(), jen.Lit(bk))
			}
			for _, s := range compRenderEncodeBody(ctx, n.Children, jen.Id("tableNode")) {
				g.Add(s)
			}
		}),
	}
}

func compEncArrayTable(ctx encCtx, n ceArrayTable, cv *jen.Statement) []jen.Code {
	src := n.Tgt.Jen()
	bk := n.TKey.BareKey()
	return []jen.Code{jen.BlockFunc(func(g *jen.Group) {
		if n.TrackHandles {
			// Top-level same-package []struct: reuse the decode-recorded entry
			// handles; new entries append at the document root.
			handleSlice := "d." + toLowerFirst(n.Tgt.Segs[len(n.Tgt.Segs)-1].Name)
			g.For(jen.Id(n.IdxVar).Op(":=").Range().Add(src.Clone())).BlockFunc(func(g *jen.Group) {
				skipNilElem(g, n.SlicePtr, src, n.IdxVar)
				g.Var().Id("container").Op("*").Qual(cstPkg, "Node")
				g.If(jen.Id(n.IdxVar).Op("<").Len(jen.Id(handleSlice))).Block(
					jen.Id("container").Op("=").Id(handleSlice).Index(jen.Id(n.IdxVar)).Dot("node"),
				).Else().Block(
					jen.Id("container").Op("=").Qual(cstPkg, "AppendArrayTableEntryAfter").Call(ctx.rootVar.Clone(), jen.Lit(bk)),
				)
				for _, s := range compRenderEncodeBody(ctx, n.Children, jen.Id("container")) {
					g.Add(s)
				}
			})
		} else if n.Scoped {
			// Nested inside an array-table entry: header is ambiguous across sibling
			// entries, so find + append within the parent container (cv) rather than
			// document-wide. cv is captured into _ap before the loop's own
			// `container` shadows it.
			pv := "_ap" + n.TDottedKey.VarSuffix()
			existVar := "_exist" + n.TDottedKey.VarSuffix()
			g.Id(pv).Op(":=").Add(cv.Clone())
			g.Id(existVar).Op(":=").Qual(cstPkg, "FindChildArrayTableNodes").Call(ctx.rootVar.Clone(), jen.Id(pv), jen.Lit(bk))
			g.For(jen.Id(n.IdxVar).Op(":=").Range().Add(src.Clone())).BlockFunc(func(g *jen.Group) {
				skipNilElem(g, n.SlicePtr, src, n.IdxVar)
				g.Var().Id("container").Op("*").Qual(cstPkg, "Node")
				g.If(jen.Id(n.IdxVar).Op("<").Len(jen.Id(existVar))).Block(
					jen.Id("container").Op("=").Id(existVar).Index(jen.Id(n.IdxVar)),
				).Else().Block(
					jen.Id("container").Op("=").Qual(cstPkg, "AppendChildArrayTableEntry").Call(ctx.rootVar.Clone(), jen.Id(pv), jen.Lit(bk)),
				)
				for _, s := range compRenderEncodeBody(ctx, n.Children, jen.Id("container")) {
					g.Add(s)
				}
			})
		} else {
			// Top-level or struct-nested (unique dotted key): find/append document-
			// wide by the full dotted key — robust even when the parent table is
			// implicit and gets created at the document end on encode.
			existVar := "_exist" + n.TDottedKey.VarSuffix()
			g.Id(existVar).Op(":=").Qual(cstPkg, "FindArrayTableNodes").Call(ctx.rootVar.Clone(), n.TDottedKey.Jen())
			g.For(jen.Id(n.IdxVar).Op(":=").Range().Add(src.Clone())).BlockFunc(func(g *jen.Group) {
				skipNilElem(g, n.SlicePtr, src, n.IdxVar)
				g.Var().Id("container").Op("*").Qual(cstPkg, "Node")
				g.If(jen.Id(n.IdxVar).Op("<").Len(jen.Id(existVar))).Block(
					jen.Id("container").Op("=").Id(existVar).Index(jen.Id(n.IdxVar)),
				).Else().Block(
					jen.Id("container").Op("=").Qual(cstPkg, "AppendArrayTableEntryAfter").Call(ctx.rootVar.Clone(), n.TDottedKey.Jen()),
				)
				for _, s := range compRenderEncodeBody(ctx, n.Children, jen.Id("container")) {
					g.Add(s)
				}
			})
		}
	})}
}

func compEncMapStruct(ctx encCtx, n ceMapStruct, cv *jen.Statement) []jen.Code {
	bk := n.TKey.BareKey()
	src := n.Tgt.Jen()
	return []jen.Code{
		jen.If(jen.Len(src.Clone()).Op(">").Lit(0)).BlockFunc(func(g *jen.Group) {
			g.For(jen.List(jen.Id("mapKey"), jen.Id("mapVal")).Op(":=").Range().Add(src.Clone())).BlockFunc(func(g *jen.Group) {
				needsSub := compEncodeNeedsContainer(n.Children)
				if needsSub {
					g.Id("subTable").Op(":=").Qual(cstPkg, "EnsureChildSubTable").Call(ctx.rootVar.Clone(), cv.Clone(), jen.Lit(bk), jen.Id("mapKey"))
				} else {
					g.Id("_").Op("=").Qual(cstPkg, "EnsureChildSubTable").Call(ctx.rootVar.Clone(), cv.Clone(), jen.Lit(bk), jen.Id("mapKey"))
				}
				if n.SlicePtr {
					g.If(jen.Id("mapVal").Op("==").Nil()).Block(jen.Continue())
				}
				for _, s := range compRenderEncodeBody(ctx, n.Children, jen.Id("subTable")) {
					g.Add(s)
				}
			})
		}),
	}
}

func compEncDelStruct(ctx encCtx, n ceDelStruct, cv *jen.Statement) []jen.Code {
	_, st := delegateParts(n.TypeName)
	bk := n.TKey.BareKey()
	encFn := "Encode" + st + "From"

	if n.Ptr {
		return []jen.Code{
			jen.If(n.Tgt.Jen().Clone().Op("!=").Nil()).BlockFunc(func(g *jen.Group) {
				g.Id("tableNode").Op(":=").Qual(cstPkg, "EnsureChildTable").Call(ctx.rootVar.Clone(), cv.Clone(), jen.Lit(bk))
				g.If(jen.Err().Op(":=").Qual(n.ImportPath, encFn).Call(
					n.Tgt.Jen().Clone(), ctx.docVar.Clone(), jen.Id("tableNode"),
				), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+": %w", jen.Err()))
			}),
		}
	}
	return []jen.Code{jen.BlockFunc(func(g *jen.Group) {
		g.Id("tableNode").Op(":=").Qual(cstPkg, "EnsureChildTable").Call(ctx.rootVar.Clone(), cv.Clone(), jen.Lit(bk))
		g.If(jen.Err().Op(":=").Qual(n.ImportPath, encFn).Call(
			jen.Op("&").Add(n.Tgt.Jen().Clone()), ctx.docVar.Clone(), jen.Id("tableNode"),
		), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+": %w", jen.Err()))
	})}
}

func compEncDelSlice(ctx encCtx, n ceDelSlice, cv *jen.Statement) []jen.Code {
	_, st := delegateParts(n.TypeName)
	bk := n.TKey.BareKey()
	encFn := "Encode" + st + "From"
	src := n.Tgt.Jen()
	existVar := "_exist" + n.TDottedKey.VarSuffix()
	pv := "_ap" + n.TDottedKey.VarSuffix()

	entry := func(g *jen.Group) {
		if n.SlicePtr {
			g.If(jen.Err().Op(":=").Qual(n.ImportPath, encFn).Call(
				src.Clone().Index(jen.Id(n.IdxVar)), ctx.docVar.Clone(), jen.Id("container"),
			), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+"[%d]: %w", jen.Id(n.IdxVar), jen.Err()))
		} else {
			g.If(jen.Err().Op(":=").Qual(n.ImportPath, encFn).Call(
				jen.Op("&").Add(src.Clone()).Index(jen.Id(n.IdxVar)), ctx.docVar.Clone(), jen.Id("container"),
			), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+"[%d]: %w", jen.Id(n.IdxVar), jen.Err()))
		}
	}

	return []jen.Code{jen.BlockFunc(func(g *jen.Group) {
		if n.Scoped {
			g.Id(pv).Op(":=").Add(cv.Clone())
			g.Id(existVar).Op(":=").Qual(cstPkg, "FindChildArrayTableNodes").Call(ctx.rootVar.Clone(), jen.Id(pv), jen.Lit(bk))
			g.For(jen.Id(n.IdxVar).Op(":=").Range().Add(src.Clone())).BlockFunc(func(g *jen.Group) {
				skipNilElem(g, n.SlicePtr, src, n.IdxVar)
				g.Var().Id("container").Op("*").Qual(cstPkg, "Node")
				g.If(jen.Id(n.IdxVar).Op("<").Len(jen.Id(existVar))).Block(
					jen.Id("container").Op("=").Id(existVar).Index(jen.Id(n.IdxVar)),
				).Else().Block(
					jen.Id("container").Op("=").Qual(cstPkg, "AppendChildArrayTableEntry").Call(ctx.rootVar.Clone(), jen.Id(pv), jen.Lit(bk)),
				)
				entry(g)
			})
		} else {
			g.Id(existVar).Op(":=").Qual(cstPkg, "FindArrayTableNodes").Call(ctx.rootVar.Clone(), n.TDottedKey.Jen())
			g.For(jen.Id(n.IdxVar).Op(":=").Range().Add(src.Clone())).BlockFunc(func(g *jen.Group) {
				skipNilElem(g, n.SlicePtr, src, n.IdxVar)
				g.Var().Id("container").Op("*").Qual(cstPkg, "Node")
				g.If(jen.Id(n.IdxVar).Op("<").Len(jen.Id(existVar))).Block(
					jen.Id("container").Op("=").Id(existVar).Index(jen.Id(n.IdxVar)),
				).Else().Block(
					jen.Id("container").Op("=").Qual(cstPkg, "AppendArrayTableEntryAfter").Call(ctx.rootVar.Clone(), n.TDottedKey.Jen()),
				)
				entry(g)
			})
		}
	})}
}

func compEncDelMap(ctx encCtx, n ceDelMap, cv *jen.Statement) []jen.Code {
	_, st := delegateParts(n.ElemType)
	bk := n.TKey.BareKey()
	encFn := "Encode" + st + "From"
	src := n.Tgt.Jen()

	return []jen.Code{
		jen.If(jen.Len(src.Clone()).Op(">").Lit(0)).BlockFunc(func(g *jen.Group) {
			g.For(jen.List(jen.Id("mapKey"), jen.Id("mapVal")).Op(":=").Range().Add(src.Clone())).BlockFunc(func(g *jen.Group) {
				g.Id("subTable").Op(":=").Qual(cstPkg, "EnsureChildSubTable").Call(ctx.rootVar.Clone(), cv.Clone(), jen.Lit(bk), jen.Id("mapKey"))
				g.If(jen.Err().Op(":=").Qual(n.ImportPath, encFn).Call(
					jen.Op("&").Id("mapVal"), ctx.docVar.Clone(), jen.Id("subTable"),
				), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+".%s: %w", jen.Id("mapKey"), jen.Err()))
			})
		}),
	}
}

// ==========================================================================
// File / struct scaffolding
// ==========================================================================

// RenderFile renders the generated *_tommy.go file for the given structs.
func RenderFile(w io.Writer, pkg string, structs []StructInfo) error {
	f := jen.NewFile(pkg)
	f.NoFormat = true
	f.HeaderComment("Code generated by tommy " + BuildVersion + " (" + BuildCommit + "); DO NOT EDIT.")
	f.ImportName("fmt", "fmt")
	f.ImportName(cstPkg, "cst")
	f.ImportName(docPkg, "document")
	f.ImportName("strings", "strings")
	for _, p := range collectImportPaths(structs) {
		parts := strings.Split(p, "/")
		f.ImportName(p, parts[len(parts)-1])
	}
	f.Var().Defs(
		jen.Id("_").Op("=").Qual("fmt", "Errorf"),
		jen.Id("_").Qual(cstPkg, "NodeKind"),
		jen.Id("_").Op("=").Qual("strings", "Contains"),
	)
	for _, si := range structs {
		compEmitStruct(f, si)
	}
	return f.Render(w)
}

func compEmitStruct(f *jen.File, si StructInfo) {
	dt := si.Name + "Document"
	// Handle types are named struct+field (handleTypeName): they are file-scoped
	// declarations, so naming by element type alone collided when two slice
	// fields (or two structs' fields) shared an element struct.
	for _, fi := range si.Fields {
		if isSamePackageSliceStruct(fi) {
			f.Type().Id(handleTypeName(si.Name, fi.GoName)).Struct(jen.Id("node").Op("*").Qual(cstPkg, "Node"))
		}
	}
	f.Type().Id(dt).StructFunc(func(g *jen.Group) {
		g.Id("data").Id(si.Name)
		g.Id("cstDoc").Op("*").Qual(docPkg, "Document")
		g.Id("model").Op("*").Qual(cstPkg, "Value")
		for _, fi := range si.Fields {
			if isSamePackageSliceStruct(fi) {
				g.Id(unexport(fi.GoName)).Index().Id(handleTypeName(si.Name, fi.GoName))
			}
		}
	})
	compEmitDecode(f, si, dt)
	f.Func().Params(jen.Id("d").Op("*").Id(dt)).Id("Data").Params().Op("*").Id(si.Name).Block(
		jen.Return(jen.Op("&").Id("d").Dot("data")),
	)
	compEmitEncode(f, si, dt)
	// Undecoded reports key-paths present in the input that no field consumed,
	// computed on the normalized model (spelling-independent, ADR 2026-06-07).
	f.Func().Params(jen.Id("d").Op("*").Id(dt)).Id("Undecoded").Params().Index().String().Block(
		jen.If(jen.Id("d").Dot("model").Op("==").Nil()).Block(jen.Return(jen.Nil())),
		jen.Return(jen.Id("d").Dot("model").Dot("Undecoded").Call()),
	)
	for _, m := range []struct{ n, d string }{
		{"Comment", "GetComment"},
		{"SetComment", "SetComment"},
		{"InlineComment", "GetInlineComment"},
		{"SetInlineComment", "SetInlineComment"},
	} {
		if strings.HasPrefix(m.n, "Set") {
			f.Func().Params(jen.Id("d").Op("*").Id(dt)).Id(m.n).Params(jen.Id("key"), jen.Id("comment").String()).Block(
				jen.Id("d").Dot("cstDoc").Dot(m.d).Call(jen.Id("key"), jen.Id("comment")),
			)
		} else {
			f.Func().Params(jen.Id("d").Op("*").Id(dt)).Id(m.n).Params(jen.Id("key").String()).String().Block(
				jen.Return(jen.Id("d").Dot("cstDoc").Dot(m.d).Call(jen.Id("key"))),
			)
		}
	}
	compEmitDecodeInto(f, si)
	compEmitEncodeFrom(f, si)
}

func compEmitDecode(f *jen.File, si StructInfo, dt string) {
	ctx := receiverJenCtx()
	nodes := foldCompDecode(&si, compPos{tkey: StaticKey(""), tgt: ReceiverTarget("d", "data"), seq: new(int)}, true)
	f.Func().Id("Decode"+si.Name).Params(jen.Id("input").Index().Byte()).Params(jen.Op("*").Id(dt), jen.Error()).BlockFunc(func(g *jen.Group) {
		g.List(jen.Id("doc"), jen.Err()).Op(":=").Qual(docPkg, "Parse").Call(jen.Id("input"))
		g.If(jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Err()))
		// Normalize every TOML spelling to one value model; this also rejects
		// duplicate keys in any spelling (ADR 2026-06-07, subsuming #110).
		g.List(jen.Id("model"), jen.Err()).Op(":=").Qual(cstPkg, "Decompose").Call(jen.Id("doc").Dot("Root").Call())
		g.If(jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Err()))
		g.Empty()
		g.Id("d").Op(":=").Op("&").Id(dt).Values(jen.Dict{
			jen.Id("cstDoc"): jen.Id("doc"),
			jen.Id("model"):  jen.Id("model"),
		})
		g.Empty()
		compModelBody(ctx, g, nodes, jen.Id("model"), "")
		if si.Validatable {
			g.If(jen.Err().Op(":=").Id("d").Dot("data").Dot("Validate").Call(), jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("validation failed: %w"), jen.Err())),
			)
		}
		g.Return(jen.Id("d"), jen.Nil())
	})
}

func compEmitEncode(f *jen.File, si StructInfo, dt string) {
	ectx := receiverEncCtx()
	nodes := foldCompEncode(&si, compPos{tkey: StaticKey(""), tgt: ReceiverTarget("d", "data")}, true)
	f.Func().Params(jen.Id("d").Op("*").Id(dt)).Id("Encode").Params().Params(jen.Index().Byte(), jen.Error()).BlockFunc(func(g *jen.Group) {
		if si.Validatable {
			g.If(jen.Err().Op(":=").Id("d").Dot("data").Dot("Validate").Call(), jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("validation failed: %w"), jen.Err())),
			)
		}
		for _, s := range compRenderEncodeBody(ectx, nodes, jen.Id("d").Dot("cstDoc").Dot("Root").Call()) {
			g.Add(s)
		}
		g.Return(jen.Id("d").Dot("cstDoc").Dot("Bytes").Call(), jen.Nil())
	})
}

func compEmitDecodeInto(f *jen.File, si StructInfo) {
	ctx := freeJenCtx()
	nodes := foldCompDecode(&si, compPos{tkey: PrefixedKey(""), tgt: LocalTarget("data"), seq: new(int)}, false)
	// Delegated decode (ADR 2026-06-07): the caller passes the already-normalized
	// sub-Value for this struct's field, so DecodeInto just folds the type algebra
	// over it — no doc/container/consumed/prefix threading, no scope resolution.
	// Marking seen on sub flows back to the parent's model for Undecoded.
	f.Func().Id("Decode"+si.Name+"Into").Params(
		jen.Id("data").Op("*").Id(si.Name),
		jen.Id("sub").Op("*").Qual(cstPkg, "Value"),
	).Error().BlockFunc(func(g *jen.Group) {
		compModelBody(ctx, g, nodes, jen.Id("sub"), "")
		// A delegated struct validates itself just like a top-level DecodeX does
		// (after all fields are set), so an invalid nested cross-package config is
		// rejected rather than silently accepted.
		if si.Validatable {
			g.If(jen.Err().Op(":=").Id("data").Dot("Validate").Call(), jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("validation failed: %w"), jen.Err())),
			)
		}
		g.Return(jen.Nil())
	})
}

func compEmitEncodeFrom(f *jen.File, si StructInfo) {
	ectx := freeEncCtx()
	// EncodeFrom writes relative to the passed container node (a delegated
	// struct never owns the document root), so its array-tables must be scoped
	// to the container — matching DecodeXInto's scoped decode. Without this a
	// nested array-table encodes document-root-relative ([[f1.f0]]) while decode
	// looks for it under the container ([[parent.f1.f0]]), losing it (#105).
	nodes := foldCompEncode(&si, compPos{tkey: StaticKey(""), tgt: LocalTarget("data"), scoped: true}, false)
	f.Func().Id("Encode"+si.Name+"From").Params(
		jen.Id("data").Op("*").Id(si.Name),
		jen.Id("doc").Op("*").Qual(docPkg, "Document"),
		jen.Id("container").Op("*").Qual(cstPkg, "Node"),
	).Error().BlockFunc(func(g *jen.Group) {
		// Validate before writing, mirroring top-level Encode — a delegated struct
		// must not serialize an invalid value just because it was reached via a
		// parent's EncodeFrom.
		if si.Validatable {
			g.If(jen.Err().Op(":=").Id("data").Dot("Validate").Call(), jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("validation failed: %w"), jen.Err())),
			)
		}
		for _, s := range compRenderEncodeBody(ectx, nodes, jen.Id("container")) {
			g.Add(s)
		}
		g.Return(jen.Nil())
	})
}
