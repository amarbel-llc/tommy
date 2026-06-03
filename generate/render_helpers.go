package generate

import (
	"strings"
	"unicode"
)

// Shared helpers for the (sole) jen renderer in ir_render_jen.go. These were
// previously spread across the now-removed alternate renderers (emit.go,
// ir_render.go, ir_render_cst.go) and the legacy template (template.go);
// collapsing to a single renderer consolidated them here.

func toLowerFirst(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

func toUpperFirst(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// unexport lowercases the first rune, used for the same-package slice-struct
// Handle helper type names.
func unexport(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

// tomlKey extracts the bare TOML key from a possibly-prefixed key.
func tomlKey(fullKey string) string {
	if i := strings.LastIndex(fullKey, "."); i >= 0 {
		return fullKey[i+1:]
	}
	return fullKey
}

// sliceStructElem returns the struct node of a slice-of-struct field (unwrapping
// an element pointer), and whether the field is one.
func sliceStructElem(fi FieldInfo) (spkStruct, bool) {
	s, ok := fi.Type.(spkSlice)
	if !ok {
		return spkStruct{}, false
	}
	elem := s.Elem
	if p, ok := elem.(spkPtr); ok {
		elem = p.Elem
	}
	st, ok := elem.(spkStruct)
	return st, ok
}

// isSamePackageSliceStruct reports whether a field is a slice of a struct
// declared in the same package (rendered via a generated Handle type). A
// cross-package element is a delegated node, not a struct, so it never matches;
// an inline cross-package struct (via an unexported-target alias) has a
// dot-qualified name and is excluded.
func isSamePackageSliceStruct(fi FieldInfo) bool {
	st, ok := sliceStructElem(fi)
	return ok && !strings.Contains(st.TypeName, ".")
}

// sliceStructName is the element struct's type name for a same-package
// slice-struct field (the basis of its generated Handle type name).
func sliceStructName(fi FieldInfo) string {
	st, _ := sliceStructElem(fi)
	return st.TypeName
}

// collectImportPaths returns the cross-package import paths referenced by the
// generated code for these structs (for the generated file's import block).
func collectImportPaths(structs []StructInfo) []string {
	seen := make(map[string]bool)
	for _, si := range structs {
		collectFieldImports(si.Fields, seen)
	}
	var paths []string
	for p := range seen {
		paths = append(paths, p)
	}
	return paths
}

func collectFieldImports(fields []FieldInfo, seen map[string]bool) {
	for _, fi := range fields {
		collectTypeImports(fi.Type, seen)
	}
}

// collectTypeImports records every import path a TypeExpr references, recursing
// into composites and struct InnerInfo. Delegated nodes contribute their own
// import but are not recursed: the target package's generated code owns its
// inner imports.
func collectTypeImports(te spkType, seen map[string]bool) {
	switch t := te.(type) {
	case spkScalar:
		if t.ImportPath != "" {
			seen[t.ImportPath] = true
		}
	case spkPtr:
		collectTypeImports(t.Elem, seen)
	case spkSlice:
		if t.ImportPath != "" {
			seen[t.ImportPath] = true
		}
		collectTypeImports(t.Elem, seen)
	case spkMap:
		if t.ImportPath != "" {
			seen[t.ImportPath] = true
		}
		collectTypeImports(t.Elem, seen)
	case spkStruct:
		if t.ImportPath != "" {
			seen[t.ImportPath] = true
		}
		if t.InnerInfo != nil {
			collectFieldImports(t.InnerInfo.Fields, seen)
		}
	case spkDelegated:
		if t.ImportPath != "" {
			seen[t.ImportPath] = true
		}
	}
}

// extractInfo describes the cst.Extract* function (and optional cast) used to
// pull a value of a given Go type out of the CST during decode.
type extractInfo struct {
	fn   string // e.g. "ExtractInt64"
	cast string // e.g. "int16" or "" if no cast needed
}

func cstExtract(typeName string) extractInfo {
	switch typeName {
	case "string":
		return extractInfo{fn: "ExtractString"}
	case "int":
		return extractInfo{fn: "ExtractInt"}
	case "int64":
		return extractInfo{fn: "ExtractInt64"}
	case "int8":
		return extractInfo{fn: "ExtractInt64", cast: "int8"}
	case "int16":
		return extractInfo{fn: "ExtractInt64", cast: "int16"}
	case "int32":
		return extractInfo{fn: "ExtractInt64", cast: "int32"}
	case "uint":
		return extractInfo{fn: "ExtractUint64", cast: "uint"}
	case "uint8":
		return extractInfo{fn: "ExtractUint64", cast: "uint8"}
	case "uint16":
		return extractInfo{fn: "ExtractUint64", cast: "uint16"}
	case "uint32":
		return extractInfo{fn: "ExtractUint64", cast: "uint32"}
	case "uint64":
		return extractInfo{fn: "ExtractUint64"}
	case "float32":
		return extractInfo{fn: "ExtractFloat64", cast: "float32"}
	case "float64":
		return extractInfo{fn: "ExtractFloat64"}
	case "bool":
		return extractInfo{fn: "ExtractBool"}
	default:
		return extractInfo{fn: "ExtractString"}
	}
}

// cstSliceExtractFunc maps a primitive slice element type to the cst extractor
// whose return slice type matches it directly (no per-element cast). Sized ints
// (int8/16/32, uint/8/16/32) and float32 are NOT here yet — they need a casting
// decode loop (tracked with #96); they currently fall through to the string
// extractor, which only matches string elements.
func cstSliceExtractFunc(elemType string) string {
	switch elemType {
	case "string":
		return "ExtractStringSlice"
	case "int":
		return "ExtractIntSlice"
	case "int64":
		return "ExtractInt64Slice"
	case "uint64":
		return "ExtractUint64Slice"
	case "float64":
		return "ExtractFloat64Slice"
	case "bool":
		return "ExtractBoolSlice"
	default:
		return "ExtractStringSlice"
	}
}
