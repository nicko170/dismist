package model

import (
	"bytes"
	"encoding/binary"
	"math"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Key layout helpers shared by the history buckets (design §4.1).
//
// Two layouts exist:
//   - ascending: <prefix> + 8-byte big-endian time (samples: unix seconds;
//     legacy events: unix nanoseconds followed by an 8-byte hash);
//   - newest first: <prefix> + invTime(8) + ... (events2, events2_ap,
//     sessions_t), where invTime is math.MaxUint64 - unixNano.
//
// Site and AP IDs never contain a NUL byte (they are UUIDs and MAC-style
// IDs), so "\x00" separates the segments. invTime bytes may contain NUL, so
// keys are split by position, never by searching for NUL across the time.

// invTime is math.MaxUint64 - unixNano, big-endian: a forward cursor walks
// newest first (design §4.1).
func invTime(at time.Time) []byte {
	var out [8]byte
	binary.BigEndian.PutUint64(out[:], math.MaxUint64-uint64(at.UnixNano()))
	return out[:]
}

func fromInvTime(raw []byte) time.Time {
	return time.Unix(0, int64(math.MaxUint64-binary.BigEndian.Uint64(raw))).UTC()
}

// prefixEnd is the smallest key greater than every key starting with
// prefix, for prefixes that end in "\x00" (the separator): the last byte
// becomes "\x01".
func prefixEnd(prefix []byte) []byte {
	end := append([]byte(nil), prefix...)
	end[len(end)-1]++
	return end
}

// segments lists the distinct first segments below prefix: for prefix
// "site\x00" in a bucket keyed site\x00ap\x00..., one entry per AP, each
// returned as the full sub-prefix "site\x00ap\x00". With an empty prefix it
// lists the top-level segments ("site\x00"). It seeks past each segment, so
// it reads one key per segment.
func segments(bucket *bolt.Bucket, prefix []byte) [][]byte {
	var result [][]byte
	cursor := bucket.Cursor()
	key, _ := cursor.Seek(prefix)
	for key != nil && bytes.HasPrefix(key, prefix) {
		rest := key[len(prefix):]
		cut := bytes.IndexByte(rest, 0)
		if cut < 0 {
			// A key that is the bare segment: not one of ours; skip it.
			key, _ = cursor.Next()
			continue
		}
		sub := append(append([]byte(nil), prefix...), rest[:cut+1]...)
		result = append(result, sub)
		key, _ = cursor.Seek(prefixEnd(sub))
	}
	return result
}

// stream walks the keys under one prefix in time order. For ascending keys
// it walks backwards (newest first) or forwards; for invTime keys forwards
// is newest first.
type stream struct {
	cursor *bolt.Cursor
	prefix []byte
	back   bool
	at     func(key []byte) (time.Time, bool)
	key    []byte
	value  []byte
	when   time.Time
}

// openStream positions a stream at start (inclusive) going forwards, or at
// the last key strictly below start going backwards. A nil start means the
// beginning (forwards) or the end (backwards) of the prefix.
func openStream(bucket *bolt.Bucket, prefix, start []byte, back bool, at func([]byte) (time.Time, bool)) *stream {
	s := &stream{cursor: bucket.Cursor(), prefix: prefix, back: back, at: at}
	if back {
		if start == nil {
			start = prefixEnd(prefix)
		}
		if key, _ := s.cursor.Seek(start); key == nil {
			s.key, s.value = s.cursor.Last()
		} else {
			s.key, s.value = s.cursor.Prev()
		}
	} else {
		if start == nil {
			start = prefix
		}
		s.key, s.value = s.cursor.Seek(start)
	}
	s.settle()
	return s
}

// settle skips keys whose time cannot be read and ends the stream when it
// leaves its prefix.
func (s *stream) settle() {
	for s.key != nil && bytes.HasPrefix(s.key, s.prefix) {
		if when, ok := s.at(s.key); ok {
			s.when = when
			return
		}
		s.step()
	}
	s.key, s.value = nil, nil
}

func (s *stream) step() {
	if s.back {
		s.key, s.value = s.cursor.Prev()
	} else {
		s.key, s.value = s.cursor.Next()
	}
}

func (s *stream) valid() bool { return s.key != nil }

func (s *stream) advance() {
	s.step()
	s.settle()
}

// pickNewest returns the index of the valid stream with the latest time
// (the first one on ties, so the merge is deterministic), or -1.
func pickNewest(streams []*stream) int {
	best := -1
	for index, s := range streams {
		if s.valid() && (best < 0 || s.when.After(streams[best].when)) {
			best = index
		}
	}
	return best
}

// ascendingSeconds reads the 8-byte big-endian unix-seconds time that
// follows a fixed prefix length (samples, samples_hourly).
func ascendingSeconds(prefixLen int) func([]byte) (time.Time, bool) {
	return func(key []byte) (time.Time, bool) {
		if len(key) < prefixLen+8 {
			return time.Time{}, false
		}
		return time.Unix(int64(binary.BigEndian.Uint64(key[prefixLen:prefixLen+8])), 0).UTC(), true
	}
}

// ascendingNanos reads the 8-byte big-endian unix-nanosecond time that
// follows a fixed prefix length (legacy events).
func ascendingNanos(prefixLen int) func([]byte) (time.Time, bool) {
	return func(key []byte) (time.Time, bool) {
		if len(key) < prefixLen+8 {
			return time.Time{}, false
		}
		return time.Unix(0, int64(binary.BigEndian.Uint64(key[prefixLen:prefixLen+8]))).UTC(), true
	}
}

// inverted reads the invTime that follows a fixed prefix length.
func inverted(prefixLen int) func([]byte) (time.Time, bool) {
	return func(key []byte) (time.Time, bool) {
		if len(key) < prefixLen+8 {
			return time.Time{}, false
		}
		return fromInvTime(key[prefixLen : prefixLen+8]), true
	}
}

// deleteKeys deletes keys from bucket (collected first: deleting while a
// cursor walks can skip keys in bbolt).
func deleteKeys(bucket *bolt.Bucket, keys [][]byte) error {
	for _, key := range keys {
		if err := bucket.Delete(key); err != nil {
			return err
		}
	}
	return nil
}
