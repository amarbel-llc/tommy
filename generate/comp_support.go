package generate

import (
	"strings"

	jen "github.com/dave/jennifer/jen"
)

// Shared rendering support for the compositional renderer (comp_render.go):
// the cst/document package consts, the decode/encode contexts, and the small
// jennifer helpers used across both directions. These were the parts of the
// (now-removed) enumerated renderer that were genuinely shared rather than
// op-specific; they outlived the cutover (#84).

const (
	cstPkg = "github.com/amarbel-llc/tommy/pkg/cst"
	docPkg = "github.com/amarbel-llc/tommy/pkg/document"
)

// jenCtx carries context for decode rendering within one function scope:
// the consumed-key map, the error-return shape, the document variable, and
// whether this is the top-level receiver Decode (vs a delegated DecodeXInto).
type jenCtx struct {
	consumed *jen.Statement
	retErr   func(fmtStr string, args ...jen.Code) jen.Code
	docVar   *jen.Statement
	// topLevel is true for the receiver Decode, where compInTable/compNilGuard
	// scan the document root as the actual table scope (so duplicate-table
	// detection there is correct, #92). A delegated DecodeXInto runs in the free
	// context with topLevel=false; its inner table-scan is document-root-relative
	// by a shared dotted key and cannot yet distinguish array-table entries, so
	// duplicate-table detection is suppressed there until that is fixed.
	topLevel bool
}

func receiverJenCtx() jenCtx {
	return jenCtx{
		consumed: jen.Id("d").Dot("consumed"),
		retErr: func(f string, a ...jen.Code) jen.Code {
			args := []jen.Code{jen.Lit(f)}
			args = append(args, a...)
			return jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(args...))
		},
		docVar:   jen.Id("d").Dot("cstDoc"),
		topLevel: true,
	}
}

func freeJenCtx() jenCtx {
	return jenCtx{
		consumed: jen.Id("consumed"),
		retErr: func(f string, a ...jen.Code) jen.Code {
			args := []jen.Code{jen.Lit(f)}
			args = append(args, a...)
			return jen.Return(jen.Qual("fmt", "Errorf").Call(args...))
		},
		docVar: jen.Id("doc"),
	}
}

func (c jenCtx) mc(key TOMLKey) jen.Code {
	return c.consumed.Clone().Index(key.Jen()).Op("=").True()
}

func (c jenCtx) mcExpr(expr *jen.Statement) jen.Code {
	return c.consumed.Clone().Index(expr).Op("=").True()
}

// dupGuard rejects a repeated known key within one table scan (#90, TOML
// "Defining a key multiple times is invalid"). It checks/sets a per-scan local
// `_seen` set (declared by compLeafScan), NOT the document-wide consumed map:
// the same logical key path recurs legitimately across array-table and map
// entries, each of which runs its own leaf scan with a fresh `_seen`. Emitted as
// the first statements of each leaf case.
func (c jenCtx) dupGuard(bareKey string) []jen.Code {
	return []jen.Code{
		jen.If(jen.Id("_seen").Index(jen.Lit(bareKey))).Block(c.retErr("duplicate key %q", jen.Lit(bareKey))),
		jen.Id("_seen").Index(jen.Lit(bareKey)).Op("=").True(),
	}
}

func (c jenCtx) root() *jen.Statement {
	return c.docVar.Clone().Dot("Root").Call().Dot("Children")
}

// encCtx carries context for encode rendering: the error-return shape, the
// root node expression, and the document variable (for cross-package delegation).
type encCtx struct {
	retErr  func(string, ...jen.Code) jen.Code
	rootVar *jen.Statement // d.cstDoc.Root() or doc.Root()
	docVar  *jen.Statement // d.cstDoc or doc
}

func receiverEncCtx() encCtx {
	return encCtx{
		retErr: func(f string, a ...jen.Code) jen.Code {
			args := []jen.Code{jen.Lit(f)}
			args = append(args, a...)
			return jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(args...))
		},
		rootVar: jen.Id("d").Dot("cstDoc").Dot("Root").Call(),
		docVar:  jen.Id("d").Dot("cstDoc"),
	}
}

func freeEncCtx() encCtx {
	return encCtx{
		retErr: func(f string, a ...jen.Code) jen.Code {
			args := []jen.Code{jen.Lit(f)}
			args = append(args, a...)
			return jen.Return(jen.Qual("fmt", "Errorf").Call(args...))
		},
		rootVar: jen.Id("doc").Dot("Root").Call(),
		docVar:  jen.Id("doc"),
	}
}

func delegateParts(typeName string) (string, string) {
	if i := strings.IndexByte(typeName, '.'); i >= 0 {
		return typeName[:i], typeName[i+1:]
	}
	return "", typeName
}

// jenType returns a jennifer Code for a type name that might be cross-package.
// If importPath is non-empty, uses Qual; otherwise uses Id.
func jenType(typeName, importPath string) *jen.Statement {
	if importPath != "" {
		_, short := delegateParts(typeName)
		return jen.Qual(importPath, short)
	}
	return jen.Id(typeName)
}

func jenSetCall(ctx encCtx, cv *jen.Statement, key TOMLKey, val *jen.Statement) jen.Code {
	return jen.If(jen.Err().Op(":=").Qual(cstPkg, "SetAny").Call(
		cv.Clone(), key.Jen(), val,
	), jen.Err().Op("!=").Nil()).Block(ctx.retErr("%w", jen.Err()))
}

func jenSetMultilineCall(ctx encCtx, cv *jen.Statement, key TOMLKey, val *jen.Statement) jen.Code {
	return jen.If(jen.Err().Op(":=").Qual(cstPkg, "SetMultilineString").Call(
		cv.Clone(), key.Jen(), val,
	), jen.Err().Op("!=").Nil()).Block(ctx.retErr("%w", jen.Err()))
}

func jenZeroLit(typeName string) *jen.Statement {
	switch typeName {
	case "bool":
		return jen.False()
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return jen.Lit(0)
	case "float32", "float64":
		return jen.Lit(0.0)
	case "string":
		return jen.Lit("")
	default:
		return jen.Lit("")
	}
}
