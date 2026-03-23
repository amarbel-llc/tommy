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
)

// StructInfo describes a struct that needs code generation.
type StructInfo struct {
	Name   string
	Fields []FieldInfo
}

// FieldInfo describes a single field within a struct.
type FieldInfo struct {
	GoName    string
	TomlKey   string
	Kind      FieldKind
	ElemType  string
	TypeName  string
	InnerInfo *StructInfo
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
			fi, err := classifyField(pkg, ident.Name, tomlKey, field.Type)
			if err != nil {
				return si, fmt.Errorf("field %s.%s: %w", name, ident.Name, err)
			}
			fi.OmitEmpty = opts.omitEmpty
			fi.Multiline = opts.multiline
			si.Fields = append(si.Fields, fi)
		}
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
		inner, ok := t.X.(*ast.Ident)
		if !ok {
			return fi, fmt.Errorf("unsupported pointer type")
		}
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

	case *ast.ArrayType:
		elemIdent, ok := t.Elt.(*ast.Ident)
		if !ok {
			return fi, fmt.Errorf("unsupported slice element type")
		}
		fi.ElemType = elemIdent.Name
		if primitiveTypes[elemIdent.Name] {
			fi.Kind = FieldSlicePrimitive
			return fi, nil
		}
		fi.Kind = FieldSliceStruct
		fi.TypeName = elemIdent.Name
		innerInfo, err := resolveStructByName(pkg, elemIdent.Name)
		if err != nil {
			return fi, err
		}
		fi.InnerInfo = &innerInfo
		return fi, nil

	case *ast.MapType:
		keyIdent, ok := t.Key.(*ast.Ident)
		if !ok || keyIdent.Name != "string" {
			return fi, fmt.Errorf("unsupported map key type (only string keys supported)")
		}
		valIdent, ok := t.Value.(*ast.Ident)
		if !ok || valIdent.Name != "string" {
			return fi, fmt.Errorf("unsupported map value type (only map[string]string supported)")
		}
		fi.Kind = FieldMapStringString
		return fi, nil

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
	var typeName string
	switch t := expr.(type) {
	case *ast.Ident:
		typeName = t.Name
	case *ast.StarExpr:
		inner, ok := t.X.(*ast.Ident)
		if !ok {
			return nil, fmt.Errorf("unsupported embedded pointer type")
		}
		typeName = inner.Name
	default:
		return nil, fmt.Errorf("unsupported embedded type %T", expr)
	}

	si, err := resolveStructByName(pkg, typeName)
	if err != nil {
		return nil, fmt.Errorf("resolving embedded %s: %w", typeName, err)
	}
	return si.Fields, nil
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
					return StructInfo{}, fmt.Errorf("%s is not a struct", name)
				}
				return analyzeStruct(pkg, name, structType)
			}
		}
	}
	return StructInfo{}, fmt.Errorf("struct %s not found in package", name)
}
