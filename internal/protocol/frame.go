package protocol

import (
	"encoding/binary"
	"errors"
)

const maxSubjectLength = 256

var ErrMalformedFrame = errors.New("malformed AP frame")

type Message struct {
	Version      byte
	ServiceClass byte
	Subject      string
	Handle       uint64
	Body         []byte
}

func Decode(raw []byte) (Message, error) {
	if len(raw) < 4 {
		return Message{}, ErrMalformedFrame
	}
	m := Message{Version: raw[0], ServiceClass: raw[1]}
	length, n := binary.Uvarint(raw[2:])
	if n <= 0 || length > maxSubjectLength || uint64(len(raw)-2-n) < length {
		return Message{}, ErrMalformedFrame
	}
	offset := 2 + n
	for _, b := range raw[offset : offset+int(length)] {
		if b > 0x7f {
			return Message{}, ErrMalformedFrame
		}
	}
	m.Subject = string(raw[offset : offset+int(length)])
	offset += int(length)
	handle, n := binary.Uvarint(raw[offset:])
	if n <= 0 {
		return Message{}, ErrMalformedFrame
	}
	m.Handle = handle
	m.Body = append([]byte(nil), raw[offset+n:]...)
	return m, nil
}

func Encode(m Message) ([]byte, error) {
	if len(m.Subject) > maxSubjectLength {
		return nil, ErrMalformedFrame
	}
	for _, b := range []byte(m.Subject) {
		if b > 0x7f {
			return nil, ErrMalformedFrame
		}
	}
	out := make([]byte, 2, 2+2*binary.MaxVarintLen64+len(m.Subject)+len(m.Body))
	out[0], out[1] = m.Version, m.ServiceClass
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], uint64(len(m.Subject)))
	out = append(out, buf[:n]...)
	out = append(out, m.Subject...)
	n = binary.PutUvarint(buf[:], m.Handle)
	out = append(out, buf[:n]...)
	out = append(out, m.Body...)
	return out, nil
}
