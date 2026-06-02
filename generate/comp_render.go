package generate

import (
	"io"
	"strings"

	jen "github.com/dave/jennifer/jen"
)

// Compositional renderer (#84). RenderFile walks the cd*/ce* node trees
// (comp_build.go) and emits the generated *_tommy.go. It uses the decode/encode
// contexts (jenCtx/encCtx) and the shared jennifer helpers (tableMatch,
// posHeader, jenType, jenZeroLit, delegateParts, jenSetCall, cst/doc package
// consts) from comp_support.go. The four behaviors the proof-of-concept spike
// deferred — consumed/undecoded tracking, same-package []struct handle tracking,
// positional nesting (#10), and the flat-key fallback (#55) — are all handled
// here.

// --- Decode walk ---

func compRenderDecodeBody(ctx jenCtx, children []cdNode, cv *jen.Statement, fv string) []jen.Code {
	var leaves, conts []cdNode
	for _, c := range children {
		if _, ok := c.(cdLeaf); ok {
			leaves = append(leaves, c)
		} else {
			conts = append(conts, c)
		}
	}
	var out []jen.Code
	if len(leaves) > 0 {
		out = append(out, compLeafScan(ctx, leaves, cv, fv))
	}
	for _, c := range conts {
		out = append(out, compContNode(ctx, c, cv, fv)...)
	}
	return out
}

func compLeafScan(ctx jenCtx, leaves []cdNode, cv *jen.Statement, fv string) jen.Code {
	var cases []jen.Code
	for _, l := range leaves {
		cases = append(cases, compLeafCase(ctx, l.(cdLeaf), fv)...)
	}
	// _seen tracks known keys seen in THIS table scan, for duplicate-key
	// rejection (#90). It is block-local so each array-table/map entry's scan
	// starts fresh — a key repeating across entries is not a duplicate.
	return jen.BlockFunc(func(g *jen.Group) {
		g.Id("_seen").Op(":=").Map(jen.String()).Bool().Values()
		g.For(jen.List(jen.Id("_"), jen.Id("_kv")).Op(":=").Range().Add(cv.Clone()).Dot("Children")).Block(
			jen.If(jen.Id("_kv").Dot("Kind").Op("!=").Qual(cstPkg, "NodeKeyValue")).Block(jen.Continue()),
			jen.Switch(jen.Qual(cstPkg, "KeyValueName").Call(jen.Id("_kv"))).Block(cases...),
		)
	})
}

func compLeafCase(ctx jenCtx, l cdLeaf, fv string) []jen.Code {
	bk := l.TKey.BareKey()
	// Every leaf case is guarded against a repeated known key (#90): dupGuard
	// checks/sets the per-scan _seen set before the extract, so a second
	// occurrence within the same table errors instead of silently overwriting.
	wrap := func(inner jen.Code) []jen.Code {
		return []jen.Code{jen.Case(jen.Lit(bk)).Block(append(ctx.dupGuard(bk), inner)...)}
	}
	switch l.Kind {
	case cdLeafPrim:
		ei := cstExtract(l.TypeName)
		var body []jen.Code
		if l.Pointer {
			if ei.cast != "" {
				body = append(body, jen.Id("_cv").Op(":=").Id(ei.cast).Call(jen.Id("v")), l.Tgt.Jen().Clone().Op("=").Op("&").Id("_cv"))
			} else {
				body = append(body, l.Tgt.Jen().Clone().Op("=").Op("&").Id("v"))
			}
		} else if l.ElemType != "" {
			et := jenType(l.ElemType, l.ImportPath)
			if ei.cast != "" {
				body = append(body, l.Tgt.Jen().Clone().Op("=").Add(et).Call(jen.Id(ei.cast).Call(jen.Id("v"))))
			} else {
				body = append(body, l.Tgt.Jen().Clone().Op("=").Add(et).Call(jen.Id("v")))
			}
		} else if ei.cast != "" {
			body = append(body, l.Tgt.Jen().Clone().Op("=").Id(ei.cast).Call(jen.Id("v")))
		} else {
			body = append(body, l.Tgt.Jen().Clone().Op("=").Id("v"))
		}
		if fv != "" {
			body = append(body, jen.Id(fv).Op("=").True())
		}
		body = append(body, ctx.mc(l.TKey))
		return wrap(jen.If(jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Qual(cstPkg, ei.fn).Call(jen.Id("_kv")), jen.Id("ok")).Block(body...))
	case cdLeafCustom:
		var body []jen.Code
		body = append(body, jen.If(jen.Err().Op(":=").Add(l.Tgt.Jen().Clone()).Dot("UnmarshalTOML").Call(jen.Id("raw")), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+": %w", jen.Err())))
		if fv != "" {
			body = append(body, jen.Id(fv).Op("=").True())
		}
		body = append(body, ctx.mc(l.TKey))
		return wrap(jen.If(jen.List(jen.Id("raw"), jen.Id("ok")).Op(":=").Qual(cstPkg, "ExtractRaw").Call(jen.Id("_kv")), jen.Id("ok")).Block(body...))
	case cdLeafText:
		var body []jen.Code
		body = append(body, jen.If(jen.Err().Op(":=").Add(l.Tgt.Jen().Clone()).Dot("UnmarshalText").Call(jen.Index().Byte().Call(jen.Id("v"))), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+": %w", jen.Err())))
		if fv != "" {
			body = append(body, jen.Id(fv).Op("=").True())
		}
		body = append(body, ctx.mc(l.TKey))
		return wrap(jen.If(jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Qual(cstPkg, "ExtractString").Call(jen.Id("_kv")), jen.Id("ok")).Block(body...))
	case cdLeafSlicePrim:
		var body []jen.Code
		if l.SlicePointer {
			body = append(body,
				l.Tgt.Jen().Clone().Op("=").Make(jen.Index().Op("*").Id(l.ElemType), jen.Len(jen.Id("v"))),
				jen.For(jen.Id("_si").Op(":=").Range().Id("v")).Block(l.Tgt.Jen().Clone().Index(jen.Id("_si")).Op("=").Op("&").Id("v").Index(jen.Id("_si"))),
			)
		} else if l.TypeName != "" {
			body = append(body, l.Tgt.Jen().Clone().Op("=").Add(jenType(l.TypeName, l.ImportPath)).Call(jen.Id("v")))
			// Faithful nil/empty (#21): the extractor returns a nil slice for a
			// present-but-empty `key = []`; keep it non-nil so it round-trips as
			// empty rather than collapsing to an absent-key nil.
			body = append(body, jen.If(l.Tgt.Jen().Clone().Op("==").Nil()).Block(
				l.Tgt.Jen().Clone().Op("=").Add(jenType(l.TypeName, l.ImportPath)).Values(),
			))
		} else {
			body = append(body, l.Tgt.Jen().Clone().Op("=").Id("v"))
			body = append(body, jen.If(l.Tgt.Jen().Clone().Op("==").Nil()).Block(
				l.Tgt.Jen().Clone().Op("=").Index().Id(l.ElemType).Values(),
			))
		}
		body = append(body, ctx.mc(l.TKey))
		return wrap(jen.If(jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Qual(cstPkg, cstSliceExtractFunc(l.ElemType)).Call(jen.Id("_kv")), jen.Id("ok")).Block(body...))
	case cdLeafSliceText:
		return wrap(jen.If(jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Qual(cstPkg, "ExtractStringSlice").Call(jen.Id("_kv")), jen.Id("ok")).Block(
			l.Tgt.Jen().Clone().Op("=").Make(jen.Index().Add(jenType(l.TypeName, l.ImportPath)), jen.Len(jen.Id("v"))),
			jen.For(jen.List(jen.Id("_si"), jen.Id("_s")).Op(":=").Range().Id("v")).Block(
				jen.If(jen.Err().Op(":=").Add(l.Tgt.Jen().Clone()).Index(jen.Id("_si")).Dot("UnmarshalText").Call(jen.Index().Byte().Call(jen.Id("_s"))), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+"[%d]: %w", jen.Id("_si"), jen.Err())),
			),
			ctx.mc(l.TKey),
		))
	}
	return nil
}

