package generate

import "github.com/dave/jennifer/jen"

// Model-based decode renderer (ADR 2026-06-07). The decoder folds the SAME
// TypeExpr/cd* algebra (comp_ir.go / comp_build.go) the old CST-pattern renderer
// used, but walks a normalized cst.Value (from cst.Decompose) instead of the raw
// CST. Because Decompose already collapsed every TOML spelling to one shape,
// each kind has exactly ONE reader: no inline/dotted/flat/implicit fallbacks, no
// root-vs-scoped duality, no dynamic-key bookkeeping. `tv` is the current table
// Value (a *cst.Value of Kind VTable); a field is read by name via tv.Get.
//
// Undecoded is tracked on the model: a leaf the decoder reads is MarkConsumed
// (its subtree is accounted for), a struct table is MarkSeen (entered, but
// Undecoded still descends to surface an unknown inner field), a map field is
// MarkConsumed (it absorbs every entry). cst.Value.Undecoded() walks the rest.

// compModelBody decodes children against table tv. fv, when non-empty, is a
// bool var set true whenever a child matches — used by the #55 flat-key fallback
// to decide whether to materialize a pointer struct.
func compModelBody(ctx jenCtx, g *jen.Group, children []cdNode, tv *jen.Statement, fv string) {
	for _, c := range children {
		compModelNode(ctx, g, c, tv, fv)
	}
}

func compModelNode(ctx jenCtx, g *jen.Group, c cdNode, tv *jen.Statement, fv string) {
	switch n := c.(type) {
	case cdLeaf:
		compModelLeaf(ctx, g, n, tv, fv)
	case cdInTable:
		compModelInTable(ctx, g, n, tv, fv)
	case cdNilGuard:
		compModelNilGuard(ctx, g, n, tv, fv)
	case cdArrayTable:
		compModelArrayTable(ctx, g, n, tv, fv)
	case cdMapScalar:
		compModelMapScalar(ctx, g, n, tv, fv)
	case cdMapMap:
		compModelMapMap(ctx, g, n, tv, fv)
	case cdMapStruct:
		compModelMapStruct(ctx, g, n, tv, fv)
	case cdDelStruct:
		compModelDelStruct(ctx, g, n, tv, fv)
	case cdDelSlice:
		compModelDelSlice(ctx, g, n, tv, fv)
	case cdDelMap:
		compModelDelMap(ctx, g, n, tv, fv)
	}
}

// field emits `if v, ok := tv.Get(bk); ok && v.Kind == <kind> { [fv=true]; body(v) }`,
// binding the matched value to a key-unique local. kind is "" to skip the Kind
// check (used by leaves, which additionally guard VLeaf themselves).
func compModelField(g *jen.Group, tv *jen.Statement, key TOMLKey, kind, fv string, body func(*jen.Group, *jen.Statement)) {
	v := "_v" + key.VarSuffix()
	cond := jen.Id("_ok")
	if kind != "" {
		cond = jen.Id("_ok").Op("&&").Id(v).Dot("Kind").Op("==").Qual(cstPkg, kind)
	}
	g.If(
		jen.List(jen.Id(v), jen.Id("_ok")).Op(":=").Add(tv.Clone()).Dot("Get").Call(jen.Lit(key.BareKey())),
		cond,
	).BlockFunc(func(b *jen.Group) {
		if fv != "" {
			b.Id(fv).Op("=").True()
		}
		body(b, jen.Id(v))
	})
}

