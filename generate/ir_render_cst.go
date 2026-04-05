package generate

import (
	"bytes"
	"fmt"
	"strings"
)

type cstDecoder struct {
	buf          *bytes.Buffer
	consumedExpr string
	returnErr    string
	docVar       string
	rootExpr     string
	keyPrefixVar bool
	// mapKeyReplace replaces a static key prefix in table header matches with
	// a dynamic one that includes the runtime map key. e.g., replaces
	// "targets." with `"targets." + _mk + "."` to match [targets.prod.auth].
	mapKeyReplaceFrom string // static prefix to replace (e.g. "targets.")
	mapKeyReplaceTo   string // dynamic Go expression (e.g. `"targets." + _mk + "."`)
}

func newReceiverCSTDecoder() *cstDecoder {
	return &cstDecoder{
		buf: &bytes.Buffer{}, consumedExpr: "d.consumed", returnErr: "return nil, ",
		docVar: "d.cstDoc", rootExpr: "d.cstDoc.Root()",
	}
}

func newFreeCSTDecoder() *cstDecoder {
	return &cstDecoder{
		buf: &bytes.Buffer{}, consumedExpr: "consumed", returnErr: "return ",
		docVar: "doc", rootExpr: "doc.Root()", keyPrefixVar: true,
	}
}

func (d *cstDecoder) consumedKeyExpr(key string) string {
	if d.keyPrefixVar {
		return fmt.Sprintf(`keyPrefix + %q`, key)
	}
	return fmt.Sprintf(`%q`, key)
}

func (d *cstDecoder) w(f string, a ...any) { fmt.Fprintf(d.buf, f, a...) }
func (d *cstDecoder) mc(indent, key string) {
	d.w("%s%s[%s] = true\n", indent, d.consumedExpr, d.consumedKeyExpr(key))
}

// tm returns a Go expression for matching a table header key.
// When inside a map iteration, replaces the static prefix with a dynamic one.
func (d *cstDecoder) tm(key string) string {
	if d.mapKeyReplaceTo != "" && strings.HasPrefix(key, d.mapKeyReplaceFrom) {
		rest := key[len(d.mapKeyReplaceFrom):]
		expr := d.mapKeyReplaceTo + ` + ` + fmt.Sprintf(`%q`, rest)
		if d.keyPrefixVar {
			return `keyPrefix + ` + expr
		}
		return expr
	}
	if d.keyPrefixVar {
		return `keyPrefix + ` + fmt.Sprintf(`%q`, key)
	}
	return fmt.Sprintf(`%q`, key)
}

// tp is an alias for tm (same logic applies to prefix matching).
func (d *cstDecoder) tp(prefix string) string {
	return d.tm(prefix)
}

// tpLen returns a Go expression for the length of a table header prefix.
func (d *cstDecoder) tpLen(prefix string) string {
	if d.mapKeyReplaceTo != "" && strings.HasPrefix(prefix, d.mapKeyReplaceFrom) {
		rest := prefix[len(d.mapKeyReplaceFrom):]
		expr := `len(` + d.mapKeyReplaceTo + `) + ` + fmt.Sprintf(`%d`, len(rest))
		if d.keyPrefixVar {
			return `len(keyPrefix) + ` + expr
		}
		return expr
	}
	if d.keyPrefixVar {
		return fmt.Sprintf(`len(keyPrefix) + %d`, len(prefix))
	}
	return fmt.Sprintf(`%d`, len(prefix))
}

func irCSTDecodeBody(si StructInfo) string {
	ops := buildDecodeOps(si, "d.data", "", ReceiverTarget("d", "data"), StaticKey(""), true, true)
	dec := newReceiverCSTDecoder()
	dec.renderOps(ops, "d.cstDoc.Root()", "\t", "")
	if si.Validatable {
		dec.w("\tif err := d.data.Validate(); err != nil {\n\t\treturn nil, fmt.Errorf(\"validation failed: %%w\", err)\n\t}\n")
	}
	return dec.buf.String()
}

func irCSTDecodeIntoBody(si StructInfo) string {
	ops := buildDecodeOps(si, "data", "", LocalTarget("data"), PrefixedKey(""), false, false)
	dec := newFreeCSTDecoder()
	dec.renderOps(ops, "container", "\t", "")
	return dec.buf.String()
}

func (d *cstDecoder) renderOps(ops []DecodeOp, cv, ind, fv string) {
	var leaf, cont []DecodeOp
	for _, op := range ops {
		switch op.(type) {
		case GetPrimitive, GetCustom, GetTextMarshaler, GetSlicePrimitive, GetSliceTextMarshaler:
			leaf = append(leaf, op)
		default:
			cont = append(cont, op)
		}
	}
	if len(leaf) > 0 {
		d.leafScan(leaf, cv, ind, fv)
	}
	for _, op := range cont {
		d.contOp(op, cv, ind, fv)
	}
}

