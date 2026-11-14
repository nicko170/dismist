// Package firmware holds the AP33 0.15 config schema: every JSON path
// ep-control decodes, with its exact Go type and kind (ap33-0.15.paths,
// generated once by `dismist-gen paths` from the firmware's type dump).
// It is the canonical-case dictionary for key folding and the type source
// for the normaliser's rule 2a and the firmware type check (design §4.2).
package firmware

import (
	"bufio"
	_ "embed"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

//go:embed ap33-0.15.paths
var ap33Paths string

// Kind is the JSON-decoding kind of a Go type.
type Kind string

const (
	KindBool      Kind = "bool"
	KindInt       Kind = "int"
	KindUint      Kind = "uint"
	KindFloat     Kind = "float"
	KindString    Kind = "string"
	KindStruct    Kind = "struct"
	KindSlice     Kind = "slice"
	KindArray     Kind = "array"
	KindMap       Kind = "map"
	KindPointer   Kind = "pointer"
	KindCustom    Kind = "custom"    // the type has its own UnmarshalJSON/UnmarshalText
	KindNamed     Kind = "named"     // a named non-struct type whose underlying kind is unknown
	KindRaw       Kind = "raw"       // json.RawMessage
	KindInterface Kind = "interface" // interface{}
	KindUnknown   Kind = "unknown"
)

var builtinKinds = map[string]Kind{
	"bool": KindBool, "string": KindString,
	"int": KindInt, "int8": KindInt, "int16": KindInt, "int32": KindInt, "int64": KindInt, "rune": KindInt,
	"uint": KindUint, "uint8": KindUint, "uint16": KindUint, "uint32": KindUint, "uint64": KindUint, "uintptr": KindUint, "byte": KindUint,
	"float32": KindFloat, "float64": KindFloat,
	"json.RawMessage": KindRaw, "interface {}": KindInterface, "any": KindInterface,
}

// Node is one path of the schema.
type Node struct {
	Path string // e.g. "WLANs[].Auth.AKM.WPA3_PSK"; "" for the root
	Name string // JSON key of a struct member; "[]" or "{}" for elements
	Type string // exact Go type, e.g. "*config.APState"
	Kind Kind
	// Pointee is the kind a pointer points to (KindPointer only).
	Pointee Kind
	// Fields are the struct members in declaration order (struct, or
	// pointer to struct).
	Fields []*Node
	// Elem is the element of a slice, array or map (or of the slice, array
	// or map a pointer points to).
	Elem *Node
	// ArrayLen is the length of an array type.
	ArrayLen int

	exact map[string]*Node
	fold  map[string]*Node
}

// ValueKind is the kind decoding works with: the pointee for a pointer.
func (n *Node) ValueKind() Kind {
	if n.Kind == KindPointer {
		return n.Pointee
	}
	return n.Kind
}

// Field finds the struct member a JSON key decodes into, the way
// encoding/json does: an exact match on the JSON name first, else a
// case-insensitive match (the first member in declaration order wins).
func (n *Node) Field(key string) (*Node, bool) {
	if field, ok := n.exact[key]; ok {
		return field, true
	}
	field, ok := n.fold[FoldKey(key)]
	return field, ok
}

// FoldKey returns a key such that FoldKey(a) == FoldKey(b) exactly when
// encoding/json matches a and b case-insensitively (Unicode simple folding,
// as bytes.EqualFold).
func FoldKey(key string) string {
	var b strings.Builder
	b.Grow(len(key))
	for _, r := range key {
		if r == utf8.RuneError {
			b.WriteRune(r)
			continue
		}
		min := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < min {
				min = f
			}
		}
		b.WriteRune(min)
	}
	return b.String()
}

// Schema is a parsed path list.
type Schema struct {
	root   *Node
	byPath map[string]*Node
	order  []string
}

// Root is the top-level struct (EPConfig).
func (s *Schema) Root() *Node { return s.root }

// Lookup returns the node of an exact path.
func (s *Schema) Lookup(path string) (*Node, bool) {
	node, ok := s.byPath[path]
	return node, ok
}

// Paths returns every path in file order.
func (s *Schema) Paths() []string { return append([]string(nil), s.order...) }

// Canonical folds every key of a dotted path ("wlans[].auth.akm.wpa3_psk")
// to its canonical spelling ("WLANs[].Auth.AKM.WPA3_PSK"). It reports false
// when a key is unknown to the schema.
func (s *Schema) Canonical(path string) (string, bool) {
	node := s.root
	var out []string
	for _, part := range splitPath(path) {
		switch part {
		case "[]":
			if node.Elem == nil || (node.ValueKind() != KindSlice && node.ValueKind() != KindArray) {
				return "", false
			}
			node = node.Elem
			out = append(out, "[]")
		case "{}":
			if node.Elem == nil || node.ValueKind() != KindMap {
				return "", false
			}
			node = node.Elem
			out = append(out, "{}")
		default:
			field, ok := node.Field(part)
			if !ok {
				return "", false
			}
			node = field
			out = append(out, field.Name)
		}
	}
	return joinPath(out), true
}

