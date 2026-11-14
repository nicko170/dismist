// Package telemetry decodes the AP's ep-telemetry messages (design §5.1):
// protobuf stats.APStats frames (ap-stats-full-, ap-stats-fast-) and the
// JSON messages (PACE events, ep-config-applied-). Field numbers come from
// the protobuf struct tags of the AP's own 0.15 pace binary.
//
// Every section decodes on its own: a malformed optional sub-message is
// skipped and counted in Update.DecodeErrors, and never drops the rest of
// the frame (VAPs and clients in particular).
package telemetry

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
)

const maxPayload = 4 << 20

// ErrMalformed means the message is not a telemetry message at all: an
// unknown encoding, a broken gzip stream, a protobuf envelope that does
// not scan, or no topic.
var ErrMalformed = errors.New("malformed AP telemetry")

// Decode decodes one ep-telemetry body. A body may be gzip-compressed
// (0x02 then a gzip stream); the inner body is JSON ('{') or a protobuf
// TelemetryEnvelope ('P'). Only a message that cannot be read at all is an
// error; problems inside sections are counted in DecodeErrors.
func Decode(body []byte) (Update, error) {
	if len(body) > maxPayload || len(body) == 0 {
		return Update{}, ErrMalformed
	}
	if len(body) >= 3 && body[0] == 2 && body[1] == 0x1f && body[2] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(body[1:]))
		if err != nil {
			return Update{}, ErrMalformed
		}
		uncompressed, err := io.ReadAll(io.LimitReader(reader, maxPayload+1))
		_ = reader.Close()
		if err != nil || len(uncompressed) > maxPayload || len(uncompressed) == 0 {
			return Update{}, ErrMalformed
		}
		body = uncompressed
	}
	switch body[0] {
	case '{':
		return decodeJSON(body)
	case 'P':
		return decodeEnvelope(body[1:])
	default:
		return Update{}, ErrMalformed
	}
}

// decodeEnvelope reads msgs.TelemetryEnvelope: 1 Msg (stats.APStats),
// 2 Topic, 3 HashKey (the AP ID), 4 Shuffles (ignored), 5 Ack.
func decodeEnvelope(body []byte) (Update, error) {
	fields, err := scan(body)
	if err != nil {
		return Update{}, ErrMalformed
	}
	update := Update{DecodeErrors: map[string]int{}}
	envelope := newReader(update.DecodeErrors, SectionFrame)
	var stats [][]byte
	for _, item := range fields {
		switch item.number {
		case 1:
			if raw := envelope.bytes(item); raw != nil {
				stats = append(stats, raw)
			}
		case 2:
			update.Topic = envelope.str(item, 64)
		case 3:
			update.APID = envelope.str(item, 64)
		}
	}
	if update.Topic == "" {
		return Update{}, ErrMalformed
	}
	for _, raw := range stats {
		decodeAPStats(raw, &update)
	}
	if len(update.DecodeErrors) == 0 {
		update.DecodeErrors = nil
	}
	return update, nil
}