func compContNode(ctx jenCtx, c cdNode, cv *jen.Statement, fv string) []jen.Code {
	switch n := c.(type) {
	case cdMapScalar:
		return compMapScalar(ctx, n, fv)
	case cdMapMap:
		return compMapMap(ctx, n)
	case cdInTable:
		return compInTable(ctx, n)
	case cdNilGuard:
		return compNilGuard(ctx, n, cv)
	case cdArrayTable:
		return compArrayTable(ctx, n, fv)
	case cdMapStruct:
		return compMapStruct(ctx, n)
	case cdDelStruct:
		return compDelStruct(ctx, n)
	case cdDelSlice:
		return compDelSlice(ctx, n, fv)
	case cdDelMap:
		return compDelMap(ctx, n)
	}
	return nil
}

func compMapScalar(ctx jenCtx, n cdMapScalar, fv string) []jen.Code {
	return []jen.Code{jen.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(ctx.root())).Block(
		jen.If(tableMatch(n.TKey)).BlockFunc(func(g *jen.Group) {
			g.Add(n.Tgt.Jen().Clone()).Op("=").Qual(cstPkg, "ExtractStringMap").Call(jen.Id("_ch"))
			// Faithful nil/empty (#21): a present-but-empty [table] extracts to a
			// nil map; keep it non-nil so it round-trips as empty, not absent.
			g.If(n.Tgt.Jen().Clone().Op("==").Nil()).Block(n.Tgt.Jen().Clone().Op("=").Map(jen.String()).String().Values())
			if fv != "" {
				g.Id(fv).Op("=").True()
			}
			g.Add(ctx.mc(n.TKey))
			g.For(jen.Id("_ik").Op(":=").Range().Add(n.Tgt.Jen().Clone())).Block(ctx.mcExpr(n.TKey.Jen().Op("+").Lit(".").Op("+").Id("_ik")))
			g.Break()
		}),
	)}
}

func compMapMap(ctx jenCtx, n cdMapMap) []jen.Code {
	pf := n.TKey.Lit(".")
	return []jen.Code{jen.BlockFunc(func(g *jen.Group) {
		if n.TypeName != "" {
			g.Var().Id("_mr").Map(jen.String()).Add(jenType(n.TypeName, n.ImportPath))
		} else {
			g.Var().Id("_mr").Map(jen.String()).Map(jen.String()).String()
		}
		g.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(ctx.root())).BlockFunc(func(g *jen.Group) {
			g.If(jen.Id("_ch").Dot("Kind").Op("!=").Qual(cstPkg, "NodeTable")).Block(jen.Continue())
			g.Id("_hdr").Op(":=").Qual(cstPkg, "TableHeaderKey").Call(jen.Id("_ch"))
			g.If(jen.Op("!").Qual("strings", "HasPrefix").Call(jen.Id("_hdr"), pf.Jen())).Block(jen.Continue())
			g.Id("_mk").Op(":=").Id("_hdr").Index(pf.JenLen().Op(":"))
			g.If(jen.Id("_mr").Op("==").Nil()).BlockFunc(func(g *jen.Group) {
				g.Add(ctx.mc(n.TKey))
				if n.TypeName != "" {
					g.Id("_mr").Op("=").Make(jen.Map(jen.String()).Add(jenType(n.TypeName, n.ImportPath)))
				} else {
					g.Id("_mr").Op("=").Make(jen.Map(jen.String()).Map(jen.String()).String())
				}
			})
			g.Add(ctx.mcExpr(n.TKey.Jen().Op("+").Lit(".").Op("+").Id("_mk")))
			g.Id("_inner").Op(":=").Qual(cstPkg, "ExtractStringMap").Call(jen.Id("_ch"))
			g.For(jen.Id("_ik").Op(":=").Range().Id("_inner")).Block(ctx.mcExpr(n.TKey.Jen().Op("+").Lit(".").Op("+").Id("_mk").Op("+").Lit(".").Op("+").Id("_ik")))
			if n.TypeName != "" {
				g.Id("_mr").Index(jen.Id("_mk")).Op("=").Add(jenType(n.TypeName, n.ImportPath)).Call(jen.Id("_inner"))
			} else {
				g.Id("_mr").Index(jen.Id("_mk")).Op("=").Id("_inner")
			}
		})
		g.If(jen.Id("_mr").Op("!=").Nil()).Block(n.Tgt.Jen().Clone().Op("=").Id("_mr"))
	})}
}

