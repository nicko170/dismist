package controller

import (
	"sync"
	"sync/atomic"
)

// Frame subjects counted by name (design §6.3 dismist_ap_frames_total);
// every other subject is counted as SubjectOther, so the label set stays
// fixed whatever an AP sends.
var frameSubjects = [...]string{
	"get-time", "ep-auth", "ep-config", "ep-telemetry", "ping", "ping2",
	"udp-setup", "udp-rekey", "ep-subscribe", "ep-file", "wlc-site-rest",
	"ep-ip-register", "ip6",
}

// SubjectOther is the frame counter for subjects outside FrameSubjects.
const SubjectOther = "other"

// FrameSubjects returns the subjects counted by name, then SubjectOther.
func FrameSubjects() []string {
	return append(append([]string(nil), frameSubjects[:]...), SubjectOther)
}

func subjectIndex(subject string) int {
	for index, known := range frameSubjects {
		if known == subject {
			return index
		}
	}
	return len(frameSubjects)
}

// Auth failure stages (dismist_ap_auth_failures_total{stage}).
const (
	AuthStageHello = "hello" // the hello was malformed or refused (Reply1)
	AuthStageProof = "proof" // the AP's proof did not verify (Reply3)
)

// Counters is a snapshot of one AP's protocol counters (design §5.4).
// Counters live as long as the AP's control session object: they restart
// from zero when the AP is re-registered (a pin or secret change) or the
// daemon restarts, like any Prometheus counter.
type Counters struct {
	// Connections counts successful authentications.
	Connections uint64 `json:"connections"`
	// Disconnects counts authenticated sessions that ended.
	Disconnects uint64 `json:"disconnects"`
	// AuthFailures counts failed authentications by stage (hello, proof).
	AuthFailures map[string]uint64 `json:"auth_failures"`
	// SourceMismatches counts hellos naming this AP from an address other
	// than its pin.
	SourceMismatches uint64 `json:"source_mismatches"`
	// ConfigDeliveries counts ep-config replies sent.
	ConfigDeliveries uint64 `json:"config_deliveries"`
	// ConfigUnavailable counts ep-config requests left unanswered because
	// the AP's config failed to compile and nothing was ever delivered.
	ConfigUnavailable uint64 `json:"config_unavailable"`
	// ConfigRejections counts ep-config-applied reports with an Error.
	ConfigRejections uint64 `json:"config_rejections"`
	// CompileErrors counts compile failures recorded for this AP.
	CompileErrors uint64 `json:"compile_errors"`
	// DecodeErrors counts skipped fields or sections by section ("hello"
	// in phase 0; telemetry sections from phase 1).
	DecodeErrors map[string]uint64 `json:"decode_errors"`
	// Frames counts every frame the AP's session received, by subject
	// (FrameSubjects). Clock requests the Manager answers before the hello
	// routes the connection are not included.
	Frames map[string]uint64 `json:"frames"`
}

type counters struct {
	connections       atomic.Uint64
	disconnects       atomic.Uint64
	authHello         atomic.Uint64
	authProof         atomic.Uint64
	sourceMismatches  atomic.Uint64
	configDeliveries  atomic.Uint64
	configUnavailable atomic.Uint64
	configRejections  atomic.Uint64
	compileErrors     atomic.Uint64
	frames            [len(frameSubjects) + 1]atomic.Uint64

	decodeMu     sync.Mutex
	decodeErrors map[string]uint64
}

func (c *counters) frame(subject string) { c.frames[subjectIndex(subject)].Add(1) }

func (c *counters) authFailure(stage string) {
	if stage == AuthStageProof {
		c.authProof.Add(1)
		return
	}
	c.authHello.Add(1)
}

func (c *counters) decodeError(section string, count int) {
	if count <= 0 {
		return
	}
	c.decodeMu.Lock()
	if c.decodeErrors == nil {
		c.decodeErrors = make(map[string]uint64)
	}
	c.decodeErrors[section] += uint64(count)
	c.decodeMu.Unlock()
}

func (c *counters) snapshot() Counters {
	result := Counters{
		Connections: c.connections.Load(), Disconnects: c.disconnects.Load(),
		AuthFailures: map[string]uint64{
			AuthStageHello: c.authHello.Load(), AuthStageProof: c.authProof.Load(),
		},
		SourceMismatches:  c.sourceMismatches.Load(),
		ConfigDeliveries:  c.configDeliveries.Load(),
		ConfigUnavailable: c.configUnavailable.Load(),
		ConfigRejections:  c.configRejections.Load(),
		CompileErrors:     c.compileErrors.Load(),
		DecodeErrors:      map[string]uint64{},
		Frames:            make(map[string]uint64, len(frameSubjects)+1),
	}
	for index, subject := range FrameSubjects() {
		result.Frames[subject] = c.frames[index].Load()
	}
	c.decodeMu.Lock()
	for section, count := range c.decodeErrors {
		result.DecodeErrors[section] = count
	}
	c.decodeMu.Unlock()
	return result
}
