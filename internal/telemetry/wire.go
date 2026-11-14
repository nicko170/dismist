package telemetry

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"net"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Wire types.
const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

// field is one protobuf field as it appeared on the wire. value holds a
// varint or a fixed32/fixed64 payload; data holds length-delimited bytes.
type field struct {
	number uint64
	wire   uint8
	value  uint64
	data   []byte
}

// scan splits one protobuf message into its fields, in wire order. Unknown
// field numbers are kept; a truncated field, a field number 0 or a group
// wire type makes the whole message malformed.
func scan(data []byte) ([]field, error) {
	fields := make([]field, 0, 16)
	for len(data) > 0 {
		key, n := binary.Uvarint(data)
		if n <= 0 || key>>3 == 0 || key>>3 > math.MaxInt32 {
			return nil, ErrMalformed
		}
		data = data[n:]
		item := field{number: key >> 3, wire: uint8(key & 7)}
		switch item.wire {
		case wireVarint:
			value, n := binary.Uvarint(data)
			if n <= 0 {
				return nil, ErrMalformed
			}
			item.value, data = value, data[n:]
		case wireFixed64:
			if len(data) < 8 {
				return nil, ErrMalformed
			}
			item.value, item.data, data = binary.LittleEndian.Uint64(data), data[:8], data[8:]
		case wireBytes:
			length, n := binary.Uvarint(data)
			if n <= 0 || length > uint64(len(data)-n) {
				return nil, ErrMalformed
			}
			item.data = data[n : n+int(length)]
			data = data[n+int(length):]
		case wireFixed32:
			if len(data) < 4 {
				return nil, ErrMalformed
			}
			item.value, item.data, data = uint64(binary.LittleEndian.Uint32(data)), data[:4], data[4:]
		default:
			return nil, ErrMalformed
		}
		fields = append(fields, item)
	}
	return fields, nil
}

// reader reads typed values out of fields for one section. A field whose
// wire type does not match its declared type is ignored (zero) and counted
// as a decode error of the section; the rest of the message still decodes.
type reader struct {
	errors  map[string]int
	section string
}

func newReader(errors map[string]int, section string) reader {
	return reader{errors: errors, section: section}
}

func (r reader) fail() {
	if r.errors != nil {
		r.errors[r.section]++
	}
}

// in returns a reader that counts errors under another section.
func (r reader) in(section string) reader { return reader{errors: r.errors, section: section} }

// message scans a nested message; a mistyped or malformed one is counted
// and returns nil, false.
func (r reader) message(f field) ([]field, bool) {
	if f.wire != wireBytes {
		r.fail()
		return nil, false
	}
	fields, err := scan(f.data)
	if err != nil {
		r.fail()
		return nil, false
	}
	return fields, true
}

// uint reads a varint (uint32, uint64, bool, enum, non-negative int).
func (r reader) uint(f field) uint64 {
	if f.wire != wireVarint {
		r.fail()
		return 0
	}
	return f.value
}

func (r reader) int(f field) int {
	value := r.uint(f)
	if value > math.MaxInt32 && int64(value) > 0 {
		return math.MaxInt32
	}
	return int(int64(value))
}

// int64 reads a varint as a two's-complement int64 (int32/int64 fields).
func (r reader) int64(f field) int64 { return int64(r.uint(f)) }

func (r reader) bool(f field) bool { return r.uint(f) != 0 }

// zigzag reads a sint32/sint64 field (RSSI, noise floor, power, budget).
func (r reader) zigzag(f field) int64 { return zigzag(r.uint(f)) }

func zigzag(value uint64) int64 {
	return int64(value>>1) ^ -int64(value&1)
}

// float32 reads a fixed32 float.
func (r reader) float32(f field) float64 {
	if f.wire != wireFixed32 {
		r.fail()
		return 0
	}
	return finite(float64(math.Float32frombits(uint32(f.value))))
}

// float64 reads a fixed64 double.
func (r reader) float64(f field) float64 {
	if f.wire != wireFixed64 {
		r.fail()
		return 0
	}
	return finite(math.Float64frombits(f.value))
}

