package generate

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

// FieldKind classifies how a struct field should be encoded/decoded.
type FieldKind int

const (
	FieldPrimitive        FieldKind = iota // string, int, int64, float64, bool
	FieldStruct                            // nested struct with toml tags
	FieldPointerStruct                     // *SomeStruct
	FieldSlicePrimitive                    // []int, []string
	FieldSliceStruct                       // []Server (array-of-tables)
	FieldCustom                            // implements TOMLUnmarshaler
	FieldPointerPrimitive                  // *bool, *int, etc.
	FieldMapStringString                   // map[string]string
	FieldTextMarshaler                     // implements encoding.TextMarshaler/TextUnmarshaler
	FieldMapStringStruct                   // map[string]SomeStruct
	FieldSliceTextMarshaler                // []TextMarshalerType
	FieldMapStringMapStringString           // map[string]NamedMap where NamedMap is map[string]string
	FieldDelegatedStruct                   // cross-package struct — delegate to its DecodeInto/EncodeFrom
	FieldPointerDelegatedStruct            // pointer to cross-package struct — delegate
	FieldSliceDelegatedStruct              // []cross-package struct — delegate per element
)

// StructInfo describes a struct that needs code generation.
type StructInfo struct {
	Name        string
	Fields      []FieldInfo
	Validatable bool
}

// FieldInfo describes a single field within a struct.
type FieldInfo struct {
	GoName     string
	TomlKey    string
	Kind       FieldKind
	ElemType   string // wrapper type name for primitive aliases (e.g., "types.Version")
	TypeName   string
	InnerInfo  *StructInfo
	OmitEmpty  bool
	Multiline  bool
	ImportPath   string // import path needed for ElemType (e.g., "example.com/pkg/types")
	SlicePointer bool   // true when slice element is a pointer (e.g., []*Struct)
}

// Analyze inspects the given Go source file for structs with
// //go:generate tommy generate directives and returns their metadata.
func Analyze(dir, filename string) ([]StructInfo, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedSyntax |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles,
		Dir:  dir,
		Fset: token.NewFileSet(),
	}

	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, fmt.Errorf("loading package: %w", err)
	}

	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no packages found in %s", dir)
	}

	pkg := pkgs[0]
	if len(pkg.Errors) > 0 {
		return nil, fmt.Errorf("package errors: %v", pkg.Errors[0])
	}

	var targetFile *ast.File
	for i, f := range pkg.CompiledGoFiles {
		if strings.HasSuffix(f, filename) {
			targetFile = pkg.Syntax[i]
			break
		}
	}
	if targetFile == nil {
		return nil, fmt.Errorf("file %s not found in package", filename)
	}

	var infos []StructInfo

	for _, decl := range targetFile.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}

		if !hasGenerateDirective(pkg.Fset, targetFile, genDecl) {
			continue
		}

		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}

			si, err := analyzeStruct(pkg, typeSpec.Name.Name, structType)
			if err != nil {
				return nil, err
			}
			infos = append(infos, si)
		}
	}

	return infos, nil
}

func hasGenerateDirective(fset *token.FileSet, file *ast.File, decl *ast.GenDecl) bool {
	declLine := fset.Position(decl.Pos()).Line
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			commentLine := fset.Position(c.Pos()).Line
			if commentLine == declLine-1 &&
				strings.Contains(c.Text, "//go:generate tommy generate") {
				return true
			}
		}
	}
	return false
}

func analyzeStruct(pkg *packages.Package, name string, st *ast.StructType) (StructInfo, error) {
	si := StructInfo{Name: name}

	for _, field := range st.Fields.List {
		// Handle embedded (anonymous) fields — promote their tagged fields.
		if len(field.Names) == 0 {
			embeddedFields, err := resolveEmbeddedFields(pkg, field.Type)
			if err != nil {
				return si, fmt.Errorf("embedded field in %s: %w", name, err)
			}
			si.Fields = append(si.Fields, embeddedFields...)
			continue
		}

		if field.Tag == nil {
			continue
		}
		tomlKey, opts := extractTomlTag(field.Tag.Value)
		if tomlKey == "" {
			continue
		}

		for _, ident := range field.Names {
			if ident.Name == "_" {
				continue
			}
			fi, err := classifyField(pkg, ident.Name, tomlKey, field.Type)
			if err != nil {
				return si, fmt.Errorf("field %s.%s: %w", name, ident.Name, err)
			}
			fi.OmitEmpty = opts.omitEmpty
			fi.Multiline = opts.multiline
			si.Fields = append(si.Fields, fi)
		}
	}

	obj := pkg.Types.Scope().Lookup(name)
	if obj != nil {
		si.Validatable = hasMethod(obj, "Validate")
	}

	return si, nil
}

