package generate

import (
	"bytes"
	"strings"
	"testing"
)

func TestIRDecodePrimitive(t *testing.T) {
	si := StructInfo{
		Name: "Config",
		Fields: []FieldInfo{
			{GoName: "Name", TomlKey: "name", Kind: FieldPrimitive, TypeName: "string"},
		},
	}
	code := irDecodeBody(si)
	if !strings.Contains(code, `GetFromContainer[string]`) {
		t.Fatalf("expected GetFromContainer[string], got:\n%s", code)
	}
	if !strings.Contains(code, `d.data.Name`) {
		t.Fatalf("expected d.data.Name, got:\n%s", code)
	}
	if !strings.Contains(code, `d.consumed["name"] = true`) {
		t.Fatalf("expected consumed tracking, got:\n%s", code)
	}
}

func TestIRDecodePrimitiveAlias(t *testing.T) {
	si := StructInfo{
		Name: "Config",
		Fields: []FieldInfo{
			{GoName: "Ver", TomlKey: "ver", Kind: FieldPrimitive, TypeName: "string", ElemType: "Version"},
		},
	}
	code := irDecodeBody(si)
	if !strings.Contains(code, `Version(v)`) {
		t.Fatalf("expected type conversion, got:\n%s", code)
	}
}

func TestIRDecodePointerPrimitive(t *testing.T) {
	si := StructInfo{
		Name: "Config",
		Fields: []FieldInfo{
			{GoName: "Enabled", TomlKey: "enabled", Kind: FieldPointerPrimitive, TypeName: "bool"},
		},
	}
	code := irDecodeBody(si)
	if !strings.Contains(code, `GetFromContainer[bool]`) {
		t.Fatalf("expected GetFromContainer[bool], got:\n%s", code)
	}
	if !strings.Contains(code, `&v`) {
		t.Fatalf("expected pointer assignment, got:\n%s", code)
	}
}

func TestIRDecodeCustom(t *testing.T) {
	si := StructInfo{
		Name: "Config",
		Fields: []FieldInfo{
			{GoName: "Command", TomlKey: "command", Kind: FieldCustom, TypeName: "Command"},
		},
	}
	code := irDecodeBody(si)
	if !strings.Contains(code, `GetRawFromContainer`) {
		t.Fatalf("expected GetRawFromContainer, got:\n%s", code)
	}
	if !strings.Contains(code, `UnmarshalTOML`) {
		t.Fatalf("expected UnmarshalTOML, got:\n%s", code)
	}
}

func TestIRDecodeSliceStruct(t *testing.T) {
	si := StructInfo{
		Name: "Config",
		Fields: []FieldInfo{
			{
				GoName: "Servers", TomlKey: "servers", Kind: FieldSliceStruct,
				TypeName: "Server",
				InnerInfo: &StructInfo{
					Name: "Server",
					Fields: []FieldInfo{
						{GoName: "Name", TomlKey: "name", Kind: FieldPrimitive, TypeName: "string"},
					},
				},
			},
		},
	}
	code := irDecodeBody(si)
	if !strings.Contains(code, `FindArrayTableNodes`) {
		t.Fatalf("expected FindArrayTableNodes, got:\n%s", code)
	}
	if !strings.Contains(code, `serverHandle`) {
		t.Fatalf("expected serverHandle, got:\n%s", code)
	}
}

func TestIRDecodePointerStruct(t *testing.T) {
	si := StructInfo{
		Name: "Config",
		Fields: []FieldInfo{
			{
				GoName: "Annotations", TomlKey: "annotations", Kind: FieldPointerStruct,
				TypeName: "AnnotationFilter",
				InnerInfo: &StructInfo{
					Name: "AnnotationFilter",
					Fields: []FieldInfo{
						{GoName: "ReadOnlyHint", TomlKey: "readOnlyHint", Kind: FieldPointerPrimitive, TypeName: "bool"},
					},
				},
			},
		},
	}
	code := irDecodeBody(si)
	if !strings.Contains(code, `FindTableInContainer`) {
		t.Fatalf("expected FindTableInContainer, got:\n%s", code)
	}
	if !strings.Contains(code, `AnnotationFilter{}`) {
		t.Fatalf("expected AnnotationFilter construction, got:\n%s", code)
	}
	if !strings.Contains(code, `found`) {
		t.Fatalf("expected found variable for fallback, got:\n%s", code)
	}
}

func TestIRDecodeDelegatedStruct(t *testing.T) {
	si := StructInfo{
		Name: "Config",
		Fields: []FieldInfo{
			{
				GoName:     "Settings",
				TomlKey:    "settings",
				Kind:       FieldDelegatedStruct,
				TypeName:   "ext.Inner",
				ImportPath: "example.com/ext",
			},
		},
	}
	code := irDecodeBody(si)
	if !strings.Contains(code, "ext.DecodeInnerInto") {
		t.Fatalf("expected delegation call ext.DecodeInnerInto, got:\n%s", code)
	}
	if !strings.Contains(code, `FindTable("settings")`) {
		t.Fatalf("expected FindTable, got:\n%s", code)
	}
}

