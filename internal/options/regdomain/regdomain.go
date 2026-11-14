// Package regdomain holds the regulatory channel tables dismist validates and
// plans against (design Appendix A, [RF §5]). The AP has no country-to-channel
// logic of its own; the ccrev it is given decides what the driver allows.
package regdomain

import (
	"sort"
	"strings"
)

// Bands.
const (
	Band24 = "2.4"
	Band5  = "5"
)

// Primary is one valid primary channel at a width.
type Primary struct {
	Channel int  `json:"channel"`
	DFS     bool `json:"dfs"`
}

// WidthTable lists the valid primaries of one band at one width.
type WidthTable struct {
	Width     int       `json:"width"`
	Primaries []Primary `json:"primaries"`
	// Blocks are the channel groups a 40/80/160 MHz channel spans; any
	// primary in a block selects that block. nil for 20 MHz.
	Blocks [][]int `json:"blocks,omitempty"`
	// ExpertOnly widths are not offered in typed forms (2.4 GHz 40 MHz
	// needs SecChan).
	ExpertOnly bool `json:"expert_only,omitempty"`
	// NeedsLabTest widths are unverified on the AP (§9 Q10).
	NeedsLabTest bool `json:"needs_lab_test,omitempty"`
}

// Domain is the table of one country and its ccrev.
type Domain struct {
	Country string
	CCRev   string
	Bands   map[string][]WidthTable
	// Recommended channels per band (2.4 GHz 1, 6, 11).
	Recommended map[string][]int
	// DefaultAuto is the default allowed list for Auto channel selection.
	DefaultAuto map[string][]int
}

var domains = map[string]*Domain{"AU": &AU}

// CCRev returns the ccrev written for a country (design §4.2: AU → AU/890
// only, §9 Q9).
func CCRev(country string) (string, bool) {
	domain, ok := domains[strings.ToUpper(strings.TrimSpace(country))]
	if !ok {
		return "", false
	}
	return domain.CCRev, true
}

// ForCountry returns the table of a country.
func ForCountry(country string) (*Domain, bool) {
	domain, ok := domains[strings.ToUpper(strings.TrimSpace(country))]
	return domain, ok
}

// Countries lists the supported countries.
func Countries() []string {
	result := make([]string, 0, len(domains))
	for country := range domains {
		result = append(result, country)
	}
	sort.Strings(result)
	return result
}

// Table returns the width table of a band, or nil.
func (d *Domain) Table(band string, width int) *WidthTable {
	for index := range d.Bands[band] {
		if d.Bands[band][index].Width == width {
			return &d.Bands[band][index]
		}
	}
	return nil
}

// Widths returns the widths a band supports, ascending.
func (d *Domain) Widths(band string) []int {
	var widths []int
	for _, table := range d.Bands[band] {
		widths = append(widths, table.Width)
	}
	sort.Ints(widths)
	return widths
}

// Valid reports whether channel is a valid primary on band at width, and
// whether it needs DFS.
func (d *Domain) Valid(band string, width, channel int) (dfs, ok bool) {
	table := d.Table(band, width)
	if table == nil {
		return false, false
	}
	for _, primary := range table.Primaries {
		if primary.Channel == channel {
			return primary.DFS, true
		}
	}
	return false, false
}

// Block returns the channels a primary's 40/80/160 MHz channel spans (the
// primary alone at 20 MHz), or nil when the primary is invalid.
func (d *Domain) Block(band string, width, channel int) []int {
	table := d.Table(band, width)
	if table == nil {
		return nil
	}
	if table.Blocks == nil {
		if _, ok := d.Valid(band, width, channel); ok {
			return []int{channel}
		}
		return nil
	}
	for _, block := range table.Blocks {
		for _, member := range block {
			if member == channel {
				return append([]int(nil), block...)
			}
		}
	}
	return nil
}
