package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// The paths subcommand turns a gorewalk dump of the AP's EPConfig type (the
// "type X ()" blocks of every struct reachable from config.EPConfig) into
// internal/options/firmware/ap33-0.15.paths: one line per JSON path with its
// exact Go type and kind. It is run once by hand against the private
// research dump; the output contains field names and types only.

// customDecoders are the 0.15 types with their own UnmarshalJSON or
// UnmarshalText (from the ep-control function list). Their JSON form is not
// the default one, so the normaliser treats them as opaque.
var customDecoders = map[string]bool{
	"ethernet.OUI": true, "ethernet.MAC": true,
	"ip.Addr6": true, "ip.Addr4": true, "ip.AddrMask4": true, "ip.Subnet4": true, "ip.Mask": true,
	"ip.AddrMask6": true, "ip.Subnet6": true, "ip.AddrPort6": true,
	"uuid.UUID": true, "uuid.TimeUUID": true, "pmtu.PMTUConfig": true,
	"wifi.PSK": true, "wifi.Rate": true, "wifi.MCSRateMask": true, "wifi.Channel": true, "wifi.Width": true,
	"wifi.ChannelSet": true, "wifi.Band": true, "wifi.RSSI": true, "wifi.Frequency": true,
	"l2tp.CCState": true, "l2tp.SesState": true, "l2tp.PseudowireType": true,
	"uptime.Uptime": true, "uptime.Duration": true,
	"hotspot.NtwAuthEnum": true, "hotspot.PortStatEnum": true, "hotspot.IPv4Enum": true, "hotspot.IPv6Enum": true,
	"mesh_mode.MeshMode":    true,
	"proximity.FtmModeEnum": true, "proximity.ExpectedToMoveEnum": true, "proximity.AltTypeEnum": true, "proximity.DatumEnum": true,
	"config.WLANAuthType": true, "config.EapolPriorityType": true, "config.WidsAlarm": true,
	"time.Time": true,
}

var builtinKinds = map[string]string{
	"bool": "bool", "string": "string",
	"int": "int", "int8": "int", "int16": "int", "int32": "int", "int64": "int", "rune": "int",
	"uint": "uint", "uint8": "uint", "uint16": "uint", "uint32": "uint", "uint64": "uint", "uintptr": "uint", "byte": "uint",
	"float32": "float", "float64": "float",
	"json.RawMessage": "raw", "interface {}": "interface", "any": "interface",
}

type walkField struct {
	name, typ, tag string
}

type pathLine struct {
	path, typ, kind string
}

var tagPattern = regexp.MustCompile(`^(?:[A-Za-z_]+:"(?:[^"\\]|\\.)*"\s*)+$`)

// splitTypeAndTag splits "<type> <tag>" where the type may contain spaces
// and quoted tags inside struct braces.
func splitTypeAndTag(rest string) (typ, tag string) {
	depth, inQuote := 0, false
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		switch {
		case inQuote:
			if c == '\\' {
				i++
			} else if c == '"' {
				inQuote = false
			}
		case c == '"':
			inQuote = true
		case c == '{':
			depth++
		case c == '}':
			depth--
		case c == ' ' && depth == 0:
			tail := strings.TrimSpace(rest[i:])
			head := strings.TrimSpace(rest[:i])
			if strings.HasPrefix(tail, "{") || tail == "" {
				continue // "struct {" or "interface {"
			}
			if tagPattern.MatchString(tail) {
				return head, tail
			}
		}
	}
	return strings.TrimSpace(rest), ""
}

func parseWalk(r io.Reader) (map[string][]walkField, error) {
	types := map[string][]walkField{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	current := ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "type ") {
			header := strings.TrimPrefix(line, "type ")
			cut := strings.LastIndex(header, " (")
			if cut < 0 {
				return nil, fmt.Errorf("bad type header %q", line)
			}
			current = strings.TrimSpace(header[:cut])
			if _, seen := types[current]; !seen {
				types[current] = []walkField{}
			}
			continue
		}
		if !strings.HasPrefix(line, "  ") || current == "" {
			continue
		}
		body := strings.TrimPrefix(line, "  ")
		cut := strings.IndexByte(body, ' ')
		if cut < 0 {
			return nil, fmt.Errorf("bad field line %q", line)
		}
		typ, tag := splitTypeAndTag(strings.TrimSpace(body[cut:]))
		types[current] = append(types[current], walkField{name: body[:cut], typ: typ, tag: tag})
	}
	return types, scanner.Err()
}

// jsonName returns the JSON key of a field and whether encoding/json
// decodes it at all.
func jsonName(field walkField) (string, bool) {
	if field.name == "" || !unicode.IsUpper(rune(field.name[0])) {
		return "", false // unexported: never decoded
	}
	for _, part := range strings.Fields(field.tag) {
		if !strings.HasPrefix(part, `json:"`) {
			continue
		}
		value := strings.TrimSuffix(strings.TrimPrefix(part, `json:"`), `"`)
		name, _, _ := strings.Cut(value, ",")
		if name == "-" && !strings.Contains(value, ",") {
			return "", false
		}
		if name != "" {
			return name, true
		}
	}
	return field.name, true
}