func (d *cstDecoder) leafScan(ops []DecodeOp, cv, ind, fv string) {
	d.w("%sfor _, _kv := range %s.Children {\n", ind, cv)
	d.w("%s\tif _kv.Kind != cst.NodeKeyValue { continue }\n", ind)
	d.w("%s\tswitch cst.KeyValueName(_kv) {\n", ind)
	for _, op := range ops {
		switch o := op.(type) {
		case GetPrimitive:
			bk := tomlKey(o.Key)
			ei := cstExtract(o.TypeName)
			d.w("%s\tcase %q:\n", ind, bk)
			d.w("%s\t\tif v, ok := cst.%s(_kv); ok {\n", ind, ei.fn)
			if o.Pointer {
				if ei.cast != "" {
					d.w("%s\t\t\t_cv := %s(v)\n", ind, ei.cast)
					d.w("%s\t\t\t%s = &_cv\n", ind, o.Target)
				} else {
					d.w("%s\t\t\t%s = &v\n", ind, o.Target)
				}
			} else if o.ElemType != "" {
				if ei.cast != "" {
					d.w("%s\t\t\t%s = %s(%s(v))\n", ind, o.Target, o.ElemType, ei.cast)
				} else {
					d.w("%s\t\t\t%s = %s(v)\n", ind, o.Target, o.ElemType)
				}
			} else if ei.cast != "" {
				d.w("%s\t\t\t%s = %s(v)\n", ind, o.Target, ei.cast)
			} else {
				d.w("%s\t\t\t%s = v\n", ind, o.Target)
			}
			if fv != "" {
				d.w("%s\t\t\t%s = true\n", ind, fv)
			}
			d.mc(ind+"\t\t\t", o.Key)
			d.w("%s\t\t}\n", ind)
		case GetCustom:
			bk := tomlKey(o.Key)
			d.w("%s\tcase %q:\n", ind, bk)
			d.w("%s\t\tif raw, ok := cst.ExtractRaw(_kv); ok {\n", ind)
			d.w("%s\t\t\tif err := %s.UnmarshalTOML(raw); err != nil {\n", ind, o.Target)
			d.w("%s\t\t\t\t%sfmt.Errorf(\"%s: %%w\", err)\n", ind, d.returnErr, bk)
			d.w("%s\t\t\t}\n", ind)
			if fv != "" {
				d.w("%s\t\t\t%s = true\n", ind, fv)
			}
			d.mc(ind+"\t\t\t", o.Key)
			d.w("%s\t\t}\n", ind)
		case GetTextMarshaler:
			bk := tomlKey(o.Key)
			d.w("%s\tcase %q:\n", ind, bk)
			d.w("%s\t\tif v, ok := cst.ExtractString(_kv); ok {\n", ind)
			d.w("%s\t\t\tif err := %s.UnmarshalText([]byte(v)); err != nil {\n", ind, o.Target)
			d.w("%s\t\t\t\t%sfmt.Errorf(\"%s: %%w\", err)\n", ind, d.returnErr, bk)
			d.w("%s\t\t\t}\n", ind)
			if fv != "" {
				d.w("%s\t\t\t%s = true\n", ind, fv)
			}
			d.mc(ind+"\t\t\t", o.Key)
			d.w("%s\t\t}\n", ind)
		case GetSlicePrimitive:
			bk := tomlKey(o.Key)
			d.w("%s\tcase %q:\n", ind, bk)
			d.w("%s\t\tif v, ok := cst.%s(_kv); ok {\n", ind, cstSliceExtractFunc(o.ElemType))
			if o.SlicePointer {
				d.w("%s\t\t\t%s = make([]*%s, len(v))\n", ind, o.Target, o.ElemType)
				d.w("%s\t\t\tfor _si := range v { %s[_si] = &v[_si] }\n", ind, o.Target)
			} else if o.TypeName != "" {
				d.w("%s\t\t\t%s = %s(v)\n", ind, o.Target, o.TypeName)
			} else {
				d.w("%s\t\t\t%s = v\n", ind, o.Target)
			}
			d.mc(ind+"\t\t\t", o.Key)
			d.w("%s\t\t}\n", ind)
		case GetSliceTextMarshaler:
			bk := tomlKey(o.Key)
			d.w("%s\tcase %q:\n", ind, bk)
			d.w("%s\t\tif v, ok := cst.ExtractStringSlice(_kv); ok {\n", ind)
			d.w("%s\t\t\t%s = make([]%s, len(v))\n", ind, o.Target, o.TypeName)
			d.w("%s\t\t\tfor _si, _s := range v {\n", ind)
			d.w("%s\t\t\t\tif err := %s[_si].UnmarshalText([]byte(_s)); err != nil {\n", ind, o.Target)
			d.w("%s\t\t\t\t\t%sfmt.Errorf(\"%s[%%d]: %%w\", _si, err)\n", ind, d.returnErr, bk)
			d.w("%s\t\t\t\t}\n", ind)
			d.w("%s\t\t\t}\n", ind)
			d.mc(ind+"\t\t\t", o.Key)
			d.w("%s\t\t}\n", ind)
		}
	}
	d.w("%s\t}\n%s}\n", ind, ind)
}

