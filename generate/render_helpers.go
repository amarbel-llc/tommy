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

// isSamePackageSliceStruct reports whether a field is a slice of a struct
// declared in the same package (rendered via a generated Handle type).
func isSamePackageSliceStruct(fi FieldInfo) bool {
	return fi.Kind == FieldSliceStruct && !strings.Contains(fi.TypeName, ".")
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
		if fi.ImportPath != "" {
			seen[fi.ImportPath] = true
		}
		// Don't recurse into delegated fields — their inner imports are
		// handled by the target package's generated code, not ours.
		if fi.InnerInfo != nil && fi.Kind != FieldDelegatedStruct && fi.Kind != FieldPointerDelegatedStruct && fi.Kind != FieldSliceDelegatedStruct && fi.Kind != FieldMapStringDelegatedStruct {
			collectFieldImports(fi.InnerInfo.Fields, seen)
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

func cstSliceExtractFunc(elemType string) string {
	switch elemType {
	case "string":
		return "ExtractStringSlice"
	case "int":
		return "ExtractIntSlice"
	default:
		return "ExtractStringSlice"
	}
}