// compDupTableGuard is the body of the root-scan match for a struct field's
// table. In the top-level receiver Decode (where the document root is the actual
// table scope) it records the first match and errors on a second occurrence — a
// TOML duplicate-table violation (#92, "Defining a table more than once is
// invalid"); tableMatch is exact-header, so a super-table completed by a
// sub-table ([a] alongside [a.b]) is not a false positive. In a delegated
// DecodeXInto (topLevel=false) the same root-scan cannot yet distinguish
// array-table entries that share a dotted key, so it keeps the historical
// first-match-wins behavior (the cross-entry mis-scoping is tracked separately).
func compDupTableGuard(ctx jenCtx, ftv, bareKey string) []jen.Code {
	if !ctx.topLevel {
		return []jen.Code{jen.Id(ftv).Op("=").Id("_ch"), jen.Break()}
	}
	return []jen.Code{
		jen.If(jen.Id(ftv).Op("!=").Nil()).Block(ctx.retErr("duplicate table %q", jen.Lit(bareKey))),
		jen.Id(ftv).Op("=").Id("_ch"),
	}
}

func compInTable(ctx jenCtx, n cdInTable) []jen.Code {
	ftv := "_ft" + n.TKey.VarSuffix()
	return []jen.Code{jen.BlockFunc(func(g *jen.Group) {
		g.Var().Id(ftv).Op("*").Qual(cstPkg, "Node")
		g.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(ctx.root())).Block(
			jen.If(tableMatch(n.TKey)).Block(compDupTableGuard(ctx, ftv, n.TKey.BareKey())...),
		)
		g.If(jen.Id(ftv).Op("!=").Nil()).BlockFunc(func(ib *jen.Group) {
			ib.Add(ctx.mc(n.TKey))
			for _, s := range compRenderDecodeBody(ctx, n.Children, jen.Id(ftv), "") {
				ib.Add(s)
			}
		}).Else().BlockFunc(func(eb *jen.Group) {
			// Header absent (#89): decode document-root-relative children (array
			// tables / delegated slices) against the root node anyway.
			for _, s := range compRenderDecodeBody(ctx, n.FlatChildren, ctx.docVar.Clone().Dot("Root").Call(), "") {
				eb.Add(s)
			}
		})
	})}
}

func compNilGuard(ctx jenCtx, n cdNilGuard, cv *jen.Statement) []jen.Code {
	lv := n.LocalVar
	ftv := "_ft" + n.TKey.VarSuffix()
	return []jen.Code{jen.BlockFunc(func(g *jen.Group) {
		g.Var().Id(ftv).Op("*").Qual(cstPkg, "Node")
		g.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(ctx.root())).Block(
			jen.If(tableMatch(n.TKey)).Block(compDupTableGuard(ctx, ftv, n.TKey.BareKey())...),
		)
		g.If(jen.Id(ftv).Op("!=").Nil()).BlockFunc(func(g *jen.Group) {
			g.Add(ctx.mc(n.TKey))
			g.Id(lv).Op(":=").Op("&").Id(n.TypeName).Values()
			for _, s := range compRenderDecodeBody(ctx, n.Children, jen.Id(ftv), "") {
				g.Add(s)
			}
			g.Add(n.Tgt.Jen().Clone()).Op("=").Id(lv)
		}).Else().BlockFunc(func(g *jen.Group) {
			g.Id(lv).Op(":=").Op("&").Id(n.TypeName).Values()
			g.Id("_found").Op(":=").False()
			for _, s := range compRenderDecodeBody(ctx, n.FlatChildren, cv.Clone(), "_found") {
				g.Add(s)
			}
			g.If(jen.Id("_found")).Block(n.Tgt.Jen().Clone().Op("=").Id(lv))
		})
	})}
}

func compArrayTable(ctx jenCtx, n cdArrayTable, fv string) []jen.Code {
	nv := "_nodes" + n.TDottedKey.VarSuffix()
	var collect jen.Code
	if n.TDottedKey.IsStatic() {
		collect = jen.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(ctx.root())).Block(
			jen.If(jen.Id("_ch").Dot("Kind").Op("==").Qual(cstPkg, "NodeArrayTable").Op("&&").Qual(cstPkg, "TableHeaderKey").Call(jen.Id("_ch")).Op("==").Add(n.TDottedKey.Jen())).Block(
				jen.Id(nv).Op("=").Append(jen.Id(nv), jen.Id("_ch")),
			),
		)
	} else {
		collect = jen.Id(nv).Op("=").Add(ctx.docVar.Clone()).Dot("FindArrayTableNodes").Call(n.TDottedKey.Jen())
	}
	var stmts []jen.Code
	stmts = append(stmts, jen.Var().Id(nv).Index().Op("*").Qual(cstPkg, "Node"), collect)
	if fv != "" {
		stmts = append(stmts, jen.If(jen.Len(jen.Id(nv)).Op(">").Lit(0)).Block(jen.Id(fv).Op("=").True()))
	}
	if n.TrackHandles {
		hn := toLowerFirst(n.TypeName) + "Handle"
		fn := toLowerFirst(n.Tgt.Segs[len(n.Tgt.Segs)-1].Name)
		stmts = append(stmts, jen.Id("d").Dot(fn).Op("=").Make(jen.Index().Id(hn), jen.Len(jen.Id(nv))))
	}
	jt := jenType(n.TypeName, n.ImportPath)
	if n.SlicePtr {
		stmts = append(stmts, n.Tgt.Jen().Clone().Op("=").Make(jen.Index().Op("*").Add(jt.Clone()), jen.Len(jen.Id(nv))))
	} else {
		stmts = append(stmts, n.Tgt.Jen().Clone().Op("=").Make(jen.Index().Add(jt.Clone()), jen.Len(jen.Id(nv))))
	}
	stmts = append(stmts, ctx.mc(n.TDottedKey))
	stmts = append(stmts, jen.For(jen.List(jen.Id(n.IdxVar), jen.Id(n.EntryVar)).Op(":=").Range().Id(nv)).BlockFunc(func(g *jen.Group) {
		if n.TrackHandles {
			hn := toLowerFirst(n.TypeName) + "Handle"
			fn := toLowerFirst(n.Tgt.Segs[len(n.Tgt.Segs)-1].Name)
			g.Id("d").Dot(fn).Index(jen.Id(n.IdxVar)).Op("=").Id(hn).Values(jen.Dict{jen.Id("node"): jen.Id(n.EntryVar)})
		}
		if n.SlicePtr {
			g.Add(n.Tgt.Jen().Clone()).Index(jen.Id(n.IdxVar)).Op("=").Op("&").Add(jt.Clone()).Values()
		}
		compScopedBody(ctx, g, n.Children, jen.Id(n.EntryVar), "")
	}))
	return stmts
}

