package generate

import (
	"bytes"
	"fmt"
	"strings"
)

// decoder holds all state needed to render IR decode ops into Go source.
type decoder struct {
	buf          *bytes.Buffer
	consumedExpr string // "d.consumed" or "consumed"
	returnErr    string // "return nil, " or "return "
	docVar       string // "d.cstDoc" or "doc"
	keyPrefixVar bool   // prepend Go variable "keyPrefix" to consumed keys
}

func newReceiverDecoder() *decoder {
	return &decoder{
		buf:          &bytes.Buffer{},
		consumedExpr: "d.consumed",
		returnErr:    "return nil, ",
		docVar:       "d.cstDoc",
	}
}

func newFreeDecoder() *decoder {
	return &decoder{
		buf:          &bytes.Buffer{},
		consumedExpr: "consumed",
		returnErr:    "return ",
		docVar:       "doc",
		keyPrefixVar: true,
	}
}

func (d *decoder) consumedKeyExpr(key string) string {
	if d.keyPrefixVar {
		return fmt.Sprintf(`keyPrefix + %q`, key)
	}
	return fmt.Sprintf(`%q`, key)
}

func (d *decoder) w(format string, args ...any) {
	fmt.Fprintf(d.buf, format, args...)
}

// irDecodeBody generates the decode function body using the IR path.
func irDecodeBody(si StructInfo) string {
	ops := buildDecodeOps(si, "d.data", "", ReceiverTarget("d", "data"), StaticKey(""), true, true)
	dec := newReceiverDecoder()
	dec.renderOps(ops, "d.cstDoc.Root()", "\t", "")
	if si.Validatable {
		dec.w("\tif err := d.data.Validate(); err != nil {\n")
		dec.w("\t\treturn nil, fmt.Errorf(\"validation failed: %%w\", err)\n")
		dec.w("\t}\n")
	}
	return dec.buf.String()
}

// irDecodeIntoBody generates the DecodeXInto function body using the IR path.
func irDecodeIntoBody(si StructInfo) string {
	ops := buildDecodeOps(si, "data", "", LocalTarget("data"), PrefixedKey(""), false, false)
	dec := newFreeDecoder()
	dec.renderOps(ops, "container", "\t", "")
	return dec.buf.String()
}

func (d *decoder) renderOps(ops []DecodeOp, containerVar, indent, foundVar string) {
	for _, op := range ops {
		d.renderOp(op, containerVar, indent, foundVar)
	}
}

func (d *decoder) renderOp(op DecodeOp, containerVar, indent, foundVar string) {
	switch o := op.(type) {
	case GetPrimitive:
		d.getPrimitive(o, containerVar, indent, foundVar)
	case GetCustom:
		d.getCustom(o, containerVar, indent, foundVar)
	case GetTextMarshaler:
		d.getTextMarshaler(o, containerVar, indent, foundVar)
	case GetSlicePrimitive:
		d.getSlicePrimitive(o, containerVar, indent)
	case GetSliceTextMarshaler:
		d.getSliceTextMarshaler(o, containerVar, indent)
	case GetMapStringString:
		d.getMapStringString(o, containerVar, indent, foundVar)
	case GetMapStringMapStringString:
		d.getMapStringMapStringString(o, containerVar, indent)
	case InTable:
		d.inTable(o, containerVar, indent)
	case InPointerTable:
		d.inPointerTable(o, containerVar, indent)
	case ForArrayTable:
		d.forArrayTable(o, containerVar, indent, foundVar)
	case ForMapStringStruct:
		d.forMapStringStruct(o, containerVar, indent)
	case DelegateStruct:
		d.delegateStruct(o, containerVar, indent)
	case DelegateSlice:
		d.delegateSlice(o, containerVar, indent, foundVar)
	case DelegateMap:
		d.delegateMap(o, containerVar, indent)
	case WithFoundVar:
		d.renderOp(o.Inner, containerVar, indent, o.FoundVar)
	}
}

// tomlKey extracts the bare TOML key from a possibly-prefixed key.
func tomlKey(fullKey string) string {
	if i := strings.LastIndex(fullKey, "."); i >= 0 {
		return fullKey[i+1:]
	}
	return fullKey
}

