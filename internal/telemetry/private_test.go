package telemetry

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestPrivateCaptures decodes every ep-telemetry capture under the
// directories in DISMIST_PRIVATE_CAPTURES (colon-separated) and fails on
// a frame that does not decode or reports decode errors. It is skipped
// without the variable: the repository holds no captured frames, and the
// output is a summary of counts only, never field values.
//
// Captures are either raw ep-telemetry bodies or whole protocol frames
// (version, class, subject, handle, body) as dismistd's
// -capture-ap-frames-dir writes them.
func TestPrivateCaptures(t *testing.T) {
	dirs := os.Getenv("DISMIST_PRIVATE_CAPTURES")
	if dirs == "" {
		t.Skip("DISMIST_PRIVATE_CAPTURES not set")
	}
	topics := map[string]int{}
	sections := map[string]int{}
	counts := map[string]int{}
	for _, dir := range filepath.SplitList(dirs) {
		files, _ := filepath.Glob(filepath.Join(dir, "*.bin"))
		for _, file := range files {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			body, ok := telemetryBody(raw)
			if !ok {
				continue
			}
			update, err := Decode(body)
			if err != nil {
				t.Errorf("%s: %v", filepath.Base(file), err)
				continue
			}
			topics[update.Topic]++
			for section, count := range update.DecodeErrors {
				sections[section] += count
			}
			counts["radios"] += len(update.Radios)
			counts["vaps"] += len(update.VAPs)
			counts["clients"] += len(update.Clients)
			counts["ports"] += len(update.Ports)
			counts["lldp"] += len(update.LLDP)
			counts["kmsg"] += len(update.KMsg)
			if update.Scan != nil {
				counts["scans"]++
				counts["neighbours"] += len(update.Scan.Band24) + len(update.Scan.Band5)
			}
			if update.Health != nil && update.Health.Cloud != nil {
				counts["cloud"]++
			}
			if update.Event != nil {
				counts["event:"+update.Event.Kind]++
				if update.Event.Noise {
					counts["noise"]++
				}
			}
			if os.Getenv("DISMIST_PRIVATE_DUMP") == filepath.Base(file) {
				encoded, _ := json.MarshalIndent(update, "", "  ")
				t.Logf("%s", encoded)
			}
		}
	}
	t.Logf("topics %v", sortedCounts(topics))
	t.Logf("sections with decode errors %v", sortedCounts(sections))
	t.Logf("decoded %v", sortedCounts(counts))
	if len(sections) != 0 {
		t.Errorf("decode errors: %v", sections)
	}
}

// telemetryBody returns the ep-telemetry body of a capture: the capture
// itself, or the body of a whole protocol frame with that subject.
func telemetryBody(raw []byte) ([]byte, bool) {
	if len(raw) > 3 && raw[0] == 1 && raw[1] <= 2 {
		length, n := binary.Uvarint(raw[2:])
		if n <= 0 || uint64(len(raw)-2-n) < length {
			return nil, false
		}
		offset := 2 + n
		subject := string(raw[offset : offset+int(length)])
		offset += int(length)
		_, n = binary.Uvarint(raw[offset:])
		if n <= 0 || subject != "ep-telemetry" {
			return nil, false
		}
		return raw[offset+n:], true
	}
	return raw, len(raw) != 0
}

func sortedCounts(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+itoa(counts[key]))
	}
	return out
}

func itoa(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