func (d *cstDecoder) contOp(op DecodeOp, cv, ind, fv string) {
	switch o := op.(type) {
	case GetMapStringString:
		d.w("%sfor _, _ch := range %s.Children {\n", ind, d.rootExpr)
		d.w("%s\tif _ch.Kind == cst.NodeTable && cst.TableHeaderKey(_ch) == %s {\n", ind, d.tm(o.Key))
		d.w("%s\t\t%s = cst.ExtractStringMap(_ch)\n", ind, o.Target)
		if fv != "" {
			d.w("%s\t\t%s = true\n", ind, fv)
		}
		d.mc(ind+"\t\t", o.Key)
		d.w("%s\t\tfor _ik := range %s { %s[%s + \".\" + _ik] = true }\n", ind, o.Target, d.consumedExpr, d.consumedKeyExpr(o.Key))
		d.w("%s\t\tbreak\n%s\t}\n%s}\n", ind, ind, ind)
	case GetMapStringMapStringString:
		d.mapSS2(o, ind)
	case InTable:
		d.w("%sfor _, _ch := range %s.Children {\n", ind, d.rootExpr)
		d.w("%s\tif _ch.Kind == cst.NodeTable && cst.TableHeaderKey(_ch) == %s {\n", ind, d.tm(o.Key))
		d.mc(ind+"\t\t", o.Key)
		d.renderOps(o.Fields, "_ch", ind+"\t\t", "")
		d.w("%s\t\tbreak\n%s\t}\n%s}\n", ind, ind, ind)
	case InPointerTable:
		lv := toLowerFirst(strings.TrimSuffix(o.Target[strings.LastIndex(o.Target, ".")+1:], "")) + "Val"
		ftv := "_ft" + keyPathSuffix(o.Key)
		d.w("%s{\n%s\tvar %s *cst.Node\n", ind, ind, ftv)
		d.w("%s\tfor _, _ch := range %s.Children {\n", ind, d.rootExpr)
		d.w("%s\t\tif _ch.Kind == cst.NodeTable && cst.TableHeaderKey(_ch) == %s { %s = _ch; break }\n", ind, d.tm(o.Key), ftv)
		d.w("%s\t}\n", ind)
		d.w("%s\tif %s != nil {\n", ind, ftv)
		d.mc(ind+"\t\t", o.Key)
		d.w("%s\t\t%s := &%s{}\n", ind, lv, o.TypeName)
		d.renderOps(o.TableFields, ftv, ind+"\t\t", "")
		d.w("%s\t\t%s = %s\n", ind, o.Target, lv)
		d.w("%s\t} else {\n", ind)
		d.w("%s\t\t%s := &%s{}\n%s\t\t_found := false\n", ind, lv, o.TypeName, ind)
		d.renderOps(o.FlatFields, cv, ind+"\t\t", "_found")
		d.w("%s\t\tif _found { %s = %s }\n", ind, o.Target, lv)
		d.w("%s\t}\n%s}\n", ind, ind)
	case ForArrayTable:
		d.fatCST(o, ind, fv)
	case ForMapStringStruct:
		d.fmsCST(o, ind)
	case DelegateStruct:
		d.dsCST(o, ind)
	case DelegateSlice:
		d.dlsCST(o, ind, fv)
	case DelegateMap:
		d.dmCST(o, ind)
	case WithFoundVar:
		d.contOp(o.Inner, cv, ind, o.FoundVar)
	}
}

func (d *cstDecoder) mapSS2(o GetMapStringMapStringString, ind string) {
	pf := o.Key + "."
	tn := "map[string]string"
	if o.TypeName != "" {
		tn = o.TypeName
	}
	d.w("%s{\n%s\tvar _mr map[string]%s\n", ind, ind, tn)
	d.w("%s\tfor _, _ch := range %s.Children {\n", ind, d.rootExpr)
	d.w("%s\t\tif _ch.Kind != cst.NodeTable { continue }\n", ind)
	d.w("%s\t\t_hdr := cst.TableHeaderKey(_ch)\n", ind)
	d.w("%s\t\tif !strings.HasPrefix(_hdr, %s) { continue }\n", ind, d.tp(pf))
	d.w("%s\t\t_mk := _hdr[%s:]\n", ind, d.tpLen(pf))
	d.w("%s\t\tif _mr == nil {\n", ind)
	d.mc(ind+"\t\t\t", o.Key)
	d.w("%s\t\t\t_mr = make(map[string]%s)\n", ind, tn)
	d.w("%s\t\t}\n", ind)
	me := d.consumedKeyExpr(o.Key)
	d.w("%s\t\t%s[%s + \".\" + _mk] = true\n", ind, d.consumedExpr, me)
	d.w("%s\t\t_inner := cst.ExtractStringMap(_ch)\n", ind)
	d.w("%s\t\tfor _ik := range _inner { %s[%s + \".\" + _mk + \".\" + _ik] = true }\n", ind, d.consumedExpr, me)
	if o.TypeName != "" {
		d.w("%s\t\t_mr[_mk] = %s(_inner)\n", ind, o.TypeName)
	} else {
		d.w("%s\t\t_mr[_mk] = _inner\n", ind)
	}
	d.w("%s\t}\n%s\tif _mr != nil { %s = _mr }\n%s}\n", ind, ind, o.Target, ind)
}