// keyPathSuffix converts a dotted TOML key to a CamelCase suffix for unique
// variable names in generated code. Splits on dots, hyphens, and underscores
// to produce valid Go identifiers.
// "haustoria.caldav" -> "HaustoriaCaldav", "exec-command" -> "ExecCommand".
func keyPathSuffix(key string) string {
	var sb strings.Builder
	for _, seg := range strings.FieldsFunc(key, func(r rune) bool {
		return r == '.' || r == '-' || r == '_'
	}) {
		seg = strings.TrimSpace(seg)
		if seg != "" {
			sb.WriteString(toUpperFirst(seg))
		}
	}
	return sb.String()
}

func (d *decoder) findTable(indent, containerVar, bareKey string, useRootAPI bool) {
	if useRootAPI {
		d.w("%sif tableNode := %s.FindTable(%q); tableNode != nil {\n", indent, d.docVar, bareKey)
	} else {
		d.w("%sif tableNode := %s.FindTableInContainer(%s, %q); tableNode != nil {\n",
			indent, d.docVar, containerVar, bareKey)
	}
}

func (d *decoder) markConsumed(indent, key string) {
	d.w("%s%s[%s] = true\n", indent, d.consumedExpr, d.consumedKeyExpr(key))
}

func (d *decoder) markFound(indent, foundVar string) {
	if foundVar != "" {
		d.w("%s%s = true\n", indent, foundVar)
	}
}

func (d *decoder) getPrimitive(o GetPrimitive, containerVar, indent, foundVar string) {
	bareKey := tomlKey(o.Key)
	d.w("%sif v, err := document.GetFromContainer[%s](%s, %s, %q); err == nil {\n",
		indent, o.TypeName, d.docVar, containerVar, bareKey)
	if o.Pointer {
		d.w("%s\t%s = &v\n", indent, o.Target)
	} else if o.ElemType != "" {
		d.w("%s\t%s = %s(v)\n", indent, o.Target, o.ElemType)
	} else {
		d.w("%s\t%s = v\n", indent, o.Target)
	}
	d.markFound(indent+"\t", foundVar)
	d.markConsumed(indent+"\t", o.Key)
	d.w("%s}\n", indent)
}

func (d *decoder) getCustom(o GetCustom, containerVar, indent, foundVar string) {
	bareKey := tomlKey(o.Key)
	d.w("%sif raw, err := document.GetRawFromContainer(%s, %s, %q); err == nil {\n",
		indent, d.docVar, containerVar, bareKey)
	d.w("%s\tif err := %s.UnmarshalTOML(raw); err != nil {\n", indent, o.Target)
	d.w("%s\t\t%sfmt.Errorf(\"%s: %%w\", err)\n", indent, d.returnErr, bareKey)
	d.w("%s\t}\n", indent)
	d.markFound(indent+"\t", foundVar)
	d.markConsumed(indent+"\t", o.Key)
	d.w("%s}\n", indent)
}

func (d *decoder) getTextMarshaler(o GetTextMarshaler, containerVar, indent, foundVar string) {
	bareKey := tomlKey(o.Key)
	d.w("%sif v, err := document.GetFromContainer[string](%s, %s, %q); err == nil {\n",
		indent, d.docVar, containerVar, bareKey)
	d.w("%s\tif err := %s.UnmarshalText([]byte(v)); err != nil {\n", indent, o.Target)
	d.w("%s\t\t%sfmt.Errorf(\"%s: %%w\", err)\n", indent, d.returnErr, bareKey)
	d.w("%s\t}\n", indent)
	d.markFound(indent+"\t", foundVar)
	d.markConsumed(indent+"\t", o.Key)
	d.w("%s}\n", indent)
}

func (d *decoder) getSlicePrimitive(o GetSlicePrimitive, containerVar, indent string) {
	bareKey := tomlKey(o.Key)
	d.w("%sif v, err := document.GetFromContainer[[]%s](%s, %s, %q); err == nil {\n",
		indent, o.ElemType, d.docVar, containerVar, bareKey)
	if o.SlicePointer {
		d.w("%s\t%s = make([]*%s, len(v))\n", indent, o.Target, o.ElemType)
		d.w("%s\tfor i := range v {\n", indent)
		d.w("%s\t\t%s[i] = &v[i]\n", indent, o.Target)
		d.w("%s\t}\n", indent)
	} else if o.TypeName != "" {
		d.w("%s\t%s = %s(v)\n", indent, o.Target, o.TypeName)
	} else {
		d.w("%s\t%s = v\n", indent, o.Target)
	}
	d.markConsumed(indent+"\t", o.Key)
	d.w("%s}\n", indent)
}

