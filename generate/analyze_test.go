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

func TestAnalyzeUnsupportedTypeErrors(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "go.mod", "module example.com/test\n\ngo 1.25.6\n")
	writeFixture(t, dir, "config.go", "package test\n\n//go:generate tommy generate\ntype Config struct {\n\tData map[string]int `toml:\"data\"`\n}\n")

	_, err := Analyze(dir, "config.go")
	if err == nil {
		t.Fatal("expected error for unsupported map value type")
	}
}