type tagOpts struct {
	omitEmpty bool
	multiline bool
}

func extractTomlTag(raw string) (string, tagOpts) {
	tag := strings.Trim(raw, "`")
	idx := strings.Index(tag, `toml:"`)
	if idx < 0 {
		return "", tagOpts{}
	}
	rest := tag[idx+6:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return "", tagOpts{}
	}
	full := rest[:end]
	name, remainder, _ := strings.Cut(full, ",")
	if name == "-" {
		return "", tagOpts{}
	}
	var opts tagOpts
	for remainder != "" {
		var opt string
		opt, remainder, _ = strings.Cut(remainder, ",")
		if opt == "omitempty" {
			opts.omitEmpty = true
		}
		if opt == "multiline" {
			opts.multiline = true
		}
	}
	return name, opts
}

var primitiveTypes = map[string]bool{
	"string":  true,
	"int":     true,
	"int64":   true,
	"uint64":  true,
	"float64": true,
	"bool":    true,
}

func classifyField(pkg *packages.Package, goName, tomlKey string, expr ast.Expr) (FieldInfo, error) {
	fi := FieldInfo{GoName: goName, TomlKey: tomlKey}

	switch t := expr.(type) {
	case *ast.Ident:
		if primitiveTypes[t.Name] {
			fi.Kind = FieldPrimitive
			fi.TypeName = t.Name
			return fi, nil
		}
		return classifyNamedType(pkg, fi, t.Name)

	case *ast.StarExpr:
		switch inner := t.X.(type) {
		case *ast.Ident:
			if primitiveTypes[inner.Name] {
				fi.Kind = FieldPointerPrimitive
				fi.TypeName = inner.Name
				return fi, nil
			}
			fi.Kind = FieldPointerStruct
			fi.TypeName = inner.Name
			innerInfo, err := resolveStructByName(pkg, inner.Name)
			if err != nil {
				return fi, err
			}
			fi.InnerInfo = &innerInfo
			return fi, nil
		case *ast.SelectorExpr:
			// *pkg.Type — resolve via type info and classify
			result, err := classifySelectorExpr(pkg, fi, inner)
			if err != nil {
				return result, err
			}
			if result.Kind == FieldStruct {
				result.Kind = FieldPointerStruct
			} else if result.Kind == FieldDelegatedStruct {
				result.Kind = FieldPointerDelegatedStruct
			}
			return result, nil
		default:
			return fi, fmt.Errorf("unsupported pointer type")
		}

	case *ast.ArrayType:
		switch elem := t.Elt.(type) {
		case *ast.Ident:
			fi.ElemType = elem.Name
			if primitiveTypes[elem.Name] {
				fi.Kind = FieldSlicePrimitive
				return fi, nil
			}
			obj := pkg.Types.Scope().Lookup(elem.Name)
			if obj != nil && hasMethod(obj, "MarshalText") && hasMethod(obj, "UnmarshalText") {
				fi.Kind = FieldSliceTextMarshaler
				fi.TypeName = elem.Name
				return fi, nil
			}
			fi.Kind = FieldSliceStruct
			fi.TypeName = elem.Name
			innerInfo, err := resolveStructByName(pkg, elem.Name)
			if err != nil {
				return fi, err
			}
			fi.InnerInfo = &innerInfo
			return fi, nil
		case *ast.SelectorExpr:
			// []pkg.Type — resolve via type info
			obj := pkg.TypesInfo.Uses[elem.Sel]
			if obj == nil {
				return fi, fmt.Errorf("cannot resolve slice element type %s", elem.Sel.Name)
			}
			qualifiedName := elem.X.(*ast.Ident).Name + "." + elem.Sel.Name
			if hasMethod(obj, "MarshalText") && hasMethod(obj, "UnmarshalText") {
				fi.Kind = FieldSliceTextMarshaler
				fi.TypeName = qualifiedName
				fi.ElemType = qualifiedName
				if p := obj.Pkg(); p != nil {
					fi.ImportPath = p.Path()
				}
				return fi, nil
			}
			// Cross-package struct: delegate or inline depending on exportedness
			typ := types.Unalias(obj.Type())
			named, ok := typ.(*types.Named)
			if !ok {
				return fi, fmt.Errorf("cross-package slice element type %s is not a struct or TextMarshaler", qualifiedName)
			}
			structType, ok := named.Underlying().(*types.Struct)
			if !ok {
				return fi, fmt.Errorf("cross-package slice element type %s is not a struct", qualifiedName)
			}
			fi.TypeName = qualifiedName
			fi.ElemType = qualifiedName
			fi.ImportPath = named.Obj().Pkg().Path()
			if named.Obj().Exported() {
				// Exported type: delegate to its DecodeInto/EncodeFrom
				fi.Kind = FieldSliceDelegatedStruct
			} else {
				// Unexported type accessed via exported alias: inline fields
				fi.Kind = FieldSliceStruct
				resolvedInfo, err := resolveStructFromTypes(pkg, named.Obj().Name(), structType)
				if err != nil {
					return fi, err
				}
				fi.InnerInfo = &resolvedInfo
			}
			return fi, nil
		case *ast.StarExpr:
			switch inner := elem.X.(type) {
			case *ast.Ident:
				fi.Kind = FieldSliceStruct
				fi.TypeName = inner.Name
				fi.SlicePointer = true
				innerInfo, err := resolveStructByName(pkg, inner.Name)
				if err != nil {
					return fi, err
				}
				fi.InnerInfo = &innerInfo
				return fi, nil
			case *ast.SelectorExpr:
				obj := pkg.TypesInfo.Uses[inner.Sel]
				if obj == nil {
					return fi, fmt.Errorf("cannot resolve slice element type %s", inner.Sel.Name)
				}
				named, ok := types.Unalias(obj.Type()).(*types.Named)
				if !ok {
					return fi, fmt.Errorf("slice pointer element type %s is not a named type", inner.Sel.Name)
				}
				if _, ok := named.Underlying().(*types.Struct); !ok {
					return fi, fmt.Errorf("slice pointer element type %s is not a struct", inner.Sel.Name)
				}
				qualifiedName := inner.X.(*ast.Ident).Name + "." + inner.Sel.Name
				fi.TypeName = qualifiedName
				fi.SlicePointer = true
				fi.ImportPath = named.Obj().Pkg().Path()
				if named.Obj().Exported() {
					fi.Kind = FieldSliceDelegatedStruct
				} else {
					fi.Kind = FieldSliceStruct
					structType := named.Underlying().(*types.Struct)
					resolvedInfo, err := resolveStructFromTypes(pkg, named.Obj().Name(), structType)
					if err != nil {
						return fi, err
					}
					fi.InnerInfo = &resolvedInfo
				}
				return fi, nil
			default:
				return fi, fmt.Errorf("unsupported pointer slice element type")
			}
		default:
			return fi, fmt.Errorf("unsupported slice element type")
		}

	case *ast.SelectorExpr:
		return classifySelectorExpr(pkg, fi, t)

	case *ast.MapType:
		keyIdent, ok := t.Key.(*ast.Ident)
		if !ok || keyIdent.Name != "string" {
			return fi, fmt.Errorf("unsupported map key type (only string keys supported)")
		}
		switch val := t.Value.(type) {
		case *ast.Ident:
			if val.Name == "string" {
				fi.Kind = FieldMapStringString
				return fi, nil
			}
			// Check if the named type is a map[string]string alias
			obj := pkg.Types.Scope().Lookup(val.Name)
			if obj != nil {
				if named, ok := obj.Type().(*types.Named); ok {
					if mapType, ok := named.Underlying().(*types.Map); ok {
						if isMapStringString(mapType) {
							fi.Kind = FieldMapStringMapStringString
							fi.TypeName = val.Name
							return fi, nil
						}
					}
				}
			}
			// map[string]Struct (same package)
			fi.Kind = FieldMapStringStruct
			fi.TypeName = val.Name
			innerInfo, err := resolveStructByName(pkg, val.Name)
			if err != nil {
				return fi, err
			}
			fi.InnerInfo = &innerInfo
			return fi, nil
		case *ast.SelectorExpr:
			// map[string]pkg.Type — resolve via type info
			obj := pkg.TypesInfo.Uses[val.Sel]
			if obj == nil {
				return fi, fmt.Errorf("cannot resolve map value type %s", val.Sel.Name)
			}
			named, ok := types.Unalias(obj.Type()).(*types.Named)
			if !ok {
				return fi, fmt.Errorf("map value type %s is not a named type", val.Sel.Name)
			}
			// Check if it's a named map[string]string alias
			if mapType, ok := named.Underlying().(*types.Map); ok {
				if isMapStringString(mapType) {
					qualifiedName := val.X.(*ast.Ident).Name + "." + val.Sel.Name
					fi.Kind = FieldMapStringMapStringString
					fi.TypeName = qualifiedName
					fi.ImportPath = named.Obj().Pkg().Path()
					return fi, nil
				}
			}
			if _, ok := named.Underlying().(*types.Interface); ok {
				return fi, fmt.Errorf("map value type %s.%s is an interface, which cannot be statically decoded; use `toml:\"-\"` to skip this field, or define a concrete struct that mirrors the TOML shape and convert to the interface after decoding",
					val.X.(*ast.Ident).Name, val.Sel.Name)
			}
			structType, ok := named.Underlying().(*types.Struct)
			if !ok {
				return fi, fmt.Errorf("cross-package map value type %s.%s is not a struct",
					val.X.(*ast.Ident).Name, val.Sel.Name)
			}
			qualifiedName := val.X.(*ast.Ident).Name + "." + val.Sel.Name
			fi.Kind = FieldMapStringStruct
			fi.TypeName = qualifiedName
			fi.ImportPath = named.Obj().Pkg().Path()
			innerInfo, err := resolveStructFromTypes(pkg, val.Sel.Name, structType)
			if err != nil {
				return fi, err
			}
			fi.InnerInfo = &innerInfo
			return fi, nil
		default:
			return fi, fmt.Errorf("unsupported map value type")
		}

	default:
		return fi, fmt.Errorf("unsupported type %T", expr)
	}
}