// compScopedBody renders decode for children scoped to an array-table entry (or
// any nested table) node: leaves scan the node, container children are matched
// within the node's child scope via cst.FindChild*, recursing so nesting is
// correct by construction at any depth (#86/#87). This replaces the former
// header-counting positional family.
func compScopedBody(ctx jenCtx, g *jen.Group, children []cdNode, scope *jen.Statement, fv string) {
	var leaves, conts []cdNode
	for _, c := range children {
		if _, ok := c.(cdLeaf); ok {
			leaves = append(leaves, c)
		} else {
			conts = append(conts, c)
		}
	}
	if len(leaves) > 0 {
		g.Add(compLeafScan(ctx, leaves, scope, fv))
	}
	for _, c := range conts {
		compScopedContainer(ctx, g, c, scope, fv)
	}
}

func compScopedContainer(ctx jenCtx, g *jen.Group, c cdNode, scope *jen.Statement, fv string) {
	rootNode := func() *jen.Statement { return ctx.docVar.Clone().Dot("Root").Call() }
	switch n := c.(type) {
	case cdInTable:
		v := "_ct" + n.TKey.VarSuffix()
		g.BlockFunc(func(b *jen.Group) {
			b.Id(v).Op(":=").Qual(cstPkg, "FindChildTable").Call(rootNode(), scope.Clone(), jen.Lit(n.TKey.BareKey()))
			b.If(jen.Id(v).Op("!=").Nil()).BlockFunc(func(ib *jen.Group) {
				ib.Add(ctx.mc(n.TKey))
				compScopedBody(ctx, ib, n.Children, jen.Id(v), "")
			}).Else().BlockFunc(func(eb *jen.Group) {
				// Header absent (#89): decode scope-relative array-table/delegated
				// children within the parent scope anyway.
				compScopedBody(ctx, eb, n.FlatChildren, scope.Clone(), "")
			})
		})

	case cdNilGuard:
		lv := n.LocalVar
		v := "_ct" + n.TKey.VarSuffix()
		g.BlockFunc(func(b *jen.Group) {
			b.Id(v).Op(":=").Qual(cstPkg, "FindChildTable").Call(rootNode(), scope.Clone(), jen.Lit(n.TKey.BareKey()))
			b.If(jen.Id(v).Op("!=").Nil()).BlockFunc(func(ib *jen.Group) {
				ib.Add(ctx.mc(n.TKey))
				ib.Id(lv).Op(":=").Op("&").Id(n.TypeName).Values()
				compScopedBody(ctx, ib, n.Children, jen.Id(v), "")
				ib.Add(n.Tgt.Jen().Clone()).Op("=").Id(lv)
			}).Else().BlockFunc(func(eb *jen.Group) {
				eb.Id(lv).Op(":=").Op("&").Id(n.TypeName).Values()
				eb.Id("_found").Op(":=").False()
				compScopedBody(ctx, eb, n.FlatChildren, scope.Clone(), "_found")
				eb.If(jen.Id("_found")).Block(n.Tgt.Jen().Clone().Op("=").Id(lv))
			})
		})

	case cdArrayTable:
		nv := "_nodes" + n.TDottedKey.VarSuffix()
		jt := jenType(n.TypeName, n.ImportPath)
		g.BlockFunc(func(b *jen.Group) {
			b.Id(nv).Op(":=").Qual(cstPkg, "FindChildArrayTableNodes").Call(rootNode(), scope.Clone(), jen.Lit(n.TKey.BareKey()))
			if fv != "" {
				b.If(jen.Len(jen.Id(nv)).Op(">").Lit(0)).Block(jen.Id(fv).Op("=").True())
			}
			if n.SlicePtr {
				b.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Index().Op("*").Add(jt.Clone()), jen.Len(jen.Id(nv)))
			} else {
				b.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Index().Add(jt.Clone()), jen.Len(jen.Id(nv)))
			}
			b.Add(ctx.mc(n.TDottedKey))
			b.For(jen.List(jen.Id(n.IdxVar), jen.Id(n.EntryVar)).Op(":=").Range().Id(nv)).BlockFunc(func(lb *jen.Group) {
				if n.SlicePtr {
					lb.Add(n.Tgt.Jen().Clone()).Index(jen.Id(n.IdxVar)).Op("=").Op("&").Add(jt.Clone()).Values()
				}
				compScopedBody(ctx, lb, n.Children, jen.Id(n.EntryVar), "")
			})
		})

	case cdMapScalar:
		v := "_ct" + n.TKey.VarSuffix()
		g.BlockFunc(func(b *jen.Group) {
			b.Id(v).Op(":=").Qual(cstPkg, "FindChildTable").Call(rootNode(), scope.Clone(), jen.Lit(n.TKey.BareKey()))
			b.If(jen.Id(v).Op("!=").Nil()).BlockFunc(func(ib *jen.Group) {
				ib.Add(n.Tgt.Jen().Clone()).Op("=").Qual(cstPkg, "ExtractStringMap").Call(jen.Id(v))
				// Faithful nil/empty (#21): present-but-empty [table] -> non-nil.
				ib.If(n.Tgt.Jen().Clone().Op("==").Nil()).Block(n.Tgt.Jen().Clone().Op("=").Map(jen.String()).String().Values())
				if fv != "" {
					ib.Id(fv).Op("=").True()
				}
				ib.Add(ctx.mc(n.TKey))
				ib.For(jen.Id("_ik").Op(":=").Range().Add(n.Tgt.Jen().Clone())).Block(ctx.mcExpr(n.TKey.Jen().Op("+").Lit(".").Op("+").Id("_ik")))
			})
		})

	case cdMapStruct:
		compScopedMapStruct(ctx, g, n, scope)

	case cdDelStruct:
		compScopedDelStruct(ctx, g, n, scope)

	case cdDelSlice:
		compScopedDelSlice(ctx, g, n, scope, fv)

	case cdDelMap:
		compScopedDelMap(ctx, g, n, scope)

	case cdMapMap:
		compScopedMapMap(ctx, g, n, scope)
	}
}

