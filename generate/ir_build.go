package generate

import "strings"

// buildDecodeOps constructs a tree of DecodeOps from a StructInfo.
// All scoping decisions (key prefixes, root vs nested APIs, handle tracking)
// are resolved during construction — the renderer needs no context threading.
func buildDecodeOps(si StructInfo, dataPath, keyPrefix string, tgt TargetPath, tkey TOMLKey, isRoot, emitHandles bool) []DecodeOp {
	var ops []DecodeOp
	for _, fi := range si.Fields {
		ops = append(ops, buildDecodeOp(fi, dataPath, keyPrefix, tgt, tkey, isRoot, emitHandles))
	}
	return ops
}

func buildDecodeOp(fi FieldInfo, dataPath, keyPrefix string, tgt TargetPath, tkey TOMLKey, isRoot, emitHandles bool) DecodeOp {
	target := dataPath + "." + fi.GoName
	fieldTgt := tgt.Dot(fi.GoName)
	fieldKey := tkey.Dot(fi.TomlKey)

	switch fi.Kind {
	case FieldPrimitive:
		return GetPrimitive{
			Target:     target,
			Key:        keyPrefix + fi.TomlKey,
			Tgt:        fieldTgt,
			TKey:       fieldKey,
			TypeName:   fi.TypeName,
			ElemType:   fi.ElemType,
			ImportPath: fi.ImportPath,
		}

	case FieldPointerPrimitive:
		return GetPrimitive{
			Target:   target,
			Key:      keyPrefix + fi.TomlKey,
			Tgt:      fieldTgt,
			TKey:     fieldKey,
			TypeName: fi.TypeName,
			Pointer:  true,
		}

	case FieldCustom:
		return GetCustom{
			Target:     target,
			Key:        keyPrefix + fi.TomlKey,
			Tgt:        fieldTgt,
			TKey:       fieldKey,
			TypeName:   fi.TypeName,
			ImportPath: fi.ImportPath,
		}

	case FieldTextMarshaler:
		return GetTextMarshaler{
			Target:     target,
			Key:        keyPrefix + fi.TomlKey,
			Tgt:        fieldTgt,
			TKey:       fieldKey,
			TypeName:   fi.TypeName,
			ImportPath: fi.ImportPath,
		}

	case FieldSlicePrimitive:
		return GetSlicePrimitive{
			Target:       target,
			Key:          keyPrefix + fi.TomlKey,
			Tgt:          fieldTgt,
			TKey:         fieldKey,
			ElemType:     fi.ElemType,
			TypeName:     fi.TypeName,
			ImportPath:   fi.ImportPath,
			SlicePointer: fi.SlicePointer,
		}

	case FieldSliceTextMarshaler:
		return GetSliceTextMarshaler{
			Target:     target,
			Key:        keyPrefix + fi.TomlKey,
			Tgt:        fieldTgt,
			TKey:       fieldKey,
			TypeName:   fi.TypeName,
			ImportPath: fi.ImportPath,
		}

	case FieldMapStringString:
		return GetMapStringString{
			Target:     target,
			Key:        keyPrefix + fi.TomlKey,
			Tgt:        fieldTgt,
			TKey:       fieldKey,
			UseRootAPI: isRoot,
		}

	case FieldMapStringMapStringString:
		return GetMapStringMapStringString{
			Target:   target,
			Key:      keyPrefix + fi.TomlKey,
			Tgt:      fieldTgt,
			TKey:     fieldKey,
			TypeName: fi.TypeName,
		}

	case FieldStruct:
		if fi.InnerInfo == nil {
			return nil
		}
		innerPrefix := keyPrefix + fi.TomlKey + "."
		innerKey := fieldKey.Lit(".")
		return InTable{
			Key:        keyPrefix + fi.TomlKey,
			TKey:       fieldKey,
			UseRootAPI: isRoot,
			Fields:     buildDecodeOps(*fi.InnerInfo, target, innerPrefix, fieldTgt, innerKey, false, false),
		}

	case FieldPointerStruct:
		if fi.InnerInfo == nil {
			return nil
		}
		innerPrefix := keyPrefix + fi.TomlKey + "."
		innerKey := fieldKey.Lit(".")
		localVar := toLowerFirst(fi.GoName) + "Val"
		localTgt := LocalTarget(localVar)
		// TableFields: ALL inner fields decoded inside the explicit [table].
		tableFields := buildDecodeOps(*fi.InnerInfo, localVar, innerPrefix, localTgt, innerKey, false, false)
		// FlatFields: ALL inner fields decoded at the parent container level.
		// Array-table fields (FieldSliceStruct/FieldSliceDelegatedStruct) keep
		// innerPrefix because they use dotted keys from the document root.
		// Other fields use bare keys at the current container level.
		var flatFields []DecodeOp
		for _, inner := range fi.InnerInfo.Fields {
			if inner.Kind == FieldSliceStruct || inner.Kind == FieldSliceDelegatedStruct {
				flatFields = append(flatFields, buildDecodeOp(inner, localVar, innerPrefix, localTgt, innerKey, false, false))
			} else {
				flatFields = append(flatFields, buildDecodeOp(inner, localVar, "", localTgt, TOMLKey{}, false, false))
			}
		}
		return InPointerTable{
			Key:         keyPrefix + fi.TomlKey,
			TKey:        fieldKey,
			TypeName:    fi.TypeName,
			Target:      target,
			Tgt:         fieldTgt,
			TableFields: tableFields,
			FlatFields:  flatFields,
		}

	case FieldSliceStruct:
		if fi.InnerInfo == nil {
			return nil
		}
		crossPkg := strings.Contains(fi.TypeName, ".")
		dottedKey := keyPrefix + fi.TomlKey
		innerPrefix := keyPrefix + fi.TomlKey + "."
		innerKey := fieldKey.Lit(".")
		return ForArrayTable{
			Key:          fi.TomlKey,
			DottedKey:    dottedKey,
			TKey:         StaticKey(fi.TomlKey),
			TDottedKey:   fieldKey,
			TypeName:     fi.TypeName,
			ImportPath:   fi.ImportPath,
			Target:       target,
			Tgt:          fieldTgt,
			SlicePointer: fi.SlicePointer,
			TrackHandles: emitHandles && !crossPkg,
			Fields:       buildDecodeOps(*fi.InnerInfo, target+"[i]", innerPrefix, fieldTgt.Index("i"), innerKey, false, false),
		}

	case FieldSliceDelegatedStruct:
		dottedKey := keyPrefix + fi.TomlKey
		return DelegateSlice{
			Target:       target,
			Key:          fi.TomlKey,
			DottedKey:    dottedKey,
			Tgt:          fieldTgt,
			TKey:         StaticKey(fi.TomlKey),
			TDottedKey:   fieldKey,
			TypeName:     fi.TypeName,
			ImportPath:   fi.ImportPath,
			SlicePointer: fi.SlicePointer,
		}

	case FieldMapStringStruct:
		if fi.InnerInfo == nil {
			return nil
		}
		innerPrefix := keyPrefix + fi.TomlKey + "."
		innerKey := fieldKey.Lit(".")
		entryTgt := LocalTarget("entry")
		return ForMapStringStruct{
			Key:          keyPrefix + fi.TomlKey,
			TKey:         fieldKey,
			TypeName:     fi.TypeName,
			Target:       target,
			Tgt:          fieldTgt,
			SlicePointer: fi.SlicePointer,
			UseRootAPI:   isRoot,
			Fields:       buildDecodeOps(*fi.InnerInfo, "entry", innerPrefix, entryTgt, innerKey, false, false),
		}

	case FieldMapStringDelegatedStruct:
		return DelegateMap{
			Target:     target,
			Key:        keyPrefix + fi.TomlKey,
			Tgt:        fieldTgt,
			TKey:       fieldKey,
			ElemType:   fi.ElemType,
			ImportPath: fi.ImportPath,
			UseRootAPI: isRoot,
		}

	case FieldDelegatedStruct:
		return DelegateStruct{
			Target:     target,
			Key:        keyPrefix + fi.TomlKey,
			Tgt:        fieldTgt,
			TKey:       fieldKey,
			TypeName:   fi.TypeName,
			ImportPath: fi.ImportPath,
			UseRootAPI: isRoot,
		}

	case FieldPointerDelegatedStruct:
		return DelegateStruct{
			Target:     target,
			Key:        keyPrefix + fi.TomlKey,
			Tgt:        fieldTgt,
			TKey:       fieldKey,
			TypeName:   fi.TypeName,
			ImportPath: fi.ImportPath,
			Pointer:    true,
			UseRootAPI: isRoot,
		}
	}

	return nil
}

