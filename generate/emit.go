package generate

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
)

func emitDecodeBody(si StructInfo) string {
	var buf bytes.Buffer
	for _, fi := range si.Fields {
		buf.WriteString(emitDecodeField(fi, "d.data", "d.cstDoc", "d.cstDoc.Root()", ""))
	}
	if si.Validatable {
		fmt.Fprintf(&buf, "\tif err := d.data.Validate(); err != nil {\n")
		fmt.Fprintf(&buf, "\t\treturn nil, fmt.Errorf(\"validation failed: %%w\", err)\n")
		fmt.Fprintf(&buf, "\t}\n")
	}
	return buf.String()
}

func emitEncodeBody(si StructInfo) string {
	var buf bytes.Buffer
	if si.Validatable {
		fmt.Fprintf(&buf, "\tif err := d.data.Validate(); err != nil {\n")
		fmt.Fprintf(&buf, "\t\treturn nil, fmt.Errorf(\"validation failed: %%w\", err)\n")
		fmt.Fprintf(&buf, "\t}\n")
	}
	for _, fi := range si.Fields {
		buf.WriteString(emitEncodeField(fi, "d.data", "d.cstDoc", "d.cstDoc.Root()"))
	}
	return buf.String()
}

func emitDecodeIntoBody(si StructInfo) string {
	var buf bytes.Buffer
	for _, fi := range si.Fields {
		if fi.Kind == FieldSliceStruct {
			buf.WriteString(emitDecodeIntoSliceStruct(fi, "data", "doc"))
		} else if fi.Kind == FieldSliceDelegatedStruct {
			buf.WriteString(emitDecodeIntoSliceDelegatedStruct(fi, "data", "doc"))
		} else if fi.Kind == FieldMapStringDelegatedStruct {
			buf.WriteString(emitDecodeIntoMapDelegatedStruct(fi, "data", "doc"))
		} else {
			code := emitDecodeField(fi, "data", "doc", "container", "")
			code = strings.ReplaceAll(code, "d.consumed", "consumed")
			code = strings.ReplaceAll(code, "return nil, ", "return ")
			// Replace consumed key literals to prepend keyPrefix variable
			code = replaceConsumedKeys(code)
			buf.WriteString(code)
		}
	}
	return buf.String()
}

func emitDecodeIntoSliceDelegatedStruct(fi FieldInfo, dataPath, docVar string) string {
	var buf bytes.Buffer
	target := dataPath + "." + fi.GoName
	parts := strings.SplitN(fi.TypeName, ".", 2)

	nodesVar := fi.TomlKey + "Nodes"
	fmt.Fprintf(&buf, "\t%s := %s.FindArrayTableNodes(%q)\n", nodesVar, docVar, fi.TomlKey)
	if fi.SlicePointer {
		fmt.Fprintf(&buf, "\t%s = make([]*%s, len(%s))\n", target, fi.TypeName, nodesVar)
	} else {
		fmt.Fprintf(&buf, "\t%s = make([]%s, len(%s))\n", target, fi.TypeName, nodesVar)
	}
	fmt.Fprintf(&buf, "\tconsumed[keyPrefix + %q] = true\n", fi.TomlKey)
	fmt.Fprintf(&buf, "\tfor i, node := range %s {\n", nodesVar)
	if fi.SlicePointer {
		fmt.Fprintf(&buf, "\t\t%s[i] = &%s{}\n", target, fi.TypeName)
		fmt.Fprintf(&buf, "\t\tif err := %s.Decode%sInto(%s[i], %s, node, consumed, keyPrefix + %q); err != nil {\n",
			parts[0], parts[1], target, docVar, fi.TomlKey+".")
	} else {
		fmt.Fprintf(&buf, "\t\tif err := %s.Decode%sInto(&%s[i], %s, node, consumed, keyPrefix + %q); err != nil {\n",
			parts[0], parts[1], target, docVar, fi.TomlKey+".")
	}
	fmt.Fprintf(&buf, "\t\t\treturn fmt.Errorf(\"%s[%%d]: %%w\", i, err)\n", fi.TomlKey)
	fmt.Fprintf(&buf, "\t\t}\n")
	fmt.Fprintf(&buf, "\t}\n")
	return buf.String()
}

func emitDecodeIntoMapDelegatedStruct(fi FieldInfo, dataPath, docVar string) string {
	var buf bytes.Buffer
	target := dataPath + "." + fi.GoName
	parts := strings.SplitN(fi.ElemType, ".", 2)

	fmt.Fprintf(&buf, "\t{\n")
	fmt.Fprintf(&buf, "\t\tsubTables := %s.FindSubTables(%q)\n", docVar, fi.TomlKey)
	fmt.Fprintf(&buf, "\t\tif len(subTables) > 0 {\n")
	fmt.Fprintf(&buf, "\t\t\tconsumed[keyPrefix + %q] = true\n", fi.TomlKey)
	fmt.Fprintf(&buf, "\t\t\t%s = make(map[string]%s)\n", target, fi.ElemType)
	fmt.Fprintf(&buf, "\t\t\tfor _, subTable := range subTables {\n")
	fmt.Fprintf(&buf, "\t\t\t\tmapKey := document.SubTableKey(subTable, %q)\n", fi.TomlKey)
	fmt.Fprintf(&buf, "\t\t\t\tif strings.Contains(mapKey, \".\") {\n")
	fmt.Fprintf(&buf, "\t\t\t\t\tcontinue\n")
	fmt.Fprintf(&buf, "\t\t\t\t}\n")
	fmt.Fprintf(&buf, "\t\t\t\tconsumed[keyPrefix + %q + \".\" + mapKey] = true\n", fi.TomlKey)
	fmt.Fprintf(&buf, "\t\t\t\tvar entry %s\n", fi.ElemType)
	fmt.Fprintf(&buf, "\t\t\t\tif err := %s.Decode%sInto(&entry, %s, subTable, consumed, keyPrefix + %q + \".\" + mapKey + \".\"); err != nil {\n",
		parts[0], parts[1], docVar, fi.TomlKey)
	fmt.Fprintf(&buf, "\t\t\t\t\treturn fmt.Errorf(\"%s.%%s: %%w\", mapKey, err)\n", fi.TomlKey)
	fmt.Fprintf(&buf, "\t\t\t\t}\n")
	fmt.Fprintf(&buf, "\t\t\t\t%s[mapKey] = entry\n", target)
	fmt.Fprintf(&buf, "\t\t\t}\n")
	fmt.Fprintf(&buf, "\t\t}\n")
	fmt.Fprintf(&buf, "\t}\n")
	return buf.String()
}