// compModelLeaf reads a scalar / scalar-slice / text / custom leaf from tv,
// extracting via the existing cst.Extract* on the leaf's source key-value node
// (Value.Leaf), and marks it consumed.
func compModelLeaf(ctx jenCtx, g *jen.Group, l cdLeaf, tv *jen.Statement, fv string) {
	compModelField(g, tv, l.TKey, "VLeaf", fv, func(b *jen.Group, v *jen.Statement) {
		node := v.Clone().Dot("Leaf")
		mark := v.Clone().Dot("MarkConsumed").Call()
		bk := l.TKey.BareKey()
		switch l.Kind {
		case cdLeafPrim:
			ei := cstExtract(l.TypeName)
			var assign []jen.Code
			switch {
			case l.Pointer && ei.cast != "":
				assign = []jen.Code{jen.Id("_cv").Op(":=").Id(ei.cast).Call(jen.Id("_x")), l.Tgt.Jen().Clone().Op("=").Op("&").Id("_cv")}
			case l.Pointer:
				assign = []jen.Code{l.Tgt.Jen().Clone().Op("=").Op("&").Id("_x")}
			case l.ElemType != "":
				et := jenType(l.ElemType, l.ImportPath)
				if ei.cast != "" {
					assign = []jen.Code{l.Tgt.Jen().Clone().Op("=").Add(et).Call(jen.Id(ei.cast).Call(jen.Id("_x")))}
				} else {
					assign = []jen.Code{l.Tgt.Jen().Clone().Op("=").Add(et).Call(jen.Id("_x"))}
				}
			case ei.cast != "":
				assign = []jen.Code{l.Tgt.Jen().Clone().Op("=").Id(ei.cast).Call(jen.Id("_x"))}
			default:
				assign = []jen.Code{l.Tgt.Jen().Clone().Op("=").Id("_x")}
			}
			b.If(jen.List(jen.Id("_x"), jen.Id("_xok")).Op(":=").Qual(cstPkg, ei.fn).Call(node), jen.Id("_xok")).Block(append(assign, mark)...)
		case cdLeafCustom:
			b.If(jen.List(jen.Id("_raw"), jen.Id("_xok")).Op(":=").Qual(cstPkg, "ExtractRaw").Call(node), jen.Id("_xok")).Block(
				jen.If(jen.Err().Op(":=").Add(l.Tgt.Jen().Clone()).Dot("UnmarshalTOML").Call(jen.Id("_raw")), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+": %w", jen.Err())),
				mark,
			)
		case cdLeafText:
			b.If(jen.List(jen.Id("_x"), jen.Id("_xok")).Op(":=").Qual(cstPkg, "ExtractString").Call(node), jen.Id("_xok")).Block(
				jen.If(jen.Err().Op(":=").Add(l.Tgt.Jen().Clone()).Dot("UnmarshalText").Call(jen.Index().Byte().Call(jen.Id("_x"))), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+": %w", jen.Err())),
				mark,
			)
		case cdLeafSlicePrim:
			compModelSlicePrim(b, l, node, mark)
		case cdLeafSliceText:
			b.If(jen.List(jen.Id("_x"), jen.Id("_xok")).Op(":=").Qual(cstPkg, "ExtractStringSlice").Call(node), jen.Id("_xok")).Block(
				l.Tgt.Jen().Clone().Op("=").Make(jen.Index().Add(jenType(l.TypeName, l.ImportPath)), jen.Len(jen.Id("_x"))),
				jen.For(jen.List(jen.Id("_si"), jen.Id("_s")).Op(":=").Range().Id("_x")).Block(
					jen.If(jen.Err().Op(":=").Add(l.Tgt.Jen().Clone()).Index(jen.Id("_si")).Dot("UnmarshalText").Call(jen.Index().Byte().Call(jen.Id("_s"))), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+"[%d]: %w", jen.Id("_si"), jen.Err())),
				),
				mark,
			)
		}
	})
}

