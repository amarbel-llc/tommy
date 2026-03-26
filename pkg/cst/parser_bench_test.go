package cst

import (
	"bytes"
	"strings"
	"testing"
)

var smallInput = []byte(`# Server configuration
title = "TOML Example"

[server]
host = "localhost"
port = 8080
enabled = true
`)

var mediumInput = []byte(generateMediumInput())

func generateMediumInput() string {
	var sb strings.Builder
	sb.WriteString("# Auto-generated config\n")
	sb.WriteString("title = \"Medium Config\"\n\n")
	for i := 0; i < 50; i++ {
		sb.WriteString("[section_" + strings.Repeat("x", 3) + "]\n")
		sb.WriteString("key_string = \"value with spaces\"\n")
		sb.WriteString("key_int = 42\n")
		sb.WriteString("key_float = 3.14\n")
		sb.WriteString("key_bool = true\n")
		sb.WriteString("key_array = [1, 2, 3, 4, 5]\n")
		sb.WriteString("key_inline = {a = 1, b = \"two\"}\n")
		sb.WriteString("# a comment\n\n")
	}
	return sb.String()
}

var largeInput = []byte(generateLargeInput())

func generateLargeInput() string {
	var sb strings.Builder
	sb.WriteString("# Large config file\n")
	for i := 0; i < 200; i++ {
		sb.WriteString("[[items]]\n")
		sb.WriteString("name = \"item_name_here\"\n")
		sb.WriteString("value = 12345\n")
		sb.WriteString("ratio = 0.99\n")
		sb.WriteString("enabled = false\n")
		sb.WriteString("tags = [\"alpha\", \"beta\", \"gamma\"]\n")
		sb.WriteString("created = 2026-03-25T12:00:00Z\n")
		sb.WriteString("description = \"\"\"A multiline\nstring value\"\"\"\n")
		sb.WriteString("path = 'C:\\Users\\file'\n\n")
	}
	return sb.String()
}

func BenchmarkParseOld_Small(b *testing.B) {
	for b.Loop() {
		ParseOld(smallInput)
	}
}

func BenchmarkParse_Small(b *testing.B) {
	for b.Loop() {
		Parse(smallInput)
	}
}

func BenchmarkParseReader_Small(b *testing.B) {
	for b.Loop() {
		ParseReader(bytes.NewReader(smallInput))
	}
}

func BenchmarkParseOld_Medium(b *testing.B) {
	for b.Loop() {
		ParseOld(mediumInput)
	}
}

func BenchmarkParse_Medium(b *testing.B) {
	for b.Loop() {
		Parse(mediumInput)
	}
}

func BenchmarkParseReader_Medium(b *testing.B) {
	for b.Loop() {
		ParseReader(bytes.NewReader(mediumInput))
	}
}

func BenchmarkParseOld_Large(b *testing.B) {
	for b.Loop() {
		ParseOld(largeInput)
	}
}

func BenchmarkParse_Large(b *testing.B) {
	for b.Loop() {
		Parse(largeInput)
	}
}

func BenchmarkParseReader_Large(b *testing.B) {
	for b.Loop() {
		ParseReader(bytes.NewReader(largeInput))
	}
}