func TestIRDecodePointerDelegatedStruct(t *testing.T) {
	si := StructInfo{
		Name: "Config",
		Fields: []FieldInfo{
			{
				GoName:     "Options",
				TomlKey:    "options",
				Kind:       FieldPointerDelegatedStruct,
				TypeName:   "ext.Opts",
				ImportPath: "example.com/ext",
			},
		},
	}
	code := irDecodeBody(si)
	if !strings.Contains(code, "ext.DecodeOptsInto") {
		t.Fatalf("expected delegation call ext.DecodeOptsInto, got:\n%s", code)
	}
	if !strings.Contains(code, "ext.Opts{}") {
		t.Fatalf("expected ext.Opts{} construction, got:\n%s", code)
	}
}

func TestIRDecodeBodyWithValidation(t *testing.T) {
	si := StructInfo{
		Name:        "Config",
		Validatable: true,
		Fields: []FieldInfo{
			{GoName: "Port", TomlKey: "port", Kind: FieldPrimitive, TypeName: "int"},
		},
	}
	code := irDecodeBody(si)
	if !strings.Contains(code, "d.data.Validate()") {
		t.Fatalf("expected Validate() call, got:\n%s", code)
	}
}

func TestIRDecodeBodyWithoutValidation(t *testing.T) {
	si := StructInfo{
		Name: "Config",
		Fields: []FieldInfo{
			{GoName: "Port", TomlKey: "port", Kind: FieldPrimitive, TypeName: "int"},
		},
	}
	code := irDecodeBody(si)
	if strings.Contains(code, "Validate") {
		t.Fatalf("unexpected Validate() call, got:\n%s", code)
	}
}

func TestIRDecodeIntoFreeContext(t *testing.T) {
	si := StructInfo{
		Name: "Config",
		Fields: []FieldInfo{
			{GoName: "Name", TomlKey: "name", Kind: FieldPrimitive, TypeName: "string"},
		},
	}
	code := irDecodeIntoBody(si)
	// Free context uses "doc" not "d.cstDoc"
	if !strings.Contains(code, `document.GetFromContainer[string](doc,`) {
		t.Fatalf("expected doc variable in free context, got:\n%s", code)
	}
	// Free context uses keyPrefix
	if !strings.Contains(code, `keyPrefix +`) {
		t.Fatalf("expected keyPrefix in free context, got:\n%s", code)
	}
	// Free context uses "consumed" not "d.consumed"
	if !strings.Contains(code, `consumed[keyPrefix`) {
		t.Fatalf("expected consumed variable in free context, got:\n%s", code)
	}
}

func TestIRDecodeMapStringString(t *testing.T) {
	si := StructInfo{
		Name: "Config",
		Fields: []FieldInfo{
			{GoName: "Labels", TomlKey: "labels", Kind: FieldMapStringString},
		},
	}
	code := irDecodeBody(si)
	if !strings.Contains(code, `FindTable("labels")`) {
		t.Fatalf("expected FindTable for root map, got:\n%s", code)
	}
	if !strings.Contains(code, `GetStringMapFromTable`) {
		t.Fatalf("expected GetStringMapFromTable, got:\n%s", code)
	}
}

func TestIRDebugPointerStructWithSliceStruct(t *testing.T) {
	si := StructInfo{
		Name: "Config",
		Fields: []FieldInfo{
			{
				GoName: "Exec", TomlKey: "exec", Kind: FieldPointerStruct,
				TypeName: "ExecConfig",
				InnerInfo: &StructInfo{
					Name: "ExecConfig",
					Fields: []FieldInfo{
						{
							GoName: "Allow", TomlKey: "allow", Kind: FieldSliceStruct,
							TypeName: "ExecRule",
							InnerInfo: &StructInfo{
								Name: "ExecRule",
								Fields: []FieldInfo{
									{GoName: "Binary", TomlKey: "binary", Kind: FieldPrimitive, TypeName: "string"},
								},
							},
						},
					},
				},
			},
		},
	}
	irCode := irDecodeBody(si)
	oldCode := func() string {
		ctx := receiverContext()
		var buf bytes.Buffer
		for _, fi := range si.Fields {
			buf.WriteString(emitDecodeField(ctx, fi, "d.data", "d.cstDoc", "d.cstDoc.Root()"))
		}
		return buf.String()
	}()

	// Functional check: the IR code must decode Allow inside the table branch
	if !strings.Contains(irCode, `FindArrayTableNodes("exec.allow")`) {
		t.Fatalf("expected FindArrayTableNodes in table branch, got:\n%s", irCode)
	}
	if !strings.Contains(irCode, `execVal.Allow[i].Binary = v`) {
		t.Fatalf("expected Binary assignment, got:\n%s", irCode)
	}
	_ = oldCode
}

func TestIRDecodeSliceDelegatedStruct(t *testing.T) {
	si := StructInfo{
		Name: "Config",
		Fields: []FieldInfo{
			{
				GoName:     "Items",
				TomlKey:    "items",
				Kind:       FieldSliceDelegatedStruct,
				TypeName:   "ext.Item",
				ImportPath: "example.com/ext",
			},
		},
	}
	code := irDecodeBody(si)
	if !strings.Contains(code, "ext.DecodeItemInto") {
		t.Fatalf("expected delegation call ext.DecodeItemInto, got:\n%s", code)
	}
	if !strings.Contains(code, `FindArrayTableNodes`) {
		t.Fatalf("expected FindArrayTableNodes, got:\n%s", code)
	}
}
