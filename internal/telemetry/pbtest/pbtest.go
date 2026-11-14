// Package pbtest encodes synthetic protobuf telemetry for tests and
// tools/fakeap (design §5.1). The repository never contains captured
// frames; every test frame is built here from field numbers taken from the
// AP's 0.15 pace struct tags.
//
// A Message is built field by field and nests by value:
//
//	radio := pbtest.New().String(1, "r0").Varint(2, 1).Varint(3, 36)
//	stats := pbtest.New().Varint(18, 600).Message(31, radio)
//	frame := pbtest.Frame("ap-stats-full-", "02-aa-00-00-00-01", stats)
package pbtest

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"math"
)

// Wire types.
const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

// Message is an encoded protobuf message under construction. The zero
// value is an empty message; every method appends one field and returns
// the message for chaining.
type Message struct{ buf []byte }

// New returns an empty message.
func New() *Message { return &Message{} }

// Bytes returns the encoded message.
func (m *Message) Bytes() []byte {
	if m == nil {
		return nil
	}
	return append([]byte(nil), m.buf...)
}

// Len is the encoded length.
func (m *Message) Len() int {
	if m == nil {
		return 0
	}
	return len(m.buf)
}

func (m *Message) key(number uint64, wire uint64) *Message {
	m.buf = binary.AppendUvarint(m.buf, number<<3|wire)
	return m
}

// Varint appends a varint field (uint32/uint64/int32 >= 0/bool/enum).
func (m *Message) Varint(number, value uint64) *Message {
	m.key(number, wireVarint)
	m.buf = binary.AppendUvarint(m.buf, value)
	return m
}

// Int appends a signed varint field the way Go's protobuf encodes int32 and
// int64 (two's complement, so a negative value takes 10 bytes).
func (m *Message) Int(number uint64, value int64) *Message {
	return m.Varint(number, uint64(value))
}

// Bool appends a varint 0 or 1.
func (m *Message) Bool(number uint64, value bool) *Message {
	if value {
		return m.Varint(number, 1)
	}
	return m.Varint(number, 0)
}

// Zigzag appends a zigzag32/zigzag64 field (RSSI, noise floor, power).
func (m *Message) Zigzag(number uint64, value int64) *Message {
	return m.Varint(number, ZigzagEncode(value))
}

// Fixed64 appends a fixed64 field.
func (m *Message) Fixed64(number, value uint64) *Message {
	m.key(number, wireFixed64)
	m.buf = binary.LittleEndian.AppendUint64(m.buf, value)
	return m
}

// Double appends a float64 as fixed64 (ConfigTimestamp, Interval).
func (m *Message) Double(number uint64, value float64) *Message {
	return m.Fixed64(number, math.Float64bits(value))
}

// Fixed32 appends a fixed32 field.
func (m *Message) Fixed32(number uint64, value uint32) *Message {
	m.key(number, wireFixed32)
	m.buf = binary.LittleEndian.AppendUint32(m.buf, value)
	return m
}

// Float appends a float32 as fixed32.
func (m *Message) Float(number uint64, value float32) *Message {
	return m.Fixed32(number, math.Float32bits(value))
}

// Raw appends a length-delimited field with the given bytes.
func (m *Message) Raw(number uint64, value []byte) *Message {
	m.key(number, wireBytes)
	m.buf = binary.AppendUvarint(m.buf, uint64(len(value)))
	m.buf = append(m.buf, value...)
	return m
}

// String appends a string field.
func (m *Message) String(number uint64, value string) *Message {
	return m.Raw(number, []byte(value))
}

// Message appends a nested message. A nil nested message is encoded as an
// empty one.
func (m *Message) Message(number uint64, nested *Message) *Message {
	return m.Raw(number, nested.Bytes())
}

// PackedVarint appends a packed repeated varint field.
func (m *Message) PackedVarint(number uint64, values ...uint64) *Message {
	var packed []byte
	for _, value := range values {
		packed = binary.AppendUvarint(packed, value)
	}
	return m.Raw(number, packed)
}

// PackedZigzag appends a packed repeated zigzag field.
func (m *Message) PackedZigzag(number uint64, values ...int64) *Message {
	encoded := make([]uint64, len(values))
	for index, value := range values {
		encoded[index] = ZigzagEncode(value)
	}
	return m.PackedVarint(number, encoded...)
}

// PackedFloat appends a packed repeated float32 field (LoadAvg).
func (m *Message) PackedFloat(number uint64, values ...float32) *Message {
	var packed []byte
	for _, value := range values {
		packed = binary.LittleEndian.AppendUint32(packed, math.Float32bits(value))
	}
	return m.Raw(number, packed)
}

// ZigzagEncode maps a signed value onto the zigzag varint encoding.
func ZigzagEncode(value int64) uint64 {
	return uint64(value<<1) ^ uint64(value>>63)
}

// Frame wraps APStats in the ep-telemetry protobuf envelope dismist
// decodes: 'P', then {1 APStats, 2 topic, 3 AP ID}.
func Frame(topic, apID string, stats *Message) []byte {
	envelope := New().Message(1, stats).String(2, topic)
	if apID != "" {
		envelope.String(3, apID)
	}
	return append([]byte{'P'}, envelope.buf...)
}

// Gzip wraps a telemetry body the way the AP compresses large frames: a
// 0x02 marker followed by a gzip stream.
func Gzip(body []byte) []byte {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write(body)
	_ = writer.Close()
	return append([]byte{2}, compressed.Bytes()...)
}
