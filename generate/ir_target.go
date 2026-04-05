package generate

import (
	"strings"

	jen "github.com/dave/jennifer/jen"
)

// TargetPath represents a Go lvalue for assignment in generated code.
// It produces both a string representation (for backward-compatible renderers)
// and a jennifer Code tree.
type TargetPath struct {
	Receiver string // "" for locals ("entry"), "d" for receiver methods
	Root     string // "data", "entry", etc.
	Segs     []TargetSeg
}

// SegKind distinguishes field access from array index in a target path.
type SegKind int

const (
	SegDot   SegKind = iota // .Name
	SegIndex                // [i]
)

// TargetSeg is one step in a target path chain.
type TargetSeg struct {
	Kind SegKind
	Name string // field name for SegDot, variable name for SegIndex
}

// ReceiverTarget creates a target rooted at a receiver field (e.g. d.data).
func ReceiverTarget(recv, root string) TargetPath {
	return TargetPath{Receiver: recv, Root: root}
}

// LocalTarget creates a target rooted at a local variable (e.g. entry).
func LocalTarget(name string) TargetPath {
	return TargetPath{Root: name}
}

// Dot appends a field access segment.
func (t TargetPath) Dot(name string) TargetPath {
	segs := make([]TargetSeg, len(t.Segs), len(t.Segs)+1)
	copy(segs, t.Segs)
	return TargetPath{
		Receiver: t.Receiver,
		Root:     t.Root,
		Segs:     append(segs, TargetSeg{Kind: SegDot, Name: name}),
	}
}

// Index appends an array index segment.
func (t TargetPath) Index(varName string) TargetPath {
	segs := make([]TargetSeg, len(t.Segs), len(t.Segs)+1)
	copy(segs, t.Segs)
	return TargetPath{
		Receiver: t.Receiver,
		Root:     t.Root,
		Segs:     append(segs, TargetSeg{Kind: SegIndex, Name: varName}),
	}
}

// String returns the Go expression as a string (e.g. "d.data.Servers[i].Host").
func (t TargetPath) String() string {
	var b strings.Builder
	if t.Receiver != "" {
		b.WriteString(t.Receiver)
		b.WriteByte('.')
	}
	b.WriteString(t.Root)
	for _, s := range t.Segs {
		switch s.Kind {
		case SegDot:
			b.WriteByte('.')
			b.WriteString(s.Name)
		case SegIndex:
			b.WriteByte('[')
			b.WriteString(s.Name)
			b.WriteByte(']')
		}
	}
	return b.String()
}

// Jen returns a jennifer Code tree for this target path.
func (t TargetPath) Jen() *jen.Statement {
	var s *jen.Statement
	if t.Receiver != "" {
		s = jen.Id(t.Receiver).Dot(t.Root)
	} else {
		s = jen.Id(t.Root)
	}
	for _, seg := range t.Segs {
		switch seg.Kind {
		case SegDot:
			s = s.Dot(seg.Name)
		case SegIndex:
			s = s.Index(jen.Id(seg.Name))
		}
	}
	return s
}

// --- TOMLKey ---

// TOMLKey represents a TOML key path used for table header matching and
// consumed-key tracking. It can contain static literals, runtime variables
// (e.g. map iteration key), and the keyPrefix function parameter.
type TOMLKey struct {
	Parts []KeyPart
}

// KeyKind distinguishes the parts of a TOML key.
type KeyKind int

const (
	KeyLit    KeyKind = iota // static string segment
	KeyVar                   // runtime variable (e.g. "_mk")
	KeyPrefix                // the `keyPrefix` function parameter
)

// KeyPart is one segment of a TOML key.
type KeyPart struct {
	Kind  KeyKind
	Value string // literal text or variable name
}

// StaticKey creates a key from a dotted string (e.g. "servers.name").
// An empty string produces an empty key.
func StaticKey(dotted string) TOMLKey {
	if dotted == "" {
		return TOMLKey{}
	}
	return TOMLKey{Parts: []KeyPart{{Kind: KeyLit, Value: dotted}}}
}

// PrefixedKey creates a key that starts with the keyPrefix runtime parameter.
// The dotted argument is appended as a literal (e.g. PrefixedKey("auth") →
// keyPrefix + "auth").
func PrefixedKey(dotted string) TOMLKey {
	parts := []KeyPart{{Kind: KeyPrefix}}
	if dotted != "" {
		parts = append(parts, KeyPart{Kind: KeyLit, Value: dotted})
	}
	return TOMLKey{Parts: parts}
}

// Dot appends a literal segment with a dot separator.
// If the key is empty, the segment is added without a leading dot.
func (k TOMLKey) Dot(seg string) TOMLKey {
	parts := make([]KeyPart, len(k.Parts))
	copy(parts, k.Parts)
	if len(parts) == 0 {
		return TOMLKey{Parts: []KeyPart{{Kind: KeyLit, Value: seg}}}
	}
	last := parts[len(parts)-1]
	if last.Kind == KeyLit {
		// Append with dot separator, but skip if already ends with dot
		if strings.HasSuffix(last.Value, ".") {
			parts[len(parts)-1].Value += seg
		} else {
			parts[len(parts)-1].Value += "." + seg
		}
	} else if last.Kind == KeyPrefix {
		// keyPrefix already includes trailing dot, so no extra dot needed
		parts = append(parts, KeyPart{Kind: KeyLit, Value: seg})
	} else {
		// After a variable, add dot separator
		parts = append(parts, KeyPart{Kind: KeyLit, Value: "." + seg})
	}
	return TOMLKey{Parts: parts}
}