func hasMethod(obj types.Object, name string) bool {
	for _, typ := range []types.Type{obj.Type(), types.NewPointer(obj.Type())} {
		mset := types.NewMethodSet(typ)
		for i := range mset.Len() {
			if mset.At(i).Obj().Name() == name {
				return true
			}
		}
	}
	return false
}

func hasMarshalTOML(obj types.Object) bool {
	for _, typ := range []types.Type{obj.Type(), types.NewPointer(obj.Type())} {
		mset := types.NewMethodSet(typ)
		for i := range mset.Len() {
			if mset.At(i).Obj().Name() == "MarshalTOML" {
				return true
			}
		}
	}
	return false
}

func classifySelectorExpr(pkg *packages.Package, fi FieldInfo, sel *ast.SelectorExpr) (FieldInfo, error) {
	obj := pkg.TypesInfo.Uses[sel.Sel]
	if obj == nil {
		return fi, fmt.Errorf("cannot resolve type %s", sel.Sel.Name)
	}
	qualifiedName := sel.X.(*ast.Ident).Name + "." + sel.Sel.Name
	fi.TypeName = qualifiedName
	if hasMethod(obj, "MarshalText") && hasMethod(obj, "UnmarshalText") {
		fi.Kind = FieldTextMarshaler
		return fi, nil
	}
	if hasMethod(obj, "UnmarshalText") {
		return fi, fmt.Errorf("type %s has UnmarshalText but no MarshalText — Encode() requires both", qualifiedName)
	}
	if hasMethod(obj, "MarshalText") {
		return fi, fmt.Errorf("type %s has MarshalText but no UnmarshalText — Decode() requires both", qualifiedName)
	}

	// Delegate to go/types-based classification for all other cases
	// (structs, slice aliases, primitive wrappers, etc.)
	return classifyFromType(pkg, fi.GoName, fi.TomlKey, obj.Type())
}

