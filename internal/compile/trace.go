package compile

import "sort"

// The recorder notes every concrete path a typed option writes, so the
// pipeline can report the trace (which step last wrote each option) and
// shadowing (an expert patch changed the option's paths, design §4.4).

type recEntry struct {
	option   string
	scope    string // scope of the option: site, ap or wlan
	objectID string // network ID for wlan entries
	step     string // site, ap, wlan
	path     string // relative to the scope's root object
}

type recorder struct {
	entries []recEntry
}

type scopedRecorder struct {
	r                     *recorder
	scope, objectID, step string
}

func (r *recorder) scoped(scope, objectID, step string) *scopedRecorder {
	return &scopedRecorder{r: r, scope: scope, objectID: objectID, step: step}
}

func (s *scopedRecorder) note(option, path string) {
	s.r.entries = append(s.r.entries, recEntry{option: option, scope: s.scope, objectID: s.objectID, step: s.step, path: path})
}

// relevant returns the indexes of entries a patch at this recorder's level
// can touch: the same network for a WLAN patch, every site and AP entry for
// a root patch.
func (s *scopedRecorder) relevant() []int {
	var result []int
	for index, entry := range s.r.entries {
		if s.scope == ScopeWLAN {
			if entry.scope == ScopeWLAN && entry.objectID == s.objectID {
				result = append(result, index)
			}
			continue
		}
		if entry.scope != ScopeWLAN {
			result = append(result, index)
		}
	}
	return result
}

// snapshot fingerprints the current value of every relevant entry.
func (s *scopedRecorder) snapshot(root map[string]any) map[int]string {
	values := map[int]string{}
	for _, index := range s.relevant() {
		value, present := lookupPath(root, s.r.entries[index].path)
		values[index] = fingerprint(value, present)
	}
	return values
}

// shadows compares the relevant entries after a patch with the snapshot.
func (s *scopedRecorder) shadows(before map[int]string, root map[string]any, by, byObject string) []Shadow {
	type group struct {
		scope, objectID string
		all, changed    []string
	}
	groups := map[string]*group{}
	var order []string
	for index, was := range before {
		entry := s.r.entries[index]
		value, present := lookupPath(root, entry.path)
		key := entry.option + "\x00" + entry.objectID
		g := groups[key]
		if g == nil {
			g = &group{scope: entry.scope, objectID: entry.objectID}
			groups[key] = g
			order = append(order, key)
		}
		g.all = append(g.all, entry.path)
		if fingerprint(value, present) != was {
			g.changed = append(g.changed, entry.path)
		}
	}
	sort.Strings(order)
	var result []Shadow
	for _, key := range order {
		g := groups[key]
		if len(g.changed) == 0 {
			continue
		}
		option := key[:len(key)-len(g.objectID)-1]
		paths := sortedUniqueStrings(g.changed)
		all := sortedUniqueStrings(g.all)
		shadow := Shadow{Option: option, Scope: g.scope, By: by, ObjectID: byObject, Paths: paths, Partly: len(paths) < len(all)}
		if g.scope == ScopeWLAN {
			shadow.ObjectID = g.objectID
		}
		result = append(result, shadow)
	}
	return result
}

// trace builds the compile trace from the recorded writes and shadows.
func (r *recorder) trace(shadows []Shadow) []TraceEntry {
	type key struct{ option, objectID string }
	byOption := map[key]*TraceEntry{}
	var order []key
	for _, entry := range r.entries {
		k := key{entry.option, entry.objectID}
		path := entry.path
		if entry.scope == ScopeWLAN {
			path = "WLANs[ID=" + entry.objectID + "]." + path
		}
		t := byOption[k]
		if t == nil {
			t = &TraceEntry{Option: entry.option, Step: entry.step, ObjectID: entry.objectID}
			byOption[k] = t
			order = append(order, k)
		}
		t.Step = entry.step
		t.Paths = append(t.Paths, path)
	}
	for _, shadow := range shadows {
		if shadow.Partly {
			continue
		}
		objectID := ""
		if shadow.Scope == ScopeWLAN {
			objectID = shadow.ObjectID
		}
		if t := byOption[key{shadow.Option, objectID}]; t != nil {
			t.Step = shadow.By + "_expert"
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].option != order[j].option {
			return order[i].option < order[j].option
		}
		return order[i].objectID < order[j].objectID
	})
	result := make([]TraceEntry, 0, len(order))
	for _, k := range order {
		t := byOption[k]
		t.Paths = sortedUniqueStrings(t.Paths)
		result = append(result, *t)
	}
	return result
}