// compModelSlicePrim mirrors the old cdLeafSlicePrim assignment (sized-element
// narrowing, named-type conversion, #21 nil→empty), reading from the model leaf.
func compModelSlicePrim(b *jen.Group, l cdLeaf, node *jen.Statement, mark jen.Code) {
	sc, _ := lookupScalar(l.ElemType)
	var body []jen.Code
	switch {
	case l.SlicePointer && sc.cast != "":
		body = []jen.Code{
			l.Tgt.Jen().Clone().Op("=").Make(jen.Index().Op("*").Id(l.ElemType), jen.Len(jen.Id("_x"))),
			jen.For(jen.Id("_si").Op(":=").Range().Id("_x")).Block(
				jen.Id("_e").Op(":=").Id(sc.cast).Call(jen.Id("_x").Index(jen.Id("_si"))),
				l.Tgt.Jen().Clone().Index(jen.Id("_si")).Op("=").Op("&").Id("_e"),
			),
		}
	case l.SlicePointer:
		body = []jen.Code{
			l.Tgt.Jen().Clone().Op("=").Make(jen.Index().Op("*").Id(l.ElemType), jen.Len(jen.Id("_x"))),
			jen.For(jen.Id("_si").Op(":=").Range().Id("_x")).Block(l.Tgt.Jen().Clone().Index(jen.Id("_si")).Op("=").Op("&").Id("_x").Index(jen.Id("_si"))),
		}
	case sc.cast != "":
		sliceType := jen.Index().Id(l.ElemType)
		if l.TypeName != "" {
			sliceType = jenType(l.TypeName, l.ImportPath)
		}
		body = []jen.Code{
			l.Tgt.Jen().Clone().Op("=").Make(sliceType, jen.Len(jen.Id("_x"))),
			jen.For(jen.Id("_si").Op(":=").Range().Id("_x")).Block(
				l.Tgt.Jen().Clone().Index(jen.Id("_si")).Op("=").Id(sc.cast).Call(jen.Id("_x").Index(jen.Id("_si"))),
			),
		}
	case l.TypeName != "":
		body = []jen.Code{
			l.Tgt.Jen().Clone().Op("=").Add(jenType(l.TypeName, l.ImportPath)).Call(jen.Id("_x")),
			jen.If(l.Tgt.Jen().Clone().Op("==").Nil()).Block(l.Tgt.Jen().Clone().Op("=").Add(jenType(l.TypeName, l.ImportPath)).Values()),
		}
	default:
		body = []jen.Code{
			l.Tgt.Jen().Clone().Op("=").Id("_x"),
			jen.If(l.Tgt.Jen().Clone().Op("==").Nil()).Block(l.Tgt.Jen().Clone().Op("=").Index().Id(l.ElemType).Values()),
		}
	}
	body = append(body, mark)
	b.If(jen.List(jen.Id("_x"), jen.Id("_xok")).Op(":=").Qual(cstPkg, cstSliceExtractFunc(l.ElemType)).Call(node), jen.Id("_xok")).Block(body...)
}

// compModelInTable decodes a non-pointer nested struct. When its child table is
// present, decode from it; else apply the #55 flat-key fallback, reading the
// struct's flat-decodable fields straight from the parent table tv (a value
// struct is always materialized, so no found-guard is needed).
func compModelInTable(ctx jenCtx, g *jen.Group, n cdInTable, tv *jen.Statement, fv string) {
	v := "_v" + n.TKey.VarSuffix()
	g.If(
		jen.List(jen.Id(v), jen.Id("_ok")).Op(":=").Add(tv.Clone()).Dot("Get").Call(jen.Lit(n.TKey.BareKey())),
		jen.Id("_ok").Op("&&").Id(v).Dot("Kind").Op("==").Qual(cstPkg, "VTable"),
	).BlockFunc(func(b *jen.Group) {
		if fv != "" {
			b.Id(fv).Op("=").True()
		}
		b.Add(jen.Id(v).Dot("MarkSeen").Call())
		compModelBody(ctx, b, n.Children, jen.Id(v), "")
	}).Else().BlockFunc(func(b *jen.Group) {
		compModelBody(ctx, b, flatLeafChildren(n.FlatChildren), tv, fv)
	})
}

// flatLeafChildren keeps only the LEAF flat-fallback children (scalars, *scalars,
// primitive slices). A slice-of-struct flat child is dropped: matching its bare
// key against the parent would claim a sibling's array (#101 phantom — e.g. an
// absent *Extra with its own `items` grabbing the outer's `items`). In the model
// such a field is reached only via its struct's own key, which a deeper
// `[parent.struct.field]` header materializes implicitly — the normal path.
func flatLeafChildren(children []cdNode) []cdNode {
	var out []cdNode
	for _, c := range children {
		if _, ok := c.(cdLeaf); ok {
			out = append(out, c)
		}
	}
	return out
}