// compScopedMapMap decodes a map[string]NamedMap nested inside an array-table or
// map entry: its sub-tables are scoped to the entry node (FindChildSubTables),
// mirroring compScopedMapStruct but extracting a string map per entry rather than
// decoding a struct. See #86/#87 (scoping) applied to the mapmap kind.
func compScopedMapMap(ctx jenCtx, g *jen.Group, n cdMapMap, scope *jen.Statement) {
	field := n.TKey.BareKey()
	mapType := func() *jen.Statement {
		if n.TypeName != "" {
			return jen.Map(jen.String()).Add(jenType(n.TypeName, n.ImportPath))
		}
		return jen.Map(jen.String()).Map(jen.String()).String()
	}
	g.BlockFunc(func(b *jen.Group) {
		pv := scopedSubTablePrefix(b, scope, n.TKey, field)
		b.Var().Id("_mr").Add(mapType())
		b.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Qual(cstPkg, "FindChildSubTables").Call(ctx.docVar.Clone().Dot("Root").Call(), scope.Clone(), jen.Lit(field))).BlockFunc(func(lb *jen.Group) {
			lb.Id("_mk").Op(":=").Qual("strings", "TrimPrefix").Call(jen.Qual(cstPkg, "TableHeaderKey").Call(jen.Id("_ch")), jen.Id(pv))
			lb.If(jen.Id("_mr").Op("==").Nil()).BlockFunc(func(ib *jen.Group) {
				ib.Add(ctx.mc(n.TKey))
				ib.Id("_mr").Op("=").Make(mapType())
			})
			lb.Add(ctx.mcExpr(n.TKey.Jen().Op("+").Lit(".").Op("+").Id("_mk")))
			lb.Id("_inner").Op(":=").Qual(cstPkg, "ExtractStringMap").Call(jen.Id("_ch"))
			lb.For(jen.Id("_ik").Op(":=").Range().Id("_inner")).Block(ctx.mcExpr(n.TKey.Jen().Op("+").Lit(".").Op("+").Id("_mk").Op("+").Lit(".").Op("+").Id("_ik")))
			if n.TypeName != "" {
				lb.Id("_mr").Index(jen.Id("_mk")).Op("=").Add(jenType(n.TypeName, n.ImportPath)).Call(jen.Id("_inner"))
			} else {
				lb.Id("_mr").Index(jen.Id("_mk")).Op("=").Id("_inner")
			}
		})
		b.If(jen.Id("_mr").Op("!=").Nil()).Block(n.Tgt.Jen().Clone().Op("=").Id("_mr"))
	})
}

// scopedSubTablePrefix declares a uniquely-named local holding
// "<scope header>.<field>." — the dotted prefix shared by a scoped map's
// sub-tables — captured BEFORE the iteration loop. This matters when a scoped
// map nests inside another: the loop's `_ch` shadows an enclosing scope that is
// also bound to `_ch`, so re-evaluating TableHeaderKey(scope) inside the loop
// would read the wrong node. Returns the local's name. See #86/#87.
func scopedSubTablePrefix(b *jen.Group, scope *jen.Statement, key TOMLKey, field string) string {
	v := "_pfx" + key.VarSuffix()
	b.Id(v).Op(":=").Qual(cstPkg, "TableHeaderKey").Call(scope.Clone()).Op("+").Lit("." + field + ".")
	return v
}

func compScopedMapStruct(ctx jenCtx, g *jen.Group, n cdMapStruct, scope *jen.Statement) {
	field := n.TKey.BareKey()
	g.BlockFunc(func(b *jen.Group) {
		pv := scopedSubTablePrefix(b, scope, n.TKey, field)
		if n.SlicePtr {
			b.Var().Id("_mr").Map(jen.String()).Op("*").Id(n.TypeName)
		} else {
			b.Var().Id("_mr").Map(jen.String()).Id(n.TypeName)
		}
		b.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Qual(cstPkg, "FindChildSubTables").Call(ctx.docVar.Clone().Dot("Root").Call(), scope.Clone(), jen.Lit(field))).BlockFunc(func(lb *jen.Group) {
			lb.Id(n.MapVar).Op(":=").Qual("strings", "TrimPrefix").Call(jen.Qual(cstPkg, "TableHeaderKey").Call(jen.Id("_ch")), jen.Id(pv))
			lb.If(jen.Id("_mr").Op("==").Nil()).BlockFunc(func(ib *jen.Group) {
				ib.Add(ctx.mc(n.TKey))
				if n.SlicePtr {
					ib.Id("_mr").Op("=").Make(jen.Map(jen.String()).Op("*").Id(n.TypeName))
				} else {
					ib.Id("_mr").Op("=").Make(jen.Map(jen.String()).Id(n.TypeName))
				}
			})
			lb.Add(ctx.mcExpr(n.TKey.Jen().Op("+").Lit(".").Op("+").Id(n.MapVar)))
			lb.Var().Id("entry").Id(n.TypeName)
			compScopedBody(ctx, lb, n.Children, jen.Id("_ch"), "")
			if n.SlicePtr {
				lb.Id("_mr").Index(jen.Id(n.MapVar)).Op("=").Op("&").Id("entry")
			} else {
				lb.Id("_mr").Index(jen.Id(n.MapVar)).Op("=").Id("entry")
			}
		})
		b.If(jen.Id("_mr").Op("!=").Nil()).Block(n.Tgt.Jen().Clone().Op("=").Id("_mr"))
	})
}

