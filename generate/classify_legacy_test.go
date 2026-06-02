package generate

// Test-only legacy bridge (#85 Phase 2b). Production classifies fields into the
// compositional TypeExpr (FieldInfo.Type); the analyze tests still assert the
// historical flat FieldKind/FieldInfo shape. adaptLegacy is the inverse of the
// former classifyType field assignments — it flattens a TypeExpr back to a
// legacy view so the ~30 TestAnalyze cases keep their assertions unchanged (they
// call analyzeLegacy instead of Analyze). This is the equivalence harness; if a
// new TypeExpr shape needs a flat mapping, add the arm here.

// FieldKind classifies how a struct field is encoded/decoded (historical flat
// enumeration, retained as the analyze tests' assertion vocabulary).
type FieldKind int

const (
	FieldPrimitive                FieldKind = iota // string, int, int64, float64, bool
	FieldStruct                                    // nested struct with toml tags
	FieldPointerStruct                             // *SomeStruct
	FieldSlicePrimitive                            // []int, []string
	FieldSliceStruct                               // []Server (array-of-tables)
	FieldCustom                                    // implements TOMLUnmarshaler
	FieldPointerPrimitive                          // *bool, *int, etc.
	FieldMapStringString                           // map[string]string
	FieldTextMarshaler                             // implements encoding.TextMarshaler/TextUnmarshaler
	FieldMapStringStruct                           // map[string]SomeStruct
	FieldSliceTextMarshaler                        // []TextMarshalerType
	FieldMapStringMapStringString                  // map[string]NamedMap where NamedMap is map[string]string
	FieldDelegatedStruct                           // cross-package struct — delegate
	FieldPointerDelegatedStruct                    // pointer to cross-package struct — delegate
	FieldSliceDelegatedStruct                      // []cross-package struct — delegate per element
	FieldMapStringDelegatedStruct                  // map[string]cross-package struct — delegate per entry
)

// legacyStructInfo / legacyFieldInfo mirror the pre-#85 StructInfo / FieldInfo
// shape the analyze tests assert against.
type legacyStructInfo struct {
	Name        string
	Fields      []legacyFieldInfo
	Validatable bool
}

type legacyFieldInfo struct {
	GoName       string
	TomlKey      string
	Kind         FieldKind
	ElemType     string
	TypeName     string
	InnerInfo    *legacyStructInfo
	OmitEmpty    bool
	Multiline    bool
	ImportPath   string
	SlicePointer bool
}

// analyzeLegacy runs Analyze and flattens each field's TypeExpr to the legacy
// view. The analyze tests call this in place of Analyze.
func analyzeLegacy(dir, filename string) ([]legacyStructInfo, error) {
	infos, err := Analyze(dir, filename)
	if err != nil {
		return nil, err
	}
	out := make([]legacyStructInfo, 0, len(infos))
	for i := range infos {
		out = append(out, *toLegacyStruct(&infos[i]))
	}
	return out, nil
}

func toLegacyStruct(si *StructInfo) *legacyStructInfo {
	if si == nil {
		return nil
	}
	out := &legacyStructInfo{Name: si.Name, Validatable: si.Validatable}
	for _, fi := range si.Fields {
		out.Fields = append(out.Fields, toLegacyField(fi))
	}
	return out
}

func toLegacyField(fi FieldInfo) legacyFieldInfo {
	lf := adaptLegacy(fi.Type)
	lf.GoName = fi.GoName
	lf.TomlKey = fi.TomlKey
	lf.OmitEmpty = fi.OmitEmpty
	lf.Multiline = fi.Multiline
	return lf
}