func classifyNamedType(pkg *packages.Package, fi FieldInfo, typeName string) (FieldInfo, error) {
	obj := pkg.Types.Scope().Lookup(typeName)
	if obj == nil {
		return fi, fmt.Errorf("type %s not found", typeName)
	}

	// Check pointer receiver methods for UnmarshalTOML
	ptrType := types.NewPointer(obj.Type())
	mset := types.NewMethodSet(ptrType)
	for i := range mset.Len() {
		if mset.At(i).Obj().Name() == "UnmarshalTOML" {
			if !hasMarshalTOML(obj) {
				return fi, fmt.Errorf("type %s has UnmarshalTOML but no MarshalTOML — Encode() requires both", typeName)
			}
			fi.Kind = FieldCustom
			fi.TypeName = typeName
			return fi, nil
		}
	}

	// Check value receiver methods too
	vmset := types.NewMethodSet(obj.Type())
	for i := range vmset.Len() {
		if vmset.At(i).Obj().Name() == "UnmarshalTOML" {
			if !hasMarshalTOML(obj) {
				return fi, fmt.Errorf("type %s has UnmarshalTOML but no MarshalTOML — Encode() requires both", typeName)
			}
			fi.Kind = FieldCustom
			fi.TypeName = typeName
			return fi, nil
		}
	}

	if hasMarshalTOML(obj) {
		return fi, fmt.Errorf("type %s has MarshalTOML but no UnmarshalTOML — Decode() requires both", typeName)
	}

	// Check for encoding.TextMarshaler/TextUnmarshaler
	hasUnmarshalText := hasMethod(obj, "UnmarshalText")
	hasMarshalText := hasMethod(obj, "MarshalText")
	if hasUnmarshalText && hasMarshalText {
		fi.Kind = FieldTextMarshaler
		fi.TypeName = typeName
		return fi, nil
	}
	if hasUnmarshalText && !hasMarshalText {
		return fi, fmt.Errorf("type %s has UnmarshalText but no MarshalText — Encode() requires both", typeName)
	}
	if hasMarshalText && !hasUnmarshalText {
		return fi, fmt.Errorf("type %s has MarshalText but no UnmarshalText — Decode() requires both", typeName)
	}

	fi.Kind = FieldStruct
	fi.TypeName = typeName
	innerInfo, err := resolveStructByName(pkg, typeName)
	if err != nil {
		return fi, err
	}
	fi.InnerInfo = &innerInfo
	return fi, nil
}

