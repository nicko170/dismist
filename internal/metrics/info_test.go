package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/expfmt"
)

// TestInfoSeriesWithoutNames: the info series carry IDs, model and firmware
// only, preferring what a connected AP reports over the stored record.
func TestInfoSeriesWithoutNames(t *testing.T) {
	store, sessions := fixture(t)
	c := testCollector(store, sessions, Options{})
	want := `
# HELP dismist_site_info Registered site, always 1; the name label is present with -metrics-names.
# TYPE dismist_site_info gauge
dismist_site_info{site="11111111-1111-4111-8111-111111111111"} 1
dismist_site_info{site="22222222-2222-4222-8222-222222222222"} 1
dismist_site_info{site="x\"\\\ny"} 1
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(want), "dismist_site_info"); err != nil {
		t.Fatal(err)
	}
	if got := testutil.CollectAndCount(c, "dismist_ap_info"); got != 6 {
		t.Fatalf("dismist_ap_info series = %d, want one per registered AP (6)", got)
	}
	lint, err := testutil.CollectAndLint(c, "dismist_ap_info", "dismist_site_info")
	if err != nil || len(lint) != 0 {
		t.Fatalf("promlint: %v %v", lint, err)
	}
	out := gather(t, c)
	if !strings.Contains(out, `dismist_ap_info{ap="02-aa-00-00-00-01",firmware="apfw-0.15.0",model="AP33",site="11111111-1111-4111-8111-111111111111"} 1`) {
		t.Fatalf("connected AP info does not use its reported model and firmware:\n%s", grep(out, "dismist_ap_info"))
	}
	for _, name := range []string{"Example Home", "Example Lab", "Lounge AP", "Office AP", `name="`} {
		if strings.Contains(out, name) {
			t.Fatalf("name %q exported without -metrics-names", name)
		}
	}
}

// TestInfoSeriesWithNames: -metrics-names adds a name label to both info
// series and nowhere else.
func TestInfoSeriesWithNames(t *testing.T) {
	store, sessions := fixture(t)
	c := testCollector(store, sessions, Options{Names: true})
	out := gather(t, c)
	for _, line := range []string{
		`dismist_site_info{name="Example Home",site="11111111-1111-4111-8111-111111111111"} 1`,
		`dismist_ap_info{ap="02-aa-00-00-00-01",firmware="apfw-0.15.0",model="AP33",name="Lounge AP",site="11111111-1111-4111-8111-111111111111"} 1`,
		`dismist_ap_info{ap="02-aa-00-00-00-03",firmware="",model="",name="",site="11111111-1111-4111-8111-111111111111"} 1`,
	} {
		if !strings.Contains(out, line) {
			t.Fatalf("missing %s in:\n%s", line, grep(out, "_info"))
		}
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, `name="`) && !strings.HasPrefix(l, "dismist_site_info{") && !strings.HasPrefix(l, "dismist_ap_info{") {
			t.Fatalf("name label outside the info series: %s", l)
		}
	}
}

func gather(t *testing.T, c *collector) string {
	t.Helper()
	var b strings.Builder
	for _, name := range []string{"dismist_ap_info", "dismist_site_info"} {
		got, err := testutil.CollectAndFormat(c, expfmt.TypeTextPlain, name)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(got)
	}
	return b.String()
}

func grep(text, needle string) string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, needle) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