func (d *cstDecoder) fatCST(o ForArrayTable, ind, fv string) {
	nv := "_nodes" + keyPathSuffix(o.DottedKey)
	d.w("%svar %s []*cst.Node\n", ind, nv)
	if d.keyPrefixVar {
		d.w("%s%s = %s.FindArrayTableNodes(%s)\n", ind, nv, d.docVar, d.consumedKeyExpr(o.DottedKey))
	} else {
		d.w("%sfor _, _ch := range %s.Children {\n", ind, d.rootExpr)
		d.w("%s\tif _ch.Kind == cst.NodeArrayTable && cst.TableHeaderKey(_ch) == %s { %s = append(%s, _ch) }\n", ind, d.tm(o.DottedKey), nv, nv)
		d.w("%s}\n", ind)
	}
	if fv != "" {
		d.w("%sif len(%s) > 0 { %s = true }\n", ind, nv, fv)
	}
	if o.TrackHandles {
		hn := toLowerFirst(o.TypeName) + "Handle"
		gn := toLowerFirst(strings.TrimSuffix(o.Target[strings.LastIndex(o.Target, ".")+1:], ""))
		d.w("%sd.%s = make([]%s, len(%s))\n", ind, gn, hn, nv)
	}
	if o.SlicePointer {
		d.w("%s%s = make([]*%s, len(%s))\n", ind, o.Target, o.TypeName, nv)
	} else {
		d.w("%s%s = make([]%s, len(%s))\n", ind, o.Target, o.TypeName, nv)
	}
	d.mc(ind, o.DottedKey)
	d.w("%sfor i, _node := range %s {\n", ind, nv)
	if o.TrackHandles {
		hn := toLowerFirst(o.TypeName) + "Handle"
		gn := toLowerFirst(strings.TrimSuffix(o.Target[strings.LastIndex(o.Target, ".")+1:], ""))
		d.w("%s\td.%s[i] = %s{node: _node}\n", ind, gn, hn)
	}
	if o.SlicePointer {
		d.w("%s\t%s[i] = &%s{}\n", ind, o.Target, o.TypeName)
	}
	d.renderArrayEntry(o.Fields, o.DottedKey, ind+"\t")
	d.w("%s}\n", ind)
}

func (d *cstDecoder) renderArrayEntry(ops []DecodeOp, pk, ind string) {
	var leaf, cont []DecodeOp
	for _, op := range ops {
		switch op.(type) {
		case GetPrimitive, GetCustom, GetTextMarshaler, GetSlicePrimitive, GetSliceTextMarshaler:
			leaf = append(leaf, op)
		default:
			cont = append(cont, op)
		}
	}
	if len(leaf) > 0 {
		d.leafScan(leaf, "_node", ind, "")
	}
	for _, op := range cont {
		d.posOp(op, pk, ind)
	}
}

