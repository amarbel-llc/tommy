package generate

import (
	"strings"
	"testing"
)

func TestEmitDecodePrimitive(t *testing.T) {
	fi := FieldInfo{GoName: "Name", TomlKey: "name", Kind: FieldPrimitive, TypeName: "string"}
	code := emitDecodeField(receiverContext(), fi, "d.data", "d.cstDoc", "d.cstDoc.Root()")
	if !strings.Contains(code, `GetFromContainer[string]`) {
		t.Fatalf("expected GetFromContainer[string], got:\n%s", code)
	}
	if !strings.Contains(code, `d.data.Name`) {
		t.Fatalf("expected d.data.Name, got:\n%s", code)
	}
}

func TestEmitDecodeSliceStruct(t *testing.T) {
	fi := FieldInfo{
		GoName: "Servers", TomlKey: "servers", Kind: FieldSliceStruct,
		TypeName: "Server",
		InnerInfo: &StructInfo{
			Name: "Server",
			Fields: []FieldInfo{
				{GoName: "Name", TomlKey: "name", Kind: FieldPrimitive, TypeName: "string"},
			},
		},
	}
	code := emitDecodeField(receiverContext(), fi, "d.data", "d.cstDoc", "d.cstDoc.Root()")
	if !strings.Contains(code, `FindArrayTableNodes("servers")`) {
		t.Fatalf("expected FindArrayTableNodes, got:\n%s", code)
	}
	if !strings.Contains(code, `serverHandle`) {
		t.Fatalf("expected serverHandle, got:\n%s", code)
	}
}

func TestEmitDecodePointerPrimitive(t *testing.T) {
	fi := FieldInfo{GoName: "Enabled", TomlKey: "enabled", Kind: FieldPointerPrimitive, TypeName: "bool"}
	code := emitDecodeField(receiverContext(), fi, "d.data", "d.cstDoc", "container")
	if !strings.Contains(code, `GetFromContainer[bool]`) {
		t.Fatalf("expected GetFromContainer[bool], got:\n%s", code)
	}
	if !strings.Contains(code, `&v`) {
		t.Fatalf("expected pointer assignment, got:\n%s", code)
	}
}

func TestEmitDecodeCustom(t *testing.T) {
	fi := FieldInfo{GoName: "Command", TomlKey: "command", Kind: FieldCustom, TypeName: "Command"}
	code := emitDecodeField(receiverContext(), fi, "d.data", "d.cstDoc", "container")
	if !strings.Contains(code, `GetRawFromContainer`) {
		t.Fatalf("expected GetRawFromContainer, got:\n%s", code)
	}
	if !strings.Contains(code, `UnmarshalTOML`) {
		t.Fatalf("expected UnmarshalTOML call, got:\n%s", code)
	}
}

func TestEmitDecodePointerStruct(t *testing.T) {
	fi := FieldInfo{
		GoName: "Annotations", TomlKey: "annotations", Kind: FieldPointerStruct,
		TypeName: "AnnotationFilter",
		InnerInfo: &StructInfo{
			Name: "AnnotationFilter",
			Fields: []FieldInfo{
				{GoName: "ReadOnlyHint", TomlKey: "readOnlyHint", Kind: FieldPointerPrimitive, TypeName: "bool"},
			},
		},
	}
	code := emitDecodeField(receiverContext(), fi, "d.data", "d.cstDoc", "container")
	if !strings.Contains(code, `FindTableInContainer`) {
		t.Fatalf("expected FindTableInContainer, got:\n%s", code)
	}
	if !strings.Contains(code, `AnnotationFilter{}`) {
		t.Fatalf("expected AnnotationFilter construction, got:\n%s", code)
	}
}

func TestEmitDecodeBodyWithValidation(t *testing.T) {
	si := StructInfo{
		Name:        "Config",
		Validatable: true,
		Fields: []FieldInfo{
			{GoName: "Port", TomlKey: "port", Kind: FieldPrimitive, TypeName: "int"},
		},
	}
	code := emitDecodeBody(si)
	if !strings.Contains(code, "d.data.Validate()") {
		t.Fatalf("expected Validate() call in decode, got:\n%s", code)
	}
}

func TestEmitDecodeBodyWithoutValidation(t *testing.T) {
	si := StructInfo{
		Name: "Config",
		Fields: []FieldInfo{
			{GoName: "Port", TomlKey: "port", Kind: FieldPrimitive, TypeName: "int"},
		},
	}
	code := emitDecodeBody(si)
	if strings.Contains(code, "Validate") {
		t.Fatalf("unexpected Validate() call in decode, got:\n%s", code)
	}
}

func TestEmitEncodeBodyWithValidation(t *testing.T) {
	si := StructInfo{
		Name:        "Config",
		Validatable: true,
		Fields: []FieldInfo{
			{GoName: "Port", TomlKey: "port", Kind: FieldPrimitive, TypeName: "int"},
		},
	}
	code := emitEncodeBody(si)
	if !strings.Contains(code, "d.data.Validate()") {
		t.Fatalf("expected Validate() call in encode, got:\n%s", code)
	}
}

func TestEmitEncodeBodyWithoutValidation(t *testing.T) {
	si := StructInfo{
		Name: "Config",
		Fields: []FieldInfo{
			{GoName: "Port", TomlKey: "port", Kind: FieldPrimitive, TypeName: "int"},
		},
	}
	code := emitEncodeBody(si)
	if strings.Contains(code, "Validate") {
		t.Fatalf("unexpected Validate() call in encode, got:\n%s", code)
	}
}

func TestEmitDecodeDelegatedStruct(t *testing.T) {
	fi := FieldInfo{
		GoName:     "Settings",
		TomlKey:    "settings",
		Kind:       FieldDelegatedStruct,
		TypeName:   "ext.Inner",
		ImportPath: "example.com/ext",
	}
	code := emitDecodeField(receiverContext(), fi, "d.data", "d.cstDoc", "d.cstDoc.Root()")
	if !strings.Contains(code, "ext.DecodeInnerInto") {
		t.Fatalf("expected delegation call ext.DecodeInnerInto, got:\n%s", code)
	}
	if !strings.Contains(code, `FindTable("settings")`) {
		t.Fatalf("expected FindTable, got:\n%s", code)
	}
}

func TestEmitEncodeDelegatedStruct(t *testing.T) {
	fi := FieldInfo{
		GoName:     "Settings",
		TomlKey:    "settings",
		Kind:       FieldDelegatedStruct,
		TypeName:   "ext.Inner",
		ImportPath: "example.com/ext",
	}
	code := emitEncodeField(receiverContext(), fi, "d.data", "d.cstDoc", "d.cstDoc.Root()")
	if !strings.Contains(code, "ext.EncodeInnerFrom") {
		t.Fatalf("expected delegation call ext.EncodeInnerFrom, got:\n%s", code)
	}
}

func TestEmitDecodePointerDelegatedStruct(t *testing.T) {
	fi := FieldInfo{
		GoName:     "Options",
		TomlKey:    "options",
		Kind:       FieldPointerDelegatedStruct,
		TypeName:   "ext.Opts",
		ImportPath: "example.com/ext",
	}
	code := emitDecodeField(receiverContext(), fi, "d.data", "d.cstDoc", "container")
	if !strings.Contains(code, "ext.DecodeOptsInto") {
		t.Fatalf("expected delegation call ext.DecodeOptsInto, got:\n%s", code)
	}
	if !strings.Contains(code, "ext.Opts{}") {
		t.Fatalf("expected ext.Opts{} construction, got:\n%s", code)
	}
}
