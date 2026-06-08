package marshal

import (
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/amarbel-llc/tommy/pkg/cst"
	"github.com/amarbel-llc/tommy/pkg/document"
)

// DocumentHandle holds the CST-backed document for round-trip editing.
type DocumentHandle struct {
	doc *document.Document
}

// UnmarshalDocument parses TOML input into a CST-backed document and populates
// the struct pointed to by v using its `toml` struct tags.
func UnmarshalDocument(input []byte, v any) (*DocumentHandle, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil, fmt.Errorf("UnmarshalDocument requires a non-nil pointer")
	}

	doc, err := document.Parse(input)
	if err != nil {
		return nil, err
	}

	model, err := cst.Decompose(doc.Root())
	if err != nil {
		return nil, err
	}
	if err := decodeStructValue(model, rv.Elem()); err != nil {
		return nil, err
	}

	return &DocumentHandle{doc: doc}, nil
}

// UnmarshalReader parses TOML from an io.Reader into a CST-backed document
// and populates the struct pointed to by v using its `toml` struct tags.
func UnmarshalReader(r io.Reader, v any) (*DocumentHandle, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil, fmt.Errorf("UnmarshalReader requires a non-nil pointer")
	}

	doc, err := document.ParseReader(r)
	if err != nil {
		return nil, err
	}

	model, err := cst.Decompose(doc.Root())
	if err != nil {
		return nil, err
	}
	if err := decodeStructValue(model, rv.Elem()); err != nil {
		return nil, err
	}

	return &DocumentHandle{doc: doc}, nil
}

// MarshalDocument writes struct field values back into the CST-backed document
// and returns the serialized bytes, preserving comments and formatting.
func MarshalDocument(handle *DocumentHandle, v any) ([]byte, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil, fmt.Errorf("MarshalDocument requires a non-nil pointer")
	}

	if err := encodeStruct(handle.doc, rv.Elem(), ""); err != nil {
		return nil, err
	}

	return handle.doc.Bytes(), nil
}

func fieldTomlKey(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("toml")
	if tag == "" || tag == "-" {
		return "", false
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return "", false
	}
	return name, true
}