// posOp renders a container op with positional scoping relative to _i-th [[pk]] entry.
func (d *cstDecoder) posOp(op DecodeOp, pk, ind string) {
	switch o := op.(type) {
	case InTable:
		d.w("%s{\n%s\t_pi := 0\n", ind, ind)
		d.w("%s\tfor _, _rc := range %s.Children {\n", ind, d.rootExpr)
		d.w("%s\t\tif _rc.Kind == cst.NodeArrayTable && cst.TableHeaderKey(_rc) == %s {\n", ind, d.tm(pk))
		d.w("%s\t\t\tif _pi > i { break }\n%s\t\t\t_pi++; continue\n%s\t\t}\n", ind, ind, ind)
		d.w("%s\t\tif _pi == i+1 && _rc.Kind == cst.NodeTable && cst.TableHeaderKey(_rc) == %s {\n", ind, d.tm(o.Key))
		d.mc(ind+"\t\t\t", o.Key)
		d.renderOps(o.Fields, "_rc", ind+"\t\t\t", "")
		d.w("%s\t\t\tbreak\n%s\t\t}\n%s\t}\n%s}\n", ind, ind, ind, ind)
	case GetMapStringString:
		d.w("%s{\n%s\t_pi := 0\n", ind, ind)
		d.w("%s\tfor _, _rc := range %s.Children {\n", ind, d.rootExpr)
		d.w("%s\t\tif _rc.Kind == cst.NodeArrayTable && cst.TableHeaderKey(_rc) == %s {\n", ind, d.tm(pk))
		d.w("%s\t\t\tif _pi > i { break }\n%s\t\t\t_pi++; continue\n%s\t\t}\n", ind, ind, ind)
		d.w("%s\t\tif _pi == i+1 && _rc.Kind == cst.NodeTable && cst.TableHeaderKey(_rc) == %s {\n", ind, d.tm(o.Key))
		d.w("%s\t\t\t%s = cst.ExtractStringMap(_rc)\n", ind, o.Target)
		d.mc(ind+"\t\t\t", o.Key)
		d.w("%s\t\t\tfor _ik := range %s { %s[%s + \".\" + _ik] = true }\n", ind, o.Target, d.consumedExpr, d.consumedKeyExpr(o.Key))
		d.w("%s\t\t\tbreak\n%s\t\t}\n%s\t}\n%s}\n", ind, ind, ind, ind)
	case ForArrayTable:
		nv := "_nodes" + keyPathSuffix(o.DottedKey)
		d.w("%s{\n%s\tvar %s []*cst.Node\n%s\t_pi := 0\n%s\t_inScope := false\n", ind, ind, nv, ind, ind)
		d.w("%s\tfor _, _rc := range %s.Children {\n", ind, d.rootExpr)
		d.w("%s\t\tif _rc.Kind == cst.NodeArrayTable {\n%s\t\t\t_hdr := cst.TableHeaderKey(_rc)\n", ind, ind)
		d.w("%s\t\t\tif _hdr == %q { if _pi == i { _inScope = true } else if _pi > i { break }; _pi++; continue }\n", ind, pk)
		d.w("%s\t\t\tif _inScope && _hdr == %s { %s = append(%s, _rc) }\n", ind, d.tm(o.DottedKey), nv, nv)
		d.w("%s\t\t}\n%s\t}\n", ind, ind)
		if o.SlicePointer {
			d.w("%s\t%s = make([]*%s, len(%s))\n", ind, o.Target, o.TypeName, nv)
		} else {
			d.w("%s\t%s = make([]%s, len(%s))\n", ind, o.Target, o.TypeName, nv)
		}
		d.mc(ind+"\t", o.DottedKey)
		d.w("%s\tfor _ii, _nn := range %s {\n", ind, nv)
		if o.SlicePointer {
			d.w("%s\t\t%s[_ii] = &%s{}\n", ind, o.Target, o.TypeName)
		}
		// leaf scan of nested entry
		var leaf []DecodeOp
		for _, f := range o.Fields {
			switch f.(type) {
			case GetPrimitive, GetCustom, GetTextMarshaler, GetSlicePrimitive, GetSliceTextMarshaler:
				leaf = append(leaf, f)
			}
		}
		if len(leaf) > 0 {
			d.leafScan(leaf, "_nn", ind+"\t\t", "")
		}
		d.w("%s\t}\n%s}\n", ind, ind)
	case InPointerTable:
		lv := toLowerFirst(strings.TrimSuffix(o.Target[strings.LastIndex(o.Target, ".")+1:], "")) + "Val"
		ftv := "_ft" + keyPathSuffix(o.Key)
		d.w("%s{\n%s\tvar %s *cst.Node\n%s\t_pi := 0\n", ind, ind, ftv, ind)
		d.w("%s\tfor _, _rc := range %s.Children {\n", ind, d.rootExpr)
		d.w("%s\t\tif _rc.Kind == cst.NodeArrayTable && cst.TableHeaderKey(_rc) == %s {\n", ind, d.tm(pk))
		d.w("%s\t\t\tif _pi > i { break }\n%s\t\t\t_pi++; continue\n%s\t\t}\n", ind, ind, ind)
		d.w("%s\t\tif _pi == i+1 && _rc.Kind == cst.NodeTable && cst.TableHeaderKey(_rc) == %s { %s = _rc; break }\n", ind, d.tm(o.Key), ftv)
		d.w("%s\t}\n", ind)
		d.w("%s\tif %s != nil {\n", ind, ftv)
		d.mc(ind+"\t\t", o.Key)
		d.w("%s\t\t%s := &%s{}\n", ind, lv, o.TypeName)
		d.renderOps(o.TableFields, ftv, ind+"\t\t", "")
		d.w("%s\t\t%s = %s\n", ind, o.Target, lv)
		d.w("%s\t} else {\n", ind)
		d.w("%s\t\t%s := &%s{}\n%s\t\t_found := false\n", ind, lv, o.TypeName, ind)
		d.renderOps(o.FlatFields, "_node", ind+"\t\t", "_found")
		d.w("%s\t\tif _found { %s = %s }\n", ind, o.Target, lv)
		d.w("%s\t}\n%s}\n", ind, ind)
	case ForMapStringStruct:
		pd := o.Key + "."
		tp := o.TypeName
		ptr := o.SlicePointer
		d.w("%s{\n", ind)
		if ptr {
			d.w("%s\tvar _mr map[string]*%s\n", ind, tp)
		} else {
			d.w("%s\tvar _mr map[string]%s\n", ind, tp)
		}
		d.w("%s\t_pi := 0; _inScope := false\n", ind)
		d.w("%s\tfor _, _rc := range %s.Children {\n", ind, d.rootExpr)
		d.w("%s\t\tif _rc.Kind == cst.NodeArrayTable && cst.TableHeaderKey(_rc) == %s {\n", ind, d.tm(pk))
		d.w("%s\t\t\tif _pi == i { _inScope = true } else if _pi > i { break }\n%s\t\t\t_pi++; continue\n%s\t\t}\n", ind, ind, ind)
		d.w("%s\t\tif _inScope && _rc.Kind == cst.NodeTable {\n%s\t\t\t_hdr := cst.TableHeaderKey(_rc)\n", ind, ind)
		d.w("%s\t\t\tif strings.HasPrefix(_hdr, %s) {\n", ind, d.tp(pd))
		d.w("%s\t\t\t\t_mk := _hdr[%s:]\n", ind, d.tpLen(pd))
		d.w("%s\t\t\t\tif strings.Contains(_mk, \".\") { continue }\n", ind)
		d.w("%s\t\t\t\tif _mr == nil {\n", ind)
		d.mc(ind+"\t\t\t\t\t", o.Key)
		if ptr {
			d.w("%s\t\t\t\t\t_mr = make(map[string]*%s)\n", ind, tp)
		} else {
			d.w("%s\t\t\t\t\t_mr = make(map[string]%s)\n", ind, tp)
		}
		d.w("%s\t\t\t\t}\n", ind)
		me := d.consumedKeyExpr(o.Key)
		d.w("%s\t\t\t\t%s[%s + \".\" + _mk] = true\n", ind, d.consumedExpr, me)
		d.w("%s\t\t\t\tvar entry %s\n", ind, tp)
		savedFrom, savedTo := d.mapKeyReplaceFrom, d.mapKeyReplaceTo
		d.mapKeyReplaceFrom = o.Key + "."
		d.mapKeyReplaceTo = fmt.Sprintf(`%q + _mk + "."`, o.Key+".")
		d.renderOps(o.Fields, "_rc", ind+"\t\t\t\t", "")
		d.mapKeyReplaceFrom, d.mapKeyReplaceTo = savedFrom, savedTo
		if ptr {
			d.w("%s\t\t\t\t_mr[_mk] = &entry\n", ind)
		} else {
			d.w("%s\t\t\t\t_mr[_mk] = entry\n", ind)
		}
		d.w("%s\t\t\t}\n%s\t\t}\n%s\t}\n", ind, ind, ind)
		d.w("%s\tif _mr != nil { %s = _mr }\n%s}\n", ind, o.Target, ind)
	case DelegateStruct:
		d.dsCST(o, ind)
	case DelegateSlice:
		d.dlsCST(o, ind, "")
	case DelegateMap:
		d.dmCST(o, ind)
	case WithFoundVar:
		d.posOp(o.Inner, pk, ind)
	}
}