// compModelNilGuard decodes a pointer nested struct. When its child table is
// present, allocate + decode from it; else apply the #55 flat-key fallback,
// allocating only if a flat-decodable field was actually found in the parent.
func compModelNilGuard(ctx jenCtx, g *jen.Group, n cdNilGuard, tv *jen.Statement, fv string) {
	v := "_v" + n.TKey.VarSuffix()
	g.If(
		jen.List(jen.Id(v), jen.Id("_ok")).Op(":=").Add(tv.Clone()).Dot("Get").Call(jen.Lit(n.TKey.BareKey())),
		jen.Id("_ok").Op("&&").Id(v).Dot("Kind").Op("==").Qual(cstPkg, "VTable"),
	).BlockFunc(func(b *jen.Group) {
		if fv != "" {
			b.Id(fv).Op("=").True()
		}
		b.Add(jen.Id(v).Dot("MarkSeen").Call())
		b.Id(n.LocalVar).Op(":=").Op("&").Id(n.TypeName).Values()
		compModelBody(ctx, b, n.Children, jen.Id(v), "")
		b.Add(n.Tgt.Jen().Clone()).Op("=").Id(n.LocalVar)
	}).Else().BlockFunc(func(b *jen.Group) {
		found := "_found" + n.TKey.VarSuffix()
		b.Id(n.LocalVar).Op(":=").Op("&").Id(n.TypeName).Values()
		b.Id(found).Op(":=").False()
		compModelBody(ctx, b, flatLeafChildren(n.FlatChildren), tv, found)
		b.If(jen.Id(found)).BlockFunc(func(ib *jen.Group) {
			if fv != "" {
				ib.Id(fv).Op("=").True()
			}
			ib.Add(n.Tgt.Jen().Clone()).Op("=").Id(n.LocalVar)
		})
	})
}

// compModelArrayTable decodes a same-package []struct from an array of tables.
func compModelArrayTable(ctx jenCtx, g *jen.Group, n cdArrayTable, tv *jen.Statement, fv string) {
	compModelField(g, tv, n.TKey, "VArray", fv, func(b *jen.Group, v *jen.Statement) {
		b.Add(v.Clone().Dot("MarkSeen").Call())
		jt := jenType(n.TypeName, n.ImportPath)
		if n.SlicePtr {
			b.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Index().Op("*").Add(jt.Clone()), jen.Len(v.Clone().Dot("Items")))
		} else {
			b.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Index().Add(jt.Clone()), jen.Len(v.Clone().Dot("Items")))
		}
		if n.TrackHandles {
			fn := toLowerFirst(n.Tgt.Segs[len(n.Tgt.Segs)-1].Name)
			b.Id("d").Dot(fn).Op("=").Make(jen.Index().Id(n.HandleType), jen.Len(v.Clone().Dot("Items")))
		}
		ev := "_e" + n.TDottedKey.VarSuffix()
		b.For(jen.Id(n.IdxVar).Op(":=").Range().Add(v.Clone()).Dot("Items")).BlockFunc(func(lb *jen.Group) {
			lb.Id(ev).Op(":=").Op("&").Add(v.Clone()).Dot("Items").Index(jen.Id(n.IdxVar))
			lb.Add(jen.Id(ev).Dot("MarkSeen").Call())
			if n.TrackHandles {
				fn := toLowerFirst(n.Tgt.Segs[len(n.Tgt.Segs)-1].Name)
				lb.Id("d").Dot(fn).Index(jen.Id(n.IdxVar)).Op("=").Id(n.HandleType).Values(jen.Dict{jen.Id("node"): jen.Id(ev).Dot("Node")})
			}
			if n.SlicePtr {
				lb.Add(n.Tgt.Jen().Clone()).Index(jen.Id(n.IdxVar)).Op("=").Op("&").Add(jt.Clone()).Values()
			}
			compModelBody(ctx, lb, n.Children, jen.Id(ev), "")
		})
	})
	compModelEmptyArrayLeaf(g, tv, n.TKey, fv, n.Tgt, jenType(n.TypeName, n.ImportPath), n.SlicePtr)
}