func replaceConsumedKeys(code string) string {
	// Replace consumed["key"] = true with consumed[keyPrefix + "key"] = true
	// and similar patterns like consumed["key.subkey"]
	result := strings.Builder{}
	for len(code) > 0 {
		idx := strings.Index(code, `consumed["`)
		if idx < 0 {
			result.WriteString(code)
			break
		}
		result.WriteString(code[:idx])
		result.WriteString(`consumed[keyPrefix + "`)
		code = code[idx+len(`consumed["`):]
	}
	return result.String()
}

func emitDecodeIntoSliceStruct(fi FieldInfo, dataPath, docVar string) string {
	var buf bytes.Buffer
	target := dataPath + "." + fi.GoName

	nodesVar := fi.TomlKey + "Nodes"
	fmt.Fprintf(&buf, "\t%s := %s.FindArrayTableNodes(%q)\n", nodesVar, docVar, fi.TomlKey)
	if fi.SlicePointer {
		fmt.Fprintf(&buf, "\t%s = make([]*%s, len(%s))\n", target, fi.TypeName, nodesVar)
	} else {
		fmt.Fprintf(&buf, "\t%s = make([]%s, len(%s))\n", target, fi.TypeName, nodesVar)
	}
	fmt.Fprintf(&buf, "\tconsumed[keyPrefix + %q] = true\n", fi.TomlKey)
	fmt.Fprintf(&buf, "\tfor i, node := range %s {\n", nodesVar)
	if fi.SlicePointer {
		fmt.Fprintf(&buf, "\t\t%s[i] = &%s{}\n", target, fi.TypeName)
	}
	if fi.InnerInfo != nil {
		for _, inner := range fi.InnerInfo.Fields {
			indexedTarget := fmt.Sprintf("%s[i]", target)
			code := emitDecodeField(inner, indexedTarget, docVar, "node", fi.TomlKey+".")
			code = strings.ReplaceAll(code, "d.consumed", "consumed")
			code = strings.ReplaceAll(code, "return nil, ", "return ")
			code = replaceConsumedKeys(code)
			buf.WriteString("\t" + code)
		}
	}
	fmt.Fprintf(&buf, "\t}\n")
	return buf.String()
}

func emitEncodeFromBody(si StructInfo) string {
	var buf bytes.Buffer
	for _, fi := range si.Fields {
		if fi.Kind == FieldSliceStruct {
			buf.WriteString(emitEncodeFromSliceStruct(fi, "data", "doc", "container"))
		} else if fi.Kind == FieldSliceDelegatedStruct {
			buf.WriteString(emitEncodeFromSliceDelegatedStruct(fi, "data", "doc"))
		} else if fi.Kind == FieldMapStringDelegatedStruct {
			buf.WriteString(emitEncodeFromMapDelegatedStruct(fi, "data", "doc"))
		} else {
			code := emitEncodeField(fi, "data", "doc", "container")
			code = strings.ReplaceAll(code, "return nil, ", "return ")
			buf.WriteString(code)
		}
	}
	return buf.String()
}

func emitEncodeFromSliceDelegatedStruct(fi FieldInfo, dataPath, docVar string) string {
	var buf bytes.Buffer
	source := dataPath + "." + fi.GoName
	parts := strings.SplitN(fi.TypeName, ".", 2)

	fmt.Fprintf(&buf, "\tfor i := range %s {\n", source)
	fmt.Fprintf(&buf, "\t\tcontainer := %s.AppendArrayTableEntry(%q)\n", docVar, fi.TomlKey)
	if fi.SlicePointer {
		fmt.Fprintf(&buf, "\t\tif err := %s.Encode%sFrom(%s[i], %s, container); err != nil {\n",
			parts[0], parts[1], source, docVar)
	} else {
		fmt.Fprintf(&buf, "\t\tif err := %s.Encode%sFrom(&%s[i], %s, container); err != nil {\n",
			parts[0], parts[1], source, docVar)
	}
	fmt.Fprintf(&buf, "\t\t\treturn fmt.Errorf(\"%s[%%d]: %%w\", i, err)\n", fi.TomlKey)
	fmt.Fprintf(&buf, "\t\t}\n")
	fmt.Fprintf(&buf, "\t}\n")
	return buf.String()
}