func resolveEmbeddedFields(pkg *packages.Package, expr ast.Expr) ([]FieldInfo, error) {
	switch t := expr.(type) {
	case *ast.Ident:
		si, err := resolveStructByName(pkg, t.Name)
		if err != nil {
			return nil, fmt.Errorf("resolving embedded %s: %w", t.Name, err)
		}
		return si.Fields, nil

	case *ast.StarExpr:
		switch inner := t.X.(type) {
		case *ast.Ident:
			si, err := resolveStructByName(pkg, inner.Name)
			if err != nil {
				return nil, fmt.Errorf("resolving embedded *%s: %w", inner.Name, err)
			}
			return si.Fields, nil
		case *ast.SelectorExpr:
			return resolveCrossPackageEmbedded(pkg, inner)
		default:
			return nil, fmt.Errorf("unsupported embedded pointer type %T", inner)
		}

	case *ast.SelectorExpr:
		return resolveCrossPackageEmbedded(pkg, t)

	default:
		return nil, fmt.Errorf("unsupported embedded type %T", expr)
	}
}

func resolveCrossPackageEmbedded(pkg *packages.Package, sel *ast.SelectorExpr) ([]FieldInfo, error) {
	obj := pkg.TypesInfo.Uses[sel.Sel]
	if obj == nil {
		return nil, fmt.Errorf("cannot resolve type %s.%s", sel.X.(*ast.Ident).Name, sel.Sel.Name)
	}

	named, ok := types.Unalias(obj.Type()).(*types.Named)
	if !ok {
		return nil, fmt.Errorf("%s is not a named type", sel.Sel.Name)
	}

	structType, ok := named.Underlying().(*types.Struct)
	if !ok {
		return nil, fmt.Errorf("%s is not a struct type", sel.Sel.Name)
	}

	si, err := resolveStructFromTypes(pkg, sel.Sel.Name, structType)
	if err != nil {
		return nil, fmt.Errorf("resolving embedded %s.%s: %w",
			sel.X.(*ast.Ident).Name, sel.Sel.Name, err)
	}
	return si.Fields, nil
}