// compModelEmptyArrayLeaf emits the `key = []` branch for a struct-slice field.
// Decompose keeps an empty array a VLeaf (an empty `[]` can't be told apart from
// an empty scalar array), so the VArray reader above misses it. Here we consume
// the leaf and assign an empty (non-nil) slice, so an explicit empty
// array-of-tables decodes to an empty slice instead of leaking as an undecoded
// key (#94). elem is the slice element type; slicePtr makes the slice []*elem.
func compModelEmptyArrayLeaf(g *jen.Group, tv *jen.Statement, key TOMLKey, fv string, tgt TargetPath, elem *jen.Statement, slicePtr bool) {
	v := "_ea" + key.VarSuffix()
	g.If(
		jen.List(jen.Id(v), jen.Id("_eaok")).Op(":=").Add(tv.Clone()).Dot("Get").Call(jen.Lit(key.BareKey())),
		jen.Id("_eaok").Op("&&").Id(v).Dot("IsEmptyArray").Call(),
	).BlockFunc(func(b *jen.Group) {
		if fv != "" {
			b.Id(fv).Op("=").True()
		}
		b.Add(jen.Id(v).Dot("MarkConsumed").Call())
		lit := jen.Index()
		if slicePtr {
			lit = lit.Op("*")
		}
		b.Add(tgt.Jen()).Op("=").Add(lit.Add(elem).Values())
	})
}

// compModelMapScalar decodes map[string]string: each leaf entry that extracts to
// a string is consumed; a non-string entry stays unconsumed (surfaced by #109).
func compModelMapScalar(ctx jenCtx, g *jen.Group, n cdMapScalar, tv *jen.Statement, fv string) {
	compModelField(g, tv, n.TKey, "VTable", fv, func(b *jen.Group, v *jen.Statement) {
		b.Add(v.Clone().Dot("MarkSeen").Call())
		b.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Map(jen.String()).String())
		idx := "_i" + n.TKey.VarSuffix()
		fv := "_f" + n.TKey.VarSuffix()
		b.For(jen.Id(idx).Op(":=").Range().Add(v.Clone()).Dot("Fields")).BlockFunc(func(lb *jen.Group) {
			lb.Id(fv).Op(":=").Op("&").Add(v.Clone()).Dot("Fields").Index(jen.Id(idx))
			lb.If(jen.Id(fv).Dot("Val").Dot("Kind").Op("==").Qual(cstPkg, "VLeaf")).BlockFunc(func(ib *jen.Group) {
				ib.If(jen.List(jen.Id("_s"), jen.Id("_sok")).Op(":=").Qual(cstPkg, "ExtractString").Call(jen.Id(fv).Dot("Val").Dot("Leaf")), jen.Id("_sok")).Block(
					n.Tgt.Jen().Clone().Index(jen.Id(fv).Dot("Key")).Op("=").Id("_s"),
					jen.Id(fv).Dot("Val").Dot("MarkConsumed").Call(),
				)
			})
		})
	})
}

// compModelMapMap decodes map[string]NamedMap (map of string-maps).
func compModelMapMap(ctx jenCtx, g *jen.Group, n cdMapMap, tv *jen.Statement, fv string) {
	innerType := func() *jen.Statement {
		if n.TypeName != "" {
			return jenType(n.TypeName, n.ImportPath)
		}
		return jen.Map(jen.String()).String()
	}
	compModelField(g, tv, n.TKey, "VTable", fv, func(b *jen.Group, v *jen.Statement) {
		b.Add(v.Clone().Dot("MarkSeen").Call())
		b.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Map(jen.String()).Add(innerType()))
		oi := "_oi" + n.TKey.VarSuffix()
		of := "_of" + n.TKey.VarSuffix()
		b.For(jen.Id(oi).Op(":=").Range().Add(v.Clone()).Dot("Fields")).BlockFunc(func(lb *jen.Group) {
			lb.Id(of).Op(":=").Op("&").Add(v.Clone()).Dot("Fields").Index(jen.Id(oi))
			lb.If(jen.Id(of).Dot("Val").Dot("Kind").Op("==").Qual(cstPkg, "VTable")).BlockFunc(func(ib *jen.Group) {
				ib.Add(jen.Id(of).Dot("Val").Dot("MarkSeen").Call())
				ib.Id("_inner").Op(":=").Make(jen.Map(jen.String()).String())
				ii := "_ii" + n.TKey.VarSuffix()
				inf := "_if" + n.TKey.VarSuffix()
				ib.For(jen.Id(ii).Op(":=").Range().Id(of).Dot("Val").Dot("Fields")).BlockFunc(func(jb *jen.Group) {
					jb.Id(inf).Op(":=").Op("&").Id(of).Dot("Val").Dot("Fields").Index(jen.Id(ii))
					jb.If(jen.Id(inf).Dot("Val").Dot("Kind").Op("==").Qual(cstPkg, "VLeaf")).Block(
						jen.If(jen.List(jen.Id("_s"), jen.Id("_sok")).Op(":=").Qual(cstPkg, "ExtractString").Call(jen.Id(inf).Dot("Val").Dot("Leaf")), jen.Id("_sok")).Block(
							jen.Id("_inner").Index(jen.Id(inf).Dot("Key")).Op("=").Id("_s"),
							jen.Id(inf).Dot("Val").Dot("MarkConsumed").Call(),
						),
					)
				})
				if n.TypeName != "" {
					ib.Add(n.Tgt.Jen().Clone()).Index(jen.Id(of).Dot("Key")).Op("=").Add(innerType()).Call(jen.Id("_inner"))
				} else {
					ib.Add(n.Tgt.Jen().Clone()).Index(jen.Id(of).Dot("Key")).Op("=").Id("_inner")
				}
			})
		})
	})
}

