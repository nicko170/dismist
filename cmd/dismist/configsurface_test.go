package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/nicko170/dismist/internal/options"
	"github.com/nicko170/dismist/internal/options/firmware"
	"github.com/nicko170/dismist/internal/options/regdomain"
)

// docs/config-surface.md has a hand-written introduction and a section
// generated from the option registry, the AU regulatory table and the AP33
// 0.15 path list. This test fails when the section is stale; regenerate it
// with:
//
//	go test ./cmd/dismist -run TestConfigSurfaceDoc -update-docs

var updateDocs = flag.Bool("update-docs", false, "rewrite the generated section of docs/config-surface.md")

const (
	surfaceBegin = "<!-- BEGIN GENERATED: go test ./cmd/dismist -run TestConfigSurfaceDoc -update-docs -->"
	surfaceEnd   = "<!-- END GENERATED -->"
)

func TestConfigSurfaceDoc(t *testing.T) {
	file := filepath.Join("..", "..", "docs", "config-surface.md")
	current, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	begin := bytes.Index(current, []byte(surfaceBegin))
	end := bytes.Index(current, []byte(surfaceEnd))
	if begin < 0 || end < begin {
		t.Fatalf("%s lacks the generated-section markers", file)
	}
	generated := renderConfigSurface(options.Default(), firmware.AP33())
	var want bytes.Buffer
	want.Write(current[:begin+len(surfaceBegin)])
	want.WriteString("\n\n")
	want.WriteString(generated)
	want.WriteString("\n")
	want.Write(current[end:])
	if bytes.Equal(want.Bytes(), current) {
		return
	}
	if *updateDocs {
		if err := os.WriteFile(file, want.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("%s is out of date with the option registry: run go test ./cmd/dismist -run TestConfigSurfaceDoc -update-docs", file)
}

var scopeTitles = []struct {
	scope options.Scope
	title string
}{
	{options.ScopeWLAN, "WLAN"},
	{options.ScopeSite, "Site"},
	{options.ScopeSiteRF, "Site radio profile"},
	{options.ScopeAP, "AP"},
	{options.ScopeAPRF, "AP radio overrides"},
}

func cell(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "|", `\|`), "\n", " ")
}

