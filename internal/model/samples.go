package model

import (
	"encoding/json"
	"errors"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Sample query limits.
const (
	DefaultSampleLimit = 20000
	MaxSampleLimit     = 200000
)

// SampleQuery selects minute samples for charts
// (GET /api/sites/$id/history/samples?ap=&range=&step=).
type SampleQuery struct {
	SiteID string // required
	APID   string // optional; "" = every AP of the site
	// Since (inclusive) and Until (exclusive) bound the sample minute;
	// zero means unbounded.
	Since, Until time.Time
	// Step, when set, keeps at most one sample per AP per step-aligned
	// bucket (the newest one in it).
	Step time.Duration
	// Limit caps the result (default DefaultSampleLimit, max
	// MaxSampleLimit). When more samples match, the newest are kept.
	Limit int
}

// Samples returns the matching minute samples (with radios) in ascending
// minute order; samples of the same minute are ordered by AP ID. A
// site-level query merges every AP of the site by time.
func (s *BoltStore) Samples(query SampleQuery) ([]MinuteSample, error) {
	if query.SiteID == "" {
		return nil, errors.New("site ID required")
	}
	limit := query.Limit
	if limit <= 0 {
		limit = DefaultSampleLimit
	}
	if limit > MaxSampleLimit {
		limit = MaxSampleLimit
	}
	result := make([]MinuteSample, 0)
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketSamples))
		if bucket == nil {
			return errors.New("samples bucket missing")
		}
		prefixes := [][]byte{historyPrefix(query.SiteID, query.APID)}
		if query.APID == "" {
			prefixes = segments(bucket, historyPrefix(query.SiteID, ""))
		}
		streams := make([]*stream, len(prefixes))
		for index, prefix := range prefixes {
			var start []byte
			if !query.Until.IsZero() {
				// The first key at or after Until; the backwards stream
				// starts strictly below it.
				until := query.Until.Unix()
				if query.Until.After(time.Unix(until, 0)) {
					until++
				}
				start = append(append([]byte(nil), prefix...), timeKey(until)...)
			}
			streams[index] = openStream(bucket, prefix, start, true, ascendingSeconds(len(prefix)))
		}
		lastBucket := make([]int64, len(streams))
		seen := make([]bool, len(streams))
		for len(result) < limit {
			pick := pickNewest(streams)
			if pick < 0 {
				break
			}
			current := streams[pick]
			if !query.Since.IsZero() && current.when.Before(query.Since) {
				// Everything left in this stream is older; drop it.
				current.key, current.value = nil, nil
				continue
			}
			if query.Step > 0 {
				step := current.when.UnixNano() / int64(query.Step)
				if seen[pick] && lastBucket[pick] == step {
					current.advance()
					continue
				}
				seen[pick], lastBucket[pick] = true, step
			}
			var sample MinuteSample
			if err := json.Unmarshal(current.value, &sample); err != nil {
				return err
			}
			result = append(result, sample)
			current.advance()
		}
		return nil
	})
	// Newest first so far: reverse, keeping AP ID order within a minute.
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	sortWithinMinute(result)
	return result, err
}

func sortWithinMinute(samples []MinuteSample) {
	for start := 0; start < len(samples); {
		end := start + 1
		for end < len(samples) && samples[end].Minute.Equal(samples[start].Minute) {
			end++
		}
		group := samples[start:end]
		for i := 1; i < len(group); i++ {
			for j := i; j > 0 && group[j].APID < group[j-1].APID; j-- {
				group[j], group[j-1] = group[j-1], group[j]
			}
		}
		start = end
	}
}