// compModelMapStruct decodes map[string]Struct / map[string]*Struct.
func compModelMapStruct(ctx jenCtx, g *jen.Group, n cdMapStruct, tv *jen.Statement, fv string) {
	compModelField(g, tv, n.TKey, "VTable", fv, func(b *jen.Group, v *jen.Statement) {
		b.Add(v.Clone().Dot("MarkSeen").Call())
		if n.SlicePtr {
			b.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Map(jen.String()).Op("*").Id(n.TypeName))
		} else {
			b.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Map(jen.String()).Id(n.TypeName))
		}
		idx := n.MapVar + "i"
		ev := n.MapVar + "e"
		b.For(jen.Id(idx).Op(":=").Range().Add(v.Clone()).Dot("Fields")).BlockFunc(func(lb *jen.Group) {
			lb.Id(ev).Op(":=").Op("&").Add(v.Clone()).Dot("Fields").Index(jen.Id(idx))
			lb.If(jen.Id(ev).Dot("Val").Dot("Kind").Op("==").Qual(cstPkg, "VTable")).BlockFunc(func(ib *jen.Group) {
				ib.Add(jen.Id(ev).Dot("Val").Dot("MarkSeen").Call())
				ib.Var().Id(n.EntryVar).Id(n.TypeName)
				compModelBody(ctx, ib, n.Children, jen.Id(ev).Dot("Val"), "")
				if n.SlicePtr {
					ib.Add(n.Tgt.Jen().Clone()).Index(jen.Id(ev).Dot("Key")).Op("=").Op("&").Id(n.EntryVar)
				} else {
					ib.Add(n.Tgt.Jen().Clone()).Index(jen.Id(ev).Dot("Key")).Op("=").Id(n.EntryVar)
				}
			})
		})
	})
}

// compModelDelStruct delegates a cross-package struct to its DecodeXInto(Value).
func compModelDelStruct(ctx jenCtx, g *jen.Group, n cdDelStruct, tv *jen.Statement, fv string) {
	_, st := delegateParts(n.TypeName)
	bk := n.TKey.BareKey()
	decFn := "Decode" + st + "Into"
	compModelField(g, tv, n.TKey, "VTable", fv, func(b *jen.Group, v *jen.Statement) {
		b.Add(v.Clone().Dot("MarkSeen").Call())
		if n.Ptr {
			lv := toLowerFirst(st) + "Val"
			b.Id(lv).Op(":=").Op("&").Qual(n.ImportPath, st).Values()
			b.If(jen.Err().Op(":=").Qual(n.ImportPath, decFn).Call(jen.Id(lv), v.Clone()), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+": %w", jen.Err()))
			b.Add(n.Tgt.Jen().Clone()).Op("=").Id(lv)
		} else {
			b.If(jen.Err().Op(":=").Qual(n.ImportPath, decFn).Call(jen.Op("&").Add(n.Tgt.Jen().Clone()), v.Clone()), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+": %w", jen.Err()))
		}
	})
}