func emitEncodeFromMapDelegatedStruct(fi FieldInfo, dataPath, docVar string) string {
	var buf bytes.Buffer
	source := dataPath + "." + fi.GoName
	parts := strings.SplitN(fi.ElemType, ".", 2)

	fmt.Fprintf(&buf, "\tif len(%s) > 0 {\n", source)
	fmt.Fprintf(&buf, "\t\tfor mapKey, mapVal := range %s {\n", source)
	fmt.Fprintf(&buf, "\t\t\tsubTable := %s.EnsureSubTable(%q, mapKey)\n", docVar, fi.TomlKey)
	fmt.Fprintf(&buf, "\t\t\tif err := %s.Encode%sFrom(&mapVal, %s, subTable); err != nil {\n",
		parts[0], parts[1], docVar)
	fmt.Fprintf(&buf, "\t\t\t\treturn fmt.Errorf(\"%s.%%s: %%w\", mapKey, err)\n", fi.TomlKey)
	fmt.Fprintf(&buf, "\t\t\t}\n")
	fmt.Fprintf(&buf, "\t\t}\n")
	fmt.Fprintf(&buf, "\t}\n")
	return buf.String()
}

func emitEncodeFromSliceStruct(fi FieldInfo, dataPath, docVar, containerExpr string) string {
	var buf bytes.Buffer
	source := dataPath + "." + fi.GoName

	fmt.Fprintf(&buf, "\tfor i := range %s {\n", source)
	fmt.Fprintf(&buf, "\t\tcontainer := %s.AppendArrayTableEntry(%q)\n", docVar, fi.TomlKey)
	if fi.InnerInfo != nil {
		for _, inner := range fi.InnerInfo.Fields {
			indexedSource := fmt.Sprintf("%s[i]", source)
			code := emitEncodeField(inner, indexedSource, docVar, "container")
			code = strings.ReplaceAll(code, "return nil, ", "return ")
			buf.WriteString("\t" + code)
		}
	}
	fmt.Fprintf(&buf, "\t}\n")
	return buf.String()
}

