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
	FieldMapStringDelegatedStruct          // map[string]cross-package struct — delegate per entry
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

	resolvingTypes = make(map[string]bool)

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
			if isTomlIgnored(field) {
				continue
			}
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
			ftyp := pkg.TypesInfo.TypeOf(field.Type)
			if ftyp == nil {
				return si, fmt.Errorf("field %s.%s: cannot resolve type", name, ident.Name)
			}
			fi, err := classifyType(pkg, ident.Name, tomlKey, ftyp)
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

	// Reject delegated fields whose generated methods live in an internal/
	// package the consumer cannot import. These are emitted directly in this
	// struct's decode/encode (named fields and promoted embedded fields), so
	// catching them here fails fast with a clear message instead of producing
	// uncompilable output.
	for _, fi := range si.Fields {
		if err := checkDelegationImportable(pkg.PkgPath, fi); err != nil {
			return si, fmt.Errorf("%s.%s: %w", name, fi.GoName, err)
		}
	}

	return si, nil
}

type tagOpts struct {
	omitEmpty bool
	multiline bool
}

func isTomlIgnored(field *ast.Field) bool {
	if field.Tag == nil {
		return false
	}
	tag := strings.Trim(field.Tag.Value, "`")
	idx := strings.Index(tag, `toml:"`)
	if idx < 0 {
		return false
	}
	rest := tag[idx+6:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return false
	}
	return rest[:end] == "-"
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
	"int8":    true,
	"int16":   true,
	"int32":   true,
	"int64":   true,
	"uint":    true,
	"uint8":   true,
	"uint16":  true,
	"uint32":  true,
	"uint64":  true,
	"float32": true,
	"float64": true,
	"bool":    true,
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
		// Non-named types have no TOML fields to promote.
		return nil, nil
	}

	structType, ok := named.Underlying().(*types.Struct)
	if !ok {
		// Non-struct types (interfaces, etc.) have no TOML fields to promote.
		return nil, nil
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

		fi, err := classifyType(pkg, field.Name(), tomlKey, field.Type())
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

func classifyNamedMapType(fi FieldInfo, obj *types.TypeName, mapType *types.Map) (FieldInfo, error) {
	key, ok := mapType.Key().(*types.Basic)
	if !ok || key.Kind() != types.String {
		return fi, fmt.Errorf("unsupported map key type in %s.%s (only string keys supported)", obj.Pkg().Name(), obj.Name())
	}

	qualifiedName := obj.Pkg().Name() + "." + obj.Name()
	fi.TypeName = qualifiedName
	fi.ImportPath = obj.Pkg().Path()

	valElem := mapType.Elem()
	if basic, ok := valElem.(*types.Basic); ok && basic.Kind() == types.String {
		fi.Kind = FieldMapStringString
		return fi, nil
	}

	valNamed, ok := valElem.(*types.Named)
	if !ok {
		return fi, fmt.Errorf("unsupported cross-package map alias %s.%s (value type %s)", obj.Pkg().Name(), obj.Name(), valElem)
	}

	if _, ok := valNamed.Underlying().(*types.Struct); ok {
		fi.Kind = FieldMapStringDelegatedStruct
		fi.ElemType = valNamed.Obj().Pkg().Name() + "." + valNamed.Obj().Name()
		return fi, nil
	}

	return fi, fmt.Errorf("unsupported cross-package map value type in %s.%s (value type %s)", obj.Pkg().Name(), obj.Name(), valElem)
}

func classifyType(pkg *packages.Package, goName, tomlKey string, typ types.Type) (FieldInfo, error) {
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

		if mapType, ok := t.Underlying().(*types.Map); ok {
			return classifyNamedMapType(fi, obj, mapType)
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
			innerFi, err := classifyType(pkg, goName, tomlKey, named)
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
			pelem := ptr.Elem()
			if basic, ok := pelem.(*types.Basic); ok && primitiveTypes[basic.Name()] {
				fi.Kind = FieldSlicePrimitive
				fi.ElemType = basic.Name()
				fi.SlicePointer = true
				return fi, nil
			}
			// []*pkg.AliasType: unwrap the alias to its underlying named struct,
			// naming via the alias (mirrors the []pkg.AliasType case below).
			if alias, ok := pelem.(*types.Alias); ok {
				if named, ok := types.Unalias(alias).(*types.Named); ok {
					if structType, ok := named.Underlying().(*types.Struct); ok {
						fi.TypeName = aliasQualifiedName(pkg, alias)
						fi.SlicePointer = true
						if err := classifySliceAliasStruct(pkg, &fi, alias, named, structType); err != nil {
							return fi, err
						}
						return fi, nil
					}
				}
			}
			if named, ok := pelem.(*types.Named); ok {
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
				return classifyType(pkg, goName, tomlKey, types.NewSlice(underlying))
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
				if err := classifySliceAliasStruct(pkg, &fi, alias, named, structType); err != nil {
					return fi, err
				}
				return fi, nil
			}
			return classifyType(pkg, goName, tomlKey, types.NewSlice(underlying))
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
		// map[string]*Struct: pointer values are supported for same-package
		// structs (FieldMapStringStruct honors SlicePointer); cross-package
		// pointer-struct map values would lose their pointer-ness through the
		// delegated fold, so they are rejected rather than mis-emitted.
		if ptr, ok := t.Elem().(*types.Pointer); ok {
			named, ok := ptr.Elem().(*types.Named)
			if !ok {
				return fi, fmt.Errorf("unsupported map value type")
			}
			if _, ok := named.Underlying().(*types.Struct); !ok {
				return fi, fmt.Errorf("unsupported map value type")
			}
			obj := named.Obj()
			if obj.Pkg() != pkg.Types {
				return fi, fmt.Errorf("map[string]*%s.%s: pointer values are only supported for same-package structs", obj.Pkg().Name(), obj.Name())
			}
			fi.Kind = FieldMapStringStruct
			fi.TypeName = obj.Name()
			fi.SlicePointer = true
			innerInfo, err := resolveStructByName(pkg, obj.Name())
			if err != nil {
				return fi, err
			}
			fi.InnerInfo = &innerInfo
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
		if named, ok := t.Elem().(*types.Named); ok {
			if _, ok := named.Underlying().(*types.Struct); ok {
				obj := named.Obj()
				if obj.Pkg() == pkg.Types {
					fi.Kind = FieldMapStringStruct
					fi.TypeName = obj.Name()
					innerInfo, err := resolveStructByName(pkg, obj.Name())
					if err != nil {
						return fi, err
					}
					fi.InnerInfo = &innerInfo
				} else {
					fi.Kind = FieldMapStringDelegatedStruct
					fi.ElemType = obj.Pkg().Name() + "." + obj.Name()
					fi.ImportPath = obj.Pkg().Path()
				}
				return fi, nil
			}
		}
		// map[string]pkg.AliasType: an alias value re-exporting a named map
		// (map[string]string → nested map) or a struct. A struct alias inlines
		// when its underlying is unexported (importing the alias's facade) and
		// otherwise delegates to the defining package (rejected by
		// checkDelegationImportable when that package is an unreachable internal/
		// one). See #43/#44 and #81.
		if alias, ok := t.Elem().(*types.Alias); ok {
			if named, ok := types.Unalias(alias).(*types.Named); ok {
				if mapType, ok := named.Underlying().(*types.Map); ok && isMapStringString(mapType) {
					fi.Kind = FieldMapStringMapStringString
					fi.TypeName = aliasQualifiedName(pkg, alias)
					if alias.Obj().Pkg() != pkg.Types {
						fi.ImportPath = alias.Obj().Pkg().Path()
					}
					return fi, nil
				}
				if structType, ok := named.Underlying().(*types.Struct); ok {
					obj := named.Obj()
					qualifiedName := aliasQualifiedName(pkg, alias)
					fi.TypeName = qualifiedName
					if obj.Exported() {
						fi.Kind = FieldMapStringDelegatedStruct
						fi.ElemType = qualifiedName
						fi.ImportPath = obj.Pkg().Path()
					} else {
						fi.Kind = FieldMapStringStruct
						fi.ImportPath = alias.Obj().Pkg().Path()
						resolvedInfo, err := resolveStructFromTypes(pkg, obj.Name(), structType)
						if err != nil {
							return fi, err
						}
						fi.InnerInfo = &resolvedInfo
					}
					return fi, nil
				}
			}
		}
		return fi, fmt.Errorf("unsupported map value type")

	case *types.Alias:
		fi, err := classifyType(pkg, goName, tomlKey, types.Unalias(t))
		if err != nil {
			return fi, err
		}
		// When an alias re-exports a type from another package (e.g. a public
		// facade over an internal/ package), the generated code must import the
		// alias's declaring package — the package the source file references —
		// not the underlying type's defining package. Resolving through the
		// alias to the underlying *types.Named otherwise emits an import of the
		// (often internal/) definition site, which Go rejects. See #81. jenType
		// re-qualifies purely from ImportPath, so only the path needs fixing.
		//
		// Delegated kinds are excluded: delegation emits calls to the target's
		// generated Decode<T>Into/EncodeFrom, which live in the type's *defining*
		// package, not the alias's. Rewriting their import to the facade would
		// reference methods that don't exist there. checkDelegationImportable
		// instead rejects the unresolvable internal/ case outright. See #81.
		if aliasObj := t.Obj(); fi.ImportPath != "" && aliasObj.Pkg() != nil && aliasObj.Pkg() != pkg.Types && !isDelegatedKind(fi.Kind) {
			fi.ImportPath = aliasObj.Pkg().Path()
		}
		return fi, nil

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

// isDelegatedKind reports whether a field kind emits calls to the target
// package's generated Decode<T>Into/EncodeFrom methods rather than inlining
// field handling. Delegated kinds require importing the type's *defining*
// package (where those methods are generated).
func isDelegatedKind(k FieldKind) bool {
	switch k {
	case FieldDelegatedStruct, FieldPointerDelegatedStruct,
		FieldSliceDelegatedStruct, FieldMapStringDelegatedStruct:
		return true
	default:
		return false
	}
}

// aliasQualifiedName renders an alias's exported, package-qualified name (e.g.
// "ids.TagStruct"), bare when the alias is declared in the analyzed package.
// Slice/map elements reached through an alias are named via the alias so an
// unexported underlying type stays referenced under its exported alias.
func aliasQualifiedName(pkg *packages.Package, alias *types.Alias) string {
	obj := alias.Obj()
	if obj.Pkg() == pkg.Types {
		return obj.Name()
	}
	return obj.Pkg().Name() + "." + obj.Name()
}

// classifySliceAliasStruct fills fi for a slice element whose type is a struct
// reached through a type alias (`[]Alias` or `[]*Alias`). An exported underlying
// delegates to its defining package — where the generated Decode/Encode methods
// live, which checkDelegationImportable then rejects if that package is an
// unreachable internal/ one. An unexported underlying is inlined under the
// alias's exported name, importing the alias's (facade) package. The caller sets
// TypeName and SlicePointer. See #81.
func classifySliceAliasStruct(pkg *packages.Package, fi *FieldInfo, alias *types.Alias, named *types.Named, structType *types.Struct) error {
	if named.Obj().Exported() {
		fi.Kind = FieldSliceDelegatedStruct
		fi.ImportPath = named.Obj().Pkg().Path()
		return nil
	}
	fi.Kind = FieldSliceStruct
	fi.ImportPath = alias.Obj().Pkg().Path()
	resolvedInfo, err := resolveStructFromTypes(pkg, named.Obj().Name(), structType)
	if err != nil {
		return err
	}
	fi.InnerInfo = &resolvedInfo
	return nil
}

// internalImportAllowed implements Go's internal-package visibility rule: a
// package path containing an "internal" path element may be imported only by
// code rooted at the directory enclosing that "internal" element. Returns true
// when target has no internal element or when importer is within the allowed
// subtree.
func internalImportAllowed(target, importer string) bool {
	var root string
	switch {
	case strings.Contains(target, "/internal/"):
		root = target[:strings.Index(target, "/internal/")]
	case strings.HasSuffix(target, "/internal"):
		root = strings.TrimSuffix(target, "/internal")
	case target == "internal" || strings.HasPrefix(target, "internal/"):
		root = ""
	default:
		return true
	}
	return importer == root || strings.HasPrefix(importer, root+"/")
}

// checkDelegationImportable rejects a delegated field whose defining package is
// an internal/ package the consumer (importerPath) may not import. Such a field
// is typically a struct re-exported via a type alias from a facade over an
// internal/ package: the generated Decode<T>Into/EncodeFrom live in the
// internal package, and the public alias does not carry them, so no importable
// package satisfies the delegation. See #81.
func checkDelegationImportable(importerPath string, fi FieldInfo) error {
	if !isDelegatedKind(fi.Kind) || fi.ImportPath == "" {
		return nil
	}
	if internalImportAllowed(fi.ImportPath, importerPath) {
		return nil
	}
	return fmt.Errorf(
		"cannot delegate decoding of %s: its generated Decode/Encode methods live in internal package %q, which %q may not import (Go internal-package rule). This type is reached through a re-export alias over an internal package. Generate the facade in copy mode so the type is concrete in the public package, or mark the field `toml:\"-\"`",
		fi.TypeName, fi.ImportPath, importerPath)
}

// resolvingTypes tracks which struct types are currently being resolved
// to detect recursive type references. Reset at the start of each Analyze call.
var resolvingTypes map[string]bool

func resolveStructByName(pkg *packages.Package, name string) (StructInfo, error) {
	if resolvingTypes[name] {
		return StructInfo{}, fmt.Errorf("recursive struct type %s", name)
	}
	resolvingTypes[name] = true
	defer delete(resolvingTypes, name)

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
					// Non-struct types (interfaces, etc.) have no TOML fields to promote.
				return StructInfo{}, nil
				}
				return analyzeStruct(pkg, name, structType)
			}
		}
	}
	return StructInfo{}, fmt.Errorf("struct %s not found in package", name)
}
