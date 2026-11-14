package effective

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/nicko170/dismist/internal/options/firmware"
)

// decoder emulates encoding/json decoding the controller's config into a
// freshly zeroed EPConfig (rules 1 and 2): keys are matched to canonical
// field names (exact, then case-insensitive), unknown keys are dropped and
// reported, and duplicate variants of one key are applied in document order
// with Go's semantics (a later object merges into a struct, a later array
// decodes element-wise into the existing slice and truncates it, a later
// scalar replaces, null is a no-op on value kinds and resets pointers, maps
// and slices).
type decoder struct {
	report *Report
}

// unchanged is returned for a null on a value kind: the decode leaves the
// field as it was.
var unchanged = &value{kind: '-'}

func bitSize(goType string) int {
	for _, size := range []string{"8", "16", "32", "64"} {
		if strings.HasSuffix(goType, size) {
			n, _ := strconv.Atoi(size)
			return n
		}
	}
	switch goType {
	case "byte":
		return 8
	case "rune":
		return 32
	}
	return 64
}

func (d *decoder) typeError(path string, j *jnode, node *firmware.Node) *value {
	d.report.TypeErrors = append(d.report.TypeErrors, fmt.Sprintf("%s: %s is not a valid %s", displayPath(path), describe(j), node.Type))
	return &value{kind: 'r', raw: j.raw}
}

func describe(j *jnode) string {
	switch j.kind {
	case '{':
		return "an object"
	case '[':
		return "an array"
	case '"':
		return "a string"
	case 'n':
		return "number " + j.text
	case 't', 'f':
		return "a bool"
	}
	return "null"
}

func displayPath(path string) string {
	if path == "" {
		return "(root)"
	}
	return path
}

func childPath(parent, name string) string {
	if parent == "" {
		return name
	}
	if name == "[]" || name == "{}" {
		return parent + name
	}
	return parent + "." + name
}

// decode decodes j into the field described by node, starting from the
// field's current value (nil = zero / absent).
func (d *decoder) decode(j *jnode, node *firmware.Node, existing *value, path string) *value {
	if node.Kind == firmware.KindPointer {
		if j.kind == '0' {
			return &value{kind: '0'}
		}
		if existing != nil && existing.kind == '0' {
			existing = nil
		}
		return d.decodeKind(j, node, node.Pointee, existing, path)
	}
	return d.decodeKind(j, node, node.Kind, existing, path)
}

func (d *decoder) decodeKind(j *jnode, node *firmware.Node, kind firmware.Kind, existing *value, path string) *value {
	switch kind {
	case firmware.KindStruct:
		if j.kind == '0' {
			return unchanged
		}
		if j.kind != '{' {
			return d.typeError(path, j, node)
		}
		result := existing
		if result == nil || result.kind != 'o' {
			result = newObject()
		}
		for _, member := range j.members {
			field, ok := node.Field(member.key)
			if !ok {
				d.report.Ignored = append(d.report.Ignored, childPath(path, member.key))
				continue
			}
			decoded := d.decode(member.value, field, result.obj[field.Name], childPath(path, field.Name))
			if decoded != unchanged {
				result.obj[field.Name] = decoded
			}
		}
		return result
	case firmware.KindSlice, firmware.KindArray:
		if j.kind == '0' {
			if kind == firmware.KindArray {
				return unchanged
			}
			return &value{kind: '0'}
		}
		if j.kind != '[' || node.Elem == nil {
			return d.typeError(path, j, node)
		}
		var previous []*value
		if existing != nil && existing.kind == 'a' {
			previous = existing.arr
		}
		elems := j.elems
		if kind == firmware.KindArray && node.ArrayLen > 0 && len(elems) > node.ArrayLen {
			elems = elems[:node.ArrayLen]
		}
		result := &value{kind: 'a', arr: make([]*value, 0, len(elems))}
		for i, elem := range elems {
			var current *value
			if i < len(previous) {
				current = previous[i]
			}
			decoded := d.decode(elem, node.Elem, current, childPath(path, "[]"))
			if decoded == unchanged {
				decoded = current
				if decoded == nil {
					decoded = zeroOf(node.Elem)
				}
			}
			result.arr = append(result.arr, decoded)
		}
		return result
	case firmware.KindMap:
		if j.kind == '0' {
			return &value{kind: '0'}
		}
		if j.kind != '{' || node.Elem == nil {
			return d.typeError(path, j, node)
		}
		result := existing
		if result == nil || result.kind != 'o' {
			result = newObject()
		}
		for _, member := range j.members {
			// Map values are decoded into a fresh zero value each time.
			decoded := d.decode(member.value, node.Elem, nil, childPath(path, "{}"))
			if decoded == unchanged {
				decoded = zeroOf(node.Elem)
			}
			result.obj[member.key] = decoded
		}
		return result
	case firmware.KindBool:
		switch j.kind {
		case '0':
			return unchanged
		case 't', 'f':
			return boolValue(j.kind == 't')
		}
		return d.typeError(path, j, node)
	case firmware.KindInt, firmware.KindUint:
		if j.kind == '0' {
			return unchanged
		}
		if j.kind != 'n' {
			return d.typeError(path, j, node)
		}
		goType := strings.TrimPrefix(node.Type, "*")
		if kind == firmware.KindInt {
			n, err := strconv.ParseInt(j.text, 10, bitSize(goType))
			if err != nil {
				return d.typeError(path, j, node)
			}
			return intValue(n)
		}
		n, err := strconv.ParseUint(j.text, 10, bitSize(goType))
		if err != nil {
			return d.typeError(path, j, node)
		}
		return &value{kind: 'n', num: strconv.FormatUint(n, 10)}
	case firmware.KindFloat:
		if j.kind == '0' {
			return unchanged
		}
		if j.kind != 'n' {
			return d.typeError(path, j, node)
		}
		size := bitSize(strings.TrimPrefix(node.Type, "*"))
		f, err := strconv.ParseFloat(j.text, size)
		if err != nil || math.IsInf(f, 0) {
			return d.typeError(path, j, node)
		}
		return &value{kind: 'n', num: strconv.FormatFloat(f, 'g', -1, size)}
	case firmware.KindString:
		switch j.kind {
		case '0':
			return unchanged
		case '"':
			return strValue(j.text)
		}
		return d.typeError(path, j, node)
	default:
		// custom decoders, named types of unknown kind, json.RawMessage,
		// interface{}: kept verbatim. When in doubt, do not normalise.
		return &value{kind: 'r', raw: j.raw}
	}
}

// zeroOf is the zero value of an element whose JSON was null.
func zeroOf(node *firmware.Node) *value {
	switch node.Kind {
	case firmware.KindStruct:
		return newObject()
	case firmware.KindBool:
		return boolValue(false)
	case firmware.KindInt, firmware.KindUint, firmware.KindFloat:
		return &value{kind: 'n', num: "0"}
	case firmware.KindString:
		return strValue("")
	}
	return &value{kind: '0'}
}