func emitDecodeField(fi FieldInfo, dataPath, docVar, containerExpr, keyPrefix string) string {
	var buf bytes.Buffer
	target := dataPath + "." + fi.GoName
	consumedKey := keyPrefix + fi.TomlKey

	switch fi.Kind {
	case FieldPrimitive:
		fmt.Fprintf(&buf, "\tif v, err := document.GetFromContainer[%s](%s, %s, %q); err == nil {\n",
			fi.TypeName, docVar, containerExpr, fi.TomlKey)
		if fi.ElemType != "" {
			fmt.Fprintf(&buf, "\t\t%s = %s(v)\n", target, fi.ElemType)
		} else {
			fmt.Fprintf(&buf, "\t\t%s = v\n", target)
		}
		fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t}\n")

	case FieldPointerPrimitive:
		fmt.Fprintf(&buf, "\tif v, err := document.GetFromContainer[%s](%s, %s, %q); err == nil {\n",
			fi.TypeName, docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t%s = &v\n", target)
		fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t}\n")

	case FieldCustom:
		fmt.Fprintf(&buf, "\tif raw, err := document.GetRawFromContainer(%s, %s, %q); err == nil {\n",
			docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\tif err := %s.UnmarshalTOML(raw); err != nil {\n", target)
		fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s: %%w\", err)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t}\n")

	case FieldTextMarshaler:
		fmt.Fprintf(&buf, "\tif v, err := document.GetFromContainer[string](%s, %s, %q); err == nil {\n",
			docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\tif err := %s.UnmarshalText([]byte(v)); err != nil {\n", target)
		fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s: %%w\", err)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t}\n")

	case FieldStruct:
		if fi.InnerInfo != nil {
			innerPrefix := consumedKey + "."
			if containerExpr == "d.cstDoc.Root()" {
				fmt.Fprintf(&buf, "\tif tableNode := %s.FindTable(%q); tableNode != nil {\n", docVar, fi.TomlKey)
			} else {
				fmt.Fprintf(&buf, "\tif tableNode := %s.FindTableInContainer(%s, %q); tableNode != nil {\n",
					docVar, containerExpr, fi.TomlKey)
			}
			fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
			for _, inner := range fi.InnerInfo.Fields {
				code := emitDecodeField(inner, target, docVar, "tableNode", innerPrefix)
				buf.WriteString(code)
			}
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldPointerStruct:
		if fi.InnerInfo != nil {
			innerPrefix := consumedKey + "."
			localVar := toLowerFirst(fi.GoName) + "Val"
			fmt.Fprintf(&buf, "\tif tableNode := %s.FindTableInContainer(%s, %q); tableNode != nil {\n",
				docVar, containerExpr, fi.TomlKey)
			fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
			fmt.Fprintf(&buf, "\t\t%s := &%s{}\n", localVar, fi.TypeName)
			for _, inner := range fi.InnerInfo.Fields {
				code := emitDecodeField(inner, localVar, docVar, "tableNode", innerPrefix)
				buf.WriteString("\t" + code)
			}
			fmt.Fprintf(&buf, "\t\t%s = %s\n", target, localVar)
			fmt.Fprintf(&buf, "\t} else {\n")
			fmt.Fprintf(&buf, "\t\t%s := &%s{}\n", localVar, fi.TypeName)
			fmt.Fprintf(&buf, "\t\tfound := false\n")
			for _, inner := range fi.InnerInfo.Fields {
				code := emitFlatKeyDecodeField(inner, localVar, docVar, containerExpr, keyPrefix)
				buf.WriteString("\t" + code)
			}
			fmt.Fprintf(&buf, "\t\tif found {\n")
			fmt.Fprintf(&buf, "\t\t\t%s = %s\n", target, localVar)
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldSlicePrimitive:
		fmt.Fprintf(&buf, "\tif v, err := document.GetFromContainer[[]%s](%s, %s, %q); err == nil {\n",
			fi.ElemType, docVar, containerExpr, fi.TomlKey)
		if fi.TypeName != "" {
			fmt.Fprintf(&buf, "\t\t%s = %s(v)\n", target, fi.TypeName)
		} else {
			fmt.Fprintf(&buf, "\t\t%s = v\n", target)
		}
		fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t}\n")

	case FieldMapStringString:
		fmt.Fprintf(&buf, "\tif tableNode := %s.FindTable(%q); tableNode != nil {\n", docVar, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t%s = document.GetStringMapFromTable(tableNode)\n", target)
		fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t\tdocument.MarkAllConsumed(tableNode, %q, d.consumed)\n", consumedKey)
		fmt.Fprintf(&buf, "\t}\n")

	case FieldSliceStruct:
		crossPkg := strings.Contains(fi.TypeName, ".")
		nodesVar := fi.TomlKey + "Nodes"
		fmt.Fprintf(&buf, "\t%s := %s.FindArrayTableNodes(%q)\n", nodesVar, docVar, fi.TomlKey)
		if !crossPkg {
			handleName := toLowerFirst(fi.TypeName) + "Handle"
			fmt.Fprintf(&buf, "\td.%s = make([]%s, len(%s))\n", toLowerFirst(fi.GoName), handleName, nodesVar)
		}
		if fi.SlicePointer {
			fmt.Fprintf(&buf, "\t%s = make([]*%s, len(%s))\n", target, fi.TypeName, nodesVar)
		} else {
			fmt.Fprintf(&buf, "\t%s = make([]%s, len(%s))\n", target, fi.TypeName, nodesVar)
		}
		fmt.Fprintf(&buf, "\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\tfor i, node := range %s {\n", nodesVar)
		if !crossPkg {
			handleName := toLowerFirst(fi.TypeName) + "Handle"
			fmt.Fprintf(&buf, "\t\td.%s[i] = %s{node: node}\n", toLowerFirst(fi.GoName), handleName)
		}
		if fi.SlicePointer {
			fmt.Fprintf(&buf, "\t\t%s[i] = &%s{}\n", target, fi.TypeName)
		}
		if fi.InnerInfo != nil {
			for _, inner := range fi.InnerInfo.Fields {
				indexedTarget := fmt.Sprintf("%s[i]", target)
				code := emitDecodeField(inner, indexedTarget, docVar, "node", consumedKey+".")
				buf.WriteString("\t" + code)
			}
		}
		fmt.Fprintf(&buf, "\t}\n")

	case FieldSliceDelegatedStruct:
		parts := strings.SplitN(fi.TypeName, ".", 2)
		nodesVar := fi.TomlKey + "Nodes"
		fmt.Fprintf(&buf, "\t%s := %s.FindArrayTableNodes(%q)\n", nodesVar, docVar, fi.TomlKey)
		if fi.SlicePointer {
			fmt.Fprintf(&buf, "\t%s = make([]*%s, len(%s))\n", target, fi.TypeName, nodesVar)
		} else {
			fmt.Fprintf(&buf, "\t%s = make([]%s, len(%s))\n", target, fi.TypeName, nodesVar)
		}
		fmt.Fprintf(&buf, "\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\tfor i, node := range %s {\n", nodesVar)
		if fi.SlicePointer {
			fmt.Fprintf(&buf, "\t\t%s[i] = &%s{}\n", target, fi.TypeName)
			fmt.Fprintf(&buf, "\t\tif err := %s.Decode%sInto(%s[i], %s, node, d.consumed, %q); err != nil {\n",
				parts[0], parts[1], target, docVar, consumedKey+".")
		} else {
			fmt.Fprintf(&buf, "\t\tif err := %s.Decode%sInto(&%s[i], %s, node, d.consumed, %q); err != nil {\n",
				parts[0], parts[1], target, docVar, consumedKey+".")
		}
		fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s[%%d]: %%w\", i, err)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldMapStringStruct:
		if fi.InnerInfo != nil {
			fmt.Fprintf(&buf, "\t{\n")
			fmt.Fprintf(&buf, "\t\tsubTables := %s.FindSubTables(%q)\n", docVar, fi.TomlKey)
			fmt.Fprintf(&buf, "\t\tif len(subTables) > 0 {\n")
			fmt.Fprintf(&buf, "\t\t\td.consumed[%q] = true\n", consumedKey)
			fmt.Fprintf(&buf, "\t\t\t%s = make(map[string]%s)\n", target, fi.TypeName)
			fmt.Fprintf(&buf, "\t\t\tfor _, subTable := range subTables {\n")
			fmt.Fprintf(&buf, "\t\t\t\tmapKey := document.SubTableKey(subTable, %q)\n", fi.TomlKey)
			fmt.Fprintf(&buf, "\t\t\t\td.consumed[%q + \".\" + mapKey] = true\n", consumedKey)
			fmt.Fprintf(&buf, "\t\t\t\tvar entry %s\n", fi.TypeName)
			for _, inner := range fi.InnerInfo.Fields {
				code := emitDecodeField(inner, "entry", docVar, "subTable", consumedKey+".\" + mapKey + \".")
				buf.WriteString("\t\t\t" + code)
			}
			fmt.Fprintf(&buf, "\t\t\t\t%s[mapKey] = entry\n", target)
			fmt.Fprintf(&buf, "\t\t\t}\n")
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldMapStringDelegatedStruct:
		parts := strings.SplitN(fi.ElemType, ".", 2)
		fmt.Fprintf(&buf, "\t{\n")
		fmt.Fprintf(&buf, "\t\tsubTables := %s.FindSubTables(%q)\n", docVar, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\tif len(subTables) > 0 {\n")
		fmt.Fprintf(&buf, "\t\t\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t\t\t%s = make(map[string]%s)\n", target, fi.ElemType)
		fmt.Fprintf(&buf, "\t\t\tfor _, subTable := range subTables {\n")
		fmt.Fprintf(&buf, "\t\t\t\tmapKey := document.SubTableKey(subTable, %q)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\t\tif strings.Contains(mapKey, \".\") {\n")
		fmt.Fprintf(&buf, "\t\t\t\t\tcontinue\n")
		fmt.Fprintf(&buf, "\t\t\t\t}\n")
		fmt.Fprintf(&buf, "\t\t\t\td.consumed[%q + \".\" + mapKey] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t\t\t\tvar entry %s\n", fi.ElemType)
		fmt.Fprintf(&buf, "\t\t\t\tif err := %s.Decode%sInto(&entry, %s, subTable, d.consumed, %q + \".\" + mapKey + \".\"); err != nil {\n",
			parts[0], parts[1], docVar, consumedKey)
		fmt.Fprintf(&buf, "\t\t\t\t\treturn nil, fmt.Errorf(\"%s.%%s: %%w\", mapKey, err)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\t\t}\n")
		fmt.Fprintf(&buf, "\t\t\t\t%s[mapKey] = entry\n", target)
		fmt.Fprintf(&buf, "\t\t\t}\n")
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldMapStringMapStringString:
		fmt.Fprintf(&buf, "\t{\n")
		fmt.Fprintf(&buf, "\t\tsubTables := %s.FindSubTables(%q)\n", docVar, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\tif len(subTables) > 0 {\n")
		fmt.Fprintf(&buf, "\t\t\td.consumed[%q] = true\n", consumedKey)
		if fi.TypeName != "" {
			fmt.Fprintf(&buf, "\t\t\t%s = make(map[string]%s)\n", target, fi.TypeName)
		} else {
			fmt.Fprintf(&buf, "\t\t\t%s = make(map[string]map[string]string)\n", target)
		}
		fmt.Fprintf(&buf, "\t\t\tfor _, subTable := range subTables {\n")
		fmt.Fprintf(&buf, "\t\t\t\tmapKey := document.SubTableKey(subTable, %q)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\t\td.consumed[%q + \".\" + mapKey] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t\t\t\tinner := document.GetStringMapFromTable(subTable)\n")
		fmt.Fprintf(&buf, "\t\t\t\tdocument.MarkAllConsumed(subTable, %q + \".\" + mapKey, d.consumed)\n", consumedKey)
		if fi.TypeName != "" {
			fmt.Fprintf(&buf, "\t\t\t\t%s[mapKey] = %s(inner)\n", target, fi.TypeName)
		} else {
			fmt.Fprintf(&buf, "\t\t\t\t%s[mapKey] = inner\n", target)
		}
		fmt.Fprintf(&buf, "\t\t\t}\n")
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldSliceTextMarshaler:
		fmt.Fprintf(&buf, "\tif v, err := document.GetFromContainer[[]string](%s, %s, %q); err == nil {\n",
			docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t%s = make([]%s, len(v))\n", target, fi.TypeName)
		fmt.Fprintf(&buf, "\t\tfor i, s := range v {\n")
		fmt.Fprintf(&buf, "\t\t\tif err := %s[i].UnmarshalText([]byte(s)); err != nil {\n", target)
		fmt.Fprintf(&buf, "\t\t\t\treturn nil, fmt.Errorf(\"%s[%%d]: %%w\", i, err)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\t}\n")
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t}\n")

	case FieldDelegatedStruct:
		parts := strings.SplitN(fi.TypeName, ".", 2)
		if containerExpr == "d.cstDoc.Root()" {
			fmt.Fprintf(&buf, "\tif tableNode := %s.FindTable(%q); tableNode != nil {\n", docVar, fi.TomlKey)
		} else {
			fmt.Fprintf(&buf, "\tif tableNode := %s.FindTableInContainer(%s, %q); tableNode != nil {\n",
				docVar, containerExpr, fi.TomlKey)
		}
		fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t\tif err := %s.Decode%sInto(&%s, %s, tableNode, d.consumed, %q); err != nil {\n",
			parts[0], parts[1], target, docVar, consumedKey+".")
		fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s: %%w\", err)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldPointerDelegatedStruct:
		parts := strings.SplitN(fi.TypeName, ".", 2)
		localVar := toLowerFirst(fi.GoName) + "Val"
		fmt.Fprintf(&buf, "\tif tableNode := %s.FindTableInContainer(%s, %q); tableNode != nil {\n",
			docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t\t%s := &%s{}\n", localVar, fi.TypeName)
		fmt.Fprintf(&buf, "\t\tif err := %s.Decode%sInto(%s, %s, tableNode, d.consumed, %q); err != nil {\n",
			parts[0], parts[1], localVar, docVar, consumedKey+".")
		fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s: %%w\", err)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t\t%s = %s\n", target, localVar)
		fmt.Fprintf(&buf, "\t}\n")
	}

	return buf.String()
}

func emitFlatKeyDecodeField(fi FieldInfo, localVar, docVar, containerExpr, keyPrefix string) string {
	var buf bytes.Buffer
	target := localVar + "." + fi.GoName
	consumedKey := keyPrefix + fi.TomlKey

	switch fi.Kind {
	case FieldPrimitive:
		fmt.Fprintf(&buf, "\tif v, err := document.GetFromContainer[%s](%s, %s, %q); err == nil {\n",
			fi.TypeName, docVar, containerExpr, fi.TomlKey)
		if fi.ElemType != "" {
			fmt.Fprintf(&buf, "\t\t%s = %s(v)\n", target, fi.ElemType)
		} else {
			fmt.Fprintf(&buf, "\t\t%s = v\n", target)
		}
		fmt.Fprintf(&buf, "\t\tfound = true\n")
		fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t}\n")

	case FieldPointerPrimitive:
		fmt.Fprintf(&buf, "\tif v, err := document.GetFromContainer[%s](%s, %s, %q); err == nil {\n",
			fi.TypeName, docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t%s = &v\n", target)
		fmt.Fprintf(&buf, "\t\tfound = true\n")
		fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t}\n")

	case FieldCustom:
		fmt.Fprintf(&buf, "\tif raw, err := document.GetRawFromContainer(%s, %s, %q); err == nil {\n",
			docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\tif err := %s.UnmarshalTOML(raw); err != nil {\n", target)
		fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s: %%w\", err)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t\tfound = true\n")
		fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t}\n")

	case FieldTextMarshaler:
		fmt.Fprintf(&buf, "\tif v, err := document.GetFromContainer[string](%s, %s, %q); err == nil {\n",
			docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\tif err := %s.UnmarshalText([]byte(v)); err != nil {\n", target)
		fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s: %%w\", err)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t\tfound = true\n")
		fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t}\n")
	}

	return buf.String()
}

func emitEncodeField(fi FieldInfo, dataPath, docVar, containerExpr string) string {
	var buf bytes.Buffer
	source := dataPath + "." + fi.GoName

	switch fi.Kind {
	case FieldPrimitive:
		zv := zeroLiteral(fi.TypeName)
		// For wrapper types (ElemType set), convert to underlying primitive
		encodeSource := source
		if fi.ElemType != "" {
			encodeSource = fi.TypeName + "(" + source + ")"
			zv = fi.ElemType + "(" + zv + ")"
		}
		if fi.OmitEmpty {
			fmt.Fprintf(&buf, "\tif %s != %s {\n", source, zv)
			if fi.Multiline && fi.TypeName == "string" {
				fmt.Fprintf(&buf, "\t\tif err := %s.SetMultilineInContainer(%s, %q, %s); err != nil {\n",
					docVar, containerExpr, fi.TomlKey, encodeSource)
			} else {
				fmt.Fprintf(&buf, "\t\tif err := %s.SetInContainer(%s, %q, %s); err != nil {\n",
					docVar, containerExpr, fi.TomlKey, encodeSource)
			}
			fmt.Fprintf(&buf, "\t\t\treturn nil, err\n")
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t} else {\n")
			fmt.Fprintf(&buf, "\t\t_ = %s.DeleteFromContainer(%s, %q)\n", docVar, containerExpr, fi.TomlKey)
			fmt.Fprintf(&buf, "\t}\n")
		} else if fi.Multiline && fi.TypeName == "string" {
			fmt.Fprintf(&buf, "\tif %s != %s || %s.HasInContainer(%s, %q) {\n",
				source, zv, docVar, containerExpr, fi.TomlKey)
			fmt.Fprintf(&buf, "\t\tif err := %s.SetMultilineInContainer(%s, %q, %s); err != nil {\n",
				docVar, containerExpr, fi.TomlKey, encodeSource)
			fmt.Fprintf(&buf, "\t\t\treturn nil, err\n")
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t}\n")
		} else {
			fmt.Fprintf(&buf, "\tif %s != %s || %s.HasInContainer(%s, %q) {\n",
				source, zv, docVar, containerExpr, fi.TomlKey)
			fmt.Fprintf(&buf, "\t\tif err := %s.SetInContainer(%s, %q, %s); err != nil {\n",
				docVar, containerExpr, fi.TomlKey, encodeSource)
			fmt.Fprintf(&buf, "\t\t\treturn nil, err\n")
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldPointerPrimitive:
		fmt.Fprintf(&buf, "\tif %s != nil {\n", source)
		fmt.Fprintf(&buf, "\t\tif err := %s.SetInContainer(%s, %q, *%s); err != nil {\n",
			docVar, containerExpr, fi.TomlKey, source)
		fmt.Fprintf(&buf, "\t\t\treturn nil, err\n")
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldCustom:
		fmt.Fprintf(&buf, "\t{\n")
		fmt.Fprintf(&buf, "\t\tv, err := %s.MarshalTOML()\n", source)
		fmt.Fprintf(&buf, "\t\tif err != nil {\n")
		fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s: %%w\", err)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t\tif err := %s.SetInContainer(%s, %q, v); err != nil {\n",
			docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\treturn nil, err\n")
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldTextMarshaler:
		fmt.Fprintf(&buf, "\t{\n")
		fmt.Fprintf(&buf, "\t\tv, err := %s.MarshalText()\n", source)
		fmt.Fprintf(&buf, "\t\tif err != nil {\n")
		fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s: %%w\", err)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t\tif err := %s.SetInContainer(%s, %q, string(v)); err != nil {\n",
			docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\treturn nil, err\n")
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldStruct:
		if fi.InnerInfo != nil {
			if containerExpr == "d.cstDoc.Root()" {
				fmt.Fprintf(&buf, "\t{\n")
				fmt.Fprintf(&buf, "\t\ttableNode := %s.EnsureTable(%q)\n", docVar, fi.TomlKey)
			} else {
				fmt.Fprintf(&buf, "\t{\n")
				fmt.Fprintf(&buf, "\t\ttableNode := %s.EnsureTableInContainer(%s, %q)\n",
					docVar, containerExpr, fi.TomlKey)
			}
			for _, inner := range fi.InnerInfo.Fields {
				code := emitEncodeField(inner, source, docVar, "tableNode")
				buf.WriteString(code)
			}
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldPointerStruct:
		if fi.InnerInfo != nil {
			fmt.Fprintf(&buf, "\tif %s != nil {\n", source)
			fmt.Fprintf(&buf, "\t\ttableNode := %s.EnsureTableInContainer(%s, %q)\n",
				docVar, containerExpr, fi.TomlKey)
			for _, inner := range fi.InnerInfo.Fields {
				code := emitEncodeField(inner, source, docVar, "tableNode")
				buf.WriteString("\t" + code)
			}
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldSliceStruct:
		crossPkg := strings.Contains(fi.TypeName, ".")
		fmt.Fprintf(&buf, "\t{\n")
		if crossPkg {
			existingVar := fi.TomlKey + "Existing"
			fmt.Fprintf(&buf, "\t%s := %s.FindArrayTableNodes(%q)\n", existingVar, docVar, fi.TomlKey)
			fmt.Fprintf(&buf, "\tfor i := range %s {\n", source)
			fmt.Fprintf(&buf, "\t\tvar container *cst.Node\n")
			fmt.Fprintf(&buf, "\t\tif i < len(%s) {\n", existingVar)
			fmt.Fprintf(&buf, "\t\t\tcontainer = %s[i]\n", existingVar)
			fmt.Fprintf(&buf, "\t\t} else {\n")
			fmt.Fprintf(&buf, "\t\t\tcontainer = %s.AppendArrayTableEntry(%q)\n", docVar, fi.TomlKey)
			fmt.Fprintf(&buf, "\t\t}\n")
		} else {
			handleSlice := "d." + toLowerFirst(fi.GoName)
			fmt.Fprintf(&buf, "\tfor i := range %s {\n", source)
			fmt.Fprintf(&buf, "\t\tvar container *cst.Node\n")
			fmt.Fprintf(&buf, "\t\tif i < len(%s) {\n", handleSlice)
			fmt.Fprintf(&buf, "\t\t\tcontainer = %s[i].node\n", handleSlice)
			fmt.Fprintf(&buf, "\t\t} else {\n")
			fmt.Fprintf(&buf, "\t\t\tcontainer = %s.AppendArrayTableEntry(%q)\n", docVar, fi.TomlKey)
			fmt.Fprintf(&buf, "\t\t}\n")
		}
		if fi.InnerInfo != nil {
			for _, inner := range fi.InnerInfo.Fields {
				indexedSource := fmt.Sprintf("%s[i]", source)
				code := emitEncodeField(inner, indexedSource, docVar, "container")
				buf.WriteString("\t" + code)
			}
		}
		fmt.Fprintf(&buf, "\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldSliceDelegatedStruct:
		parts := strings.SplitN(fi.TypeName, ".", 2)
		existingVar := fi.TomlKey + "Existing"
		fmt.Fprintf(&buf, "\t%s := %s.FindArrayTableNodes(%q)\n", existingVar, docVar, fi.TomlKey)
		fmt.Fprintf(&buf, "\tfor i := range %s {\n", source)
		fmt.Fprintf(&buf, "\t\tvar container *cst.Node\n")
		fmt.Fprintf(&buf, "\t\tif i < len(%s) {\n", existingVar)
		fmt.Fprintf(&buf, "\t\t\tcontainer = %s[i]\n", existingVar)
		fmt.Fprintf(&buf, "\t\t} else {\n")
		fmt.Fprintf(&buf, "\t\t\tcontainer = %s.AppendArrayTableEntry(%q)\n", docVar, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		if fi.SlicePointer {
			fmt.Fprintf(&buf, "\t\tif err := %s.Encode%sFrom(%s[i], %s, container); err != nil {\n",
				parts[0], parts[1], source, docVar)
		} else {
			fmt.Fprintf(&buf, "\t\tif err := %s.Encode%sFrom(&%s[i], %s, container); err != nil {\n",
				parts[0], parts[1], source, docVar)
		}
		fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s[%%d]: %%w\", i, err)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldSlicePrimitive:
		if fi.OmitEmpty {
			fmt.Fprintf(&buf, "\tif len(%s) > 0 || %s.HasInContainer(%s, %q) {\n",
				source, docVar, containerExpr, fi.TomlKey)
		}
		encodeSource := source
		if fi.TypeName != "" {
			encodeSource = "[]" + fi.ElemType + "(" + source + ")"
		}
		fmt.Fprintf(&buf, "\tif err := %s.SetInContainer(%s, %q, %s); err != nil {\n",
			docVar, containerExpr, fi.TomlKey, encodeSource)
		fmt.Fprintf(&buf, "\t\treturn nil, err\n")
		fmt.Fprintf(&buf, "\t}\n")
		if fi.OmitEmpty {
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldMapStringString:
		fmt.Fprintf(&buf, "\tif len(%s) > 0 {\n", source)
		fmt.Fprintf(&buf, "\t\ttableNode := %s.EnsureTable(%q)\n", docVar, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\tdocument.DeleteAllInContainer(tableNode)\n")
		fmt.Fprintf(&buf, "\t\tfor k, v := range %s {\n", source)
		fmt.Fprintf(&buf, "\t\t\tif err := %s.SetInContainer(tableNode, k, v); err != nil {\n", docVar)
		fmt.Fprintf(&buf, "\t\t\t\treturn nil, err\n")
		fmt.Fprintf(&buf, "\t\t\t}\n")
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldMapStringStruct:
		if fi.InnerInfo != nil {
			fmt.Fprintf(&buf, "\tif len(%s) > 0 {\n", source)
			fmt.Fprintf(&buf, "\t\tfor mapKey, mapVal := range %s {\n", source)
			fmt.Fprintf(&buf, "\t\t\tsubTable := %s.EnsureSubTable(%q, mapKey)\n", docVar, fi.TomlKey)
			for _, inner := range fi.InnerInfo.Fields {
				code := emitEncodeField(inner, "mapVal", docVar, "subTable")
				buf.WriteString("\t\t" + code)
			}
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldMapStringDelegatedStruct:
		parts := strings.SplitN(fi.ElemType, ".", 2)
		fmt.Fprintf(&buf, "\tif len(%s) > 0 {\n", source)
		fmt.Fprintf(&buf, "\t\tfor mapKey, mapVal := range %s {\n", source)
		fmt.Fprintf(&buf, "\t\t\tsubTable := %s.EnsureSubTable(%q, mapKey)\n", docVar, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\tif err := %s.Encode%sFrom(&mapVal, %s, subTable); err != nil {\n",
			parts[0], parts[1], docVar)
		fmt.Fprintf(&buf, "\t\t\t\treturn nil, fmt.Errorf(\"%s.%%s: %%w\", mapKey, err)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\t}\n")
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldMapStringMapStringString:
		fmt.Fprintf(&buf, "\tif len(%s) > 0 {\n", source)
		fmt.Fprintf(&buf, "\t\tfor mapKey, mapVal := range %s {\n", source)
		fmt.Fprintf(&buf, "\t\t\tsubTable := %s.EnsureSubTable(%q, mapKey)\n", docVar, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\tdocument.DeleteAllInContainer(subTable)\n")
		fmt.Fprintf(&buf, "\t\t\tfor k, v := range map[string]string(mapVal) {\n")
		fmt.Fprintf(&buf, "\t\t\t\tif err := %s.SetInContainer(subTable, k, v); err != nil {\n", docVar)
		fmt.Fprintf(&buf, "\t\t\t\t\treturn nil, err\n")
		fmt.Fprintf(&buf, "\t\t\t\t}\n")
		fmt.Fprintf(&buf, "\t\t\t}\n")
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldSliceTextMarshaler:
		fmt.Fprintf(&buf, "\t{\n")
		fmt.Fprintf(&buf, "\t\tvals := make([]string, len(%s))\n", source)
		fmt.Fprintf(&buf, "\t\tfor i, item := range %s {\n", source)
		fmt.Fprintf(&buf, "\t\t\tv, err := item.MarshalText()\n")
		fmt.Fprintf(&buf, "\t\t\tif err != nil {\n")
		fmt.Fprintf(&buf, "\t\t\t\treturn nil, fmt.Errorf(\"%s[%%d]: %%w\", i, err)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\t}\n")
		fmt.Fprintf(&buf, "\t\t\tvals[i] = string(v)\n")
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t\tif err := %s.SetInContainer(%s, %q, vals); err != nil {\n",
			docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\treturn nil, err\n")
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldDelegatedStruct:
		parts := strings.SplitN(fi.TypeName, ".", 2)
		if containerExpr == "d.cstDoc.Root()" {
			fmt.Fprintf(&buf, "\t{\n")
			fmt.Fprintf(&buf, "\t\ttableNode := %s.EnsureTable(%q)\n", docVar, fi.TomlKey)
		} else {
			fmt.Fprintf(&buf, "\t{\n")
			fmt.Fprintf(&buf, "\t\ttableNode := %s.EnsureTableInContainer(%s, %q)\n",
				docVar, containerExpr, fi.TomlKey)
		}
		fmt.Fprintf(&buf, "\t\tif err := %s.Encode%sFrom(&%s, %s, tableNode); err != nil {\n",
			parts[0], parts[1], source, docVar)
		fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s: %%w\", err)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldPointerDelegatedStruct:
		parts := strings.SplitN(fi.TypeName, ".", 2)
		fmt.Fprintf(&buf, "\tif %s != nil {\n", source)
		fmt.Fprintf(&buf, "\t\ttableNode := %s.EnsureTableInContainer(%s, %q)\n",
			docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\tif err := %s.Encode%sFrom(%s, %s, tableNode); err != nil {\n",
			parts[0], parts[1], source, docVar)
		fmt.Fprintf(&buf, "\t\t\treturn nil, fmt.Errorf(\"%s: %%w\", err)\n", fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")
	}

	return buf.String()
}

func zeroLiteral(typeName string) string {
	switch typeName {
	case "bool":
		return "false"
	case "int", "int64", "uint64":
		return "0"
	case "float64":
		return "0.0"
	case "string":
		return `""`
	default:
		return `""`
	}
}

func toLowerFirst(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}
