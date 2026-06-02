package generate

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

// StructInfo describes a struct that needs code generation.
type StructInfo struct {
	Name        string
	Fields      []FieldInfo
	Validatable bool
}

// FieldInfo describes a single field within a struct. Type carries the
// compositional classification (#85); GoName/TomlKey and the tag-derived
// OmitEmpty/Multiline are the field metadata the type cannot supply.
type FieldInfo struct {
	GoName    string
	TomlKey   string
	Type      spkType
	OmitEmpty bool
	Multiline bool
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

// classifyNamedTypeExpr classifies a same-package named type into its TypeExpr:
// a custom/text-marshaler scalar codec, or (the common case) a struct whose
// fields are resolved by name. Non-struct same-package named types (slice/map/
// primitive aliases used as a direct field) fall through to the struct path and
// surface resolveStructByName's "not a struct" error — matching the historical
// classifier, which never supported them as direct fields.
func classifyNamedTypeExpr(pkg *packages.Package, typeName string) (spkType, error) {
	obj := pkg.Types.Scope().Lookup(typeName)
	if obj == nil {
		return nil, fmt.Errorf("type %s not found", typeName)
	}

	// Check pointer receiver methods for UnmarshalTOML
	ptrType := types.NewPointer(obj.Type())
	mset := types.NewMethodSet(ptrType)
	for i := range mset.Len() {
		if mset.At(i).Obj().Name() == "UnmarshalTOML" {
			if !hasMarshalTOML(obj) {
				return nil, fmt.Errorf("type %s has UnmarshalTOML but no MarshalTOML — Encode() requires both", typeName)
			}
			return spkScalar{Codec: codecCustom, TypeName: typeName}, nil
		}
	}

	// Check value receiver methods too
	vmset := types.NewMethodSet(obj.Type())
	for i := range vmset.Len() {
		if vmset.At(i).Obj().Name() == "UnmarshalTOML" {
			if !hasMarshalTOML(obj) {
				return nil, fmt.Errorf("type %s has UnmarshalTOML but no MarshalTOML — Encode() requires both", typeName)
			}
			return spkScalar{Codec: codecCustom, TypeName: typeName}, nil
		}
	}

	if hasMarshalTOML(obj) {
		return nil, fmt.Errorf("type %s has MarshalTOML but no UnmarshalTOML — Decode() requires both", typeName)
	}

	// Check for encoding.TextMarshaler/TextUnmarshaler
	hasUnmarshalText := hasMethod(obj, "UnmarshalText")
	hasMarshalText := hasMethod(obj, "MarshalText")
	if hasUnmarshalText && hasMarshalText {
		return spkScalar{Codec: codecText, TypeName: typeName}, nil
	}
	if hasUnmarshalText && !hasMarshalText {
		return nil, fmt.Errorf("type %s has UnmarshalText but no MarshalText — Encode() requires both", typeName)
	}
	if hasMarshalText && !hasUnmarshalText {
		return nil, fmt.Errorf("type %s has MarshalText but no UnmarshalText — Decode() requires both", typeName)
	}

	innerInfo, err := resolveStructByName(pkg, typeName)
	if err != nil {
		return nil, err
	}
	return spkStruct{TypeName: typeName, InnerInfo: &innerInfo}, nil
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

// classifyNamedMapTypeExpr classifies a named map type (type Foo map[string]V)
// used as a direct field. The named-wrapper qualified name + import ride on the
// Map node; the value determines the element: string → nested-map scalar leaf,
// struct → delegated. Mirrors the historical FieldMapStringString /
// FieldMapStringDelegatedStruct assignments.
func classifyNamedMapTypeExpr(obj *types.TypeName, mapType *types.Map) (spkType, error) {
	key, ok := mapType.Key().(*types.Basic)
	if !ok || key.Kind() != types.String {
		return nil, fmt.Errorf("unsupported map key type in %s.%s (only string keys supported)", obj.Pkg().Name(), obj.Name())
	}

	qualifiedName := obj.Pkg().Name() + "." + obj.Name()
	importPath := obj.Pkg().Path()

	valElem := mapType.Elem()
	if basic, ok := valElem.(*types.Basic); ok && basic.Kind() == types.String {
		return spkMap{Elem: spkScalar{Codec: codecPrim}, TypeName: qualifiedName, ImportPath: importPath}, nil
	}

	valNamed, ok := valElem.(*types.Named)
	if !ok {
		return nil, fmt.Errorf("unsupported cross-package map alias %s.%s (value type %s)", obj.Pkg().Name(), obj.Name(), valElem)
	}

	if _, ok := valNamed.Underlying().(*types.Struct); ok {
		valQual := valNamed.Obj().Pkg().Name() + "." + valNamed.Obj().Name()
		return spkMap{Elem: spkDelegated{TypeName: valQual}, TypeName: qualifiedName, ImportPath: importPath}, nil
	}

	return nil, fmt.Errorf("unsupported cross-package map value type in %s.%s (value type %s)", obj.Pkg().Name(), obj.Name(), valElem)
}

// classifyType builds a struct field's FieldInfo: its compositional TypeExpr
// (from the single go/types classifier classifyTypeExpr) plus the field metadata
// (GoName/TomlKey) the type cannot supply. The folds consume FieldInfo.Type
// directly; OmitEmpty/Multiline are filled by the caller from the struct tag.
func classifyType(pkg *packages.Package, goName, tomlKey string, typ types.Type) (FieldInfo, error) {
	te, err := classifyTypeExpr(pkg, typ)
	if err != nil {
		return FieldInfo{GoName: goName, TomlKey: tomlKey}, err
	}
	return FieldInfo{GoName: goName, TomlKey: tomlKey, Type: te}, nil
}

// classifyTypeExpr is the single field-type classifier (#85 Phase 2b). It maps a
// go/types type to its compositional TypeExpr, recursing on element/value types,
// with the payload (names, imports, inner StructInfo, codec) carried on the node
// that owns it. Every classification decision — same-pkg vs cross-pkg,
// inline-via-unexported-alias vs delegate, the #81 facade-vs-defining import
// rule — matches the historical flat classifier; the equivalence harness
// (TestAnalyze*) is the oracle. It deliberately keeps the historical accept/
// reject set (e.g. bare map[string]map[string]string still errors): broadening
// it is a separate follow-up that also adds the matching fold/adapter arm.
func classifyTypeExpr(pkg *packages.Package, typ types.Type) (spkType, error) {
	switch t := typ.(type) {
	case *types.Basic:
		if primitiveTypes[t.Name()] {
			return spkScalar{Codec: codecPrim, TypeName: t.Name()}, nil
		}
		return nil, fmt.Errorf("unsupported basic type %s", t.Name())

	case *types.Named:
		obj := t.Obj()
		if obj.Pkg() == pkg.Types {
			return classifyNamedTypeExpr(pkg, obj.Name())
		}
		qual := obj.Pkg().Name() + "." + obj.Name()
		if hasMethod(obj, "UnmarshalTOML") && hasMarshalTOML(obj) {
			return spkScalar{Codec: codecCustom, TypeName: qual}, nil
		}
		if hasMethod(obj, "MarshalText") && hasMethod(obj, "UnmarshalText") {
			return spkScalar{Codec: codecText, TypeName: qual}, nil
		}
		if basic, ok := t.Underlying().(*types.Basic); ok && primitiveTypes[basic.Name()] {
			return spkScalar{Codec: codecPrim, TypeName: basic.Name(), ElemType: qual, ImportPath: obj.Pkg().Path()}, nil
		}
		if structType, ok := t.Underlying().(*types.Struct); ok {
			innerInfo, err := resolveStructFromTypes(pkg, obj.Name(), structType)
			if err != nil {
				return nil, err
			}
			return spkDelegated{TypeName: qual, ImportPath: obj.Pkg().Path(), InnerInfo: &innerInfo}, nil
		}
		if sliceType, ok := t.Underlying().(*types.Slice); ok {
			elem := sliceType.Elem()
			if basic, ok := elem.(*types.Basic); ok && primitiveTypes[basic.Name()] {
				return spkSlice{Elem: spkScalar{Codec: codecPrim, TypeName: basic.Name()}, TypeName: qual, ImportPath: obj.Pkg().Path()}, nil
			}
			return nil, fmt.Errorf("unsupported cross-package slice alias %s (element type %s)", qual, elem)
		}
		if mapType, ok := t.Underlying().(*types.Map); ok {
			return classifyNamedMapTypeExpr(obj, mapType)
		}
		return nil, fmt.Errorf("unsupported cross-package type %s.%s", obj.Pkg().Name(), obj.Name())

	case *types.Pointer:
		elem := t.Elem()
		if basic, ok := elem.(*types.Basic); ok && primitiveTypes[basic.Name()] {
			return spkPtr{Elem: spkScalar{Codec: codecPrim, TypeName: basic.Name()}}, nil
		}
		if named, ok := elem.(*types.Named); ok {
			inner, err := classifyTypeExpr(pkg, named)
			if err != nil {
				return nil, err
			}
			return spkPtr{Elem: inner}, nil
		}
		return nil, fmt.Errorf("unsupported pointer type")

	case *types.Slice:
		elem := t.Elem()
		if basic, ok := elem.(*types.Basic); ok && primitiveTypes[basic.Name()] {
			return spkSlice{Elem: spkScalar{Codec: codecPrim, TypeName: basic.Name()}}, nil
		}
		if named, ok := elem.(*types.Named); ok {
			obj := named.Obj()
			qualifiedName := obj.Pkg().Name() + "." + obj.Name()
			if obj.Pkg() == pkg.Types {
				qualifiedName = obj.Name()
			}
			if hasMethod(obj, "MarshalText") && hasMethod(obj, "UnmarshalText") {
				sc := spkScalar{Codec: codecText, TypeName: qualifiedName}
				if obj.Pkg() != pkg.Types {
					sc.ImportPath = obj.Pkg().Path()
				}
				return spkSlice{Elem: sc}, nil
			}
			if _, ok := named.Underlying().(*types.Struct); ok {
				if obj.Pkg() == pkg.Types {
					innerInfo, err := resolveStructByName(pkg, obj.Name())
					if err != nil {
						return nil, err
					}
					return spkSlice{Elem: spkStruct{TypeName: qualifiedName, InnerInfo: &innerInfo}}, nil
				}
				return spkSlice{Elem: spkDelegated{TypeName: qualifiedName, ImportPath: obj.Pkg().Path()}}, nil
			}
		}
		if ptr, ok := elem.(*types.Pointer); ok {
			pelem := ptr.Elem()
			if basic, ok := pelem.(*types.Basic); ok && primitiveTypes[basic.Name()] {
				return spkSlice{Elem: spkPtr{Elem: spkScalar{Codec: codecPrim, TypeName: basic.Name()}}}, nil
			}
			// []*pkg.AliasType: unwrap the alias to its underlying named struct,
			// naming via the alias (mirrors the []pkg.AliasType case below).
			if alias, ok := pelem.(*types.Alias); ok {
				if named, ok := types.Unalias(alias).(*types.Named); ok {
					if structType, ok := named.Underlying().(*types.Struct); ok {
						inner, err := classifyAliasStruct(pkg, alias, named, structType)
						if err != nil {
							return nil, err
						}
						return spkSlice{Elem: spkPtr{Elem: inner}}, nil
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
					if obj.Pkg() == pkg.Types {
						innerInfo, err := resolveStructByName(pkg, obj.Name())
						if err != nil {
							return nil, err
						}
						return spkSlice{Elem: spkPtr{Elem: spkStruct{TypeName: qualifiedName, InnerInfo: &innerInfo}}}, nil
					}
					return spkSlice{Elem: spkPtr{Elem: spkDelegated{TypeName: qualifiedName, ImportPath: obj.Pkg().Path()}}}, nil
				}
			}
		}
		// Unwrap type aliases (e.g., type TagStruct = tagStruct)
		if alias, ok := elem.(*types.Alias); ok {
			underlying := types.Unalias(alias)
			named, ok := underlying.(*types.Named)
			if !ok {
				return classifyTypeExpr(pkg, types.NewSlice(underlying))
			}
			obj := named.Obj()
			qualifiedName := aliasQualifiedName(pkg, alias)
			if hasMethod(obj, "MarshalText") && hasMethod(obj, "UnmarshalText") {
				sc := spkScalar{Codec: codecText, TypeName: qualifiedName}
				if alias.Obj().Pkg() != pkg.Types {
					sc.ImportPath = alias.Obj().Pkg().Path()
				}
				return spkSlice{Elem: sc}, nil
			}
			if structType, ok := named.Underlying().(*types.Struct); ok {
				inner, err := classifyAliasStruct(pkg, alias, named, structType)
				if err != nil {
					return nil, err
				}
				return spkSlice{Elem: inner}, nil
			}
			return classifyTypeExpr(pkg, types.NewSlice(underlying))
		}
		return nil, fmt.Errorf("unsupported slice element type")

	case *types.Map:
		key, ok := t.Key().(*types.Basic)
		if !ok || key.Kind() != types.String {
			return nil, fmt.Errorf("unsupported map key type (only string keys supported)")
		}
		if val, ok := t.Elem().(*types.Basic); ok && val.Kind() == types.String {
			return spkMap{Elem: spkScalar{Codec: codecPrim}}, nil
		}
		// map[string]*Struct: pointer values are supported for same-package
		// structs (FieldMapStringStruct honors SlicePointer); cross-package
		// pointer-struct map values would lose their pointer-ness through the
		// delegated fold, so they are rejected rather than mis-emitted.
		if ptr, ok := t.Elem().(*types.Pointer); ok {
			named, ok := ptr.Elem().(*types.Named)
			if !ok {
				return nil, fmt.Errorf("unsupported map value type")
			}
			if _, ok := named.Underlying().(*types.Struct); !ok {
				return nil, fmt.Errorf("unsupported map value type")
			}
			obj := named.Obj()
			if obj.Pkg() != pkg.Types {
				return nil, fmt.Errorf("map[string]*%s.%s: pointer values are only supported for same-package structs", obj.Pkg().Name(), obj.Name())
			}
			innerInfo, err := resolveStructByName(pkg, obj.Name())
			if err != nil {
				return nil, err
			}
			return spkMap{Elem: spkPtr{Elem: spkStruct{TypeName: obj.Name(), InnerInfo: &innerInfo}}}, nil
		}
		if named, ok := t.Elem().(*types.Named); ok {
			if mapType, ok := named.Underlying().(*types.Map); ok && isMapStringString(mapType) {
				obj := named.Obj()
				m := spkMap{Elem: spkMap{Elem: spkScalar{Codec: codecPrim}}}
				if obj.Pkg() == pkg.Types {
					m.TypeName = obj.Name()
				} else {
					m.TypeName = obj.Pkg().Name() + "." + obj.Name()
					m.ImportPath = obj.Pkg().Path()
				}
				return m, nil
			}
		}
		if _, ok := t.Elem().Underlying().(*types.Interface); ok {
			return nil, fmt.Errorf("map value type is an interface, which cannot be statically decoded; use `toml:\"-\"` to skip this field, or define a concrete struct that mirrors the TOML shape and convert to the interface after decoding")
		}
		if named, ok := t.Elem().(*types.Named); ok {
			if _, ok := named.Underlying().(*types.Struct); ok {
				obj := named.Obj()
				if obj.Pkg() == pkg.Types {
					innerInfo, err := resolveStructByName(pkg, obj.Name())
					if err != nil {
						return nil, err
					}
					return spkMap{Elem: spkStruct{TypeName: obj.Name(), InnerInfo: &innerInfo}}, nil
				}
				return spkMap{Elem: spkDelegated{TypeName: obj.Pkg().Name() + "." + obj.Name(), ImportPath: obj.Pkg().Path()}}, nil
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
					m := spkMap{Elem: spkMap{Elem: spkScalar{Codec: codecPrim}}, TypeName: aliasQualifiedName(pkg, alias)}
					if alias.Obj().Pkg() != pkg.Types {
						m.ImportPath = alias.Obj().Pkg().Path()
					}
					return m, nil
				}
				if structType, ok := named.Underlying().(*types.Struct); ok {
					inner, err := classifyAliasStruct(pkg, alias, named, structType)
					if err != nil {
						return nil, err
					}
					m := spkMap{Elem: inner}
					// The historical map-alias-value classifier set FieldInfo.TypeName
					// to the alias's qualified name for the delegated case too (it is
					// unused by the fold but asserted for parity); surface it via the
					// Map node, which adaptToFieldInfo lifts to TypeName.
					if _, isDelegated := inner.(spkDelegated); isDelegated {
						m.TypeName = aliasQualifiedName(pkg, alias)
					}
					return m, nil
				}
			}
		}
		return nil, fmt.Errorf("unsupported map value type")

	case *types.Alias:
		inner, err := classifyTypeExpr(pkg, types.Unalias(t))
		if err != nil {
			return nil, err
		}
		// #81: a re-export alias's import must point at the alias's declaring
		// (facade) package — the one the source references — not the underlying
		// definition site (often internal/), which Go rejects. Delegated kinds
		// are excluded: delegation emits calls to the target's generated
		// Decode<T>Into/EncodeFrom, which live in the *defining* package;
		// checkDelegationImportable rejects the unreachable internal/ case.
		if aliasObj := t.Obj(); aliasObj.Pkg() != nil && aliasObj.Pkg() != pkg.Types {
			inner = overrideAliasImport(inner, aliasObj.Pkg().Path())
		}
		return inner, nil

	default:
		return nil, fmt.Errorf("unsupported type %T", typ)
	}
}

// classifyAliasStruct classifies a struct reached through a type alias (a slice
// or map element `[]Alias`/`[]*Alias`/`map[string]Alias`). An exported underlying
// delegates to its defining package — where the generated Decode/Encode methods
// live, which checkDelegationImportable then rejects if that package is an
// unreachable internal/ one. An unexported underlying is inlined under the
// alias's exported name, importing the alias's (facade) package. See #81.
func classifyAliasStruct(pkg *packages.Package, alias *types.Alias, named *types.Named, structType *types.Struct) (spkType, error) {
	qual := aliasQualifiedName(pkg, alias)
	if named.Obj().Exported() {
		return spkDelegated{TypeName: qual, ImportPath: named.Obj().Pkg().Path()}, nil
	}
	innerInfo, err := resolveStructFromTypes(pkg, named.Obj().Name(), structType)
	if err != nil {
		return nil, err
	}
	return spkStruct{TypeName: qual, ImportPath: alias.Obj().Pkg().Path(), InnerInfo: &innerInfo}, nil
}

// overrideAliasImport rewrites the top node's import to the facade path for the
// #81 alias rule. Delegated nodes are left untouched (they need the defining
// package); a node with no import is unaffected.
func overrideAliasImport(te spkType, facadePath string) spkType {
	switch t := te.(type) {
	case spkScalar:
		if t.ImportPath != "" {
			t.ImportPath = facadePath
		}
		return t
	case spkSlice:
		if t.ImportPath != "" {
			t.ImportPath = facadePath
		}
		return t
	case spkMap:
		if t.ImportPath != "" {
			t.ImportPath = facadePath
		}
		return t
	case spkStruct:
		if t.ImportPath != "" {
			t.ImportPath = facadePath
		}
		return t
	default:
		return te
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

// delegatedTarget returns the import path and qualified name of the cross-package
// struct a field delegates to — directly, or as a pointer/slice/map element — if
// any. For a map whose element delegates, an import on the Map node (a named map
// alias) overrides the element's, matching how the delegation is emitted.
func delegatedTarget(te spkType) (importPath, typeName string, ok bool) {
	switch t := te.(type) {
	case spkDelegated:
		return t.ImportPath, t.TypeName, true
	case spkPtr:
		return delegatedTarget(t.Elem)
	case spkSlice:
		return delegatedTarget(t.Elem)
	case spkMap:
		ip, tn, found := delegatedTarget(t.Elem)
		if found && t.ImportPath != "" {
			ip = t.ImportPath
		}
		return ip, tn, found
	}
	return "", "", false
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
	importPath, typeName, ok := delegatedTarget(fi.Type)
	if !ok || importPath == "" {
		return nil
	}
	if internalImportAllowed(importPath, importerPath) {
		return nil
	}
	return fmt.Errorf(
		"cannot delegate decoding of %s: its generated Decode/Encode methods live in internal package %q, which %q may not import (Go internal-package rule). This type is reached through a re-export alias over an internal package. Generate the facade in copy mode so the type is concrete in the public package, or mark the field `toml:\"-\"`",
		typeName, importPath, importerPath)
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