func (d *cstDecoder) fmsCST(o ForMapStringStruct, ind string) {
	pd := o.Key + "."
	tp := o.TypeName
	ptr := o.SlicePointer
	d.w("%s{\n", ind)
	if ptr {
		d.w("%s\tvar _mr map[string]*%s\n", ind, tp)
	} else {
		d.w("%s\tvar _mr map[string]%s\n", ind, tp)
	}
	d.w("%s\tfor _, _ch := range %s.Children {\n", ind, d.rootExpr)
	d.w("%s\t\tif _ch.Kind != cst.NodeTable { continue }\n", ind)
	d.w("%s\t\t_hdr := cst.TableHeaderKey(_ch)\n", ind)
	d.w("%s\t\tif !strings.HasPrefix(_hdr, %q) { continue }\n", ind, pd)
	d.w("%s\t\t_mk := _hdr[%s:]\n", ind, d.tpLen(pd))
	d.w("%s\t\tif strings.Contains(_mk, \".\") { continue }\n", ind)
	d.w("%s\t\tif _mr == nil {\n", ind)
	d.mc(ind+"\t\t\t", o.Key)
	if ptr {
		d.w("%s\t\t\t_mr = make(map[string]*%s)\n", ind, tp)
	} else {
		d.w("%s\t\t\t_mr = make(map[string]%s)\n", ind, tp)
	}
	d.w("%s\t\t}\n", ind)
	me := d.consumedKeyExpr(o.Key)
	d.w("%s\t\t%s[%s + \".\" + _mk] = true\n", ind, d.consumedExpr, me)
	d.w("%s\t\tvar entry %s\n", ind, tp)
	savedFrom, savedTo := d.mapKeyReplaceFrom, d.mapKeyReplaceTo
	d.mapKeyReplaceFrom = o.Key + "."
	d.mapKeyReplaceTo = fmt.Sprintf(`%q + _mk + "."`, o.Key+".")
	d.renderOps(o.Fields, "_ch", ind+"\t\t", "")
	d.mapKeyReplaceFrom, d.mapKeyReplaceTo = savedFrom, savedTo
	if ptr {
		d.w("%s\t\t_mr[_mk] = &entry\n", ind)
	} else {
		d.w("%s\t\t_mr[_mk] = entry\n", ind)
	}
	d.w("%s\t}\n%s\tif _mr != nil { %s = _mr }\n%s}\n", ind, ind, o.Target, ind)
}