func (d *decoder) getSliceTextMarshaler(o GetSliceTextMarshaler, containerVar, indent string) {
	bareKey := tomlKey(o.Key)
	d.w("%sif v, err := document.GetFromContainer[[]string](%s, %s, %q); err == nil {\n",
		indent, d.docVar, containerVar, bareKey)
	d.w("%s\t%s = make([]%s, len(v))\n", indent, o.Target, o.TypeName)
	d.w("%s\tfor i, s := range v {\n", indent)
	d.w("%s\t\tif err := %s[i].UnmarshalText([]byte(s)); err != nil {\n", indent, o.Target)
	d.w("%s\t\t\t%sfmt.Errorf(\"%s[%%d]: %%w\", i, err)\n", indent, d.returnErr, bareKey)
	d.w("%s\t\t}\n", indent)
	d.w("%s\t}\n", indent)
	d.markConsumed(indent+"\t", o.Key)
	d.w("%s}\n", indent)
}

func (d *decoder) getMapStringString(o GetMapStringString, containerVar, indent, foundVar string) {
	bareKey := tomlKey(o.Key)
	d.findTable(indent, containerVar, bareKey, o.UseRootAPI)
	d.w("%s\t%s = document.GetStringMapFromTable(tableNode)\n", indent, o.Target)
	d.markFound(indent+"\t", foundVar)
	d.markConsumed(indent+"\t", o.Key)
	d.w("%s\tdocument.MarkAllConsumed(tableNode, %s, %s)\n",
		indent, d.consumedKeyExpr(o.Key), d.consumedExpr)
	d.w("%s}\n", indent)
}

func (d *decoder) getMapStringMapStringString(o GetMapStringMapStringString, containerVar, indent string) {
	bareKey := tomlKey(o.Key)
	d.w("%s{\n", indent)
	d.w("%s\tsubTables := %s.FindSubTables(%q)\n", indent, d.docVar, bareKey)
	d.w("%s\tif len(subTables) > 0 {\n", indent)
	d.markConsumed(indent+"\t\t", o.Key)
	if o.TypeName != "" {
		d.w("%s\t\t%s = make(map[string]%s)\n", indent, o.Target, o.TypeName)
	} else {
		d.w("%s\t\t%s = make(map[string]map[string]string)\n", indent, o.Target)
	}
	d.w("%s\t\tfor _, subTable := range subTables {\n", indent)
	d.w("%s\t\t\tmapKey := document.SubTableKey(subTable, %q)\n", indent, bareKey)
	mapEntry := d.consumedKeyExpr(o.Key)
	d.w("%s\t\t\t%s[%s + \".\" + mapKey] = true\n", indent, d.consumedExpr, mapEntry)
	d.w("%s\t\t\tinner := document.GetStringMapFromTable(subTable)\n", indent)
	d.w("%s\t\t\tdocument.MarkAllConsumed(subTable, %s + \".\" + mapKey, %s)\n",
		indent, mapEntry, d.consumedExpr)
	if o.TypeName != "" {
		d.w("%s\t\t\t%s[mapKey] = %s(inner)\n", indent, o.Target, o.TypeName)
	} else {
		d.w("%s\t\t\t%s[mapKey] = inner\n", indent, o.Target)
	}
	d.w("%s\t\t}\n", indent)
	d.w("%s\t}\n", indent)
	d.w("%s}\n", indent)
}

func (d *decoder) inTable(o InTable, containerVar, indent string) {
	bareKey := tomlKey(o.Key)
	d.findTable(indent, containerVar, bareKey, o.UseRootAPI)
	d.markConsumed(indent+"\t", o.Key)
	d.renderOps(o.Fields, "tableNode", indent+"\t", "")
	d.w("%s}\n", indent)
}