// --- Encode IR builder ---

func buildEncodeOps(si StructInfo, tgt TargetPath, tkey TOMLKey, isRoot, emitHandles bool) []EncodeOp {
	var ops []EncodeOp
	for _, fi := range si.Fields {
		if op := buildEncodeOp(fi, tgt, tkey, isRoot, emitHandles); op != nil {
			ops = append(ops, op)
		}
	}
	return ops
}

func buildEncodeOp(fi FieldInfo, tgt TargetPath, tkey TOMLKey, isRoot, emitHandles bool) EncodeOp {
	fieldTgt := tgt.Dot(fi.GoName)
	fieldKey := tkey.Dot(fi.TomlKey)

	switch fi.Kind {
	case FieldPrimitive:
		return SetPrimitive{
			Tgt:        fieldTgt,
			TKey:       fieldKey,
			TypeName:   fi.TypeName,
			ElemType:   fi.ElemType,
			ImportPath: fi.ImportPath,
			OmitEmpty:  fi.OmitEmpty,
			Multiline:  fi.Multiline,
		}

	case FieldPointerPrimitive:
		return SetPointerPrimitive{
			Tgt:      fieldTgt,
			TKey:     fieldKey,
			TypeName: fi.TypeName,
		}

	case FieldCustom:
		return SetCustom{
			Tgt:  fieldTgt,
			TKey: fieldKey,
		}

	case FieldTextMarshaler:
		return SetTextMarshaler{
			Tgt:       fieldTgt,
			TKey:      fieldKey,
			OmitEmpty: fi.OmitEmpty,
		}

	case FieldSlicePrimitive:
		return SetSlicePrimitive{
			Tgt:          fieldTgt,
			TKey:         fieldKey,
			ElemType:     fi.ElemType,
			TypeName:     fi.TypeName,
			ImportPath:   fi.ImportPath,
			SlicePointer: fi.SlicePointer,
			OmitEmpty:    fi.OmitEmpty,
		}

	case FieldSliceTextMarshaler:
		return SetSliceTextMarshaler{
			Tgt:        fieldTgt,
			TKey:       fieldKey,
			TypeName:   fi.TypeName,
			ImportPath: fi.ImportPath,
		}

	case FieldMapStringString:
		return SetMapStringString{
			Tgt:        fieldTgt,
			TKey:       fieldKey,
			UseRootAPI: isRoot,
		}

	case FieldMapStringMapStringString:
		return ForEncodeMapStringMapStringString{
			Tgt:      fieldTgt,
			TKey:     fieldKey,
			TypeName: fi.TypeName,
		}

	case FieldStruct:
		if fi.InnerInfo == nil {
			return nil
		}
		return InEncodeTable{
			TKey:       fieldKey,
			UseRootAPI: isRoot,
			Fields:     buildEncodeOps(*fi.InnerInfo, fieldTgt, fieldKey.Lit("."), false, false),
		}

	case FieldPointerStruct:
		if fi.InnerInfo == nil {
			return nil
		}
		return InEncodePointerTable{
			Tgt:      fieldTgt,
			TKey:     fieldKey,
			TypeName: fi.TypeName,
			Fields:   buildEncodeOps(*fi.InnerInfo, fieldTgt, fieldKey.Lit("."), false, false),
		}

	case FieldSliceStruct:
		if fi.InnerInfo == nil {
			return nil
		}
		crossPkg := strings.Contains(fi.TypeName, ".")
		return ForEncodeArrayTable{
			Tgt:          fieldTgt,
			TKey:         StaticKey(fi.TomlKey),
			TDottedKey:   fieldKey,
			TypeName:     fi.TypeName,
			ImportPath:   fi.ImportPath,
			SlicePointer: fi.SlicePointer,
			TrackHandles: emitHandles && !crossPkg,
			Fields:       buildEncodeOps(*fi.InnerInfo, fieldTgt.Index("i"), fieldKey.Lit("."), false, false),
		}

	case FieldSliceDelegatedStruct:
		return EncodeDelegateSlice{
			Tgt:          fieldTgt,
			TKey:         StaticKey(fi.TomlKey),
			TDottedKey:   fieldKey,
			TypeName:     fi.TypeName,
			ImportPath:   fi.ImportPath,
			SlicePointer: fi.SlicePointer,
		}

	case FieldMapStringStruct:
		if fi.InnerInfo == nil {
			return nil
		}
		var entryTgt TargetPath
		if fi.SlicePointer {
			entryTgt = LocalTarget("(*mapVal)")
		} else {
			entryTgt = LocalTarget("mapVal")
		}
		return ForEncodeMapStringStruct{
			Tgt:          fieldTgt,
			TKey:         fieldKey,
			TypeName:     fi.TypeName,
			SlicePointer: fi.SlicePointer,
			UseRootAPI:   isRoot,
			Fields:       buildEncodeOps(*fi.InnerInfo, entryTgt, fieldKey.Lit("."), false, false),
		}

	case FieldMapStringDelegatedStruct:
		return EncodeDelegateMap{
			Tgt:        fieldTgt,
			TKey:       fieldKey,
			ElemType:   fi.ElemType,
			ImportPath: fi.ImportPath,
			UseRootAPI: isRoot,
		}

	case FieldDelegatedStruct:
		return EncodeDelegateStruct{
			Tgt:        fieldTgt,
			TKey:       fieldKey,
			TypeName:   fi.TypeName,
			ImportPath: fi.ImportPath,
			UseRootAPI: isRoot,
		}

	case FieldPointerDelegatedStruct:
		return EncodeDelegateStruct{
			Tgt:        fieldTgt,
			TKey:       fieldKey,
			TypeName:   fi.TypeName,
			ImportPath: fi.ImportPath,
			Pointer:    true,
			UseRootAPI: isRoot,
		}
	}

	return nil
}
