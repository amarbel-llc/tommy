package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyzePrimitiveFields(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.25.6\n")
	writeFixture(t, dir, "config.go", "package test\n\n//go:generate tommy generate\ntype Config struct {\n\tName    string  `toml:\"name\"`\n\tPort    int     `toml:\"port\"`\n\tEnabled bool    `toml:\"enabled\"`\n}\n")

	infos, err := Analyze(dir, "config.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	si := infos[0]
	if si.Name != "Config" {
		t.Fatalf("expected name Config, got %s", si.Name)
	}
	if len(si.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(si.Fields))
	}
	if si.Fields[0].Kind != FieldPrimitive {
		t.Fatalf("expected FieldPrimitive, got %v", si.Fields[0].Kind)
	}
	if si.Fields[0].GoName != "Name" {
		t.Fatalf("expected GoName Name, got %s", si.Fields[0].GoName)
	}
	if si.Fields[0].TomlKey != "name" {
		t.Fatalf("expected TomlKey name, got %s", si.Fields[0].TomlKey)
	}
}

func TestAnalyzeSliceOfStructs(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.25.6\n")
	writeFixture(t, dir, "config.go", "package test\n\n//go:generate tommy generate\ntype Config struct {\n\tServers []Server `toml:\"servers\"`\n}\n\ntype Server struct {\n\tName    string `toml:\"name\"`\n\tCommand string `toml:\"command\"`\n}\n")

	infos, err := Analyze(dir, "config.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	f := infos[0].Fields[0]
	if f.Kind != FieldSliceStruct {
		t.Fatalf("expected FieldSliceStruct, got %v", f.Kind)
	}
	if f.TypeName != "Server" {
		t.Fatalf("expected TypeName Server, got %s", f.TypeName)
	}
	if f.InnerInfo == nil {
		t.Fatal("expected InnerInfo to be set")
	}
	if len(f.InnerInfo.Fields) != 2 {
		t.Fatalf("expected 2 inner fields, got %d", len(f.InnerInfo.Fields))
	}
}

func TestAnalyzeUnmarshalWithoutMarshalErrors(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.25.6\n")
	writeFixture(t, dir, "config.go", `package test

//go:generate tommy generate
type Config struct {
	Command Command `+"`"+`toml:"command"`+"`"+`
}

type Command struct {
	value string
}

func (c *Command) UnmarshalTOML(v interface{}) error {
	c.value = v.(string)
	return nil
}
`)

	_, err := Analyze(dir, "config.go")
	if err == nil {
		t.Fatal("expected error for type with UnmarshalTOML but no MarshalTOML")
	}
	expected := "has UnmarshalTOML but no MarshalTOML"
	if !strings.Contains(err.Error(), expected) {
		t.Fatalf("expected error containing %q, got: %s", expected, err)
	}
}

func TestAnalyzeUnmarshalWithMarshalSucceeds(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.25.6\n")
	writeFixture(t, dir, "config.go", `package test

//go:generate tommy generate
type Config struct {
	Command Command `+"`"+`toml:"command"`+"`"+`
}

type Command struct {
	value string
}

func (c *Command) UnmarshalTOML(v interface{}) error {
	c.value = v.(string)
	return nil
}

func (c Command) MarshalTOML() (string, error) {
	return c.value, nil
}
`)

	infos, err := Analyze(dir, "config.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	f := infos[0].Fields[0]
	if f.Kind != FieldCustom {
		t.Fatalf("expected FieldCustom, got %v", f.Kind)
	}
}

func TestAnalyzeMarshalWithoutUnmarshalErrors(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.25.6\n")
	writeFixture(t, dir, "config.go", `package test

//go:generate tommy generate
type Config struct {
	Command Command `+"`"+`toml:"command"`+"`"+`
}

type Command struct {
	value string
}

func (c Command) MarshalTOML() (string, error) {
	return c.value, nil
}
`)

	_, err := Analyze(dir, "config.go")
	if err == nil {
		t.Fatal("expected error for type with MarshalTOML but no UnmarshalTOML")
	}
	expected := "has MarshalTOML but no UnmarshalTOML"
	if !strings.Contains(err.Error(), expected) {
		t.Fatalf("expected error containing %q, got: %s", expected, err)
	}
}

func TestAnalyzeMapStringString(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.25.6\n")
	writeFixture(t, dir, "config.go", "package test\n\n//go:generate tommy generate\ntype Config struct {\n\tEnv map[string]string `toml:\"env\"`\n}\n")

	infos, err := Analyze(dir, "config.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	f := infos[0].Fields[0]
	if f.Kind != FieldMapStringString {
		t.Fatalf("expected FieldMapStringString, got %v", f.Kind)
	}
	if f.TomlKey != "env" {
		t.Fatalf("expected TomlKey 'env', got %s", f.TomlKey)
	}
}

func TestAnalyzeCrossPackageTextMarshaler(t *testing.T) {
	dir := t.TempDir()

	// Create the external package with a TextMarshaler type
	extDir := filepath.Join(dir, "ext")
	writeFixture(t, extDir, "id.go", `package ext

type Id struct {
	value string
}

func (id Id) MarshalText() ([]byte, error) {
	return []byte(id.value), nil
}

func (id *Id) UnmarshalText(b []byte) error {
	id.value = string(b)
	return nil
}
`)

	// Create the main package that imports the external type
	mainDir := filepath.Join(dir, "main")
	writeFixture(t, mainDir, "go.mod", "module example.com/test\n\ngo 1.26\n\nrequire example.com/ext v0.0.0\n\nreplace example.com/ext => ../ext\n")
	writeFixture(t, extDir, "go.mod", "module example.com/ext\n\ngo 1.26\n")
	writeFixture(t, mainDir, "config.go", `package main

import "example.com/ext"

//go:generate tommy generate
type Config struct {
	Encryption ext.Id `+"`"+`toml:"encryption"`+"`"+`
}
`)

	infos, err := Analyze(mainDir, "config.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	f := infos[0].Fields[0]
	if f.Kind != FieldTextMarshaler {
		t.Fatalf("expected FieldTextMarshaler, got %v", f.Kind)
	}
	if f.TypeName != "ext.Id" {
		t.Fatalf("expected TypeName 'ext.Id', got %s", f.TypeName)
	}
}

func TestAnalyzeSliceCrossPackageTextMarshaler(t *testing.T) {
	dir := t.TempDir()

	extDir := filepath.Join(dir, "ext")
	writeFixture(t, extDir, "go.mod", "module example.com/ext\n\ngo 1.26\n")
	writeFixture(t, extDir, "id.go", `package ext

type Id struct {
	value string
}

func (id Id) MarshalText() ([]byte, error) {
	return []byte(id.value), nil
}

func (id *Id) UnmarshalText(b []byte) error {
	id.value = string(b)
	return nil
}
`)

	mainDir := filepath.Join(dir, "main")
	writeFixture(t, mainDir, "go.mod", "module example.com/test\n\ngo 1.26\n\nrequire example.com/ext v0.0.0\n\nreplace example.com/ext => ../ext\n")
	writeFixture(t, mainDir, "config.go", `package main

import "example.com/ext"

//go:generate tommy generate
type Config struct {
	Ids []ext.Id `+"`"+`toml:"ids"`+"`"+`
}
`)

	infos, err := Analyze(mainDir, "config.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	f := infos[0].Fields[0]
	if f.Kind != FieldSliceTextMarshaler {
		t.Fatalf("expected FieldSliceTextMarshaler, got %v", f.Kind)
	}
	if f.TypeName != "ext.Id" {
		t.Fatalf("expected TypeName 'ext.Id', got %s", f.TypeName)
	}
}

func TestAnalyzePointerCrossPackageTextMarshaler(t *testing.T) {
	dir := t.TempDir()

	extDir := filepath.Join(dir, "ext")
	writeFixture(t, extDir, "go.mod", "module example.com/ext\n\ngo 1.26\n")
	writeFixture(t, extDir, "id.go", `package ext

type Id struct {
	value string
}

func (id Id) MarshalText() ([]byte, error) {
	return []byte(id.value), nil
}

func (id *Id) UnmarshalText(b []byte) error {
	id.value = string(b)
	return nil
}
`)

	mainDir := filepath.Join(dir, "main")
	writeFixture(t, mainDir, "go.mod", "module example.com/test\n\ngo 1.26\n\nrequire example.com/ext v0.0.0\n\nreplace example.com/ext => ../ext\n")
	writeFixture(t, mainDir, "config.go", `package main

import "example.com/ext"

//go:generate tommy generate
type Config struct {
	Encryption *ext.Id `+"`"+`toml:"encryption"`+"`"+`
}
`)

	infos, err := Analyze(mainDir, "config.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	f := infos[0].Fields[0]
	// *ext.Id where ext.Id implements TextMarshaler — should still be FieldTextMarshaler,
	// not FieldPointerStruct (we don't need to resolve inner struct fields)
	if f.Kind != FieldTextMarshaler {
		t.Fatalf("expected FieldTextMarshaler, got %v", f.Kind)
	}
	if f.TypeName != "ext.Id" {
		t.Fatalf("expected TypeName 'ext.Id', got %s", f.TypeName)
	}
}

func fieldNames(fields []FieldInfo) []string {
	var names []string
	for _, f := range fields {
		names = append(names, f.GoName)
	}
	return names
}

func TestAnalyzeSkipsBlankIdentifierFields(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.26\n")
	writeFixture(t, dir, "config.go", `package test

type Common struct {
	_    string `+"`"+`toml:"repo-type"`+"`"+`
	Name string `+"`"+`toml:"name"`+"`"+`
}

//go:generate tommy generate
type Config struct {
	Common
	Extra string `+"`"+`toml:"extra"`+"`"+`
}
`)

	infos, err := Analyze(dir, "config.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	// Should have 2 fields (Name promoted from Common + Extra), NOT 3
	if len(infos[0].Fields) != 2 {
		t.Errorf("expected 2 fields, got %d: %v", len(infos[0].Fields), fieldNames(infos[0].Fields))
	}
	for _, f := range infos[0].Fields {
		if f.GoName == "_" {
			t.Error("blank identifier field should have been skipped")
		}
	}
}

func TestAnalyzeCrossPackageEmbeddedStruct(t *testing.T) {
	dir := t.TempDir()

	// Create the external package with a struct
	baseDir := filepath.Join(dir, "base")
	writeFixture(t, baseDir, "go.mod", "module example.com/base\n\ngo 1.26\n")
	writeFixture(t, baseDir, "base.go", `package base

type Config struct {
	Name   string            `+"`"+`toml:"name"`+"`"+`
	Script string            `+"`"+`toml:"script,omitempty,multiline"`+"`"+`
	Env    map[string]string `+"`"+`toml:"env,omitempty"`+"`"+`
}
`)

	// Consumer package that embeds cross-package struct
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", "module example.com/test\n\ngo 1.26\n\nrequire example.com/base v0.0.0\n\nreplace example.com/base => ../base\n")
	writeFixture(t, consumerDir, "consumer.go", `package consumer

import "example.com/base"

//go:generate tommy generate
type Extended struct {
	base.Config
	Extra string `+"`"+`toml:"extra"`+"`"+`
}
`)

	infos, err := Analyze(consumerDir, "consumer.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	// Should have 4 fields: Name, Script, Env (promoted from base.Config) + Extra
	if len(infos[0].Fields) != 4 {
		t.Errorf("expected 4 fields, got %d: %v", len(infos[0].Fields), fieldNames(infos[0].Fields))
	}
}

func TestAnalyzeCrossPackagePrimitiveWrapper(t *testing.T) {
	dir := t.TempDir()

	// Package with a named type wrapping int
	typesDir := filepath.Join(dir, "types")
	writeFixture(t, typesDir, "go.mod", "module example.com/types\n\ngo 1.26\n")
	writeFixture(t, typesDir, "types.go", `package types

type Version int

func (v Version) String() string     { return "" }
func (v *Version) Set(string) error  { return nil }
`)

	// Consumer using the type via embedded struct
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", "module example.com/test\n\ngo 1.26\n\nrequire example.com/types v0.0.0\n\nreplace example.com/types => ../types\n")
	writeFixture(t, consumerDir, "consumer.go", `package consumer

import "example.com/types"

type Common struct {
	Version types.Version `+"`"+`toml:"version"`+"`"+`
	Name    string        `+"`"+`toml:"name"`+"`"+`
}

//go:generate tommy generate
type Config struct {
	Common
	Extra string `+"`"+`toml:"extra"`+"`"+`
}
`)

	infos, err := Analyze(consumerDir, "consumer.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	// Should have 3 fields: Version + Name (promoted) + Extra
	if len(infos[0].Fields) != 3 {
		t.Errorf("expected 3 fields, got %d", len(infos[0].Fields))
	}
	// Version field should be classified as FieldPrimitive (underlying int)
	for _, f := range infos[0].Fields {
		if f.GoName == "Version" {
			if f.Kind != FieldPrimitive {
				t.Errorf("Version field kind = %d, want FieldPrimitive (%d)", f.Kind, FieldPrimitive)
			}
			if f.TypeName != "int" {
				t.Errorf("Version TypeName = %q, want \"int\"", f.TypeName)
			}
			if f.ElemType != "types.Version" {
				t.Errorf("Version ElemType = %q, want \"types.Version\"", f.ElemType)
			}
		}
	}
}

func TestAnalyzeDirectCrossPackagePrimitiveWrapper(t *testing.T) {
	dir := t.TempDir()

	typesDir := filepath.Join(dir, "types")
	writeFixture(t, typesDir, "go.mod", "module example.com/types\n\ngo 1.26\n")
	writeFixture(t, typesDir, "types.go", `package types

type Version int
`)

	mainDir := filepath.Join(dir, "main")
	writeFixture(t, mainDir, "go.mod", "module example.com/test\n\ngo 1.26\n\nrequire example.com/types v0.0.0\n\nreplace example.com/types => ../types\n")
	writeFixture(t, mainDir, "config.go", `package main

import "example.com/types"

//go:generate tommy generate
type Config struct {
	Version types.Version `+"`"+`toml:"version"`+"`"+`
	Name    string        `+"`"+`toml:"name"`+"`"+`
}
`)

	infos, err := Analyze(mainDir, "config.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	if len(infos[0].Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(infos[0].Fields))
	}
	f := infos[0].Fields[0]
	if f.Kind != FieldPrimitive {
		t.Errorf("Version field kind = %d, want FieldPrimitive (%d)", f.Kind, FieldPrimitive)
	}
	if f.TypeName != "int" {
		t.Errorf("Version TypeName = %q, want \"int\"", f.TypeName)
	}
	if f.ElemType != "types.Version" {
		t.Errorf("Version ElemType = %q, want \"types.Version\"", f.ElemType)
	}
}

// Regression test for #28: ImportPath must be set for both scalar and slice
// cross-package TextMarshaler fields so collectImportPaths includes them.
func TestAnalyzeCrossPackageImportPathSet(t *testing.T) {
	dir := t.TempDir()

	extDir := filepath.Join(dir, "ext")
	writeFixture(t, extDir, "go.mod", "module example.com/ext\n\ngo 1.26\n")
	writeFixture(t, extDir, "id.go", `package ext

type Tag struct{ value string }
func (t Tag) MarshalText() ([]byte, error)  { return []byte(t.value), nil }
func (t *Tag) UnmarshalText(b []byte) error { t.value = string(b); return nil }

type Typ struct{ value string }
func (t Typ) MarshalText() ([]byte, error)  { return []byte(t.value), nil }
func (t *Typ) UnmarshalText(b []byte) error { t.value = string(b); return nil }
`)

	mainDir := filepath.Join(dir, "main")
	writeFixture(t, mainDir, "go.mod", "module example.com/test\n\ngo 1.26\n\nrequire example.com/ext v0.0.0\n\nreplace example.com/ext => ../ext\n")
	writeFixture(t, mainDir, "config.go", `package main

import "example.com/ext"

//go:generate tommy generate
type Defaults struct {
	Typ  ext.Typ  `+"`"+`toml:"typ"`+"`"+`
	Tags []ext.Tag `+"`"+`toml:"tags"`+"`"+`
}
`)

	infos, err := Analyze(mainDir, "config.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}

	// Scalar FieldTextMarshaler (Typ) should NOT have ImportPath — generated
	// code uses d.data.Typ.UnmarshalText(), no qualified type name needed.
	typ := infos[0].Fields[0]
	if typ.GoName != "Typ" {
		t.Fatalf("expected first field Typ, got %s", typ.GoName)
	}
	if typ.ImportPath != "" {
		t.Errorf("field Typ: ImportPath = %q, want empty (no qualified reference in generated code)", typ.ImportPath)
	}

	// Slice FieldSliceTextMarshaler (Tags) MUST have ImportPath — generated
	// code uses make([]ext.Tag, len(v)) which requires the import.
	tags := infos[0].Fields[1]
	if tags.GoName != "Tags" {
		t.Fatalf("expected second field Tags, got %s", tags.GoName)
	}
	if tags.ImportPath != "example.com/ext" {
		t.Errorf("field Tags: ImportPath = %q, want %q", tags.ImportPath, "example.com/ext")
	}
}

func TestAnalyzeValidatableStruct(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.25.6\n")
	writeFixture(t, dir, "config.go", `package test

//go:generate tommy generate
type Config struct {
	Port int `+"`"+`toml:"port"`+"`"+`
}

func (c Config) Validate() error {
	return nil
}
`)

	infos, err := Analyze(dir, "config.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	if !infos[0].Validatable {
		t.Fatal("expected Validatable to be true")
	}
}

func TestAnalyzeNonValidatableStruct(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.25.6\n")
	writeFixture(t, dir, "config.go", `package test

//go:generate tommy generate
type Config struct {
	Name string `+"`"+`toml:"name"`+"`"+`
}
`)

	infos, err := Analyze(dir, "config.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	if infos[0].Validatable {
		t.Fatal("expected Validatable to be false")
	}
}

func TestAnalyzeUnsupportedTypeErrors(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.25.6\n")
	writeFixture(t, dir, "config.go", "package test\n\n//go:generate tommy generate\ntype Config struct {\n\tData map[string]int `toml:\"data\"`\n}\n")

	_, err := Analyze(dir, "config.go")
	if err == nil {
		t.Fatal("expected error for unsupported map value type")
	}
}

func TestAnalyzeCrossPackageStructDelegation(t *testing.T) {
	dir := t.TempDir()

	extDir := filepath.Join(dir, "ext")
	writeFixture(t, extDir, "go.mod", "module example.com/ext\n\ngo 1.26\n")
	writeFixture(t, extDir, "ext.go", `package ext

type Inner struct {
	Name string `+"`"+`toml:"name"`+"`"+`
}
`)

	mainDir := filepath.Join(dir, "main")
	writeFixture(t, mainDir, "go.mod", "module example.com/test\n\ngo 1.26\n\nrequire example.com/ext v0.0.0\n\nreplace example.com/ext => ../ext\n")
	writeFixture(t, mainDir, "config.go", `package main

import "example.com/ext"

//go:generate tommy generate
type Config struct {
	Settings ext.Inner `+"`"+`toml:"settings"`+"`"+`
}
`)

	infos, err := Analyze(mainDir, "config.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	f := infos[0].Fields[0]
	if f.Kind != FieldDelegatedStruct {
		t.Fatalf("expected FieldDelegatedStruct (%d), got %d", FieldDelegatedStruct, f.Kind)
	}
	if f.TypeName != "ext.Inner" {
		t.Fatalf("expected TypeName ext.Inner, got %s", f.TypeName)
	}
	if f.ImportPath != "example.com/ext" {
		t.Fatalf("expected ImportPath example.com/ext, got %s", f.ImportPath)
	}
}

// Regression test for #35: cross-package struct with unexported nested pointer
// struct should be classified as FieldDelegatedStruct — delegation avoids
// emitting code that references unexported types.
func TestAnalyzeCrossPackageUnexportedNestedStruct(t *testing.T) {
	dir := t.TempDir()

	// External package with an exported struct containing an unexported pointer struct field
	extDir := filepath.Join(dir, "options")
	writeFixture(t, extDir, "go.mod", "module example.com/options\n\ngo 1.26\n")
	writeFixture(t, extDir, "options.go", `package options

type abbreviations struct {
	ZettelIds *bool `+"`"+`toml:"zettel_ids"`+"`"+`
	MarkIds   *bool `+"`"+`toml:"mark_ids"`+"`"+`
}

type V2 struct {
	Abbreviations *abbreviations `+"`"+`toml:"abbreviations"`+"`"+`
	PrintColors   *bool          `+"`"+`toml:"print-colors"`+"`"+`
}
`)

	// Consumer package with a field of type options.V2
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", "module example.com/test\n\ngo 1.26\n\nrequire example.com/options v0.0.0\n\nreplace example.com/options => ../options\n")
	writeFixture(t, consumerDir, "config.go", `package consumer

import "example.com/options"

//go:generate tommy generate
type Config struct {
	PrintOptions options.V2 `+"`"+`toml:"cli-output"`+"`"+`
}
`)

	infos, err := Analyze(consumerDir, "config.go")
	if err != nil {
		t.Fatalf("expected no error with delegation, got: %s", err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	f := infos[0].Fields[0]
	if f.Kind != FieldDelegatedStruct {
		t.Fatalf("expected FieldDelegatedStruct (%d), got %d", FieldDelegatedStruct, f.Kind)
	}
}

// Regression test for #36: []TypeAlias where alias target is unexported should
// be handled in the recursive classifyFromType path (cross-package struct resolution).
func TestAnalyzeCrossPackageSliceTypeAliasInRecursion(t *testing.T) {
	dir := t.TempDir()

	// External package with a type alias to an unexported TextMarshaler type
	idsDir := filepath.Join(dir, "ids")
	writeFixture(t, idsDir, "go.mod", "module example.com/ids\n\ngo 1.26\n")
	writeFixture(t, idsDir, "ids.go", `package ids

type tagStruct struct{ value string }

type TagStruct = tagStruct

func (t tagStruct) MarshalText() ([]byte, error)  { return []byte(t.value), nil }
func (t *tagStruct) UnmarshalText(b []byte) error  { t.value = string(b); return nil }

type Container struct {
	Tags []TagStruct `+"`"+`toml:"tags"`+"`"+`
}
`)

	// Consumer package with a cross-package struct field containing []TagStruct
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", "module example.com/test\n\ngo 1.26\n\nrequire example.com/ids v0.0.0\n\nreplace example.com/ids => ../ids\n")
	writeFixture(t, consumerDir, "config.go", `package consumer

import "example.com/ids"

//go:generate tommy generate
type Wrapper struct {
	Inner ids.Container `+"`"+`toml:"inner"`+"`"+`
}
`)

	infos, err := Analyze(consumerDir, "config.go")
	if err != nil {
		t.Fatalf("expected no error for []TypeAlias with TextMarshaler in recursive resolution, got: %s", err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}

	// Find the Inner field, then check its InnerInfo has Tags classified correctly
	innerField := infos[0].Fields[0]
	if innerField.GoName != "Inner" {
		t.Fatalf("expected first field Inner, got %s", innerField.GoName)
	}
	if innerField.InnerInfo == nil {
		t.Fatal("expected InnerInfo to be set for struct field")
	}

	var tagsField *FieldInfo
	for _, f := range innerField.InnerInfo.Fields {
		if f.GoName == "Tags" {
			tagsField = &f
			break
		}
	}
	if tagsField == nil {
		t.Fatal("expected Tags field in Container inner info")
	}
	if tagsField.Kind != FieldSliceTextMarshaler {
		t.Fatalf("Tags field kind = %d, want FieldSliceTextMarshaler (%d)", tagsField.Kind, FieldSliceTextMarshaler)
	}
}

// Regression test for #44: []TypeAlias where alias target is an unexported struct
// (not a TextMarshaler) should be resolved transitively through the types path.
// The alias should resolve to the underlying struct for classification, but use
// the exported alias name in the generated code.
func TestAnalyzeCrossPackageSliceTypeAliasStructInRecursion(t *testing.T) {
	dir := t.TempDir()

	// Package ids: type alias from exported name to unexported struct
	idsDir := filepath.Join(dir, "ids")
	writeFixture(t, idsDir, "go.mod", "module example.com/ids\n\ngo 1.26\n")
	writeFixture(t, idsDir, "ids.go", `package ids

type tagStruct struct {
	Value string `+"`"+`toml:"value"`+"`"+`
}

// TagStruct is an exported alias for the unexported tagStruct.
type TagStruct = tagStruct
`)

	// Consumer: wraps struct that contains []ids.TagStruct, resolved transitively
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", "module example.com/test\n\ngo 1.26\n\nrequire example.com/ids v0.0.0\n\nreplace example.com/ids => ../ids\n")
	writeFixture(t, consumerDir, "wrapper.go", `package consumer

import "example.com/ids"

// Inner is NOT annotated with //go:generate — resolved transitively
type Inner struct {
	Tags []ids.TagStruct `+"`"+`toml:"tags"`+"`"+`
}

//go:generate tommy generate
type Outer struct {
	Data Inner `+"`"+`toml:"data"`+"`"+`
}
`)

	infos, err := Analyze(consumerDir, "wrapper.go")
	if err != nil {
		t.Fatalf("expected no error for transitive []TypeAlias (struct, not TextMarshaler), got: %s", err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}

	// Outer has one field "Data" which should be FieldStruct with InnerInfo
	dataField := infos[0].Fields[0]
	if dataField.GoName != "Data" {
		t.Fatalf("expected first field Data, got %s", dataField.GoName)
	}
	if dataField.InnerInfo == nil {
		t.Fatal("expected InnerInfo to be set for Data field")
	}

	// Find the Tags field in Inner's InnerInfo
	var tagsField *FieldInfo
	for _, f := range dataField.InnerInfo.Fields {
		if f.GoName == "Tags" {
			tagsField = &f
			break
		}
	}
	if tagsField == nil {
		t.Fatal("expected Tags field in Inner inner info")
	}
	// Alias to unexported struct: must inline (can't delegate to unexported DecodeInto)
	if tagsField.Kind != FieldSliceStruct {
		t.Fatalf("Tags field kind = %d, want FieldSliceStruct (%d)", tagsField.Kind, FieldSliceStruct)
	}
	// TypeName must use the exported alias name, not the unexported underlying name
	if strings.Contains(tagsField.TypeName, "tagStruct") {
		t.Fatalf("Tags TypeName = %q references unexported type; should use exported alias", tagsField.TypeName)
	}
}

// Regression test for #43/#44: classifyField AST path for []pkg.StructType
// should support cross-package struct delegation, not only TextMarshaler.
func TestAnalyzeCrossPackageSliceStructDirect(t *testing.T) {
	dir := t.TempDir()

	// External package with an exported struct
	extDir := filepath.Join(dir, "ext")
	writeFixture(t, extDir, "go.mod", "module example.com/ext\n\ngo 1.26\n")
	writeFixture(t, extDir, "ext.go", `package ext

type Item struct {
	Name  string `+"`"+`toml:"name"`+"`"+`
	Value int    `+"`"+`toml:"value"`+"`"+`
}
`)

	// Consumer: directly has []ext.Item
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", "module example.com/test\n\ngo 1.26\n\nrequire example.com/ext v0.0.0\n\nreplace example.com/ext => ../ext\n")
	writeFixture(t, consumerDir, "config.go", `package consumer

import "example.com/ext"

//go:generate tommy generate
type Config struct {
	Items []ext.Item `+"`"+`toml:"items"`+"`"+`
}
`)

	infos, err := Analyze(consumerDir, "config.go")
	if err != nil {
		t.Fatalf("expected no error for []pkg.StructType, got: %s", err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}

	itemsField := infos[0].Fields[0]
	if itemsField.GoName != "Items" {
		t.Fatalf("expected first field Items, got %s", itemsField.GoName)
	}
	if itemsField.Kind != FieldSliceDelegatedStruct {
		t.Fatalf("Items field kind = %d, want FieldSliceDelegatedStruct (%d)", itemsField.Kind, FieldSliceDelegatedStruct)
	}
}

// Regression test for #43/#44: []*pkg.AliasType where alias resolves to
// unexported struct fails with "is not a named type" because the AST path
// asserts *types.Named without handling *types.Alias.
func TestAnalyzeCrossPackagePointerSliceAlias(t *testing.T) {
	dir := t.TempDir()

	idsDir := filepath.Join(dir, "ids")
	writeFixture(t, idsDir, "go.mod", "module example.com/ids\n\ngo 1.26\n")
	writeFixture(t, idsDir, "ids.go", `package ids

type tagStruct struct {
	Value string `+"`"+`toml:"value"`+"`"+`
}

type TagStruct = tagStruct
`)

	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", "module example.com/test\n\ngo 1.26\n\nrequire example.com/ids v0.0.0\n\nreplace example.com/ids => ../ids\n")
	writeFixture(t, consumerDir, "config.go", `package consumer

import "example.com/ids"

//go:generate tommy generate
type Config struct {
	Tags []*ids.TagStruct `+"`"+`toml:"tags"`+"`"+`
}
`)

	infos, err := Analyze(consumerDir, "config.go")
	if err != nil {
		t.Fatalf("expected no error for []*pkg.AliasType, got: %s", err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	f := infos[0].Fields[0]
	// Alias to unexported struct: must inline (can't delegate to unexported DecodeInto)
	if f.Kind != FieldSliceStruct {
		t.Fatalf("Tags kind = %d, want FieldSliceStruct (%d)", f.Kind, FieldSliceStruct)
	}
	if strings.Contains(f.TypeName, "tagStruct") {
		t.Fatalf("Tags TypeName = %q references unexported type", f.TypeName)
	}
}

// Regression test for #43/#44: map[string]pkg.AliasType where alias resolves to
// unexported struct fails with "is not a named type" because the AST path
// asserts *types.Named without handling *types.Alias.
func TestAnalyzeCrossPackageMapValueAlias(t *testing.T) {
	dir := t.TempDir()

	extDir := filepath.Join(dir, "ext")
	writeFixture(t, extDir, "go.mod", "module example.com/ext\n\ngo 1.26\n")
	writeFixture(t, extDir, "ext.go", `package ext

type settings struct {
	Value string `+"`"+`toml:"value"`+"`"+`
}

type Settings = settings
`)

	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", "module example.com/test\n\ngo 1.26\n\nrequire example.com/ext v0.0.0\n\nreplace example.com/ext => ../ext\n")
	writeFixture(t, consumerDir, "config.go", `package consumer

import "example.com/ext"

//go:generate tommy generate
type Config struct {
	Profiles map[string]ext.Settings `+"`"+`toml:"profiles"`+"`"+`
}
`)

	infos, err := Analyze(consumerDir, "config.go")
	if err != nil {
		t.Fatalf("expected no error for map[string]pkg.AliasType, got: %s", err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	f := infos[0].Fields[0]
	if f.Kind != FieldMapStringStruct {
		t.Fatalf("Profiles kind = %d, want FieldMapStringStruct (%d)", f.Kind, FieldMapStringStruct)
	}
	if strings.Contains(f.TypeName, "settings") && !strings.Contains(f.TypeName, "Settings") {
		t.Fatalf("Profiles TypeName = %q references unexported type", f.TypeName)
	}
}

// Regression test for #37: map[string]InterfaceType should produce a clear
// error at generation time, not invalid code.
func TestAnalyzeMapStringInterfaceErrors(t *testing.T) {
	dir := t.TempDir()

	// External package with an interface type
	extDir := filepath.Join(dir, "scripts")
	writeFixture(t, extDir, "go.mod", "module example.com/scripts\n\ngo 1.26\n")
	writeFixture(t, extDir, "scripts.go", `package scripts

type RemoteScript interface {
	Cmd(args ...string) error
}
`)

	// Consumer package with map[string]InterfaceType
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", "module example.com/test\n\ngo 1.26\n\nrequire example.com/scripts v0.0.0\n\nreplace example.com/scripts => ../scripts\n")
	writeFixture(t, consumerDir, "config.go", `package consumer

import "example.com/scripts"

//go:generate tommy generate
type Config struct {
	Name          string                              `+"`"+`toml:"name"`+"`"+`
	RemoteScripts map[string]scripts.RemoteScript     `+"`"+`toml:"remote-scripts"`+"`"+`
}
`)

	_, err := Analyze(consumerDir, "config.go")
	if err == nil {
		t.Fatal("expected error for map[string]InterfaceType, but got nil")
	}
	// Error should mention interface and suggest toml:"-"
	errStr := err.Error()
	if !strings.Contains(errStr, "interface") {
		t.Fatalf("expected error mentioning 'interface', got: %s", errStr)
	}
}

func TestAnalyzeEmbeddedCrossPackageInterfaceSkipped(t *testing.T) {
	dir := t.TempDir()

	// Package with an interface type and a type alias to it
	ifaceDir := filepath.Join(dir, "interfaces")
	writeFixture(t, ifaceDir, "go.mod", "module example.com/interfaces\n\ngo 1.26\n")
	writeFixture(t, ifaceDir, "pool.go", `package interfaces

type Ptr[V any] interface {
	*V
}

type PoolPtr[V any, VP Ptr[V]] interface {
	Get() (VP, func())
}
`)

	// Intermediate package with a type alias to the interface
	luaDir := filepath.Join(dir, "lua")
	writeFixture(t, luaDir, "go.mod", "module example.com/lua\n\ngo 1.26\n\nrequire example.com/interfaces v0.0.0\n\nreplace example.com/interfaces => ../interfaces\n")
	writeFixture(t, luaDir, "pool.go", `package lua

import "example.com/interfaces"

type VM struct{}

type VMPool = interfaces.PoolPtr[VM, *VM]
`)

	// Consumer that embeds the interface alias — should be silently skipped
	consumerDir := filepath.Join(dir, "consumer")
	writeFixture(t, consumerDir, "go.mod", "module example.com/test\n\ngo 1.26\n\nrequire (\n\texample.com/lua v0.0.0\n\texample.com/interfaces v0.0.0\n)\n\nreplace (\n\texample.com/lua => ../lua\n\texample.com/interfaces => ../interfaces\n)\n")
	writeFixture(t, consumerDir, "consumer.go", `package consumer

import "example.com/lua"

//go:generate tommy generate
type Config struct {
	lua.VMPool
	Filter string `+"`"+`toml:"filter"`+"`"+`
}
`)

	infos, err := Analyze(consumerDir, "consumer.go")
	if err != nil {
		t.Fatalf("expected no error for embedded interface, got: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(infos))
	}
	// Should have only the Filter field — the embedded interface is skipped
	if len(infos[0].Fields) != 1 {
		t.Errorf("expected 1 field (Filter only), got %d: %v",
			len(infos[0].Fields), fieldNames(infos[0].Fields))
	}
	if len(infos[0].Fields) > 0 && infos[0].Fields[0].TomlKey != "filter" {
		t.Errorf("expected field toml key 'filter', got %q", infos[0].Fields[0].TomlKey)
	}
}