func (d *decoder) inPointerTable(o InPointerTable, containerVar, indent string) {
	bareKey := tomlKey(o.Key)
	localVar := toLowerFirst(strings.TrimSuffix(o.Target[strings.LastIndex(o.Target, ".")+1:], "")) + "Val"
	tableVar := "_tbl" + keyPathSuffix(o.Key)

	d.w("%s%s := %s.FindTableInContainer(%s, %q)\n",
		indent, tableVar, d.docVar, containerVar, bareKey)
	d.w("%sif %s != nil {\n", indent, tableVar)
	d.markConsumed(indent+"\t", o.Key)
	d.w("%s\t%s := &%s{}\n", indent, localVar, o.TypeName)
	d.renderOps(o.TableFields, tableVar, indent+"\t", "")
	d.w("%s\t%s = %s\n", indent, o.Target, localVar)
	d.w("%s} else {\n", indent)
	d.w("%s\t%s := &%s{}\n", indent, localVar, o.TypeName)
	d.w("%s\tfound := false\n", indent)
	d.renderOps(o.FlatFields, containerVar, indent+"\t", "found")
	d.w("%s\tif found {\n", indent)
	d.w("%s\t\t%s = %s\n", indent, o.Target, localVar)
	d.w("%s\t}\n", indent)
	d.w("%s}\n", indent)
}

func (d *decoder) forArrayTable(o ForArrayTable, containerVar, indent, foundVar string) {
	nodesVar := "_nodes" + keyPathSuffix(o.DottedKey)
	d.w("%s%s := %s.FindArrayTableNodes(%s)\n", indent, nodesVar,
		d.docVar, d.consumedKeyExpr(o.DottedKey))
	if foundVar != "" {
		d.w("%sif len(%s) > 0 {\n", indent, nodesVar)
		d.w("%s\t%s = true\n", indent, foundVar)
		d.w("%s}\n", indent)
	}
	if o.TrackHandles {
		handleName := toLowerFirst(o.TypeName) + "Handle"
		goName := toLowerFirst(strings.TrimSuffix(o.Target[strings.LastIndex(o.Target, ".")+1:], ""))
		d.w("%sd.%s = make([]%s, len(%s))\n", indent, goName, handleName, nodesVar)
	}
	if o.SlicePointer {
		d.w("%s%s = make([]*%s, len(%s))\n", indent, o.Target, o.TypeName, nodesVar)
	} else {
		d.w("%s%s = make([]%s, len(%s))\n", indent, o.Target, o.TypeName, nodesVar)
	}
	d.markConsumed(indent, o.DottedKey)
	d.w("%sfor i, node := range %s {\n", indent, nodesVar)
	if o.TrackHandles {
		handleName := toLowerFirst(o.TypeName) + "Handle"
		goName := toLowerFirst(strings.TrimSuffix(o.Target[strings.LastIndex(o.Target, ".")+1:], ""))
		d.w("%s\td.%s[i] = %s{node: node}\n", indent, goName, handleName)
	}
	if o.SlicePointer {
		d.w("%s\t%s[i] = &%s{}\n", indent, o.Target, o.TypeName)
	}
	d.renderOps(o.Fields, "node", indent+"\t", "")
	d.w("%s}\n", indent)
}