func compScopedDelStruct(ctx jenCtx, g *jen.Group, n cdDelStruct, scope *jen.Statement) {
	_, st := delegateParts(n.TypeName)
	bk := n.TKey.BareKey()
	pk := n.TKey.Lit(".")
	decFn := "Decode" + st + "Into"
	v := "_ct" + n.TKey.VarSuffix()
	g.BlockFunc(func(b *jen.Group) {
		b.Id(v).Op(":=").Qual(cstPkg, "FindChildTable").Call(ctx.docVar.Clone().Dot("Root").Call(), scope.Clone(), jen.Lit(bk))
		b.If(jen.Id(v).Op("!=").Nil()).BlockFunc(func(ib *jen.Group) {
			ib.Add(ctx.mc(n.TKey))
			if n.Ptr {
				lv := toLowerFirst(st) + "Val"
				ib.Id(lv).Op(":=").Op("&").Qual(n.ImportPath, st).Values()
				ib.If(jen.Err().Op(":=").Qual(n.ImportPath, decFn).Call(jen.Id(lv), ctx.docVar.Clone(), jen.Id(v), ctx.consumed.Clone(), pk.Jen()), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+": %w", jen.Err()))
				ib.Add(n.Tgt.Jen().Clone()).Op("=").Id(lv)
			} else {
				ib.If(jen.Err().Op(":=").Qual(n.ImportPath, decFn).Call(jen.Op("&").Add(n.Tgt.Jen().Clone()), ctx.docVar.Clone(), jen.Id(v), ctx.consumed.Clone(), pk.Jen()), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+": %w", jen.Err()))
			}
		})
	})
}

func compScopedDelSlice(ctx jenCtx, g *jen.Group, n cdDelSlice, scope *jen.Statement, fv string) {
	_, st := delegateParts(n.TypeName)
	bk := n.TKey.BareKey()
	pk := n.TDottedKey.Lit(".")
	decFn := "Decode" + st + "Into"
	nv := "_nodes" + n.TDottedKey.VarSuffix()
	g.BlockFunc(func(b *jen.Group) {
		b.Id(nv).Op(":=").Qual(cstPkg, "FindChildArrayTableNodes").Call(ctx.docVar.Clone().Dot("Root").Call(), scope.Clone(), jen.Lit(bk))
		if fv != "" {
			b.If(jen.Len(jen.Id(nv)).Op(">").Lit(0)).Block(jen.Id(fv).Op("=").True())
		}
		if n.SlicePtr {
			b.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Index().Op("*").Qual(n.ImportPath, st), jen.Len(jen.Id(nv)))
		} else {
			b.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Index().Qual(n.ImportPath, st), jen.Len(jen.Id(nv)))
		}
		b.Add(ctx.mc(n.TDottedKey))
		b.For(jen.List(jen.Id(n.IdxVar), jen.Id("_dn")).Op(":=").Range().Id(nv)).BlockFunc(func(lb *jen.Group) {
			if n.SlicePtr {
				lb.Add(n.Tgt.Jen().Clone()).Index(jen.Id(n.IdxVar)).Op("=").Op("&").Qual(n.ImportPath, st).Values()
				lb.If(jen.Err().Op(":=").Qual(n.ImportPath, decFn).Call(n.Tgt.Jen().Clone().Index(jen.Id(n.IdxVar)), ctx.docVar.Clone(), jen.Id("_dn"), ctx.consumed.Clone(), pk.Jen()), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+"[%d]: %w", jen.Id(n.IdxVar), jen.Err()))
			} else {
				lb.If(jen.Err().Op(":=").Qual(n.ImportPath, decFn).Call(jen.Op("&").Add(n.Tgt.Jen().Clone()).Index(jen.Id(n.IdxVar)), ctx.docVar.Clone(), jen.Id("_dn"), ctx.consumed.Clone(), pk.Jen()), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+"[%d]: %w", jen.Id(n.IdxVar), jen.Err()))
			}
		})
	})
}

func compScopedDelMap(ctx jenCtx, g *jen.Group, n cdDelMap, scope *jen.Statement) {
	_, st := delegateParts(n.ElemType)
	field := n.TKey.BareKey()
	bk := n.TKey.BareKey()
	decFn := "Decode" + st + "Into"
	pv := scopedSubTablePrefix(g, scope, n.TKey, field)
	g.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Qual(cstPkg, "FindChildSubTables").Call(ctx.docVar.Clone().Dot("Root").Call(), scope.Clone(), jen.Lit(field))).BlockFunc(func(lb *jen.Group) {
		lb.Id("_mk").Op(":=").Qual("strings", "TrimPrefix").Call(jen.Qual(cstPkg, "TableHeaderKey").Call(jen.Id("_ch")), jen.Id(pv))
		lb.If(n.Tgt.Jen().Clone().Op("==").Nil()).BlockFunc(func(ib *jen.Group) {
			ib.Add(ctx.mc(n.TKey))
			ib.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Map(jen.String()).Qual(n.ImportPath, st))
		})
		lb.Add(ctx.mcExpr(n.TKey.Jen().Op("+").Lit(".").Op("+").Id("_mk")))
		lb.Var().Id("entry").Qual(n.ImportPath, st)
		dke := n.TKey.Lit(".").Var("_mk").Lit(".")
		lb.If(jen.Err().Op(":=").Qual(n.ImportPath, decFn).Call(jen.Op("&").Id("entry"), ctx.docVar.Clone(), jen.Id("_ch"), ctx.consumed.Clone(), dke.Jen()), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+".%s: %w", jen.Id("_mk"), jen.Err()))
		lb.Add(n.Tgt.Jen().Clone()).Index(jen.Id("_mk")).Op("=").Id("entry")
	})
}