// adaptLegacy flattens a TypeExpr to the legacy FieldInfo Kind + payload, the
// inverse of the former classifyType assignments. One arm per FieldKind shape.
func adaptLegacy(te spkType) legacyFieldInfo {
	var lf legacyFieldInfo
	switch t := te.(type) {
	case spkScalar:
		switch t.Codec {
		case codecCustom:
			lf.Kind = FieldCustom
			lf.TypeName = t.TypeName
		case codecText:
			lf.Kind = FieldTextMarshaler
			lf.TypeName = t.TypeName
		default: // codecPrim
			lf.Kind = FieldPrimitive
			lf.TypeName = t.TypeName
			lf.ElemType = t.ElemType
			lf.ImportPath = t.ImportPath
		}

	case spkStruct:
		lf.Kind = FieldStruct
		lf.TypeName = t.TypeName
		lf.ImportPath = t.ImportPath
		lf.InnerInfo = toLegacyStruct(t.InnerInfo)

	case spkDelegated:
		lf.Kind = FieldDelegatedStruct
		lf.TypeName = t.TypeName
		lf.ImportPath = t.ImportPath
		lf.InnerInfo = toLegacyStruct(t.InnerInfo)

	case spkPtr:
		lf = adaptLegacy(t.Elem)
		switch lf.Kind {
		case FieldPrimitive:
			// Only a bare *basic becomes FieldPointerPrimitive; a named prim
			// wrapper under a pointer historically drops the pointer (as do
			// *TextMarshaler/*Custom/*alias), so leave it unchanged there.
			if lf.ElemType == "" && lf.ImportPath == "" {
				lf.Kind = FieldPointerPrimitive
			}
		case FieldStruct:
			lf.Kind = FieldPointerStruct
		case FieldDelegatedStruct:
			lf.Kind = FieldPointerDelegatedStruct
		}

	case spkSlice:
		switch elem := t.Elem.(type) {
		case spkScalar:
			if elem.Codec == codecText {
				lf.Kind = FieldSliceTextMarshaler
				lf.TypeName = elem.TypeName
				lf.ElemType = elem.TypeName
				lf.ImportPath = elem.ImportPath
			} else {
				lf.Kind = FieldSlicePrimitive
				lf.ElemType = elem.TypeName
				lf.TypeName = t.TypeName // named slice-alias wrapper
				lf.ImportPath = t.ImportPath
			}
		case spkStruct:
			lf.Kind = FieldSliceStruct
			lf.TypeName = elem.TypeName
			lf.ImportPath = elem.ImportPath
			lf.InnerInfo = toLegacyStruct(elem.InnerInfo)
		case spkDelegated:
			lf.Kind = FieldSliceDelegatedStruct
			lf.TypeName = elem.TypeName
			lf.ImportPath = elem.ImportPath
		case spkPtr:
			lf.SlicePointer = true
			switch pe := elem.Elem.(type) {
			case spkScalar:
				lf.Kind = FieldSlicePrimitive
				lf.ElemType = pe.TypeName
			case spkStruct:
				lf.Kind = FieldSliceStruct
				lf.TypeName = pe.TypeName
				lf.ImportPath = pe.ImportPath
				lf.InnerInfo = toLegacyStruct(pe.InnerInfo)
			case spkDelegated:
				lf.Kind = FieldSliceDelegatedStruct
				lf.TypeName = pe.TypeName
				lf.ImportPath = pe.ImportPath
			}
		}

	case spkMap:
		switch elem := t.Elem.(type) {
		case spkScalar:
			lf.Kind = FieldMapStringString
			lf.TypeName = t.TypeName
			lf.ImportPath = t.ImportPath
		case spkMap:
			lf.Kind = FieldMapStringMapStringString
			lf.TypeName = t.TypeName
			lf.ImportPath = t.ImportPath
		case spkStruct:
			lf.Kind = FieldMapStringStruct
			lf.TypeName = elem.TypeName
			lf.ImportPath = elem.ImportPath
			lf.InnerInfo = toLegacyStruct(elem.InnerInfo)
		case spkDelegated:
			lf.Kind = FieldMapStringDelegatedStruct
			lf.ElemType = elem.TypeName
			lf.TypeName = t.TypeName
			if t.ImportPath != "" {
				lf.ImportPath = t.ImportPath
			} else {
				lf.ImportPath = elem.ImportPath
			}
		case spkPtr:
			lf.SlicePointer = true
			if ps, ok := elem.Elem.(spkStruct); ok {
				lf.Kind = FieldMapStringStruct
				lf.TypeName = ps.TypeName
				lf.ImportPath = ps.ImportPath
				lf.InnerInfo = toLegacyStruct(ps.InnerInfo)
			}
		}
	}
	return lf
}