// DotPrefix appends a literal dot-terminated segment (e.g. "servers.").
func (k TOMLKey) DotPrefix(seg string) TOMLKey {
	parts := make([]KeyPart, len(k.Parts))
	copy(parts, k.Parts)
	var suffix string
	if len(parts) == 0 || (len(parts) > 0 && parts[len(parts)-1].Kind == KeyPrefix) {
		suffix = seg + "."
	} else {
		suffix = "." + seg + "."
	}
	if len(parts) > 0 && parts[len(parts)-1].Kind == KeyLit {
		parts[len(parts)-1].Value += suffix
	} else {
		parts = append(parts, KeyPart{Kind: KeyLit, Value: suffix})
	}
	return TOMLKey{Parts: parts}
}

// Var appends a runtime variable part (e.g. "_mk" for map iteration key).
func (k TOMLKey) Var(name string) TOMLKey {
	parts := make([]KeyPart, len(k.Parts))
	copy(parts, k.Parts)
	return TOMLKey{Parts: append(parts, KeyPart{Kind: KeyVar, Value: name})}
}

// Lit appends a literal part.
func (k TOMLKey) Lit(s string) TOMLKey {
	parts := make([]KeyPart, len(k.Parts))
	copy(parts, k.Parts)
	if len(parts) > 0 && parts[len(parts)-1].Kind == KeyLit {
		parts[len(parts)-1].Value += s
		return TOMLKey{Parts: parts}
	}
	return TOMLKey{Parts: append(parts, KeyPart{Kind: KeyLit, Value: s})}
}

// Static returns the key as a static string. Panics if the key contains
// dynamic parts (KeyVar or KeyPrefix).
func (k TOMLKey) Static() string {
	var b strings.Builder
	for _, p := range k.Parts {
		switch p.Kind {
		case KeyLit:
			b.WriteString(p.Value)
		default:
			panic("TOMLKey.Static() called on dynamic key")
		}
	}
	return b.String()
}

// IsStatic returns true if the key contains only literal parts.
func (k TOMLKey) IsStatic() bool {
	for _, p := range k.Parts {
		if p.Kind != KeyLit {
			return false
		}
	}
	return true
}

// BareKey returns the last segment after the final dot separator.
// For "servers.settings.max_conns", returns "max_conns".
// Works on dynamic keys by examining the last literal part.
func (k TOMLKey) BareKey() string {
	// Find the last literal part and extract after final dot
	for i := len(k.Parts) - 1; i >= 0; i-- {
		if k.Parts[i].Kind == KeyLit {
			s := k.Parts[i].Value
			if j := strings.LastIndexByte(s, '.'); j >= 0 {
				return s[j+1:]
			}
			return s
		}
	}
	return ""
}

// VarSuffix returns a CamelCase suffix derived from the full key path, suitable
// for making generated variable names unique across nesting levels.
// "haustoria.caldav" -> "HaustoriaCaldav", "exec-command" -> "ExecCommand".
// Dynamic key parts (KeyVar, KeyPrefix) are skipped since runtime variables
// already provide uniqueness through iteration.
func (k TOMLKey) VarSuffix() string {
	var sb strings.Builder
	for _, p := range k.Parts {
		if p.Kind != KeyLit {
			continue
		}
		for _, seg := range strings.FieldsFunc(p.Value, func(r rune) bool {
			return r == '.' || r == '-' || r == '_'
		}) {
			seg = strings.TrimSpace(seg)
			if seg != "" {
				sb.WriteString(toUpperFirst(seg))
			}
		}
	}
	return sb.String()
}

// Jen returns a jennifer expression for this key.
// Static keys become Lit("key"). Dynamic keys become concatenation expressions
// like Lit("targets.").Op("+").Id("_mk").Op("+").Lit(".auth").
func (k TOMLKey) Jen() *jen.Statement {
	if len(k.Parts) == 0 {
		return jen.Lit("")
	}
	if len(k.Parts) == 1 {
		return k.Parts[0].jen()
	}
	s := k.Parts[0].jen()
	for _, p := range k.Parts[1:] {
		s = s.Op("+").Add(p.jen())
	}
	return s
}

// JenLen returns a jennifer expression for the length of this key's string value.
// Static keys use a literal int. Dynamic keys use len() calls.
func (k TOMLKey) JenLen() *jen.Statement {
	if k.IsStatic() {
		return jen.Lit(len(k.Static()))
	}
	if len(k.Parts) == 1 {
		return jen.Len(k.Parts[0].jen())
	}
	s := jen.Len(k.Parts[0].jen())
	for _, p := range k.Parts[1:] {
		s = s.Op("+").Len(p.jen())
	}
	return s
}

func (p KeyPart) jen() *jen.Statement {
	switch p.Kind {
	case KeyLit:
		return jen.Lit(p.Value)
	case KeyVar:
		return jen.Id(p.Value)
	case KeyPrefix:
		return jen.Id("keyPrefix")
	}
	return jen.Lit("")
}