func compMapStruct(ctx jenCtx, n cdMapStruct) []jen.Code {
	pf := n.TKey.Lit(".")
	return []jen.Code{jen.BlockFunc(func(g *jen.Group) {
		if n.SlicePtr {
			g.Var().Id("_mr").Map(jen.String()).Op("*").Id(n.TypeName)
		} else {
			g.Var().Id("_mr").Map(jen.String()).Id(n.TypeName)
		}
		g.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(ctx.root())).BlockFunc(func(g *jen.Group) {
			g.If(jen.Id("_ch").Dot("Kind").Op("!=").Qual(cstPkg, "NodeTable")).Block(jen.Continue())
			g.Id("_hdr").Op(":=").Qual(cstPkg, "TableHeaderKey").Call(jen.Id("_ch"))
			g.If(jen.Op("!").Qual("strings", "HasPrefix").Call(jen.Id("_hdr"), pf.Jen())).Block(jen.Continue())
			g.Id(n.MapVar).Op(":=").Id("_hdr").Index(pf.JenLen().Op(":"))
			g.If(jen.Qual("strings", "Contains").Call(jen.Id(n.MapVar), jen.Lit("."))).Block(jen.Continue())
			g.If(jen.Id("_mr").Op("==").Nil()).BlockFunc(func(g *jen.Group) {
				g.Add(ctx.mc(n.TKey))
				if n.SlicePtr {
					g.Id("_mr").Op("=").Make(jen.Map(jen.String()).Op("*").Id(n.TypeName))
				} else {
					g.Id("_mr").Op("=").Make(jen.Map(jen.String()).Id(n.TypeName))
				}
			})
			g.Add(ctx.mcExpr(n.TKey.Jen().Op("+").Lit(".").Op("+").Id(n.MapVar)))
			g.Var().Id("entry").Id(n.TypeName)
			for _, s := range compRenderDecodeBody(ctx, n.Children, jen.Id("_ch"), "") {
				g.Add(s)
			}
			if n.SlicePtr {
				g.Id("_mr").Index(jen.Id(n.MapVar)).Op("=").Op("&").Id("entry")
			} else {
				g.Id("_mr").Index(jen.Id(n.MapVar)).Op("=").Id("entry")
			}
		})
		g.If(jen.Id("_mr").Op("!=").Nil()).Block(n.Tgt.Jen().Clone().Op("=").Id("_mr"))
	})}
}

func compDelStruct(ctx jenCtx, n cdDelStruct) []jen.Code {
	_, st := delegateParts(n.TypeName)
	bk := n.TKey.BareKey()
	pk := n.TKey.Lit(".")
	decFn := "Decode" + st + "Into"

	if n.Ptr {
		lv := toLowerFirst(st) + "Val"
		tblv := "_tbl" + n.TKey.VarSuffix()
		return []jen.Code{jen.BlockFunc(func(g *jen.Group) {
			g.Var().Id(tblv).Op("*").Qual(cstPkg, "Node")
			g.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(ctx.root())).Block(
				jen.If(tableMatch(n.TKey)).Block(jen.Id(tblv).Op("=").Id("_ch"), jen.Break()),
			)
			g.If(jen.Id(tblv).Op("!=").Nil()).BlockFunc(func(g *jen.Group) {
				g.Add(ctx.mc(n.TKey))
				g.Id(lv).Op(":=").Op("&").Qual(n.ImportPath, st).Values()
				g.If(jen.Err().Op(":=").Qual(n.ImportPath, decFn).Call(
					jen.Id(lv), ctx.docVar.Clone(), jen.Id(tblv), ctx.consumed.Clone(), pk.Jen(),
				), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+": %w", jen.Err()))
				g.Add(n.Tgt.Jen().Clone()).Op("=").Id(lv)
			})
		})}
	}
	return []jen.Code{jen.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(ctx.root())).Block(
		jen.If(tableMatch(n.TKey)).BlockFunc(func(g *jen.Group) {
			g.Add(ctx.mc(n.TKey))
			g.If(jen.Err().Op(":=").Qual(n.ImportPath, decFn).Call(
				jen.Op("&").Add(n.Tgt.Jen().Clone()), ctx.docVar.Clone(), jen.Id("_ch"), ctx.consumed.Clone(), pk.Jen(),
			), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+": %w", jen.Err()))
			g.Break()
		}),
	)}
}

func compDelSlice(ctx jenCtx, n cdDelSlice, fv string) []jen.Code {
	_, st := delegateParts(n.TypeName)
	nv := "_nodes" + n.TDottedKey.VarSuffix()
	pk := n.TDottedKey.Lit(".")
	decFn := "Decode" + st + "Into"

	var stmts []jen.Code
	stmts = append(stmts, jen.Var().Id(nv).Index().Op("*").Qual(cstPkg, "Node"))
	if n.TDottedKey.IsStatic() {
		stmts = append(stmts, jen.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(ctx.root())).Block(
			jen.If(jen.Id("_ch").Dot("Kind").Op("==").Qual(cstPkg, "NodeArrayTable").Op("&&").Qual(cstPkg, "TableHeaderKey").Call(jen.Id("_ch")).Op("==").Add(n.TDottedKey.Jen())).Block(
				jen.Id(nv).Op("=").Append(jen.Id(nv), jen.Id("_ch")),
			),
		))
	} else {
		stmts = append(stmts, jen.Id(nv).Op("=").Add(ctx.docVar.Clone()).Dot("FindArrayTableNodes").Call(n.TDottedKey.Jen()))
	}
	if fv != "" {
		stmts = append(stmts, jen.If(jen.Len(jen.Id(nv)).Op(">").Lit(0)).Block(jen.Id(fv).Op("=").True()))
	}
	if n.SlicePtr {
		stmts = append(stmts, n.Tgt.Jen().Clone().Op("=").Make(jen.Index().Op("*").Qual(n.ImportPath, st), jen.Len(jen.Id(nv))))
	} else {
		stmts = append(stmts, n.Tgt.Jen().Clone().Op("=").Make(jen.Index().Qual(n.ImportPath, st), jen.Len(jen.Id(nv))))
	}
	stmts = append(stmts, ctx.mc(n.TDottedKey))
	errKey := n.TKey.BareKey()
	stmts = append(stmts, jen.For(jen.List(jen.Id(n.IdxVar), jen.Id("_node")).Op(":=").Range().Id(nv)).BlockFunc(func(g *jen.Group) {
		if n.SlicePtr {
			g.Add(n.Tgt.Jen().Clone()).Index(jen.Id(n.IdxVar)).Op("=").Op("&").Qual(n.ImportPath, st).Values()
			g.If(jen.Err().Op(":=").Qual(n.ImportPath, decFn).Call(
				n.Tgt.Jen().Clone().Index(jen.Id(n.IdxVar)), ctx.docVar.Clone(), jen.Id("_node"), ctx.consumed.Clone(), pk.Jen(),
			), jen.Err().Op("!=").Nil()).Block(ctx.retErr(errKey+"[%d]: %w", jen.Id(n.IdxVar), jen.Err()))
		} else {
			g.If(jen.Err().Op(":=").Qual(n.ImportPath, decFn).Call(
				jen.Op("&").Add(n.Tgt.Jen().Clone()).Index(jen.Id(n.IdxVar)), ctx.docVar.Clone(), jen.Id("_node"), ctx.consumed.Clone(), pk.Jen(),
			), jen.Err().Op("!=").Nil()).Block(ctx.retErr(errKey+"[%d]: %w", jen.Id(n.IdxVar), jen.Err()))
		}
	}))
	return stmts
}