func resolveStructFromTypes(pkg *packages.Package, name string, st *types.Struct) (StructInfo, error) {
	si := StructInfo{Name: name}

	for i := range st.NumFields() {
		field := st.Field(i)

		if field.Anonymous() {
			fields, err := resolveEmbeddedFieldFromType(pkg, field)
			if err != nil {
				return si, fmt.Errorf("embedded field in %s: %w", name, err)
			}
			si.Fields = append(si.Fields, fields...)
			continue
		}

		if field.Name() == "_" {
			continue
		}

		tag := st.Tag(i)
		tomlKey, opts := extractTomlTagFromString(tag)
		if tomlKey == "" {
			continue
		}

		fi, err := classifyFromType(pkg, field.Name(), tomlKey, field.Type())
		if err != nil {
			return si, fmt.Errorf("field %s.%s: %w", name, field.Name(), err)
		}
		fi.OmitEmpty = opts.omitEmpty
		fi.Multiline = opts.multiline
		si.Fields = append(si.Fields, fi)
	}

	return si, nil
}

func resolveEmbeddedFieldFromType(pkg *packages.Package, field *types.Var) ([]FieldInfo, error) {
	typ := field.Type()

	// Unwrap pointer
	if ptr, ok := typ.(*types.Pointer); ok {
		typ = ptr.Elem()
	}

	named, ok := typ.(*types.Named)
	if !ok {
		return nil, fmt.Errorf("unsupported embedded type %s", typ)
	}

	// Same package — fall back to AST-based resolution
	if named.Obj().Pkg() == pkg.Types {
		si, err := resolveStructByName(pkg, named.Obj().Name())
		if err != nil {
			return nil, err
		}
		return si.Fields, nil
	}

	// Cross-package
	structType, ok := named.Underlying().(*types.Struct)
	if !ok {
		return nil, fmt.Errorf("%s is not a struct type", named.Obj().Name())
	}

	si, err := resolveStructFromTypes(pkg, named.Obj().Name(), structType)
	if err != nil {
		return nil, err
	}
	return si.Fields, nil
}

func extractTomlTagFromString(tag string) (string, tagOpts) {
	idx := strings.Index(tag, `toml:"`)
	if idx < 0 {
		return "", tagOpts{}
	}
	rest := tag[idx+6:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return "", tagOpts{}
	}
	full := rest[:end]
	name, remainder, _ := strings.Cut(full, ",")
	if name == "-" {
		return "", tagOpts{}
	}
	var opts tagOpts
	for remainder != "" {
		var opt string
		opt, remainder, _ = strings.Cut(remainder, ",")
		if opt == "omitempty" {
			opts.omitEmpty = true
		}
		if opt == "multiline" {
			opts.multiline = true
		}
	}
	return name, opts
}