func (d *cstDecoder) dsCST(o DelegateStruct, ind string) {
	p := strings.SplitN(o.TypeName, ".", 2)
	bk := tomlKey(o.Key)
	if o.Pointer {
		lv := toLowerFirst(p[1]) + "Val"
		tblv := "_tbl" + keyPathSuffix(o.Key)
		d.w("%s{\n%s\tvar %s *cst.Node\n", ind, ind, tblv)
		d.w("%s\tfor _, _ch := range %s.Children {\n", ind, d.rootExpr)
		d.w("%s\t\tif _ch.Kind == cst.NodeTable && cst.TableHeaderKey(_ch) == %s { %s = _ch; break }\n", ind, d.tm(o.Key), tblv)
		d.w("%s\t}\n%s\tif %s != nil {\n", ind, ind, tblv)
		d.mc(ind+"\t\t", o.Key)
		d.w("%s\t\t%s := &%s{}\n", ind, lv, o.TypeName)
		d.w("%s\t\tif err := %s.Decode%sInto(%s, %s, %s, %s, %s); err != nil {\n",
			ind, p[0], p[1], lv, d.docVar, tblv, d.consumedExpr, d.consumedKeyExpr(o.Key+"."))
		d.w("%s\t\t\t%sfmt.Errorf(\"%s: %%w\", err)\n", ind, d.returnErr, bk)
		d.w("%s\t\t}\n%s\t\t%s = %s\n%s\t}\n%s}\n", ind, ind, o.Target, lv, ind, ind)
	} else {
		d.w("%sfor _, _ch := range %s.Children {\n", ind, d.rootExpr)
		d.w("%s\tif _ch.Kind == cst.NodeTable && cst.TableHeaderKey(_ch) == %s {\n", ind, d.tm(o.Key))
		d.mc(ind+"\t\t", o.Key)
		d.w("%s\t\tif err := %s.Decode%sInto(&%s, %s, _ch, %s, %s); err != nil {\n",
			ind, p[0], p[1], o.Target, d.docVar, d.consumedExpr, d.consumedKeyExpr(o.Key+"."))
		d.w("%s\t\t\t%sfmt.Errorf(\"%s: %%w\", err)\n", ind, d.returnErr, bk)
		d.w("%s\t\t}\n%s\t\tbreak\n%s\t}\n%s}\n", ind, ind, ind, ind)
	}
}

func (d *cstDecoder) dlsCST(o DelegateSlice, ind, fv string) {
	p := strings.SplitN(o.TypeName, ".", 2)
	nv := "_nodes" + keyPathSuffix(o.DottedKey)
	d.w("%svar %s []*cst.Node\n", ind, nv)
	if d.keyPrefixVar {
		d.w("%s%s = %s.FindArrayTableNodes(%s)\n", ind, nv, d.docVar, d.consumedKeyExpr(o.DottedKey))
	} else {
		d.w("%sfor _, _ch := range %s.Children {\n", ind, d.rootExpr)
		d.w("%s\tif _ch.Kind == cst.NodeArrayTable && cst.TableHeaderKey(_ch) == %s { %s = append(%s, _ch) }\n", ind, d.tm(o.DottedKey), nv, nv)
		d.w("%s}\n", ind)
	}
	if fv != "" {
		d.w("%sif len(%s) > 0 { %s = true }\n", ind, nv, fv)
	}
	if o.SlicePointer {
		d.w("%s%s = make([]*%s, len(%s))\n", ind, o.Target, o.TypeName, nv)
	} else {
		d.w("%s%s = make([]%s, len(%s))\n", ind, o.Target, o.TypeName, nv)
	}
	d.mc(ind, o.DottedKey)
	d.w("%sfor i, _node := range %s {\n", ind, nv)
	if o.SlicePointer {
		d.w("%s\t%s[i] = &%s{}\n", ind, o.Target, o.TypeName)
		d.w("%s\tif err := %s.Decode%sInto(%s[i], %s, _node, %s, %s); err != nil {\n",
			ind, p[0], p[1], o.Target, d.docVar, d.consumedExpr, d.consumedKeyExpr(o.DottedKey+"."))
	} else {
		d.w("%s\tif err := %s.Decode%sInto(&%s[i], %s, _node, %s, %s); err != nil {\n",
			ind, p[0], p[1], o.Target, d.docVar, d.consumedExpr, d.consumedKeyExpr(o.DottedKey+"."))
	}
	d.w("%s\t\t%sfmt.Errorf(\"%s[%%d]: %%w\", i, err)\n", ind, d.returnErr, tomlKey(o.Key))
	d.w("%s\t}\n%s}\n", ind, ind)
}