func compDelMap(ctx jenCtx, n cdDelMap) []jen.Code {
	_, st := delegateParts(n.ElemType)
	bk := n.TKey.BareKey()
	pf := n.TKey.Lit(".")
	decFn := "Decode" + st + "Into"

	return []jen.Code{jen.BlockFunc(func(g *jen.Group) {
		g.For(jen.List(jen.Id("_"), jen.Id("_ch")).Op(":=").Range().Add(ctx.root())).BlockFunc(func(g *jen.Group) {
			g.If(jen.Id("_ch").Dot("Kind").Op("!=").Qual(cstPkg, "NodeTable")).Block(jen.Continue())
			g.Id("_hdr").Op(":=").Qual(cstPkg, "TableHeaderKey").Call(jen.Id("_ch"))
			g.If(jen.Op("!").Qual("strings", "HasPrefix").Call(jen.Id("_hdr"), pf.Jen())).Block(jen.Continue())
			g.Id("_mk").Op(":=").Id("_hdr").Index(pf.JenLen().Op(":"))
			g.If(jen.Qual("strings", "Contains").Call(jen.Id("_mk"), jen.Lit("."))).Block(jen.Continue())
			g.If(n.Tgt.Jen().Clone().Op("==").Nil()).BlockFunc(func(g *jen.Group) {
				g.Add(ctx.mc(n.TKey))
				g.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Map(jen.String()).Qual(n.ImportPath, st))
			})
			g.Add(ctx.mcExpr(n.TKey.Jen().Op("+").Lit(".").Op("+").Id("_mk")))
			g.Var().Id("entry").Qual(n.ImportPath, st)
			dke := n.TKey.Lit(".").Var("_mk").Lit(".")
			g.If(jen.Err().Op(":=").Qual(n.ImportPath, decFn).Call(
				jen.Op("&").Id("entry"), ctx.docVar.Clone(), jen.Id("_ch"), ctx.consumed.Clone(), dke.Jen(),
			), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+".%s: %w", jen.Id("_mk"), jen.Err()))
			g.Add(n.Tgt.Jen().Clone()).Index(jen.Id("_mk")).Op("=").Id("entry")
		})
	})}
}

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
		default:
			return true
		}
	}
	return false
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
	f.HeaderComment("Code generated by tommy; DO NOT EDIT.")
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
	for _, fi := range si.Fields {
		if isSamePackageSliceStruct(fi) {
			f.Type().Id(unexport(sliceStructName(fi)) + "Handle").Struct(jen.Id("node").Op("*").Qual(cstPkg, "Node"))
		}
	}
	f.Type().Id(dt).StructFunc(func(g *jen.Group) {
		g.Id("data").Id(si.Name)
		g.Id("cstDoc").Op("*").Qual(docPkg, "Document")
		g.Id("consumed").Map(jen.String()).Bool()
		for _, fi := range si.Fields {
			if isSamePackageSliceStruct(fi) {
				g.Id(unexport(fi.GoName)).Index().Id(unexport(sliceStructName(fi)) + "Handle")
			}
		}
	})
	compEmitDecode(f, si, dt)
	f.Func().Params(jen.Id("d").Op("*").Id(dt)).Id("Data").Params().Op("*").Id(si.Name).Block(
		jen.Return(jen.Op("&").Id("d").Dot("data")),
	)
	compEmitEncode(f, si, dt)
	f.Func().Params(jen.Id("d").Op("*").Id(dt)).Id("Undecoded").Params().Index().String().Block(
		jen.Return(jen.Qual(docPkg, "UndecodedKeys").Call(jen.Id("d").Dot("cstDoc").Dot("Root").Call(), jen.Id("d").Dot("consumed"))),
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
		g.Empty()
		g.Id("d").Op(":=").Op("&").Id(dt).Values(jen.Dict{
			jen.Id("cstDoc"):   jen.Id("doc"),
			jen.Id("consumed"): jen.Make(jen.Map(jen.String()).Bool()),
		})
		g.Empty()
		for _, s := range compRenderDecodeBody(ctx, nodes, jen.Id("d").Dot("cstDoc").Dot("Root").Call(), "") {
			g.Add(s)
		}
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
	f.Func().Id("Decode"+si.Name+"Into").Params(
		jen.Id("data").Op("*").Id(si.Name),
		jen.Id("doc").Op("*").Qual(docPkg, "Document"),
		jen.Id("container").Op("*").Qual(cstPkg, "Node"),
		jen.Id("consumed").Map(jen.String()).Bool(),
		jen.Id("keyPrefix").String(),
	).Error().BlockFunc(func(g *jen.Group) {
		for _, s := range compRenderDecodeBody(ctx, nodes, jen.Id("container"), "") {
			g.Add(s)
		}
		g.Return(jen.Nil())
	})
}

func compEmitEncodeFrom(f *jen.File, si StructInfo) {
	ectx := freeEncCtx()
	nodes := foldCompEncode(&si, compPos{tkey: StaticKey(""), tgt: LocalTarget("data")}, false)
	f.Func().Id("Encode"+si.Name+"From").Params(
		jen.Id("data").Op("*").Id(si.Name),
		jen.Id("doc").Op("*").Qual(docPkg, "Document"),
		jen.Id("container").Op("*").Qual(cstPkg, "Node"),
	).Error().BlockFunc(func(g *jen.Group) {
		for _, s := range compRenderEncodeBody(ectx, nodes, jen.Id("container")) {
			g.Add(s)
		}
		g.Return(jen.Nil())
	})
}