// compModelDelSlice delegates a cross-package []struct per array entry.
func compModelDelSlice(ctx jenCtx, g *jen.Group, n cdDelSlice, tv *jen.Statement, fv string) {
	_, st := delegateParts(n.TypeName)
	bk := n.TKey.BareKey()
	decFn := "Decode" + st + "Into"
	compModelField(g, tv, n.TKey, "VArray", fv, func(b *jen.Group, v *jen.Statement) {
		b.Add(v.Clone().Dot("MarkSeen").Call())
		if n.SlicePtr {
			b.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Index().Op("*").Qual(n.ImportPath, st), jen.Len(v.Clone().Dot("Items")))
		} else {
			b.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Index().Qual(n.ImportPath, st), jen.Len(v.Clone().Dot("Items")))
		}
		ev := "_e" + n.TDottedKey.VarSuffix()
		b.For(jen.Id(n.IdxVar).Op(":=").Range().Add(v.Clone()).Dot("Items")).BlockFunc(func(lb *jen.Group) {
			lb.Id(ev).Op(":=").Op("&").Add(v.Clone()).Dot("Items").Index(jen.Id(n.IdxVar))
			lb.Add(jen.Id(ev).Dot("MarkSeen").Call())
			if n.SlicePtr {
				lb.Add(n.Tgt.Jen().Clone()).Index(jen.Id(n.IdxVar)).Op("=").Op("&").Qual(n.ImportPath, st).Values()
				lb.If(jen.Err().Op(":=").Qual(n.ImportPath, decFn).Call(n.Tgt.Jen().Clone().Index(jen.Id(n.IdxVar)), jen.Id(ev)), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+"[%d]: %w", jen.Id(n.IdxVar), jen.Err()))
			} else {
				lb.If(jen.Err().Op(":=").Qual(n.ImportPath, decFn).Call(jen.Op("&").Add(n.Tgt.Jen().Clone()).Index(jen.Id(n.IdxVar)), jen.Id(ev)), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+"[%d]: %w", jen.Id(n.IdxVar), jen.Err()))
			}
		})
	})
	compModelEmptyArrayLeaf(g, tv, n.TKey, fv, n.Tgt, jen.Qual(n.ImportPath, st), n.SlicePtr)
}

// compModelDelMap delegates a cross-package map[string]Struct per entry.
func compModelDelMap(ctx jenCtx, g *jen.Group, n cdDelMap, tv *jen.Statement, fv string) {
	_, st := delegateParts(n.ElemType)
	bk := n.TKey.BareKey()
	decFn := "Decode" + st + "Into"
	compModelField(g, tv, n.TKey, "VTable", fv, func(b *jen.Group, v *jen.Statement) {
		b.Add(v.Clone().Dot("MarkSeen").Call())
		b.Add(n.Tgt.Jen().Clone()).Op("=").Make(jen.Map(jen.String()).Qual(n.ImportPath, st))
		idx := n.MapVar + "i"
		ev := n.MapVar + "e"
		b.For(jen.Id(idx).Op(":=").Range().Add(v.Clone()).Dot("Fields")).BlockFunc(func(lb *jen.Group) {
			lb.Id(ev).Op(":=").Op("&").Add(v.Clone()).Dot("Fields").Index(jen.Id(idx))
			lb.If(jen.Id(ev).Dot("Val").Dot("Kind").Op("==").Qual(cstPkg, "VTable")).BlockFunc(func(ib *jen.Group) {
				ib.Add(jen.Id(ev).Dot("Val").Dot("MarkSeen").Call())
				ib.Var().Id(n.EntryVar).Qual(n.ImportPath, st)
				ib.If(jen.Err().Op(":=").Qual(n.ImportPath, decFn).Call(jen.Op("&").Id(n.EntryVar), jen.Op("&").Id(ev).Dot("Val")), jen.Err().Op("!=").Nil()).Block(ctx.retErr(bk+".%s: %w", jen.Id(ev).Dot("Key"), jen.Err()))
				ib.Add(n.Tgt.Jen().Clone()).Index(jen.Id(ev).Dot("Key")).Op("=").Id(n.EntryVar)
			})
		})
	})
}
