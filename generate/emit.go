package generate

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// emitContext captures the differences between receiver-context (Decode/Encode
// methods on *XDocument) and free-function context (DecodeXInto/EncodeXFrom).
type emitContext struct {
	consumedExpr    string // "d.consumed" or "consumed"
	returnErr       string // "return nil, " or "return "
	isRoot          bool   // true when operating at document root level
	emitHandles     bool   // emit handle-related code (receiver context only)
	keyPrefix       string // static key prefix for consumed keys (e.g. "servers.")
	useKeyPrefixVar bool   // when true, prepend Go variable "keyPrefix" to consumed keys
	foundVar        string // if set, emit "foundVar = true" alongside consumed tracking
}

func receiverContext() emitContext {
	return emitContext{
		consumedExpr: "d.consumed",
		returnErr:    "return nil, ",
		isRoot:       true,
		emitHandles:  true,
	}
}

func freeContext() emitContext {
	return emitContext{
		consumedExpr:    "consumed",
		returnErr:       "return ",
		isRoot:          false,
		emitHandles:     false,
		useKeyPrefixVar: true,
	}
}

// consumedKeyExpr returns a Go expression for the consumed map key.
// In receiver context: `"servers.name"`
// In free-function context: `keyPrefix + "servers.name"`
func (ctx emitContext) consumedKeyExpr(key string) string {
	fullKey := ctx.keyPrefix + key
	if ctx.useKeyPrefixVar {
		return fmt.Sprintf(`keyPrefix + %q`, fullKey)
	}
	return fmt.Sprintf(`%q`, fullKey)
}

// nested returns a child context for fields inside a parent struct or array table.
func (ctx emitContext) nested(prefix string) emitContext {
	child := ctx
	child.isRoot = false
	child.emitHandles = false
	child.keyPrefix = ctx.keyPrefix + prefix
	return child
}

// withFoundVar returns a context that emits "foundVar = true" on successful decode.
func (ctx emitContext) withFoundVar(varName string) emitContext {
	child := ctx
	child.foundVar = varName
	child.emitHandles = false
	return child
}

func emitDecodeBody(si StructInfo) string {
	switch os.Getenv("TOMMY_CODEGEN_IR") {
	case "cst":
		return irCSTDecodeBody(si)
	case "api", "1":
		return irDecodeBody(si)
	}
	ctx := receiverContext()
	var buf bytes.Buffer
	for _, fi := range si.Fields {
		buf.WriteString(emitDecodeField(ctx, fi, "d.data", "d.cstDoc", "d.cstDoc.Root()"))
	}
	if si.Validatable {
		fmt.Fprintf(&buf, "\tif err := d.data.Validate(); err != nil {\n")
		fmt.Fprintf(&buf, "\t\treturn nil, fmt.Errorf(\"validation failed: %%w\", err)\n")
		fmt.Fprintf(&buf, "\t}\n")
	}
	return buf.String()
}

func emitEncodeBody(si StructInfo) string {
	ctx := receiverContext()
	var buf bytes.Buffer
	if si.Validatable {
		fmt.Fprintf(&buf, "\tif err := d.data.Validate(); err != nil {\n")
		fmt.Fprintf(&buf, "\t\treturn nil, fmt.Errorf(\"validation failed: %%w\", err)\n")
		fmt.Fprintf(&buf, "\t}\n")
	}
	for _, fi := range si.Fields {
		buf.WriteString(emitEncodeField(ctx, fi, "d.data", "d.cstDoc", "d.cstDoc.Root()"))
	}
	return buf.String()
}

func emitDecodeIntoBody(si StructInfo) string {
	switch os.Getenv("TOMMY_CODEGEN_IR") {
	case "cst":
		return irCSTDecodeIntoBody(si)
	case "api", "1":
		return irDecodeIntoBody(si)
	}
	ctx := freeContext()
	var buf bytes.Buffer
	for _, fi := range si.Fields {
		buf.WriteString(emitDecodeField(ctx, fi, "data", "doc", "container"))
	}
	return buf.String()
}

func emitEncodeFromBody(si StructInfo) string {
	ctx := freeContext()
	var buf bytes.Buffer
	for _, fi := range si.Fields {
		buf.WriteString(emitEncodeField(ctx, fi, "data", "doc", "container"))
	}
	return buf.String()
}

