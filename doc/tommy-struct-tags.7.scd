tommy-struct-tags(7)

# NAME

tommy-struct-tags - struct tag reference for tommy codegen

# DESCRIPTION

*tommy generate* (see *tommy-generate*(1)) classifies each struct field by
its Go type and the *toml* struct tag, then emits decode/encode code
appropriate to that classification. This page documents the tag syntax and
the set of supported field kinds.

# TAG SYNTAX

The tag has the form:

```
`toml:"<key>[,omitempty][,multiline]"`
```

*<key>*
	Required. The TOML key the field maps to. Fields without a *toml* tag
	are ignored by codegen.

*omitempty*
	On encode, skip the field if its value is the zero value for its type.
	For pointer fields, nil is the skipped value. On decode, the modifier
	has no effect.

*multiline*
	On encode, emit the value using triple-quoted (*"""*) TOML string
	syntax. Only meaningful for string-kinded fields.

Modifiers may appear in any order after the key.

# FIELD KINDS

Every tagged field is classified into exactly one kind. Each kind has its
own code path for decode and encode.

*FieldPrimitive*
	*string*, *int*, *int64*, *uint64*, *float64*, *bool*, and defined
	types whose underlying type is one of these.

*FieldPointerPrimitive*
	Pointer to a primitive (e.g. *\*bool*, *\*int*). A nil pointer omits
	the key on encode and signals "absent in input" on decode.

*FieldStruct*, *FieldPointerStruct*
	Nested struct defined in the same package, also carrying *toml* tags.
	Decode/encode is inlined field-by-field.

*FieldDelegatedStruct*, *FieldPointerDelegatedStruct*
	Nested struct defined in another package. The generator emits calls to
	the target package's *Decode<Name>Into* and *Encode<Name>From*
	(see *tommy-generate*(1)) rather than inlining. This enables structs
	whose fields reference unexported types in the other package.

*FieldSlicePrimitive*
	Slice of a primitive kind (e.g. *[]int*, *[]string*). Encoded as a
	TOML array.

*FieldSliceStruct*
	Slice of a struct kind. Encoded as a TOML array-of-tables
	(*[[key]]*).

*FieldMapStringString*
	*map[string]string*. Encoded as a TOML table of string values.

*FieldMapStringStruct*
	*map[string]SomeStruct*. Encoded as a TOML table whose values are
	nested tables.

*FieldMapStringMapStringString*
	*map[string]NamedAlias*, where *NamedAlias* is a defined type whose
	underlying type is *map[string]string*. Encoded as a nested TOML
	table.

*FieldCustom*
	Type implements *TOMLMarshaler* and/or *TOMLUnmarshaler*. The generator
	delegates to those methods and passes an *any* value.

*FieldTextMarshaler*
	Type implements *encoding.TextMarshaler* and/or
	*encoding.TextUnmarshaler*. The value round-trips through its string
	representation.

*FieldSliceTextMarshaler*
	Slice of a type that implements the *encoding.TextMarshaler*/
	*TextUnmarshaler* interfaces. Each element round-trips as a string.

A field whose Go type does not fall into one of the above kinds produces a
codegen error at *tommy generate* time.

# SUPPORTED INTERFACES

*TOMLUnmarshaler*, *TOMLMarshaler*
	Declared in *github.com/amarbel-llc/tommy/pkg/tommy*. A type that
	implements these is classified as *FieldCustom*.

*encoding.TextMarshaler*, *encoding.TextUnmarshaler*
	Standard library. A type that implements these is classified as
	*FieldTextMarshaler* (or *FieldSliceTextMarshaler* in slices).

*Validate() error*
	Optional. When a tagged struct implements this method, the generated
	*Decode<Name>* calls it after decoding and the generated *Encode*
	calls it before serialization. A non-nil return aborts the operation.

# EXAMPLES

Primitive with all modifiers:

```
type Server struct {
	Host    string  `toml:"host"`
	Port    int     `toml:"port,omitempty"`
	Banner  string  `toml:"banner,multiline"`
	Debug   *bool   `toml:"debug,omitempty"`
}
```

Array-of-tables:

```
type Config struct {
	Servers []Server `toml:"servers"`
}
```

Cross-package delegated struct (generator emits calls to the other
package's *DecodeServerInto*/*EncodeServerFrom*):

```
import "example.com/netpkg"

type Config struct {
	Upstream netpkg.Server `toml:"upstream"`
}
```

# SEE ALSO

*tommy*(1), *tommy-fmt*(1), *tommy-generate*(1)