func finite(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

// bytes reads a length-delimited field.
func (r reader) bytes(f field) []byte {
	if f.wire != wireBytes {
		r.fail()
		return nil
	}
	return f.data
}

// str reads a string field bounded to limit bytes, with control and
// invalid characters removed. AP strings reach the UI and logs.
func (r reader) str(f field, limit int) string {
	return cleanString(r.bytes(f), limit)
}

func cleanString(raw []byte, limit int) string {
	if len(raw) == 0 {
		return ""
	}
	var builder strings.Builder
	for len(raw) > 0 && builder.Len() < limit {
		char, size := utf8.DecodeRune(raw)
		raw = raw[size:]
		if char == utf8.RuneError || unicode.IsControl(char) {
			continue
		}
		if builder.Len()+utf8.RuneLen(char) > limit {
			break
		}
		builder.WriteRune(char)
	}
	return builder.String()
}

// varints reads a repeated varint field, packed (wire 2) or not (wire 0).
func (r reader) varints(f field) []uint64 {
	switch f.wire {
	case wireVarint:
		return []uint64{f.value}
	case wireBytes:
		data := f.data
		values := make([]uint64, 0, len(data))
		for len(data) > 0 {
			value, n := binary.Uvarint(data)
			if n <= 0 {
				r.fail()
				return values
			}
			values = append(values, value)
			data = data[n:]
		}
		return values
	}
	r.fail()
	return nil
}

// floats32 reads a repeated float field, packed (wire 2) or not (wire 5).
func (r reader) floats32(f field) []float64 {
	switch f.wire {
	case wireFixed32:
		return []float64{r.float32(f)}
	case wireBytes:
		if len(f.data)%4 != 0 {
			r.fail()
			return nil
		}
		values := make([]float64, 0, len(f.data)/4)
		for index := 0; index+4 <= len(f.data); index += 4 {
			values = append(values, finite(float64(math.Float32frombits(binary.LittleEndian.Uint32(f.data[index:])))))
		}
		return values
	}
	r.fail()
	return nil
}

// mac reads an ethernet.MAC (6 raw bytes) as "02:aa:…"; anything else is
// counted and returns "".
func (r reader) mac(f field) string {
	raw := r.bytes(f)
	if len(raw) == 0 {
		return ""
	}
	if len(raw) != 6 {
		r.fail()
		return ""
	}
	return net.HardwareAddr(raw).String()
}

// uuid reads a uuid.UUID (16 raw bytes) in its canonical text form. A
// text UUID (36 bytes) is accepted too.
func (r reader) uuid(f field) string {
	raw := r.bytes(f)
	switch len(raw) {
	case 0:
		return ""
	case 16:
		text := hex.EncodeToString(raw)
		return text[0:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:]
	case 36:
		if text := strings.ToLower(string(raw)); validUUIDText(text) {
			return text
		}
	}
	r.fail()
	return ""
}

func validUUIDText(text string) bool {
	if len(text) != 36 {
		return false
	}
	for index, char := range text {
		switch index {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdef", char) {
				return false
			}
		}
	}
	return true
}

// addr reads an ip.Addr4 or ip.Addr6 (4 or 16 raw bytes). An all-zero
// address is "unset" and returns "".
func (r reader) addr(f field) string {
	raw := r.bytes(f)
	if len(raw) == 0 {
		return ""
	}
	address, ok := addrFrom(raw)
	if !ok {
		r.fail()
		return ""
	}
	if address.IsUnspecified() {
		return ""
	}
	return address.String()
}

func addrFrom(raw []byte) (netip.Addr, bool) {
	switch len(raw) {
	case 4:
		return netip.AddrFrom4([4]byte(raw)), true
	case 16:
		return netip.AddrFrom16([16]byte(raw)).Unmap(), true
	}
	return netip.Addr{}, false
}

// addrs reads an ip.Addr6s: one or more 16-byte (or 4-byte) addresses
// concatenated in one bytes field.
func (r reader) addrs(f field) []string {
	raw := r.bytes(f)
	if len(raw) == 0 {
		return nil
	}
	size := 16
	if len(raw)%16 != 0 {
		if len(raw)%4 != 0 {
			r.fail()
			return nil
		}
		size = 4
	}
	var result []string
	for ; len(raw) >= size; raw = raw[size:] {
		if address, ok := addrFrom(raw[:size]); ok && !address.IsUnspecified() {
			result = append(result, address.String())
		}
	}
	return result
}

// addrMask reads an ip.AddrMask4/6 or ip.Subnet4/6: {2 prefix length,
// 3 address}. It returns the address and the prefix length (-1 if absent).
func (r reader) addrMask(f field) (string, int) {
	fields, ok := r.message(f)
	if !ok {
		return "", -1
	}
	address, prefix := "", -1
	for _, item := range fields {
		switch item.number {
		case 2:
			prefix = int(min(r.uint(item), 128))
		case 3:
			address = r.addr(item)
		}
	}
	return address, prefix
}

// goZeroSeconds is a zero Go time.Time in Unix seconds, which the AP sends
// for "never".
const goZeroSeconds = -62135596800

// time reads a time.Time as protobuf3 encodes it: {1 Unix seconds (int64),
// 2 nanoseconds}. Zero and the Go zero time read as the zero Time.
func (r reader) time(f field) time.Time {
	fields, ok := r.message(f)
	if !ok {
		return time.Time{}
	}
	var seconds, nanos int64
	for _, item := range fields {
		switch item.number {
		case 1:
			seconds = r.int64(item)
		case 2:
			nanos = r.int64(item)
		}
	}
	if (seconds == 0 && nanos == 0) || seconds <= goZeroSeconds || nanos < 0 || nanos >= 1e9 {
		return time.Time{}
	}
	return time.Unix(seconds, nanos).UTC()
}

// radioIndex returns N for a radio device "rN", or -1.
func radioIndex(dev string) int {
	if len(dev) < 2 || dev[0] != 'r' {
		return -1
	}
	value := 0
	for _, char := range dev[1:] {
		if char < '0' || char > '9' || value > 1000 {
			return -1
		}
		value = value*10 + int(char-'0')
	}
	return value
}