func (d *cstDecoder) dmCST(o DelegateMap, ind string) {
	p := strings.SplitN(o.ElemType, ".", 2)
	bk := tomlKey(o.Key)
	pd := o.Key + "."
	d.w("%s{\n%s\tfor _, _ch := range %s.Children {\n", ind, ind, d.rootExpr)
	d.w("%s\t\tif _ch.Kind != cst.NodeTable { continue }\n", ind)
	d.w("%s\t\t_hdr := cst.TableHeaderKey(_ch)\n", ind)
	d.w("%s\t\tif !strings.HasPrefix(_hdr, %q) { continue }\n", ind, pd)
	d.w("%s\t\t_mk := _hdr[%s:]\n", ind, d.tpLen(pd))
	d.w("%s\t\tif strings.Contains(_mk, \".\") { continue }\n", ind)
	d.w("%s\t\tif %s == nil {\n", ind, o.Target)
	d.mc(ind+"\t\t\t", o.Key)
	d.w("%s\t\t\t%s = make(map[string]%s)\n", ind, o.Target, o.ElemType)
	d.w("%s\t\t}\n", ind)
	me := d.consumedKeyExpr(o.Key)
	d.w("%s\t\t%s[%s + \".\" + _mk] = true\n", ind, d.consumedExpr, me)
	d.w("%s\t\tvar entry %s\n", ind, o.ElemType)
	dke := me + ` + "." + _mk + "."`
	d.w("%s\t\tif err := %s.Decode%sInto(&entry, %s, _ch, %s, %s); err != nil {\n",
		ind, p[0], p[1], d.docVar, d.consumedExpr, dke)
	d.w("%s\t\t\t%sfmt.Errorf(\"%s.%%s: %%w\", _mk, err)\n", ind, d.returnErr, bk)
	d.w("%s\t\t}\n%s\t\t%s[_mk] = entry\n", ind, ind, o.Target)
	d.w("%s\t}\n%s}\n", ind, ind)
}

// cstExtractInfo returns the cst.Extract* function and an optional cast
// needed to convert the extracted value to the target Go type.
// For types without a direct extractor (int8, uint16, float32, etc.),
// we extract the wider type and cast.
type extractInfo struct {
	fn   string // e.g. "ExtractInt64"
	cast string // e.g. "int16" or "" if no cast needed
}

func cstExtract(typeName string) extractInfo {
	switch typeName {
	case "string":
		return extractInfo{fn: "ExtractString"}
	case "int":
		return extractInfo{fn: "ExtractInt"}
	case "int64":
		return extractInfo{fn: "ExtractInt64"}
	case "int8":
		return extractInfo{fn: "ExtractInt64", cast: "int8"}
	case "int16":
		return extractInfo{fn: "ExtractInt64", cast: "int16"}
	case "int32":
		return extractInfo{fn: "ExtractInt64", cast: "int32"}
	case "uint":
		return extractInfo{fn: "ExtractUint64", cast: "uint"}
	case "uint8":
		return extractInfo{fn: "ExtractUint64", cast: "uint8"}
	case "uint16":
		return extractInfo{fn: "ExtractUint64", cast: "uint16"}
	case "uint32":
		return extractInfo{fn: "ExtractUint64", cast: "uint32"}
	case "uint64":
		return extractInfo{fn: "ExtractUint64"}
	case "float32":
		return extractInfo{fn: "ExtractFloat64", cast: "float32"}
	case "float64":
		return extractInfo{fn: "ExtractFloat64"}
	case "bool":
		return extractInfo{fn: "ExtractBool"}
	default:
		return extractInfo{fn: "ExtractString"}
	}
}

func cstExtractFunc(typeName string) string {
	return cstExtract(typeName).fn
}

func cstSliceExtractFunc(elemType string) string {
	switch elemType {
	case "string":
		return "ExtractStringSlice"
	case "int":
		return "ExtractIntSlice"
	default:
		return "ExtractStringSlice"
	}
}