func qualifiedKey(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// decodeStructValue populates rv (a struct) from the normalized value model
// table v: every tagged field present in v is decoded; absent fields stay zero.
// Reading from the model (cst.Decompose) means the reflection unmarshal accepts
// every TOML spelling — inline tables, dotted keys, inline arrays, implicit
// parents — for free, the same robustness the generated decoder gained (ADR
// 2026-06-07). Encode (MarshalDocument) is unchanged and stays CST-based.
func decodeStructValue(v *cst.Value, rv reflect.Value) error {
	rt := rv.Type()
	for i := range rt.NumField() {
		name, ok := fieldTomlKey(rt.Field(i))
		if !ok {
			continue
		}
		child, present := v.Get(name)
		if !present {
			continue
		}
		if err := decodeFieldValue(child, rv.Field(i), name); err != nil {
			return err
		}
	}
	return nil
}

func decodeFieldValue(v *cst.Value, fv reflect.Value, key string) error {
	switch fv.Kind() {
	case reflect.Struct:
		if v.Kind != cst.VTable {
			return typeErr(key, "table")
		}
		return decodeStructValue(v, fv)
	case reflect.String:
		s, ok := leafExtract(v, cst.ExtractString)
		if !ok {
			return typeErr(key, "string")
		}
		fv.SetString(s)
	case reflect.Int:
		n, ok := leafExtract(v, cst.ExtractInt)
		if !ok {
			return typeErr(key, "int")
		}
		fv.SetInt(int64(n))
	case reflect.Int64:
		n, ok := leafExtract(v, cst.ExtractInt64)
		if !ok {
			return typeErr(key, "int64")
		}
		fv.SetInt(n)
	case reflect.Uint64:
		n, ok := leafExtract(v, cst.ExtractUint64)
		if !ok {
			return typeErr(key, "uint64")
		}
		fv.SetUint(n)
	case reflect.Float64:
		f, ok := leafExtract(v, cst.ExtractFloat64)
		if !ok {
			return typeErr(key, "float64")
		}
		fv.SetFloat(f)
	case reflect.Bool:
		b, ok := leafExtract(v, cst.ExtractBool)
		if !ok {
			return typeErr(key, "bool")
		}
		fv.SetBool(b)
	case reflect.Slice:
		return decodeSliceValue(v, fv, key)
	default:
		return fmt.Errorf("unsupported field type %s for key %q", fv.Kind(), key)
	}
	return nil
}

func decodeSliceValue(v *cst.Value, fv reflect.Value, key string) error {
	elemType := fv.Type().Elem()
	if elemType.Kind() == reflect.Struct {
		if v.Kind != cst.VArray {
			return typeErr(key, "array of tables")
		}
		slice := reflect.MakeSlice(fv.Type(), len(v.Items), len(v.Items))
		for i := range v.Items {
			if err := decodeStructValue(&v.Items[i], slice.Index(i)); err != nil {
				return fmt.Errorf("%q[%d]: %w", key, i, err)
			}
		}
		fv.Set(slice)
		return nil
	}

	switch elemType.Kind() {
	case reflect.Int:
		s, ok := leafExtract(v, cst.ExtractIntSlice)
		if !ok {
			return typeErr(key, "[]int")
		}
		fv.Set(reflect.ValueOf(s))
	case reflect.Int64:
		s, ok := leafExtract(v, cst.ExtractInt64Slice)
		if !ok {
			return typeErr(key, "[]int64")
		}
		fv.Set(reflect.ValueOf(s))
	case reflect.Uint64:
		s, ok := leafExtract(v, cst.ExtractUint64Slice)
		if !ok {
			return typeErr(key, "[]uint64")
		}
		fv.Set(reflect.ValueOf(s))
	case reflect.Float64:
		s, ok := leafExtract(v, cst.ExtractFloat64Slice)
		if !ok {
			return typeErr(key, "[]float64")
		}
		fv.Set(reflect.ValueOf(s))
	case reflect.Bool:
		s, ok := leafExtract(v, cst.ExtractBoolSlice)
		if !ok {
			return typeErr(key, "[]bool")
		}
		fv.Set(reflect.ValueOf(s))
	case reflect.String:
		s, ok := leafExtract(v, cst.ExtractStringSlice)
		if !ok {
			return typeErr(key, "[]string")
		}
		fv.Set(reflect.ValueOf(s))
	default:
		return fmt.Errorf("unsupported slice element type %s for key %q", elemType.Kind(), key)
	}
	return nil
}

// leafExtract applies a cst scalar/slice extractor to a model leaf value,
// returning false (so the caller reports a type error) when v is not a leaf.
func leafExtract[T any](v *cst.Value, extract func(*cst.Node) (T, bool)) (T, bool) {
	if v.Kind != cst.VLeaf {
		var zero T
		return zero, false
	}
	return extract(v.Leaf)
}

func typeErr(key, want string) error {
	return fmt.Errorf("key %q: cannot decode as %s", key, want)
}

func encodeStruct(doc *document.Document, rv reflect.Value, prefix string) error {
	rt := rv.Type()

	for i := range rt.NumField() {
		field := rt.Field(i)
		name, ok := fieldTomlKey(field)
		if !ok {
			continue
		}

		fv := rv.Field(i)
		key := qualifiedKey(prefix, name)

		if field.Type.Kind() == reflect.Struct {
			if err := encodeStruct(doc, fv, key); err != nil {
				return err
			}
			continue
		}

		if err := encodeField(doc, fv, key); err != nil {
			return err
		}
	}

	return nil
}

func encodeField(doc *document.Document, fv reflect.Value, key string) error {
	var val any

	switch fv.Kind() {
	case reflect.String:
		if doc.IsMultilineString(key) {
			return doc.SetMultiline(key, fv.String())
		}
		val = fv.String()
	case reflect.Int:
		val = int(fv.Int())
	case reflect.Int64:
		val = fv.Int()
	case reflect.Float64:
		val = fv.Float()
	case reflect.Bool:
		val = fv.Bool()
	case reflect.Slice:
		return encodeSliceField(doc, fv, key)
	default:
		return fmt.Errorf("unsupported field type %s for key %q", fv.Kind(), key)
	}

	// Skip zero-value fields that don't already exist in the document
	if fv.IsZero() && !doc.Has(key) {
		return nil
	}

	return doc.Set(key, val)
}

func encodeSliceField(doc *document.Document, fv reflect.Value, key string) error {
	elemType := fv.Type().Elem()

	switch elemType.Kind() {
	case reflect.Int:
		s := make([]int, fv.Len())
		for i := range fv.Len() {
			s[i] = int(fv.Index(i).Int())
		}
		return doc.Set(key, s)

	case reflect.String:
		s := make([]string, fv.Len())
		for i := range fv.Len() {
			s[i] = fv.Index(i).String()
		}
		return doc.Set(key, s)

	case reflect.Struct:
		return encodeStructSliceField(doc, fv, key)

	default:
		return fmt.Errorf("unsupported slice element type %s for key %q", elemType.Kind(), key)
	}
}

func encodeStructSliceField(doc *document.Document, fv reflect.Value, key string) error {
	nodes := doc.FindArrayTableNodes(key)
	elemType := fv.Type().Elem()

	for i := range fv.Len() {
		var container *cst.Node
		if i < len(nodes) {
			container = nodes[i]
		} else {
			container = doc.AppendArrayTableEntry(key)
		}
		elem := fv.Index(i)
		for j := range elemType.NumField() {
			field := elemType.Field(j)
			name, ok := fieldTomlKey(field)
			if !ok {
				continue
			}
			fieldVal := elem.Field(j)
			if fieldVal.Kind() == reflect.String && document.IsMultilineStringInContainer(container, name) {
				if err := doc.SetMultilineInContainer(container, name, fieldVal.String()); err != nil {
					return err
				}
				continue
			}
			val := encodeFieldValue(fieldVal)
			if val == nil {
				continue
			}
			if err := doc.SetInContainer(container, name, val); err != nil {
				return err
			}
		}
	}

	// Remove trailing entries if slice shrank
	for i := fv.Len(); i < len(nodes); i++ {
		if err := doc.RemoveArrayTableEntry(nodes[i]); err != nil {
			return err
		}
	}

	// If entries were removed, strip trailing blank line from the last
	// remaining entry so we don't leave a stray separator.
	if fv.Len() < len(nodes) && fv.Len() > 0 {
		lastNode := nodes[fv.Len()-1]
		trimTrailingBlankLine(lastNode)
	}

	return nil
}

func trimTrailingBlankLine(node *cst.Node) {
	n := len(node.Children)
	if n > 0 && node.Children[n-1].Kind == cst.NodeNewline {
		node.Children = node.Children[:n-1]
	}
}

func encodeFieldValue(fv reflect.Value) any {
	switch fv.Kind() {
	case reflect.String:
		return fv.String()
	case reflect.Int:
		return int(fv.Int())
	case reflect.Int64:
		return fv.Int()
	case reflect.Float64:
		return fv.Float()
	case reflect.Bool:
		return fv.Bool()
	default:
		return nil
	}
}