func (d *decoder) forMapStringStruct(o ForMapStringStruct, containerVar, indent string) {
	bareKey := tomlKey(o.Key)
	d.w("%s{\n", indent)
	if o.UseRootAPI {
		d.w("%s\tsubTables := %s.FindSubTables(%q)\n", indent, d.docVar, bareKey)
	} else {
		d.w("%s\tsubTables := %s.FindSubTablesInContainer(%s, %q)\n",
			indent, d.docVar, containerVar, bareKey)
	}
	d.w("%s\tif len(subTables) > 0 {\n", indent)
	d.markConsumed(indent+"\t\t", o.Key)
	if o.SlicePointer {
		d.w("%s\t\t%s = make(map[string]*%s)\n", indent, o.Target, o.TypeName)
	} else {
		d.w("%s\t\t%s = make(map[string]%s)\n", indent, o.Target, o.TypeName)
	}
	d.w("%s\t\tfor _, subTable := range subTables {\n", indent)
	if o.UseRootAPI {
		d.w("%s\t\t\tmapKey := document.SubTableKey(subTable, %q)\n", indent, bareKey)
	} else {
		d.w("%s\t\t\tmapKey := document.SubTableKeyInContainer(subTable, %s, %q)\n",
			indent, containerVar, bareKey)
	}
	mapEntry := d.consumedKeyExpr(o.Key)
	d.w("%s\t\t\t%s[%s + \".\" + mapKey] = true\n", indent, d.consumedExpr, mapEntry)
	d.w("%s\t\t\tvar entry %s\n", indent, o.TypeName)
	d.renderMapInnerFields(o.Fields, "subTable", indent+"\t\t\t", o.Key)
	if o.SlicePointer {
		d.w("%s\t\t\t%s[mapKey] = &entry\n", indent, o.Target)
	} else {
		d.w("%s\t\t\t%s[mapKey] = entry\n", indent, o.Target)
	}
	d.w("%s\t\t}\n", indent)
	d.w("%s\t}\n", indent)
	d.w("%s}\n", indent)
}

func (d *decoder) renderMapInnerFields(ops []DecodeOp, containerVar, indent, parentKey string) {
	// Render each inner field into a temporary buffer, then rewrite consumed
	// keys to include the runtime mapKey variable.
	saved := d.buf
	for _, op := range ops {
		tmp := &bytes.Buffer{}
		d.buf = tmp
		d.renderOp(op, containerVar, indent, "")
		code := tmp.String()
		code = d.injectMapKey(code, parentKey)
		saved.WriteString(code)
	}
	d.buf = saved
}

func (d *decoder) injectMapKey(code, parentKey string) string {
	parentDot := parentKey + "."
	if d.keyPrefixVar {
		old := d.consumedExpr + `[keyPrefix + "` + parentDot
		repl := d.consumedExpr + `[keyPrefix + "` + parentKey + `." + mapKey + ".`
		code = strings.ReplaceAll(code, old, repl)
	} else {
		old := d.consumedExpr + `["` + parentDot
		repl := d.consumedExpr + `["` + parentKey + `." + mapKey + ".`
		code = strings.ReplaceAll(code, old, repl)
	}
	return code
}

func (d *decoder) delegateStruct(o DelegateStruct, containerVar, indent string) {
	parts := strings.SplitN(o.TypeName, ".", 2)
	bareKey := tomlKey(o.Key)
	if o.Pointer {
		localVar := toLowerFirst(parts[1]) + "Val"
		d.w("%sif tableNode := %s.FindTableInContainer(%s, %q); tableNode != nil {\n",
			indent, d.docVar, containerVar, bareKey)
		d.markConsumed(indent+"\t", o.Key)
		d.w("%s\t%s := &%s{}\n", indent, localVar, o.TypeName)
		d.w("%s\tif err := %s.Decode%sInto(%s, %s, tableNode, %s, %s); err != nil {\n",
			indent, parts[0], parts[1], localVar, d.docVar, d.consumedExpr, d.consumedKeyExpr(o.Key+"."))
		d.w("%s\t\t%sfmt.Errorf(\"%s: %%w\", err)\n", indent, d.returnErr, bareKey)
		d.w("%s\t}\n", indent)
		d.w("%s\t%s = %s\n", indent, o.Target, localVar)
		d.w("%s}\n", indent)
	} else {
		d.findTable(indent, containerVar, bareKey, o.UseRootAPI)
		d.markConsumed(indent+"\t", o.Key)
		d.w("%s\tif err := %s.Decode%sInto(&%s, %s, tableNode, %s, %s); err != nil {\n",
			indent, parts[0], parts[1], o.Target, d.docVar, d.consumedExpr, d.consumedKeyExpr(o.Key+"."))
		d.w("%s\t\t%sfmt.Errorf(\"%s: %%w\", err)\n", indent, d.returnErr, bareKey)
		d.w("%s\t}\n", indent)
		d.w("%s}\n", indent)
	}
}

