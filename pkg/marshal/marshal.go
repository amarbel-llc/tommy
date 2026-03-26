package marshal

import (
	"errors"
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

	if err := decodeStruct(doc, rv.Elem(), ""); err != nil {
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

	if err := decodeStruct(doc, rv.Elem(), ""); err != nil {
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

func decodeStruct(doc *document.Document, rv reflect.Value, prefix string) error {
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
			if err := decodeStruct(doc, fv, key); err != nil {
				return err
			}
			continue
		}

		if err := decodeField(doc, fv, key); err != nil {
			return err
		}
	}

	return nil
}

func isNotFoundError(err error) bool {
	return errors.Is(err, document.ErrNotFound)
}

func decodeField(doc *document.Document, fv reflect.Value, key string) error {
	switch fv.Kind() {
	case reflect.String:
		v, err := document.Get[string](doc, key)
		if isNotFoundError(err) {
			return nil
		}
		if err != nil {
			return err
		}
		fv.SetString(v)

	case reflect.Int:
		v, err := document.Get[int](doc, key)
		if isNotFoundError(err) {
			return nil
		}
		if err != nil {
			return err
		}
		fv.SetInt(int64(v))

	case reflect.Int64:
		v, err := document.Get[int64](doc, key)
		if isNotFoundError(err) {
			return nil
		}
		if err != nil {
			return err
		}
		fv.SetInt(v)

	case reflect.Float64:
		v, err := document.Get[float64](doc, key)
		if isNotFoundError(err) {
			return nil
		}
		if err != nil {
			return err
		}
		fv.SetFloat(v)

	case reflect.Bool:
		v, err := document.Get[bool](doc, key)
		if isNotFoundError(err) {
			return nil
		}
		if err != nil {
			return err
		}
		fv.SetBool(v)

	case reflect.Slice:
		return decodeSliceField(doc, fv, key)

	default:
		return fmt.Errorf("unsupported field type %s for key %q", fv.Kind(), key)
	}

	return nil
}

func decodeSliceField(doc *document.Document, fv reflect.Value, key string) error {
	elemType := fv.Type().Elem()

	switch elemType.Kind() {
	case reflect.Int:
		v, err := document.Get[[]int](doc, key)
		if err != nil {
			return err
		}
		fv.Set(reflect.ValueOf(v))

	case reflect.String:
		v, err := document.Get[[]string](doc, key)
		if err != nil {
			return err
		}
		fv.Set(reflect.ValueOf(v))

	case reflect.Struct:
		return decodeStructSliceField(doc, fv, key)

	default:
		return fmt.Errorf("unsupported slice element type %s for key %q", elemType.Kind(), key)
	}

	return nil
}

func decodeStructSliceField(doc *document.Document, fv reflect.Value, key string) error {
	nodes := doc.FindArrayTableNodes(key)
	if len(nodes) == 0 {
		return nil
	}

	slice := reflect.MakeSlice(fv.Type(), len(nodes), len(nodes))
	elemType := fv.Type().Elem()

	for i, node := range nodes {
		elem := slice.Index(i)
		for j := range elemType.NumField() {
			field := elemType.Field(j)
			name, ok := fieldTomlKey(field)
			if !ok {
				continue
			}
			fieldVal := elem.Field(j)
			if err := decodeContainerField(doc, node, fieldVal, name); err != nil {
				return fmt.Errorf("field %q in %q[%d]: %w", name, key, i, err)
			}
		}
	}

	fv.Set(slice)
	return nil
}

func decodeContainerField(doc *document.Document, container *cst.Node, fv reflect.Value, key string) error {
	switch fv.Kind() {
	case reflect.String:
		v, err := document.GetFromContainer[string](doc, container, key)
		if isNotFoundError(err) {
			return nil
		}
		if err != nil {
			return err
		}
		fv.SetString(v)
	case reflect.Int:
		v, err := document.GetFromContainer[int](doc, container, key)
		if isNotFoundError(err) {
			return nil
		}
		if err != nil {
			return err
		}
		fv.SetInt(int64(v))
	case reflect.Int64:
		v, err := document.GetFromContainer[int64](doc, container, key)
		if isNotFoundError(err) {
			return nil
		}
		if err != nil {
			return err
		}
		fv.SetInt(v)
	case reflect.Float64:
		v, err := document.GetFromContainer[float64](doc, container, key)
		if isNotFoundError(err) {
			return nil
		}
		if err != nil {
			return err
		}
		fv.SetFloat(v)
	case reflect.Bool:
		v, err := document.GetFromContainer[bool](doc, container, key)
		if isNotFoundError(err) {
			return nil
		}
		if err != nil {
			return err
		}
		fv.SetBool(v)
	default:
		return fmt.Errorf("unsupported field type %s", fv.Kind())
	}
	return nil
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

