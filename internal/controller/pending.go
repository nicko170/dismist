package controller

import (
	"sort"
	"sync"
	"time"
)

// Pending reasons (PendingAP.Reason).
const (
	// PendingUnregistered: the hello named an AP ID dismist does not know.
	PendingUnregistered = "unregistered"
	// PendingSourceMismatch: a registered AP pinned to another source
	// address connected from this one.
	PendingSourceMismatch = "source_mismatch"
	// PendingAuthFailed: a registered AP failed authentication (wrong
	// secret, or a client impersonating it).
	PendingAuthFailed = "auth_failed"
)

// Bounds of the in-memory pending map (design §5.4).
const (
	pendingMax = 64
	pendingTTL = 24 * time.Hour
)

// PendingAP is one AP that tried to connect and was not served (design
// §5.4): an unregistered ID, or a registered AP refused for its source
// address or a failed authentication. Everything but RemoteAddress, the
// times, the attempt count and the reason comes from an unauthenticated
// hello, bounded and cleaned before it is stored.
type PendingAP struct {
	ID            string    `json:"id"`
	Model         string    `json:"model"`
	SKU           string    `json:"sku"`
	Serial        string    `json:"serial"`
	Firmware      string    `json:"firmware"`
	LocalIP       string    `json:"local_ip"`
	RemoteAddress string    `json:"remote_address"`
	FirstSeen     time.Time `json:"first_seen"`
	LastSeen      time.Time `json:"last_seen"`
	Attempts      uint64    `json:"attempts"`
	Reason        string    `json:"reason"`
}

// pendingMap is bounded in entries and age. A full map evicts the entry
// seen least recently, so a flood of invented IDs can only push out other
// pending rows, never grow memory.
type pendingMap struct {
	mu       sync.Mutex
	entries  map[string]*PendingAP
	revision uint64
	now      func() time.Time
}

func newPendingMap() *pendingMap {
	return &pendingMap{entries: make(map[string]*PendingAP), now: time.Now}
}

// record adds or refreshes the entry for a hello. The ID must already be
// bounded and cleaned.
func (p *pendingMap) record(hello Hello, extras HelloExtras, remoteAddr, reason string) {
	if hello.ID == "" {
		return
	}
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(now)
	entry, found := p.entries[hello.ID]
	if !found {
		if len(p.entries) >= pendingMax {
			p.evictOldestLocked()
		}
		entry = &PendingAP{ID: hello.ID, FirstSeen: now}
		p.entries[hello.ID] = entry
	}
	entry.Model = cleanHelloString(hello.Model, maxHelloString)
	entry.Firmware = cleanHelloString(hello.Firmware, maxHelloString)
	entry.SKU = extras.SKU
	entry.Serial = extras.SerialNo
	entry.LocalIP = extras.LocalIP
	entry.RemoteAddress = remoteHost(remoteAddr)
	entry.LastSeen = now
	entry.Attempts++
	entry.Reason = reason
	p.revision++
}

// forget removes an ID: it was registered, or it authenticated.
func (p *pendingMap) forget(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, found := p.entries[id]; found {
		delete(p.entries, id)
		p.revision++
	}
}

func (p *pendingMap) list() []PendingAP {
	now := p.now()
	p.mu.Lock()
	p.pruneLocked(now)
	result := make([]PendingAP, 0, len(p.entries))
	for _, entry := range p.entries {
		result = append(result, *entry)
	}
	p.mu.Unlock()
	sort.Slice(result, func(i, j int) bool {
		if !result[i].LastSeen.Equal(result[j].LastSeen) {
			return result[i].LastSeen.After(result[j].LastSeen)
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func (p *pendingMap) currentRevision() uint64 {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(now)
	return p.revision
}

func (p *pendingMap) pruneLocked(now time.Time) {
	for id, entry := range p.entries {
		if now.Sub(entry.LastSeen) >= pendingTTL {
			delete(p.entries, id)
			p.revision++
		}
	}
}

func (p *pendingMap) evictOldestLocked() {
	var oldest *PendingAP
	for _, entry := range p.entries {
		if oldest == nil || entry.LastSeen.Before(oldest.LastSeen) ||
			(entry.LastSeen.Equal(oldest.LastSeen) && entry.ID < oldest.ID) {
			oldest = entry
		}
	}
	if oldest != nil {
		delete(p.entries, oldest.ID)
		p.revision++
	}
}