func classifyFromType(pkg *packages.Package, goName, tomlKey string, typ types.Type) (FieldInfo, error) {
	fi := FieldInfo{GoName: goName, TomlKey: tomlKey}

	switch t := typ.(type) {
	case *types.Basic:
		if primitiveTypes[t.Name()] {
			fi.Kind = FieldPrimitive
			fi.TypeName = t.Name()
			return fi, nil
		}
		return fi, fmt.Errorf("unsupported basic type %s", t.Name())

	case *types.Named:
		obj := t.Obj()

		// Same package — delegate to AST-based classification
		if obj.Pkg() == pkg.Types {
			return classifyNamedType(pkg, fi, obj.Name())
		}

		// Cross-package: check marshal interfaces
		if hasMethod(obj, "UnmarshalTOML") && hasMarshalTOML(obj) {
			fi.Kind = FieldCustom
			fi.TypeName = obj.Pkg().Name() + "." + obj.Name()
			return fi, nil
		}

		if hasMethod(obj, "MarshalText") && hasMethod(obj, "UnmarshalText") {
			fi.Kind = FieldTextMarshaler
			fi.TypeName = obj.Pkg().Name() + "." + obj.Name()
			return fi, nil
		}

		// Check underlying type for primitive wrapper
		if basic, ok := t.Underlying().(*types.Basic); ok {
			if primitiveTypes[basic.Name()] {
				fi.Kind = FieldPrimitive
				fi.TypeName = basic.Name()
				fi.ElemType = obj.Pkg().Name() + "." + obj.Name()
				fi.ImportPath = obj.Pkg().Path()
				return fi, nil
			}
		}

		// Check if underlying is struct — delegate to cross-package DecodeInto/EncodeFrom
		if structType, ok := t.Underlying().(*types.Struct); ok {
			fi.Kind = FieldDelegatedStruct
			fi.TypeName = obj.Pkg().Name() + "." + obj.Name()
			fi.ImportPath = obj.Pkg().Path()
			innerInfo, err := resolveStructFromTypes(pkg, obj.Name(), structType)
			if err != nil {
				return fi, err
			}
			fi.InnerInfo = &innerInfo
			return fi, nil
		}

		// Check if underlying is a slice (e.g., type IntSlice []int)
		if sliceType, ok := t.Underlying().(*types.Slice); ok {
			qualifiedName := obj.Pkg().Name() + "." + obj.Name()
			elem := sliceType.Elem()
			if basic, ok := elem.(*types.Basic); ok && primitiveTypes[basic.Name()] {
				fi.Kind = FieldSlicePrimitive
				fi.ElemType = basic.Name()
				fi.TypeName = qualifiedName
				fi.ImportPath = obj.Pkg().Path()
				return fi, nil
			}
			return fi, fmt.Errorf("unsupported cross-package slice alias %s (element type %s)", qualifiedName, elem)
		}

		return fi, fmt.Errorf("unsupported cross-package type %s.%s", obj.Pkg().Name(), obj.Name())

	case *types.Pointer:
		elem := t.Elem()
		if basic, ok := elem.(*types.Basic); ok {
			if primitiveTypes[basic.Name()] {
				fi.Kind = FieldPointerPrimitive
				fi.TypeName = basic.Name()
				return fi, nil
			}
		}
		if named, ok := elem.(*types.Named); ok {
			innerFi, err := classifyFromType(pkg, goName, tomlKey, named)
			if err != nil {
				return fi, err
			}
			// Upgrade to pointer variant where applicable
			if innerFi.Kind == FieldStruct {
				innerFi.Kind = FieldPointerStruct
			} else if innerFi.Kind == FieldDelegatedStruct {
				innerFi.Kind = FieldPointerDelegatedStruct
			}
			return innerFi, nil
		}
		return fi, fmt.Errorf("unsupported pointer type")

	case *types.Slice:
		elem := t.Elem()
		if basic, ok := elem.(*types.Basic); ok {
			if primitiveTypes[basic.Name()] {
				fi.Kind = FieldSlicePrimitive
				fi.ElemType = basic.Name()
				return fi, nil
			}
		}
		if named, ok := elem.(*types.Named); ok {
			obj := named.Obj()
			qualifiedName := obj.Pkg().Name() + "." + obj.Name()
			if obj.Pkg() == pkg.Types {
				qualifiedName = obj.Name()
			}

			if hasMethod(obj, "MarshalText") && hasMethod(obj, "UnmarshalText") {
				fi.Kind = FieldSliceTextMarshaler
				fi.TypeName = qualifiedName
				fi.ElemType = qualifiedName
				if obj.Pkg() != pkg.Types {
					fi.ImportPath = obj.Pkg().Path()
				}
				return fi, nil
			}

			if _, ok := named.Underlying().(*types.Struct); ok {
				if obj.Pkg() == pkg.Types {
					fi.Kind = FieldSliceStruct
					fi.TypeName = qualifiedName
					innerInfo, err := resolveStructByName(pkg, obj.Name())
					if err != nil {
						return fi, err
					}
					fi.InnerInfo = &innerInfo
				} else {
					fi.Kind = FieldSliceDelegatedStruct
					fi.TypeName = qualifiedName
					fi.ImportPath = obj.Pkg().Path()
				}
				return fi, nil
			}
		}
		if ptr, ok := elem.(*types.Pointer); ok {
			if named, ok := ptr.Elem().(*types.Named); ok {
				obj := named.Obj()
				qualifiedName := obj.Pkg().Name() + "." + obj.Name()
				if obj.Pkg() == pkg.Types {
					qualifiedName = obj.Name()
				}

				if _, ok := named.Underlying().(*types.Struct); ok {
					fi.TypeName = qualifiedName
					fi.SlicePointer = true
					if obj.Pkg() == pkg.Types {
						fi.Kind = FieldSliceStruct
						innerInfo, err := resolveStructByName(pkg, obj.Name())
						if err != nil {
							return fi, err
						}
						fi.InnerInfo = &innerInfo
					} else {
						fi.Kind = FieldSliceDelegatedStruct
						fi.ImportPath = obj.Pkg().Path()
					}
					return fi, nil
				}
			}
		}
		// Unwrap type aliases (e.g., type TagStruct = tagStruct)
		if alias, ok := elem.(*types.Alias); ok {
			underlying := types.Unalias(alias)
			named, ok := underlying.(*types.Named)
			if !ok {
				return classifyFromType(pkg, goName, tomlKey, types.NewSlice(underlying))
			}
			obj := named.Obj()
			// Use the alias name (exported) rather than the underlying name (possibly unexported)
			qualifiedName := alias.Obj().Pkg().Name() + "." + alias.Obj().Name()
			if alias.Obj().Pkg() == pkg.Types {
				qualifiedName = alias.Obj().Name()
			}
			if hasMethod(obj, "MarshalText") && hasMethod(obj, "UnmarshalText") {
				fi.Kind = FieldSliceTextMarshaler
				fi.TypeName = qualifiedName
				fi.ElemType = qualifiedName
				if alias.Obj().Pkg() != pkg.Types {
					fi.ImportPath = alias.Obj().Pkg().Path()
				}
				return fi, nil
			}
			if structType, ok := named.Underlying().(*types.Struct); ok {
				fi.TypeName = qualifiedName
				fi.ImportPath = alias.Obj().Pkg().Path()
				if obj.Exported() {
					fi.Kind = FieldSliceDelegatedStruct
				} else {
					fi.Kind = FieldSliceStruct
					resolvedInfo, err := resolveStructFromTypes(pkg, obj.Name(), structType)
					if err != nil {
						return fi, err
					}
					fi.InnerInfo = &resolvedInfo
				}
				return fi, nil
			}
			return classifyFromType(pkg, goName, tomlKey, types.NewSlice(underlying))
		}
		return fi, fmt.Errorf("unsupported slice element type")

	case *types.Map:
		key, ok := t.Key().(*types.Basic)
		if !ok || key.Kind() != types.String {
			return fi, fmt.Errorf("unsupported map key type (only string keys supported)")
		}
		if val, ok := t.Elem().(*types.Basic); ok && val.Kind() == types.String {
			fi.Kind = FieldMapStringString
			return fi, nil
		}
		if named, ok := t.Elem().(*types.Named); ok {
			if mapType, ok := named.Underlying().(*types.Map); ok && isMapStringString(mapType) {
				obj := named.Obj()
				qualifiedName := obj.Pkg().Name() + "." + obj.Name()
				if obj.Pkg() == pkg.Types {
					qualifiedName = obj.Name()
				}
				fi.Kind = FieldMapStringMapStringString
				fi.TypeName = qualifiedName
				if obj.Pkg() != pkg.Types {
					fi.ImportPath = obj.Pkg().Path()
				}
				return fi, nil
			}
		}
		if _, ok := t.Elem().Underlying().(*types.Interface); ok {
			return fi, fmt.Errorf("map value type is an interface, which cannot be statically decoded; use `toml:\"-\"` to skip this field, or define a concrete struct that mirrors the TOML shape and convert to the interface after decoding")
		}
		return fi, fmt.Errorf("unsupported map value type")

	case *types.Alias:
		return classifyFromType(pkg, goName, tomlKey, types.Unalias(t))

	default:
		return fi, fmt.Errorf("unsupported type %T", typ)
	}
}

func isMapStringString(m *types.Map) bool {
	key, ok := m.Key().(*types.Basic)
	if !ok || key.Kind() != types.String {
		return false
	}
	val, ok := m.Elem().(*types.Basic)
	return ok && val.Kind() == types.String
}

func resolveStructByName(pkg *packages.Package, name string) (StructInfo, error) {
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if typeSpec.Name.Name != name {
					continue
				}
				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					// Follow type aliases (e.g. type Alias = inner)
					if typeSpec.Assign.IsValid() {
						if ident, ok := typeSpec.Type.(*ast.Ident); ok {
							return resolveStructByName(pkg, ident.Name)
						}
					}
					return StructInfo{}, fmt.Errorf("%s is not a struct", name)
				}
				return analyzeStruct(pkg, name, structType)
			}
		}
	}
	return StructInfo{}, fmt.Errorf("struct %s not found in package", name)
}