func emitDecodeField(ctx emitContext, fi FieldInfo, dataPath, docVar, containerExpr string) string {
	var buf bytes.Buffer
	target := dataPath + "." + fi.GoName
	consumedKey := ctx.keyPrefix + fi.TomlKey

	switch fi.Kind {
	case FieldPrimitive:
		fmt.Fprintf(&buf, "\tif v, err := document.GetFromContainer[%s](%s, %s, %q); err == nil {\n",
			fi.TypeName, docVar, containerExpr, fi.TomlKey)
		if fi.ElemType != "" {
			fmt.Fprintf(&buf, "\t\t%s = %s(v)\n", target, fi.ElemType)
		} else {
			fmt.Fprintf(&buf, "\t\t%s = v\n", target)
		}
		if ctx.foundVar != "" {
			fmt.Fprintf(&buf, "\t\t%s = true\n", ctx.foundVar)
		}
		fmt.Fprintf(&buf, "\t\t%s[%s] = true\n", ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey))
		fmt.Fprintf(&buf, "\t}\n")

	case FieldPointerPrimitive:
		fmt.Fprintf(&buf, "\tif v, err := document.GetFromContainer[%s](%s, %s, %q); err == nil {\n",
			fi.TypeName, docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t%s = &v\n", target)
		if ctx.foundVar != "" {
			fmt.Fprintf(&buf, "\t\t%s = true\n", ctx.foundVar)
		}
		fmt.Fprintf(&buf, "\t\t%s[%s] = true\n", ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey))
		fmt.Fprintf(&buf, "\t}\n")

	case FieldCustom:
		fmt.Fprintf(&buf, "\tif raw, err := document.GetRawFromContainer(%s, %s, %q); err == nil {\n",
			docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\tif err := %s.UnmarshalTOML(raw); err != nil {\n", target)
		fmt.Fprintf(&buf, "\t\t\t%sfmt.Errorf(\"%s: %%w\", err)\n", ctx.returnErr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		if ctx.foundVar != "" {
			fmt.Fprintf(&buf, "\t\t%s = true\n", ctx.foundVar)
		}
		fmt.Fprintf(&buf, "\t\t%s[%s] = true\n", ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey))
		fmt.Fprintf(&buf, "\t}\n")

	case FieldTextMarshaler:
		fmt.Fprintf(&buf, "\tif v, err := document.GetFromContainer[string](%s, %s, %q); err == nil {\n",
			docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\tif err := %s.UnmarshalText([]byte(v)); err != nil {\n", target)
		fmt.Fprintf(&buf, "\t\t\t%sfmt.Errorf(\"%s: %%w\", err)\n", ctx.returnErr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		if ctx.foundVar != "" {
			fmt.Fprintf(&buf, "\t\t%s = true\n", ctx.foundVar)
		}
		fmt.Fprintf(&buf, "\t\t%s[%s] = true\n", ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey))
		fmt.Fprintf(&buf, "\t}\n")

	case FieldStruct:
		if fi.InnerInfo != nil {
			innerCtx := ctx.nested(fi.TomlKey + ".")
			if ctx.isRoot {
				fmt.Fprintf(&buf, "\tif tableNode := %s.FindTable(%q); tableNode != nil {\n", docVar, fi.TomlKey)
			} else {
				fmt.Fprintf(&buf, "\tif tableNode := %s.FindTableInContainer(%s, %q); tableNode != nil {\n",
					docVar, containerExpr, fi.TomlKey)
			}
			fmt.Fprintf(&buf, "\t\t%s[%s] = true\n", ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey))
			for _, inner := range fi.InnerInfo.Fields {
				code := emitDecodeField(innerCtx, inner, target, docVar, "tableNode")
				buf.WriteString(code)
			}
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldPointerStruct:
		if fi.InnerInfo != nil {
			innerCtx := ctx.nested(fi.TomlKey + ".")
			localVar := toLowerFirst(fi.GoName) + "Val"
			fmt.Fprintf(&buf, "\tif tableNode := %s.FindTableInContainer(%s, %q); tableNode != nil {\n",
				docVar, containerExpr, fi.TomlKey)
			fmt.Fprintf(&buf, "\t\t%s[%s] = true\n", ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey))
			fmt.Fprintf(&buf, "\t\t%s := &%s{}\n", localVar, fi.TypeName)
			for _, inner := range fi.InnerInfo.Fields {
				code := emitDecodeField(innerCtx, inner, localVar, docVar, "tableNode")
				buf.WriteString("\t" + code)
			}
			fmt.Fprintf(&buf, "\t\t%s = %s\n", target, localVar)
			fmt.Fprintf(&buf, "\t} else {\n")
			fmt.Fprintf(&buf, "\t\t%s := &%s{}\n", localVar, fi.TypeName)
			fmt.Fprintf(&buf, "\t\tfound := false\n")
			flatCtx := ctx.withFoundVar("found")
			// FieldSliceStruct/FieldSliceDelegatedStruct use dotted keys from the
			// document root (e.g. FindArrayTableNodes("exec.allow")), so they need
			// the parent key prefix even in the flat-key fallback. Other field kinds
			// look for bare keys at the current container level.
			arrayTableCtx := innerCtx.withFoundVar("found")
			for _, inner := range fi.InnerInfo.Fields {
				var code string
				if inner.Kind == FieldSliceStruct || inner.Kind == FieldSliceDelegatedStruct {
					code = emitDecodeField(arrayTableCtx, inner, localVar, docVar, containerExpr)
				} else {
					code = emitDecodeField(flatCtx, inner, localVar, docVar, containerExpr)
				}
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
		if fi.SlicePointer {
			fmt.Fprintf(&buf, "\t\t%s = make([]*%s, len(v))\n", target, fi.ElemType)
			fmt.Fprintf(&buf, "\t\tfor i := range v {\n")
			fmt.Fprintf(&buf, "\t\t\t%s[i] = &v[i]\n", target)
			fmt.Fprintf(&buf, "\t\t}\n")
		} else if fi.TypeName != "" {
			fmt.Fprintf(&buf, "\t\t%s = %s(v)\n", target, fi.TypeName)
		} else {
			fmt.Fprintf(&buf, "\t\t%s = v\n", target)
		}
		fmt.Fprintf(&buf, "\t\t%s[%s] = true\n", ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey))
		fmt.Fprintf(&buf, "\t}\n")

	case FieldMapStringString:
		if ctx.isRoot {
			fmt.Fprintf(&buf, "\tif tableNode := %s.FindTable(%q); tableNode != nil {\n", docVar, fi.TomlKey)
		} else {
			fmt.Fprintf(&buf, "\tif tableNode := %s.FindTableInContainer(%s, %q); tableNode != nil {\n",
				docVar, containerExpr, fi.TomlKey)
		}
		fmt.Fprintf(&buf, "\t\t%s = document.GetStringMapFromTable(tableNode)\n", target)
		fmt.Fprintf(&buf, "\t\t%s[%s] = true\n", ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey))
		fmt.Fprintf(&buf, "\t\tdocument.MarkAllConsumed(tableNode, %s, %s)\n",
			ctx.consumedKeyExpr(fi.TomlKey), ctx.consumedExpr)
		fmt.Fprintf(&buf, "\t}\n")

	case FieldSliceStruct:
		crossPkg := strings.Contains(fi.TypeName, ".")
		nodesVar := fi.TomlKey + "Nodes"
		fmt.Fprintf(&buf, "\t%s := %s.FindArrayTableNodes(%s)\n", nodesVar, docVar,
			ctx.consumedKeyExpr(fi.TomlKey))
		if ctx.foundVar != "" {
			fmt.Fprintf(&buf, "\tif len(%s) > 0 {\n", nodesVar)
			fmt.Fprintf(&buf, "\t\t%s = true\n", ctx.foundVar)
			fmt.Fprintf(&buf, "\t}\n")
		}
		if ctx.emitHandles && !crossPkg {
			handleName := toLowerFirst(fi.TypeName) + "Handle"
			fmt.Fprintf(&buf, "\td.%s = make([]%s, len(%s))\n", toLowerFirst(fi.GoName), handleName, nodesVar)
		}
		if fi.SlicePointer {
			fmt.Fprintf(&buf, "\t%s = make([]*%s, len(%s))\n", target, fi.TypeName, nodesVar)
		} else {
			fmt.Fprintf(&buf, "\t%s = make([]%s, len(%s))\n", target, fi.TypeName, nodesVar)
		}
		fmt.Fprintf(&buf, "\t%s[%s] = true\n", ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey))
		fmt.Fprintf(&buf, "\tfor i, node := range %s {\n", nodesVar)
		if ctx.emitHandles && !crossPkg {
			handleName := toLowerFirst(fi.TypeName) + "Handle"
			fmt.Fprintf(&buf, "\t\td.%s[i] = %s{node: node}\n", toLowerFirst(fi.GoName), handleName)
		}
		if fi.SlicePointer {
			fmt.Fprintf(&buf, "\t\t%s[i] = &%s{}\n", target, fi.TypeName)
		}
		if fi.InnerInfo != nil {
			innerCtx := ctx.nested(fi.TomlKey + ".")
			for _, inner := range fi.InnerInfo.Fields {
				indexedTarget := fmt.Sprintf("%s[i]", target)
				code := emitDecodeField(innerCtx, inner, indexedTarget, docVar, "node")
				buf.WriteString("\t" + code)
			}
		}
		fmt.Fprintf(&buf, "\t}\n")

	case FieldSliceDelegatedStruct:
		parts := strings.SplitN(fi.TypeName, ".", 2)
		nodesVar := fi.TomlKey + "Nodes"
		fmt.Fprintf(&buf, "\t%s := %s.FindArrayTableNodes(%s)\n", nodesVar, docVar,
			ctx.consumedKeyExpr(fi.TomlKey))
		if ctx.foundVar != "" {
			fmt.Fprintf(&buf, "\tif len(%s) > 0 {\n", nodesVar)
			fmt.Fprintf(&buf, "\t\t%s = true\n", ctx.foundVar)
			fmt.Fprintf(&buf, "\t}\n")
		}
		if fi.SlicePointer {
			fmt.Fprintf(&buf, "\t%s = make([]*%s, len(%s))\n", target, fi.TypeName, nodesVar)
		} else {
			fmt.Fprintf(&buf, "\t%s = make([]%s, len(%s))\n", target, fi.TypeName, nodesVar)
		}
		fmt.Fprintf(&buf, "\t%s[%s] = true\n", ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey))
		fmt.Fprintf(&buf, "\tfor i, node := range %s {\n", nodesVar)
		if fi.SlicePointer {
			fmt.Fprintf(&buf, "\t\t%s[i] = &%s{}\n", target, fi.TypeName)
			fmt.Fprintf(&buf, "\t\tif err := %s.Decode%sInto(%s[i], %s, node, %s, %s); err != nil {\n",
				parts[0], parts[1], target, docVar, ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey+"."))
		} else {
			fmt.Fprintf(&buf, "\t\tif err := %s.Decode%sInto(&%s[i], %s, node, %s, %s); err != nil {\n",
				parts[0], parts[1], target, docVar, ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey+"."))
		}
		fmt.Fprintf(&buf, "\t\t\t%sfmt.Errorf(\"%s[%%d]: %%w\", i, err)\n", ctx.returnErr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldMapStringStruct:
		if fi.InnerInfo != nil {
			innerCtx := ctx.nested(fi.TomlKey + ".")
			fmt.Fprintf(&buf, "\t{\n")
			if ctx.isRoot {
				fmt.Fprintf(&buf, "\t\tsubTables := %s.FindSubTables(%q)\n", docVar, fi.TomlKey)
			} else {
				fmt.Fprintf(&buf, "\t\tsubTables := %s.FindSubTablesInContainer(%s, %q)\n", docVar, containerExpr, fi.TomlKey)
			}
			fmt.Fprintf(&buf, "\t\tif len(subTables) > 0 {\n")
			fmt.Fprintf(&buf, "\t\t\t%s[%s] = true\n", ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey))
			if fi.SlicePointer {
				fmt.Fprintf(&buf, "\t\t\t%s = make(map[string]*%s)\n", target, fi.TypeName)
			} else {
				fmt.Fprintf(&buf, "\t\t\t%s = make(map[string]%s)\n", target, fi.TypeName)
			}
			fmt.Fprintf(&buf, "\t\t\tfor _, subTable := range subTables {\n")
			if ctx.isRoot {
				fmt.Fprintf(&buf, "\t\t\t\tmapKey := document.SubTableKey(subTable, %q)\n", fi.TomlKey)
			} else {
				fmt.Fprintf(&buf, "\t\t\t\tmapKey := document.SubTableKeyInContainer(subTable, %s, %q)\n", containerExpr, fi.TomlKey)
			}
			// For the map entry consumed key, we need: consumedKey + "." + mapKey
			// This is a runtime expression since mapKey is a variable.
			mapEntryConsumedExpr := ctx.consumedKeyExpr(fi.TomlKey) // base expression
			fmt.Fprintf(&buf, "\t\t\t\t%s[%s + \".\" + mapKey] = true\n", ctx.consumedExpr, mapEntryConsumedExpr)
			fmt.Fprintf(&buf, "\t\t\t\tvar entry %s\n", fi.TypeName)
			// Inner fields use a special context where keyPrefix includes the map key (runtime)
			// We emit the key as a concatenation: consumedKey + "." + mapKey + "."
			for _, inner := range fi.InnerInfo.Fields {
				code := emitDecodeField(innerCtx, inner, "entry", docVar, "subTable")
				// The innerCtx already has the static prefix. But we need mapKey injected.
				// The inner fields' consumed keys will be like: ctx.keyPrefix + consumedKey + "." + inner.TomlKey
				// But we need them to include mapKey too.
				// This is the one case where we can't fully use the context system because mapKey
				// is a runtime variable. We handle it by string replacement on the generated code.
				code = injectMapKeyIntoConsumedKeys(code, ctx.consumedExpr, consumedKey, innerCtx.keyPrefix)
				buf.WriteString("\t\t\t" + code)
			}
			if fi.SlicePointer {
				fmt.Fprintf(&buf, "\t\t\t\t%s[mapKey] = &entry\n", target)
			} else {
				fmt.Fprintf(&buf, "\t\t\t\t%s[mapKey] = entry\n", target)
			}
			fmt.Fprintf(&buf, "\t\t\t}\n")
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldMapStringDelegatedStruct:
		parts := strings.SplitN(fi.ElemType, ".", 2)
		fmt.Fprintf(&buf, "\t{\n")
		if ctx.isRoot {
			fmt.Fprintf(&buf, "\t\tsubTables := %s.FindSubTables(%q)\n", docVar, fi.TomlKey)
		} else {
			fmt.Fprintf(&buf, "\t\tsubTables := %s.FindSubTablesInContainer(%s, %q)\n", docVar, containerExpr, fi.TomlKey)
		}
		fmt.Fprintf(&buf, "\t\tif len(subTables) > 0 {\n")
		fmt.Fprintf(&buf, "\t\t\t%s[%s] = true\n", ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey))
		fmt.Fprintf(&buf, "\t\t\t%s = make(map[string]%s)\n", target, fi.ElemType)
		fmt.Fprintf(&buf, "\t\t\tfor _, subTable := range subTables {\n")
		if ctx.isRoot {
			fmt.Fprintf(&buf, "\t\t\t\tmapKey := document.SubTableKey(subTable, %q)\n", fi.TomlKey)
		} else {
			fmt.Fprintf(&buf, "\t\t\t\tmapKey := document.SubTableKeyInContainer(subTable, %s, %q)\n", containerExpr, fi.TomlKey)
		}
		fmt.Fprintf(&buf, "\t\t\t\tif strings.Contains(mapKey, \".\") {\n")
		fmt.Fprintf(&buf, "\t\t\t\t\tcontinue\n")
		fmt.Fprintf(&buf, "\t\t\t\t}\n")
		mapEntryConsumedExpr := ctx.consumedKeyExpr(fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\t\t%s[%s + \".\" + mapKey] = true\n", ctx.consumedExpr, mapEntryConsumedExpr)
		fmt.Fprintf(&buf, "\t\t\t\tvar entry %s\n", fi.ElemType)
		// For delegated, we pass the consumed expression and a key prefix that includes mapKey
		delegateConsumedKeyExpr := mapEntryConsumedExpr + ` + "." + mapKey + "."`
		fmt.Fprintf(&buf, "\t\t\t\tif err := %s.Decode%sInto(&entry, %s, subTable, %s, %s); err != nil {\n",
			parts[0], parts[1], docVar, ctx.consumedExpr, delegateConsumedKeyExpr)
		fmt.Fprintf(&buf, "\t\t\t\t\t%sfmt.Errorf(\"%s.%%s: %%w\", mapKey, err)\n", ctx.returnErr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\t\t}\n")
		fmt.Fprintf(&buf, "\t\t\t\t%s[mapKey] = entry\n", target)
		fmt.Fprintf(&buf, "\t\t\t}\n")
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldMapStringMapStringString:
		fmt.Fprintf(&buf, "\t{\n")
		fmt.Fprintf(&buf, "\t\tsubTables := %s.FindSubTables(%q)\n", docVar, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\tif len(subTables) > 0 {\n")
		fmt.Fprintf(&buf, "\t\t\t%s[%s] = true\n", ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey))
		if fi.TypeName != "" {
			fmt.Fprintf(&buf, "\t\t\t%s = make(map[string]%s)\n", target, fi.TypeName)
		} else {
			fmt.Fprintf(&buf, "\t\t\t%s = make(map[string]map[string]string)\n", target)
		}
		fmt.Fprintf(&buf, "\t\t\tfor _, subTable := range subTables {\n")
		fmt.Fprintf(&buf, "\t\t\t\tmapKey := document.SubTableKey(subTable, %q)\n", fi.TomlKey)
		mapEntryConsumedExpr := ctx.consumedKeyExpr(fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\t\t%s[%s + \".\" + mapKey] = true\n", ctx.consumedExpr, mapEntryConsumedExpr)
		fmt.Fprintf(&buf, "\t\t\t\tinner := document.GetStringMapFromTable(subTable)\n")
		fmt.Fprintf(&buf, "\t\t\t\tdocument.MarkAllConsumed(subTable, %s + \".\" + mapKey, %s)\n",
			mapEntryConsumedExpr, ctx.consumedExpr)
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
		fmt.Fprintf(&buf, "\t\t\t\t%sfmt.Errorf(\"%s[%%d]: %%w\", i, err)\n", ctx.returnErr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\t}\n")
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t\t%s[%s] = true\n", ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey))
		fmt.Fprintf(&buf, "\t}\n")

	case FieldDelegatedStruct:
		parts := strings.SplitN(fi.TypeName, ".", 2)
		if ctx.isRoot {
			fmt.Fprintf(&buf, "\tif tableNode := %s.FindTable(%q); tableNode != nil {\n", docVar, fi.TomlKey)
		} else {
			fmt.Fprintf(&buf, "\tif tableNode := %s.FindTableInContainer(%s, %q); tableNode != nil {\n",
				docVar, containerExpr, fi.TomlKey)
		}
		fmt.Fprintf(&buf, "\t\t%s[%s] = true\n", ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey))
		fmt.Fprintf(&buf, "\t\tif err := %s.Decode%sInto(&%s, %s, tableNode, %s, %s); err != nil {\n",
			parts[0], parts[1], target, docVar, ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey+"."))
		fmt.Fprintf(&buf, "\t\t\t%sfmt.Errorf(\"%s: %%w\", err)\n", ctx.returnErr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldPointerDelegatedStruct:
		parts := strings.SplitN(fi.TypeName, ".", 2)
		localVar := toLowerFirst(fi.GoName) + "Val"
		fmt.Fprintf(&buf, "\tif tableNode := %s.FindTableInContainer(%s, %q); tableNode != nil {\n",
			docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t%s[%s] = true\n", ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey))
		fmt.Fprintf(&buf, "\t\t%s := &%s{}\n", localVar, fi.TypeName)
		fmt.Fprintf(&buf, "\t\tif err := %s.Decode%sInto(%s, %s, tableNode, %s, %s); err != nil {\n",
			parts[0], parts[1], localVar, docVar, ctx.consumedExpr, ctx.consumedKeyExpr(fi.TomlKey+"."))
		fmt.Fprintf(&buf, "\t\t\t%sfmt.Errorf(\"%s: %%w\", err)\n", ctx.returnErr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t\t%s = %s\n", target, localVar)
		fmt.Fprintf(&buf, "\t}\n")
	}

	return buf.String()
}

// injectMapKeyIntoConsumedKeys replaces consumed key expressions in generated code
// to include the runtime mapKey variable. This handles the case where inner fields
// of a map[string]Struct need consumed keys like: baseKey + "." + mapKey + "." + innerKey
func injectMapKeyIntoConsumedKeys(code, consumedExpr, baseKey, innerPrefix string) string {
	// The inner fields generated consumed keys using innerPrefix (e.g. "servers.")
	// We need to replace those with baseKey + "." + mapKey + "." + innerKey
	// The generated code will have patterns like:
	//   consumed["servers.name"] or consumed[keyPrefix + "servers.name"]
	// We need to inject mapKey between the base key and the inner key.

	// For receiver context: consumed["servers.innerKey"] -> consumed["servers." + mapKey + ".innerKey"]
	// For free context: consumed[keyPrefix + "servers.innerKey"] -> consumed[keyPrefix + "servers." + mapKey + ".innerKey"]
	oldPrefix := consumedExpr + `[` + fmt.Sprintf(`%q`, innerPrefix)
	newPrefix := consumedExpr + `[` + fmt.Sprintf(`%q`, baseKey+".") + ` + mapKey + ".`

	// Handle the case where useKeyPrefixVar is true
	oldPrefixVar := consumedExpr + `[keyPrefix + ` + fmt.Sprintf(`%q`, innerPrefix)
	newPrefixVar := consumedExpr + `[keyPrefix + ` + fmt.Sprintf(`%q`, baseKey+".") + ` + mapKey + ".`

	code = strings.ReplaceAll(code, oldPrefixVar, newPrefixVar)
	code = strings.ReplaceAll(code, oldPrefix, newPrefix)
	return code
}

func emitEncodeField(ctx emitContext, fi FieldInfo, dataPath, docVar, containerExpr string) string {
	var buf bytes.Buffer
	source := dataPath + "." + fi.GoName

	switch fi.Kind {
	case FieldPrimitive:
		zv := zeroLiteral(fi.TypeName)
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
			fmt.Fprintf(&buf, "\t\t\t%serr\n", ctx.returnErr)
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t} else {\n")
			fmt.Fprintf(&buf, "\t\t_ = %s.DeleteFromContainer(%s, %q)\n", docVar, containerExpr, fi.TomlKey)
			fmt.Fprintf(&buf, "\t}\n")
		} else if fi.Multiline && fi.TypeName == "string" {
			fmt.Fprintf(&buf, "\tif %s != %s || %s.HasInContainer(%s, %q) {\n",
				source, zv, docVar, containerExpr, fi.TomlKey)
			fmt.Fprintf(&buf, "\t\tif err := %s.SetMultilineInContainer(%s, %q, %s); err != nil {\n",
				docVar, containerExpr, fi.TomlKey, encodeSource)
			fmt.Fprintf(&buf, "\t\t\t%serr\n", ctx.returnErr)
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t}\n")
		} else {
			fmt.Fprintf(&buf, "\tif %s != %s || %s.HasInContainer(%s, %q) {\n",
				source, zv, docVar, containerExpr, fi.TomlKey)
			fmt.Fprintf(&buf, "\t\tif err := %s.SetInContainer(%s, %q, %s); err != nil {\n",
				docVar, containerExpr, fi.TomlKey, encodeSource)
			fmt.Fprintf(&buf, "\t\t\t%serr\n", ctx.returnErr)
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldPointerPrimitive:
		fmt.Fprintf(&buf, "\tif %s != nil {\n", source)
		fmt.Fprintf(&buf, "\t\tif err := %s.SetInContainer(%s, %q, *%s); err != nil {\n",
			docVar, containerExpr, fi.TomlKey, source)
		fmt.Fprintf(&buf, "\t\t\t%serr\n", ctx.returnErr)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldCustom:
		fmt.Fprintf(&buf, "\t{\n")
		fmt.Fprintf(&buf, "\t\tv, err := %s.MarshalTOML()\n", source)
		fmt.Fprintf(&buf, "\t\tif err != nil {\n")
		fmt.Fprintf(&buf, "\t\t\t%sfmt.Errorf(\"%s: %%w\", err)\n", ctx.returnErr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t\tif err := %s.SetInContainer(%s, %q, v); err != nil {\n",
			docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\t%serr\n", ctx.returnErr)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldTextMarshaler:
		if fi.OmitEmpty {
			fmt.Fprintf(&buf, "\t{\n")
			fmt.Fprintf(&buf, "\t\tv, err := %s.MarshalText()\n", source)
			fmt.Fprintf(&buf, "\t\tif err != nil {\n")
			fmt.Fprintf(&buf, "\t\t\t%sfmt.Errorf(\"%s: %%w\", err)\n", ctx.returnErr, fi.TomlKey)
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t\tif len(v) > 0 {\n")
			fmt.Fprintf(&buf, "\t\t\tif err := %s.SetInContainer(%s, %q, string(v)); err != nil {\n",
				docVar, containerExpr, fi.TomlKey)
			fmt.Fprintf(&buf, "\t\t\t\t%serr\n", ctx.returnErr)
			fmt.Fprintf(&buf, "\t\t\t}\n")
			fmt.Fprintf(&buf, "\t\t} else {\n")
			fmt.Fprintf(&buf, "\t\t\t_ = %s.DeleteFromContainer(%s, %q)\n", docVar, containerExpr, fi.TomlKey)
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t}\n")
		} else {
			fmt.Fprintf(&buf, "\t{\n")
			fmt.Fprintf(&buf, "\t\tv, err := %s.MarshalText()\n", source)
			fmt.Fprintf(&buf, "\t\tif err != nil {\n")
			fmt.Fprintf(&buf, "\t\t\t%sfmt.Errorf(\"%s: %%w\", err)\n", ctx.returnErr, fi.TomlKey)
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t\tif err := %s.SetInContainer(%s, %q, string(v)); err != nil {\n",
				docVar, containerExpr, fi.TomlKey)
			fmt.Fprintf(&buf, "\t\t\t%serr\n", ctx.returnErr)
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldStruct:
		if fi.InnerInfo != nil {
			innerCtx := ctx.nested(fi.TomlKey + ".")
			needsTableNode := innerFieldsNeedContainer(fi.InnerInfo.Fields)
			if ctx.isRoot {
				fmt.Fprintf(&buf, "\t{\n")
				if needsTableNode {
					fmt.Fprintf(&buf, "\t\ttableNode := %s.EnsureTable(%q)\n", docVar, fi.TomlKey)
				} else {
					fmt.Fprintf(&buf, "\t\t_ = %s.EnsureTable(%q)\n", docVar, fi.TomlKey)
				}
			} else {
				fmt.Fprintf(&buf, "\t{\n")
				if needsTableNode {
					fmt.Fprintf(&buf, "\t\ttableNode := %s.EnsureTableInContainer(%s, %q)\n",
						docVar, containerExpr, fi.TomlKey)
				} else {
					fmt.Fprintf(&buf, "\t\t_ = %s.EnsureTableInContainer(%s, %q)\n",
						docVar, containerExpr, fi.TomlKey)
				}
			}
			for _, inner := range fi.InnerInfo.Fields {
				code := emitEncodeField(innerCtx, inner, source, docVar, "tableNode")
				buf.WriteString(code)
			}
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldPointerStruct:
		if fi.InnerInfo != nil {
			innerCtx := ctx.nested(fi.TomlKey + ".")
			fmt.Fprintf(&buf, "\tif %s != nil {\n", source)
			needsTableNode := innerFieldsNeedContainer(fi.InnerInfo.Fields)
			if needsTableNode {
				fmt.Fprintf(&buf, "\t\ttableNode := %s.EnsureTableInContainer(%s, %q)\n",
					docVar, containerExpr, fi.TomlKey)
			} else {
				fmt.Fprintf(&buf, "\t\t_ = %s.EnsureTableInContainer(%s, %q)\n",
					docVar, containerExpr, fi.TomlKey)
			}
			for _, inner := range fi.InnerInfo.Fields {
				code := emitEncodeField(innerCtx, inner, source, docVar, "tableNode")
				buf.WriteString("\t" + code)
			}
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldSliceStruct:
		crossPkg := strings.Contains(fi.TypeName, ".")
		fullKey := ctx.keyPrefix + fi.TomlKey
		fmt.Fprintf(&buf, "\t{\n")
		if ctx.emitHandles && !crossPkg {
			handleSlice := "d." + toLowerFirst(fi.GoName)
			fmt.Fprintf(&buf, "\tfor i := range %s {\n", source)
			fmt.Fprintf(&buf, "\t\tvar container *cst.Node\n")
			fmt.Fprintf(&buf, "\t\tif i < len(%s) {\n", handleSlice)
			fmt.Fprintf(&buf, "\t\t\tcontainer = %s[i].node\n", handleSlice)
			fmt.Fprintf(&buf, "\t\t} else {\n")
			fmt.Fprintf(&buf, "\t\t\tcontainer = %s.AppendArrayTableEntry(%q)\n", docVar, fi.TomlKey)
			fmt.Fprintf(&buf, "\t\t}\n")
		} else {
			existingVar := fi.TomlKey + "Existing"
			fmt.Fprintf(&buf, "\t%s := %s.FindArrayTableNodes(%q)\n", existingVar, docVar, fullKey)
			fmt.Fprintf(&buf, "\tfor i := range %s {\n", source)
			fmt.Fprintf(&buf, "\t\tvar container *cst.Node\n")
			fmt.Fprintf(&buf, "\t\tif i < len(%s) {\n", existingVar)
			fmt.Fprintf(&buf, "\t\t\tcontainer = %s[i]\n", existingVar)
			fmt.Fprintf(&buf, "\t\t} else {\n")
			fmt.Fprintf(&buf, "\t\t\tcontainer = %s.AppendArrayTableEntry(%q)\n", docVar, fullKey)
			fmt.Fprintf(&buf, "\t\t}\n")
		}
		if fi.InnerInfo != nil {
			for _, inner := range fi.InnerInfo.Fields {
				indexedSource := fmt.Sprintf("%s[i]", source)
				code := emitEncodeField(ctx.nested(""), inner, indexedSource, docVar, "container")
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
		fmt.Fprintf(&buf, "\t\t\t%sfmt.Errorf(\"%s[%%d]: %%w\", i, err)\n", ctx.returnErr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldSlicePrimitive:
		if fi.OmitEmpty {
			fmt.Fprintf(&buf, "\tif len(%s) > 0 || %s.HasInContainer(%s, %q) {\n",
				source, docVar, containerExpr, fi.TomlKey)
		}
		encodeSource := source
		if fi.SlicePointer {
			tmpVar := "tmp" + fi.GoName
			fmt.Fprintf(&buf, "\t%s := make([]%s, 0, len(%s))\n", tmpVar, fi.ElemType, source)
			fmt.Fprintf(&buf, "\tfor _, p := range %s {\n", source)
			fmt.Fprintf(&buf, "\t\tif p != nil {\n")
			fmt.Fprintf(&buf, "\t\t\t%s = append(%s, *p)\n", tmpVar, tmpVar)
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t}\n")
			encodeSource = tmpVar
		} else if fi.TypeName != "" {
			encodeSource = "[]" + fi.ElemType + "(" + source + ")"
		}
		fmt.Fprintf(&buf, "\tif err := %s.SetInContainer(%s, %q, %s); err != nil {\n",
			docVar, containerExpr, fi.TomlKey, encodeSource)
		fmt.Fprintf(&buf, "\t\t%serr\n", ctx.returnErr)
		fmt.Fprintf(&buf, "\t}\n")
		if fi.OmitEmpty {
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldMapStringString:
		fmt.Fprintf(&buf, "\tif len(%s) > 0 {\n", source)
		if ctx.isRoot {
			fmt.Fprintf(&buf, "\t\ttableNode := %s.EnsureTable(%q)\n", docVar, fi.TomlKey)
		} else {
			fmt.Fprintf(&buf, "\t\ttableNode := %s.EnsureTableInContainer(%s, %q)\n", docVar, containerExpr, fi.TomlKey)
		}
		fmt.Fprintf(&buf, "\t\tdocument.DeleteAllInContainer(tableNode)\n")
		fmt.Fprintf(&buf, "\t\tfor k, v := range %s {\n", source)
		fmt.Fprintf(&buf, "\t\t\tif err := %s.SetInContainer(tableNode, k, v); err != nil {\n", docVar)
		fmt.Fprintf(&buf, "\t\t\t\t%serr\n", ctx.returnErr)
		fmt.Fprintf(&buf, "\t\t\t}\n")
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldMapStringStruct:
		if fi.InnerInfo != nil {
			fmt.Fprintf(&buf, "\tif len(%s) > 0 {\n", source)
			fmt.Fprintf(&buf, "\t\tfor mapKey, mapVal := range %s {\n", source)
			if ctx.isRoot {
				fmt.Fprintf(&buf, "\t\t\tsubTable := %s.EnsureSubTable(%q, mapKey)\n", docVar, fi.TomlKey)
			} else {
				fmt.Fprintf(&buf, "\t\t\tsubTable := %s.EnsureSubTableInContainer(%s, %q, mapKey)\n", docVar, containerExpr, fi.TomlKey)
			}
			if fi.SlicePointer {
				fmt.Fprintf(&buf, "\t\t\tif mapVal == nil {\n")
				fmt.Fprintf(&buf, "\t\t\t\tcontinue\n")
				fmt.Fprintf(&buf, "\t\t\t}\n")
				for _, inner := range fi.InnerInfo.Fields {
					code := emitEncodeField(ctx.nested(""), inner, "(*mapVal)", docVar, "subTable")
					buf.WriteString("\t\t" + code)
				}
			} else {
				for _, inner := range fi.InnerInfo.Fields {
					code := emitEncodeField(ctx.nested(""), inner, "mapVal", docVar, "subTable")
					buf.WriteString("\t\t" + code)
				}
			}
			fmt.Fprintf(&buf, "\t\t}\n")
			fmt.Fprintf(&buf, "\t}\n")
		}

	case FieldMapStringDelegatedStruct:
		parts := strings.SplitN(fi.ElemType, ".", 2)
		fmt.Fprintf(&buf, "\tif len(%s) > 0 {\n", source)
		fmt.Fprintf(&buf, "\t\tfor mapKey, mapVal := range %s {\n", source)
		if ctx.isRoot {
			fmt.Fprintf(&buf, "\t\t\tsubTable := %s.EnsureSubTable(%q, mapKey)\n", docVar, fi.TomlKey)
		} else {
			fmt.Fprintf(&buf, "\t\t\tsubTable := %s.EnsureSubTableInContainer(%s, %q, mapKey)\n", docVar, containerExpr, fi.TomlKey)
		}
		fmt.Fprintf(&buf, "\t\t\tif err := %s.Encode%sFrom(&mapVal, %s, subTable); err != nil {\n",
			parts[0], parts[1], docVar)
		fmt.Fprintf(&buf, "\t\t\t\t%sfmt.Errorf(\"%s.%%s: %%w\", mapKey, err)\n", ctx.returnErr, fi.TomlKey)
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
		fmt.Fprintf(&buf, "\t\t\t\t\t%serr\n", ctx.returnErr)
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
		fmt.Fprintf(&buf, "\t\t\t\t%sfmt.Errorf(\"%s[%%d]: %%w\", i, err)\n", ctx.returnErr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\t}\n")
		fmt.Fprintf(&buf, "\t\t\tvals[i] = string(v)\n")
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t\tif err := %s.SetInContainer(%s, %q, vals); err != nil {\n",
			docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t\t%serr\n", ctx.returnErr)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldDelegatedStruct:
		parts := strings.SplitN(fi.TypeName, ".", 2)
		if ctx.isRoot {
			fmt.Fprintf(&buf, "\t{\n")
			fmt.Fprintf(&buf, "\t\ttableNode := %s.EnsureTable(%q)\n", docVar, fi.TomlKey)
		} else {
			fmt.Fprintf(&buf, "\t{\n")
			fmt.Fprintf(&buf, "\t\ttableNode := %s.EnsureTableInContainer(%s, %q)\n",
				docVar, containerExpr, fi.TomlKey)
		}
		fmt.Fprintf(&buf, "\t\tif err := %s.Encode%sFrom(&%s, %s, tableNode); err != nil {\n",
			parts[0], parts[1], source, docVar)
		fmt.Fprintf(&buf, "\t\t\t%sfmt.Errorf(\"%s: %%w\", err)\n", ctx.returnErr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")

	case FieldPointerDelegatedStruct:
		parts := strings.SplitN(fi.TypeName, ".", 2)
		fmt.Fprintf(&buf, "\tif %s != nil {\n", source)
		fmt.Fprintf(&buf, "\t\ttableNode := %s.EnsureTableInContainer(%s, %q)\n",
			docVar, containerExpr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\tif err := %s.Encode%sFrom(%s, %s, tableNode); err != nil {\n",
			parts[0], parts[1], source, docVar)
		fmt.Fprintf(&buf, "\t\t\t%sfmt.Errorf(\"%s: %%w\", err)\n", ctx.returnErr, fi.TomlKey)
		fmt.Fprintf(&buf, "\t\t}\n")
		fmt.Fprintf(&buf, "\t}\n")
	}

	return buf.String()
}

func zeroLiteral(typeName string) string {
	switch typeName {
	case "bool":
		return "false"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return "0"
	case "float32", "float64":
		return "0.0"
	case "string":
		return `""`
	default:
		return `""`
	}
}

func innerFieldsNeedContainer(fields []FieldInfo) bool {
	for _, f := range fields {
		switch f.Kind {
		case FieldSliceStruct, FieldSliceDelegatedStruct:
			continue
		default:
			return true
		}
	}
	return false
}

func toLowerFirst(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}