func renderValue(value any) string {
	if value == nil {
		return "–"
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return "`" + string(data) + "`"
}

func optionValues(option options.Option) string {
	var parts []string
	for _, enum := range option.Enum {
		label := "`" + enum.Value + "`"
		if enum.Confidence == options.NeedsLiveTest {
			label += " (experimental)"
		}
		parts = append(parts, label)
	}
	switch {
	case option.Min != nil && option.Max != nil:
		parts = append(parts, fmt.Sprintf("%d to %d", *option.Min, *option.Max))
	case option.Min != nil:
		parts = append(parts, fmt.Sprintf("≥ %d", *option.Min))
	case option.Max != nil:
		parts = append(parts, fmt.Sprintf("≤ %d", *option.Max))
	}
	if option.MaxBytes > 0 {
		parts = append(parts, fmt.Sprintf("≤ %d bytes", option.MaxBytes))
	}
	if option.Unit != "" {
		parts = append(parts, option.Unit)
	}
	if len(parts) == 0 {
		return option.Type
	}
	return strings.Join(parts, ", ")
}

func optionStatus(option options.Option) string {
	switch option.Confidence {
	case options.NeedsLiveTest:
		return "experimental"
	case "":
		return "–"
	}
	return string(option.Confidence)
}

func renderConfigSurface(registry *options.Registry, schema *firmware.Schema) string {
	var b strings.Builder
	b.WriteString("## Typed options\n\n")
	all := registry.All()
	if len(all) == 0 {
		b.WriteString("No typed options are declared in this build. The registry (`internal/options`) gains them in phase 2; this section is regenerated from it.\n\n")
	} else {
		fmt.Fprintf(&b, "%d options. *Experimental* options and values are accepted only on a site with the experimental switch on, until a live test promotes them (see [verification](verification.md)). *Legacy default* is what an object that existed before the schema-2 migration keeps.\n\n", len(all))
	}
	for _, group := range scopeTitles {
		scoped := registry.ByScope(group.scope)
		if len(scoped) == 0 {
			continue
		}
		fmt.Fprintf(&b, "### %s\n\n", group.title)
		b.WriteString("| Option | Writes | Values | Default | Legacy default | Tier | Status | Disruption |\n")
		b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
		for _, option := range scoped {
			paths := "–"
			if len(option.FirmwarePaths) > 0 {
				paths = "`" + strings.Join(option.FirmwarePaths, "`, `") + "`"
			}
			legacy := "same"
			if option.LegacyDefault != nil {
				legacy = renderValue(option.LegacyDefault)
			}
			name := "`" + option.Key + "`"
			if option.Label != "" {
				name = option.Label + " (" + name + ")"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n", cell(name), cell(paths), cell(optionValues(option)),
				cell(renderValue(option.Default)), cell(legacy), option.Tier, optionStatus(option), option.Disruption)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Never written\n\nNo typed option writes these paths, and an expert patch that sets one is refused (a path matches at or below an entry; `*` ends a name prefix):\n\n")
	never := options.NeverPaths()
	for i := range never {
		never[i] = "`" + never[i] + "`"
	}
	b.WriteString(strings.Join(never, ", ") + ".\n\n")

	b.WriteString("## AU regulatory table (AU/890)\n\nWhat the forms and the validator allow for a site in Australia. Widths marked *not offered* are kept out of typed forms.\n\n")
	b.WriteString("| Band | Width | Primary channels | DFS channels | Offered |\n| --- | --- | --- | --- | --- |\n")
	if domain, ok := regdomain.ForCountry("AU"); ok {
		for _, band := range []string{regdomain.Band24, regdomain.Band5} {
			for _, width := range domain.Widths(band) {
				table := domain.Table(band, width)
				var all, dfs []string
				for _, primary := range table.Primaries {
					all = append(all, strconv.Itoa(primary.Channel))
					if primary.DFS {
						dfs = append(dfs, strconv.Itoa(primary.Channel))
					}
				}
				offered := "yes"
				switch {
				case table.NeedsLabTest:
					offered = "not offered (unverified on AP33)"
				case table.ExpertOnly:
					offered = "expert patch only"
				}
				dfsText := "none"
				if len(dfs) > 0 {
					dfsText = strings.Join(dfs, " ")
				}
				fmt.Fprintf(&b, "| %s GHz | %d MHz | %s | %s | %s |\n", band, width, strings.Join(all, " "), dfsText, offered)
			}
		}
	}

	b.WriteString("\n## AP33 0.15 config sections\n\nTop-level keys of the config ep-control 0.15 decodes, with the number of paths below each (from `internal/options/firmware/ap33-0.15.paths`, field names and types only). Keys are matched case-insensitively by the firmware; dismist writes the canonical spelling.\n\n")
	counts := map[string]int{}
	for _, path := range schema.Paths() {
		top := path
		if index := strings.IndexAny(top, ".["); index >= 0 {
			top = top[:index]
		}
		counts[top]++
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	b.WriteString("| Key | Paths | Key | Paths | Key | Paths |\n| --- | --- | --- | --- | --- | --- |\n")
	for i := 0; i < len(names); i += 3 {
		row := make([]string, 0, 6)
		for j := i; j < i+3; j++ {
			if j < len(names) {
				row = append(row, "`"+names[j]+"`", strconv.Itoa(counts[names[j]]))
			} else {
				row = append(row, "", "")
			}
		}
		fmt.Fprintf(&b, "| %s |\n", strings.Join(row, " | "))
	}
	fmt.Fprintf(&b, "\n%d paths in total.\n", len(schema.Paths()))
	return b.String()
}