type walker struct {
	types map[string][]walkField
	lines []pathLine
	stack map[string]bool
}

func (w *walker) kind(typ string) string {
	switch {
	case strings.HasPrefix(typ, "*"):
		return "pointer"
	case strings.HasPrefix(typ, "[]"):
		return "slice"
	case strings.HasPrefix(typ, "["):
		return "array"
	case strings.HasPrefix(typ, "map["):
		return "map"
	case customDecoders[typ]:
		return "custom"
	case builtinKinds[typ] != "":
		return builtinKinds[typ]
	case strings.HasPrefix(typ, "struct {") || strings.HasPrefix(typ, "struct{"):
		return "struct"
	}
	if _, ok := w.types[typ]; ok {
		return "struct"
	}
	if strings.Contains(typ, ".") {
		return "named"
	}
	return "unknown"
}

// elem returns the element type of a pointer, slice, array or map type.
func elem(typ string) string {
	switch {
	case strings.HasPrefix(typ, "*"):
		return typ[1:]
	case strings.HasPrefix(typ, "[]"):
		return typ[2:]
	case strings.HasPrefix(typ, "["):
		return typ[strings.IndexByte(typ, ']')+1:]
	case strings.HasPrefix(typ, "map["):
		depth := 0
		for i := 3; i < len(typ); i++ {
			switch typ[i] {
			case '[':
				depth++
			case ']':
				depth--
				if depth == 0 {
					return typ[i+1:]
				}
			}
		}
	}
	return ""
}

// walk emits the children of a value of type typ found at path.
func (w *walker) walk(path, typ string, depth int) error {
	if depth > 24 {
		return fmt.Errorf("type nesting too deep at %s", path)
	}
	switch w.kind(typ) {
	case "pointer":
		return w.walk(path, elem(typ), depth+1)
	case "slice", "array":
		return w.walkElem(path+"[]", elem(typ), depth)
	case "map":
		return w.walkElem(path+"{}", elem(typ), depth)
	case "struct":
		if w.stack[typ] {
			return nil // recursive type: stop
		}
		fields, ok := w.types[typ]
		if !ok {
			return fmt.Errorf("struct type %q at %s missing from the walk", typ, path)
		}
		w.stack[typ] = true
		defer delete(w.stack, typ)
		for _, field := range fields {
			name, ok := jsonName(field)
			if !ok {
				continue
			}
			child := name
			if path != "" {
				child = path + "." + name
			}
			w.lines = append(w.lines, pathLine{path: child, typ: field.typ, kind: w.kind(field.typ)})
			if err := w.walk(child, field.typ, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// walkElem emits an element node (path ending in [] or {}) and its children.
func (w *walker) walkElem(path, typ string, depth int) error {
	w.lines = append(w.lines, pathLine{path: path, typ: typ, kind: w.kind(typ)})
	return w.walk(path, typ, depth+1)
}

func generatePaths(in io.Reader, out io.Writer, root string) error {
	types, err := parseWalk(in)
	if err != nil {
		return err
	}
	if _, ok := types[root]; !ok {
		return fmt.Errorf("root type %s not in the walk", root)
	}
	w := &walker{types: types, stack: map[string]bool{}}
	if err := w.walk("", root, 0); err != nil {
		return err
	}
	if len(w.lines) == 0 {
		return errors.New("no paths")
	}
	seen := map[string]bool{}
	fmt.Fprintf(out, "# AP33 apfw-0.15 config paths: every JSON path ep-control decodes from the\n")
	fmt.Fprintf(out, "# controller's config (%s), with its exact Go type and kind.\n", root)
	fmt.Fprintf(out, "# Generated once by `dismist-gen paths` from the 0.15 type walk; field\n")
	fmt.Fprintf(out, "# names and types only. Format: path<TAB>go type<TAB>kind.\n")
	fmt.Fprintf(out, "# [] is a slice or array element, {} a map value. Kinds: bool int uint\n")
	fmt.Fprintf(out, "# float string struct slice array map pointer custom (own JSON decoder)\n")
	fmt.Fprintf(out, "# named (underlying kind unknown) raw interface.\n")
	for _, line := range w.lines {
		if seen[line.path] {
			return fmt.Errorf("duplicate path %s", line.path)
		}
		seen[line.path] = true
		if _, err := fmt.Fprintf(out, "%s\t%s\t%s\n", line.path, line.typ, line.kind); err != nil {
			return err
		}
	}
	return nil
}

// sortedKeys is used by tests.
func sortedKeys(m map[string][]walkField) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
