package effective

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
)

// jnode is a parsed JSON value that keeps object members in document order,
// duplicates included (encoding/json processes them in that order).
type jnode struct {
	kind    byte // '{', '[', '"', 'n' (number), 't', 'f', '0' (null)
	members []jmember
	elems   []*jnode
	text    string // string value, or number literal
	raw     []byte // the value's bytes, compacted
}

type jmember struct {
	key   string
	value *jnode
}

type parser struct {
	data []byte
	pos  int
}

func parseJSON(data []byte) (*jnode, error) {
	if !json.Valid(data) {
		return nil, errors.New("config is not valid JSON")
	}
	p := &parser{data: data}
	node, err := p.value()
	if err != nil {
		return nil, err
	}
	return node, nil
}

func (p *parser) skip() {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *parser) value() (*jnode, error) {
	p.skip()
	if p.pos >= len(p.data) {
		return nil, errors.New("unexpected end of JSON")
	}
	start := p.pos
	var node *jnode
	switch c := p.data[p.pos]; {
	case c == '{':
		node = &jnode{kind: '{'}
		p.pos++
		p.skip()
		if p.data[p.pos] == '}' {
			p.pos++
			break
		}
		for {
			p.skip()
			key, err := p.str()
			if err != nil {
				return nil, err
			}
			p.skip()
			p.pos++ // ':'
			value, err := p.value()
			if err != nil {
				return nil, err
			}
			node.members = append(node.members, jmember{key: key, value: value})
			p.skip()
			if p.data[p.pos] == ',' {
				p.pos++
				continue
			}
			p.pos++ // '}'
			break
		}
	case c == '[':
		node = &jnode{kind: '['}
		p.pos++
		p.skip()
		if p.data[p.pos] == ']' {
			p.pos++
			break
		}
		for {
			value, err := p.value()
			if err != nil {
				return nil, err
			}
			node.elems = append(node.elems, value)
			p.skip()
			if p.data[p.pos] == ',' {
				p.pos++
				continue
			}
			p.pos++ // ']'
			break
		}
	case c == '"':
		text, err := p.str()
		if err != nil {
			return nil, err
		}
		node = &jnode{kind: '"', text: text}
	case c == 't':
		p.pos += 4
		node = &jnode{kind: 't'}
	case c == 'f':
		p.pos += 5
		node = &jnode{kind: 'f'}
	case c == 'n':
		p.pos += 4
		node = &jnode{kind: '0'}
	default:
		for p.pos < len(p.data) && bytes.IndexByte([]byte("+-0123456789.eE"), p.data[p.pos]) >= 0 {
			p.pos++
		}
		node = &jnode{kind: 'n', text: string(p.data[start:p.pos])}
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, p.data[start:p.pos]); err != nil {
		return nil, err
	}
	node.raw = compacted.Bytes()
	return node, nil
}

// str reads a JSON string at the current position.
func (p *parser) str() (string, error) {
	start := p.pos
	p.pos++
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case '\\':
			p.pos += 2
			continue
		case '"':
			p.pos++
			var text string
			if err := json.Unmarshal(p.data[start:p.pos], &text); err != nil {
				return "", err
			}
			return text, nil
		}
		p.pos++
	}
	return "", errors.New("unterminated string")
}

// value is a canonical value: what ep-control holds after decoding, with
// presence kept (a member that is absent is not in the object).
type value struct {
	kind byte // 'o' object, 'a' array, 's' string, 'n' number, 'b' bool, '0' null, 'r' raw (opaque, verbatim)
	obj  map[string]*value
	arr  []*value
	str  string
	num  string // canonical number literal
	b    bool
	raw  []byte
}

func newObject() *value { return &value{kind: 'o', obj: map[string]*value{}} }

func strValue(s string) *value { return &value{kind: 's', str: s} }

func intValue(n int64) *value { return &value{kind: 'n', num: strconv.FormatInt(n, 10)} }

func boolValue(b bool) *value { return &value{kind: 'b', b: b} }

// get walks object members by canonical key.
func (v *value) get(keys ...string) *value {
	current := v
	for _, key := range keys {
		if current == nil || current.kind != 'o' {
			return nil
		}
		current = current.obj[key]
	}
	return current
}

// ensure returns the object at keys, creating empty objects on the way.
func (v *value) ensure(keys ...string) *value {
	current := v
	for _, key := range keys {
		next := current.obj[key]
		if next == nil || next.kind != 'o' {
			next = newObject()
			current.obj[key] = next
		}
		current = next
	}
	return current
}

func (v *value) set(child *value, keys ...string) {
	parent := v.ensure(keys[:len(keys)-1]...)
	parent.obj[keys[len(keys)-1]] = child
}

func (v *value) del(keys ...string) {
	parent := v.get(keys[:len(keys)-1]...)
	if parent != nil && parent.kind == 'o' {
		delete(parent.obj, keys[len(keys)-1])
	}
}

// boolAt reads a bool member; absent (or zero, removed by rule 2a) is false.
func (v *value) boolAt(keys ...string) bool {
	child := v.get(keys...)
	return child != nil && child.kind == 'b' && child.b
}

// stringAt reads a string member, or the decoded string of an opaque value
// whose JSON form is a string (custom types such as wifi.Band).
func (v *value) stringAt(keys ...string) (string, bool) {
	child := v.get(keys...)
	if child == nil {
		return "", true // absent: zero
	}
	return child.asString()
}

func (v *value) asString() (string, bool) {
	switch v.kind {
	case 's':
		return v.str, true
	case 'r':
		var text string
		if err := json.Unmarshal(v.raw, &text); err == nil {
			return text, true
		}
	}
	return "", false
}

// intAt reads an integer member or an opaque value whose JSON form is an
// integer (wifi.ChannelNumber, wifi.Width). Absent is 0.
func (v *value) intAt(keys ...string) (int64, bool) {
	child := v.get(keys...)
	if child == nil {
		return 0, true
	}
	return child.asInt()
}

func (v *value) asInt() (int64, bool) {
	literal := v.num
	if v.kind == 'r' {
		literal = string(v.raw)
	} else if v.kind != 'n' {
		return 0, false
	}
	n, err := strconv.ParseInt(literal, 10, 64)
	return n, err == nil
}

// marshal writes v with sorted keys and no whitespace (rule 8).
func (v *value) marshal(buf *bytes.Buffer) {
	switch v.kind {
	case 'o':
		keys := make([]string, 0, len(v.obj))
		for key := range v.obj {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			encoded, _ := json.Marshal(key)
			buf.Write(encoded)
			buf.WriteByte(':')
			v.obj[key].marshal(buf)
		}
		buf.WriteByte('}')
	case 'a':
		buf.WriteByte('[')
		for i, elem := range v.arr {
			if i > 0 {
				buf.WriteByte(',')
			}
			elem.marshal(buf)
		}
		buf.WriteByte(']')
	case 's':
		encoded, _ := json.Marshal(v.str)
		buf.Write(encoded)
	case 'n':
		buf.WriteString(v.num)
	case 'b':
		if v.b {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case '0':
		buf.WriteString("null")
	case 'r':
		buf.Write(v.raw)
	default:
		panic(fmt.Sprintf("effective: unknown value kind %q", v.kind))
	}
}
