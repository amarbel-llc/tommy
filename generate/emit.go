package generate

import (
	"bytes"
	"fmt"
	"unicode"
)

func emitDecodeBody(si StructInfo) string {
	var buf bytes.Buffer
	for _, fi := range si.Fields {
		buf.WriteString(emitDecodeField(fi, "d.data", "d.cstDoc", "d.cstDoc.Root()", ""))
	}
	return buf.String()
}

func emitEncodeBody(si StructInfo) string {
	var buf bytes.Buffer
	for _, fi := range si.Fields {
		buf.WriteString(emitEncodeField(fi, "d.data", "d.cstDoc", "d.cstDoc.Root()"))
	}
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
		fmt.Fprintf(&buf, "\t\t%s = v\n", target)
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

	case FieldStruct:
		if fi.InnerInfo != nil {
			innerPrefix := consumedKey + "."
			fmt.Fprintf(&buf, "\tif tableNode := %s.FindTable(%q); tableNode != nil {\n", docVar, fi.TomlKey)
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
		fmt.Fprintf(&buf, "\t\t%s = v\n", target)
		fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t}\n")

	case FieldMapStringString:
		fmt.Fprintf(&buf, "\tif tableNode := %s.FindTable(%q); tableNode != nil {\n", docVar, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t%s = document.GetStringMapFromTable(tableNode)\n", target)
		fmt.Fprintf(&buf, "\t\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\t\tdocument.MarkAllConsumed(tableNode, %q, d.consumed)\n", consumedKey)
		fmt.Fprintf(&buf, "\t}\n")

	case FieldSliceStruct:
		handleName := toLowerFirst(fi.TypeName) + "Handle"
		nodesVar := fi.TomlKey + "Nodes"
		fmt.Fprintf(&buf, "\t%s := %s.FindArrayTableNodes(%q)\n", nodesVar, docVar, fi.TomlKey)
		fmt.Fprintf(&buf, "\td.%s = make([]%s, len(%s))\n", toLowerFirst(fi.GoName), handleName, nodesVar)
		fmt.Fprintf(&buf, "\t%s = make([]%s, len(%s))\n", target, fi.TypeName, nodesVar)
		fmt.Fprintf(&buf, "\td.consumed[%q] = true\n", consumedKey)
		fmt.Fprintf(&buf, "\tfor i, node := range %s {\n", nodesVar)
		fmt.Fprintf(&buf, "\t\td.%s[i] = %s{node: node}\n", toLowerFirst(fi.GoName), handleName)
		if fi.InnerInfo != nil {
			for _, inner := range fi.InnerInfo.Fields {
				indexedTarget := fmt.Sprintf("%s[i]", target)
				code := emitDecodeField(inner, indexedTarget, docVar, "node", consumedKey+".")
				buf.WriteString("\t" + code)
			}
		}
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
		fmt.Fprintf(&buf, "\t\t%s = v\n", target)
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
	}

	return buf.String()
}

func emitEncodeField(fi FieldInfo, dataPath, docVar, containerExpr string) string {
	var buf bytes.Buffer
	source := dataPath + "." + fi.GoName

	switch fi.Kind {
	case FieldPrimitive:
		zv := zeroLiteral(fi.TypeName)
		if fi.OmitEmpty {
			fmt.Fprintf(&buf, "\tif %s != %s {\n", source, zv)
			if fi.Multiline && fi.TypeName == "string" {
				fmt.Fprintf(&buf, "\t\tif err := %s.SetMultilineInContainer(%s, %q, %s); err != nil {\n",
					docVar, containerExpr, fi.TomlKey, source)
			} else {
				fmt.Fprintf(&buf, "\t\tif err := %s.SetInContainer(%s, %q, %s); err != nil {\n",
					docVar, containerExpr, fi.TomlKey, source)
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
				docVar, containerExpr, fi.TomlKey, source)
			fmt.Fprintf(&buf, "\t\t\treturn nil, err\n")
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t}\n")
		} else {
			fmt.Fprintf(&buf, "\tif %s != %s || %s.HasInContainer(%s, %q) {\n",
				source, zv, docVar, containerExpr, fi.TomlKey)
			fmt.Fprintf(&buf, "\t\tif err := %s.SetInContainer(%s, %q, %s); err != nil {\n",
				docVar, containerExpr, fi.TomlKey, source)
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

	case FieldStruct:
		if fi.InnerInfo != nil {
			fmt.Fprintf(&buf, "\tif tableNode := %s.FindTable(%q); tableNode != nil {\n", docVar, fi.TomlKey)
			for _, inner := range fi.InnerInfo.Fields {
				code := emitEncodeField(inner, source, docVar, "tableNode")
				buf.WriteString(code)
			}
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldPointerStruct:
		if fi.InnerInfo != nil {
			fmt.Fprintf(&buf, "\tif %s != nil {\n", source)
			fmt.Fprintf(&buf, "\t\tif tableNode := %s.FindTableInContainer(%s, %q); tableNode != nil {\n",
				docVar, containerExpr, fi.TomlKey)
			for _, inner := range fi.InnerInfo.Fields {
				code := emitEncodeField(inner, source, docVar, "tableNode")
				buf.WriteString("\t" + code)
			}
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldSliceStruct:
		handleSlice := "d." + toLowerFirst(fi.GoName)
		fmt.Fprintf(&buf, "\tfor i := range %s {\n", source)
		fmt.Fprintf(&buf, "\t\tvar container *cst.Node\n")
		fmt.Fprintf(&buf, "\t\tif i < len(%s) {\n", handleSlice)
		fmt.Fprintf(&buf, "\t\t\tcontainer = %s[i].node\n", handleSlice)
		fmt.Fprintf(&buf, "\t\t} else {\n")
		fmt.Fprintf(&buf, "\t\t\tcontainer = %s.AppendArrayTableEntry(%q)\n", docVar, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		if fi.InnerInfo != nil {
			for _, inner := range fi.InnerInfo.Fields {
				indexedSource := fmt.Sprintf("%s[i]", source)
				code := emitEncodeField(inner, indexedSource, docVar, "container")
				buf.WriteString("\t" + code)
			}
		}
		fmt.Fprintf(&buf, "\t}\n")

	case FieldSlicePrimitive:
		if fi.OmitEmpty {
			fmt.Fprintf(&buf, "\tif len(%s) > 0 || %s.HasInContainer(%s, %q) {\n",
				source, docVar, containerExpr, fi.TomlKey)
		}
		fmt.Fprintf(&buf, "\tif err := %s.SetInContainer(%s, %q, %s); err != nil {\n",
			docVar, containerExpr, fi.TomlKey, source)
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
	}

	return buf.String()
}

func zeroLiteral(typeName string) string {
	switch typeName {
	case "bool":
		return "false"
	case "int", "int64":
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