func (d *decoder) delegateSlice(o DelegateSlice, containerVar, indent, foundVar string) {
	parts := strings.SplitN(o.TypeName, ".", 2)
	nodesVar := tomlKey(o.Key) + "Nodes"
	d.w("%s%s := %s.FindArrayTableNodes(%s)\n", indent, nodesVar,
		d.docVar, d.consumedKeyExpr(o.DottedKey))
	if foundVar != "" {
		d.w("%sif len(%s) > 0 {\n", indent, nodesVar)
		d.w("%s\t%s = true\n", indent, foundVar)
		d.w("%s}\n", indent)
	}
	if o.SlicePointer {
		d.w("%s%s = make([]*%s, len(%s))\n", indent, o.Target, o.TypeName, nodesVar)
	} else {
		d.w("%s%s = make([]%s, len(%s))\n", indent, o.Target, o.TypeName, nodesVar)
	}
	d.markConsumed(indent, o.DottedKey)
	d.w("%sfor i, node := range %s {\n", indent, nodesVar)
	if o.SlicePointer {
		d.w("%s\t%s[i] = &%s{}\n", indent, o.Target, o.TypeName)
		d.w("%s\tif err := %s.Decode%sInto(%s[i], %s, node, %s, %s); err != nil {\n",
			indent, parts[0], parts[1], o.Target, d.docVar, d.consumedExpr, d.consumedKeyExpr(o.DottedKey+"."))
	} else {
		d.w("%s\tif err := %s.Decode%sInto(&%s[i], %s, node, %s, %s); err != nil {\n",
			indent, parts[0], parts[1], o.Target, d.docVar, d.consumedExpr, d.consumedKeyExpr(o.DottedKey+"."))
	}
	d.w("%s\t\t%sfmt.Errorf(\"%s[%%d]: %%w\", i, err)\n", indent, d.returnErr, tomlKey(o.Key))
	d.w("%s\t}\n", indent)
	d.w("%s}\n", indent)
}

func (d *decoder) delegateMap(o DelegateMap, containerVar, indent string) {
	parts := strings.SplitN(o.ElemType, ".", 2)
	bareKey := tomlKey(o.Key)
	d.w("%s{\n", indent)
	if o.UseRootAPI {
		d.w("%s\tsubTables := %s.FindSubTables(%q)\n", indent, d.docVar, bareKey)
	} else {
		d.w("%s\tsubTables := %s.FindSubTablesInContainer(%s, %q)\n",
			indent, d.docVar, containerVar, bareKey)
	}
	d.w("%s\tif len(subTables) > 0 {\n", indent)
	d.markConsumed(indent+"\t\t", o.Key)
	d.w("%s\t\t%s = make(map[string]%s)\n", indent, o.Target, o.ElemType)
	d.w("%s\t\tfor _, subTable := range subTables {\n", indent)
	if o.UseRootAPI {
		d.w("%s\t\t\tmapKey := document.SubTableKey(subTable, %q)\n", indent, bareKey)
	} else {
		d.w("%s\t\t\tmapKey := document.SubTableKeyInContainer(subTable, %s, %q)\n",
			indent, containerVar, bareKey)
	}
	d.w("%s\t\t\tif strings.Contains(mapKey, \".\") {\n", indent)
	d.w("%s\t\t\t\tcontinue\n", indent)
	d.w("%s\t\t\t}\n", indent)
	mapEntry := d.consumedKeyExpr(o.Key)
	d.w("%s\t\t\t%s[%s + \".\" + mapKey] = true\n", indent, d.consumedExpr, mapEntry)
	d.w("%s\t\t\tvar entry %s\n", indent, o.ElemType)
	delegateKeyExpr := mapEntry + ` + "." + mapKey + "."`
	d.w("%s\t\t\tif err := %s.Decode%sInto(&entry, %s, subTable, %s, %s); err != nil {\n",
		indent, parts[0], parts[1], d.docVar, d.consumedExpr, delegateKeyExpr)
	d.w("%s\t\t\t\t%sfmt.Errorf(\"%s.%%s: %%w\", mapKey, err)\n", indent, d.returnErr, bareKey)
	d.w("%s\t\t\t}\n", indent)
	d.w("%s\t\t\t%s[mapKey] = entry\n", indent, o.Target)
	d.w("%s\t\t}\n", indent)
	d.w("%s\t}\n", indent)
	d.w("%s}\n", indent)
}