var (
	ap33Once   sync.Once
	ap33Schema *Schema
	ap33Err    error
)

// AP33 returns the embedded AP33 0.15 schema.
func AP33() *Schema {
	ap33Once.Do(func() { ap33Schema, ap33Err = Parse(strings.NewReader(ap33Paths)) })
	if ap33Err != nil {
		panic("firmware: embedded ap33-0.15.paths: " + ap33Err.Error())
	}
	return ap33Schema
}

// splitPath splits "WLANs[].Auth" into ["WLANs", "[]", "Auth"].
func splitPath(path string) []string {
	var parts []string
	for _, segment := range strings.Split(path, ".") {
		for {
			cut := strings.IndexAny(segment, "[{")
			if cut < 0 {
				break
			}
			if cut > 0 {
				parts = append(parts, segment[:cut])
			}
			parts = append(parts, segment[cut:cut+2])
			segment = segment[cut+2:]
		}
		if segment != "" {
			parts = append(parts, segment)
		}
	}
	return parts
}

func joinPath(parts []string) string {
	var b strings.Builder
	for i, part := range parts {
		if part != "[]" && part != "{}" && i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(part)
	}
	return b.String()
}

// parentAndName splits a path into its parent path and last segment.
func parentAndName(path string) (string, string) {
	if strings.HasSuffix(path, "[]") || strings.HasSuffix(path, "{}") {
		return path[:len(path)-2], path[len(path)-2:]
	}
	cut := strings.LastIndexByte(path, '.')
	if cut < 0 {
		return "", path
	}
	return path[:cut], path[cut+1:]
}

func elemType(typ string) (string, int) {
	switch {
	case strings.HasPrefix(typ, "*"):
		return typ[1:], 0
	case strings.HasPrefix(typ, "[]"):
		return typ[2:], 0
	case strings.HasPrefix(typ, "["):
		end := strings.IndexByte(typ, ']')
		length := 0
		fmt.Sscanf(typ[1:end], "%d", &length)
		return typ[end+1:], length
	}
	return "", 0
}

// kindOfType classifies a type string that has no line of its own (the
// pointee of a pointer).
func kindOfType(typ string, hasFields, hasElem bool) Kind {
	switch {
	case strings.HasPrefix(typ, "[]"):
		return KindSlice
	case strings.HasPrefix(typ, "["):
		return KindArray
	case strings.HasPrefix(typ, "map["):
		return KindMap
	case strings.HasPrefix(typ, "*"):
		return KindPointer
	}
	if kind, ok := builtinKinds[typ]; ok {
		return kind
	}
	if hasFields || strings.HasPrefix(typ, "struct") {
		return KindStruct
	}
	if hasElem {
		return KindUnknown
	}
	return KindNamed
}

// Parse reads a path list.
func Parse(r io.Reader) (*Schema, error) {
	schema := &Schema{root: &Node{Kind: KindStruct, Type: "config.EPConfig"}, byPath: map[string]*Node{}}
	schema.byPath[""] = schema.root
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return nil, fmt.Errorf("line %d: want path, type and kind", lineNo)
		}
		path, typ, kind := fields[0], fields[1], Kind(fields[2])
		if _, dup := schema.byPath[path]; dup {
			return nil, fmt.Errorf("line %d: duplicate path %s", lineNo, path)
		}
		parentPath, name := parentAndName(path)
		parent, ok := schema.byPath[parentPath]
		if !ok {
			return nil, fmt.Errorf("line %d: parent of %s not declared first", lineNo, path)
		}
		node := &Node{Path: path, Name: name, Type: typ, Kind: kind}
		if kind == KindArray {
			_, node.ArrayLen = elemType(typ)
		}
		if name == "[]" || name == "{}" {
			parent.Elem = node
		} else {
			if parent.exact == nil {
				parent.exact, parent.fold = map[string]*Node{}, map[string]*Node{}
			}
			parent.Fields = append(parent.Fields, node)
			parent.exact[name] = node
			if _, taken := parent.fold[FoldKey(name)]; !taken {
				parent.fold[FoldKey(name)] = node
			}
		}
		schema.byPath[path] = node
		schema.order = append(schema.order, path)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for _, node := range schema.byPath {
		if node.Kind == KindPointer {
			pointee, _ := elemType(node.Type)
			node.Pointee = kindOfType(pointee, len(node.Fields) > 0, node.Elem != nil)
			if node.Pointee == KindArray {
				_, node.ArrayLen = elemType(pointee)
			}
		}
	}
	return schema, nil
}

// SortedPaths returns the schema paths sorted, for stable listings.
func (s *Schema) SortedPaths() []string {
	paths := s.Paths()
	sort.Strings(paths)
	return paths
}
