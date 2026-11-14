package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/auth"
	"github.com/nicko170/dismist/tools/fakeap"
)

// The golden hellos are sanitised copies of captured 0.14 and 0.15 hellos
// (testdata/hello/README.md): names, order and JSON types kept, values
// replaced.
var helloGoldens = []string{"ap33-0.14.json", "ap33-0.15.json"}

// goldenExtras is what the tolerant pass must read from both goldens.
var goldenExtras = HelloExtras{
	SKU: "AP33-WW", SerialNo: "EXAMPLE000001", Uptime: 420, ConnectUptime: 410, BootCount: 75,
	LastRebootTime:   time.Date(2026, 1, 2, 3, 4, 5, 60708090, time.UTC),
	LastRebootReason: "unknown", LastBootState: "warm", LocalIP: "192.0.2.10",
}

// helloSecret is the synthetic AP secret the golden-hello tests use.
func helloSecret() [32]byte {
	var secret [32]byte
	for index := range secret {
		secret[index] = byte(0xa0 + index)
	}
	return secret
}

func loadGoldenHello(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "hello", name))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSpace(body)
}

// jsonField is one top-level member of a hello, in wire order.
type jsonField struct {
	key string
	raw json.RawMessage
}

func splitObject(t *testing.T, body []byte) []jsonField {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		t.Fatalf("hello is not an object: %v", err)
	}
	var fields []jsonField
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			t.Fatal(err)
		}
		fields = append(fields, jsonField{key: token.(string), raw: raw})
	}
	return fields
}

func joinObject(fields []jsonField) []byte {
	var out bytes.Buffer
	out.WriteByte('{')
	for index, field := range fields {
		if index > 0 {
			out.WriteByte(',')
		}
		key, _ := json.Marshal(field.key)
		out.Write(key)
		out.WriteByte(':')
		out.Write(field.raw)
	}
	out.WriteByte('}')
	return out.Bytes()
}

// helloMutation changes one hello field. raw "" removes the field; a field
// the golden lacks is appended.
type helloMutation struct {
	name   string
	field  string
	raw    string
	extras func(*HelloExtras) // expected extras after the mutation
	errors []string           // expected decode errors
}

func (m helloMutation) apply(t *testing.T, golden []byte) []byte {
	t.Helper()
	fields := splitObject(t, golden)
	result := make([]jsonField, 0, len(fields)+1)
	found := false
	for _, field := range fields {
		if field.key != m.field {
			result = append(result, field)
			continue
		}
		found = true
		if m.raw != "" {
			result = append(result, jsonField{key: field.key, raw: json.RawMessage(m.raw)})
		}
	}
	if !found && m.raw != "" {
		result = append(result, jsonField{key: m.field, raw: json.RawMessage(m.raw)})
	}
	return joinObject(result)
}

// wrongValues are JSON values of the wrong type (or an invalid value) for
// each extra field.
var wrongValues = map[string][]string{
	"SKU":              {`7`, `true`, `{"a":1}`, `["x"]`},
	"SerialNo":         {`7`, `false`, `{}`, `[1]`},
	"Developer":        {`"true"`, `1`, `{}`, `[]`},
	"Uptime":           {`"420"`, `4.5`, `true`, `[]`, `1e400`, `{}`},
	"ConnectUptime":    {`"410"`, `-1.5`, `false`, `{"s":1}`},
	"BootCount":        {`-1`, `"75"`, `{}`, `18446744073709551616`, `7.5`},
	"LastRebootTime":   {`7`, `"yesterday"`, `""`, `{}`, `["2026-01-02T03:04:05Z"]`},
	"LastRebootReason": {`7`, `true`, `{}`, `[]`},
	"LastBootState":    {`0`, `true`, `{"warm":true}`, `["warm"]`},
	"LocalIP":          {`7`, `"not-an-ip"`, `[]`, `{"ip":"192.0.2.1"}`, `"192.0.2.300"`},
	"Timeout_sec":      {`"30"`, `30.5`, `true`, `[30]`},
}

// zeroField clears one extra field in the expected extras.
func zeroField(field string) func(*HelloExtras) {
	return func(extras *HelloExtras) {
		switch field {
		case "SKU":
			extras.SKU = ""
		case "SerialNo":
			extras.SerialNo = ""
		case "Developer":
			extras.Developer = false
		case "Uptime":
			extras.Uptime = 0
		case "ConnectUptime":
			extras.ConnectUptime = 0
		case "BootCount":
			extras.BootCount = 0
		case "LastRebootTime":
			extras.LastRebootTime = time.Time{}
		case "LastRebootReason":
			extras.LastRebootReason = ""
		case "LastBootState":
			extras.LastBootState = ""
		case "LocalIP":
			extras.LocalIP = ""
		case "Timeout_sec":
			extras.TimeoutSec = 0
		}
	}
}

// helloMutations is every mutation of §5.4: each extra field missing,
// null and with each wrong type, plus unknown, duplicate-case, oversized
// and hostile fields.
func helloMutations() []helloMutation {
	var mutations []helloMutation
	for _, field := range helloFields {
		name := field.name
		mutations = append(mutations,
			helloMutation{name: name + "/missing", field: name, raw: "", extras: zeroField(name)},
			helloMutation{name: name + "/null", field: name, raw: "null", extras: zeroField(name)},
		)
		for index, raw := range wrongValues[name] {
			mutations = append(mutations, helloMutation{
				name: name + "/wrong-" + string(rune('a'+index)), field: name, raw: raw,
				extras: zeroField(name), errors: []string{name},
			})
		}
	}
	keep := func(*HelloExtras) {}
	mutations = append(mutations,
		helloMutation{name: "unknown-field", field: "FutureField", raw: `{"nested":[1,{"x":null}],"n":1e400}`, extras: keep},
		helloMutation{name: "case-variant-duplicate", field: "serialno", raw: `7`, extras: keep},
		helloMutation{name: "developer-and-timeout", field: "Developer", raw: `true`, extras: func(e *HelloExtras) { e.Developer = true }},
		helloMutation{name: "timeout-sec", field: "Timeout_sec", raw: `30`, extras: func(e *HelloExtras) { e.TimeoutSec = 30 }},
		helloMutation{name: "oversized-serial", field: "SerialNo", raw: `"` + strings.Repeat("S", 10000) + `"`,
			extras: func(e *HelloExtras) { e.SerialNo = strings.Repeat("S", maxHelloString) }},
		helloMutation{name: "control-characters", field: "LastRebootReason", raw: `"reboot/\u0000un\nknown\u001b"`,
			extras: func(e *HelloExtras) { e.LastRebootReason = "reboot/unknown" }},
		helloMutation{name: "ipv6-local-ip", field: "LocalIP", raw: `"2001:db8::10"`,
			extras: func(e *HelloExtras) { e.LocalIP = "2001:db8::10" }},
	)
	return mutations
}

// authenticateHello runs both ep-auth steps on a hello body with the same
// decode the controller uses, playing the AP with auth.ClientProof.
func authenticateHello(t *testing.T, body []byte) error {
	t.Helper()
	var hello Hello
	if err := json.Unmarshal(body, &hello); err != nil {
		return err
	}
	authenticator := Authenticator{ID: "02-aa-00-00-00-01", Secret: helloSecret(), CertDER: []byte{1, 2, 3}, CipherSuite: 4865}
	var qs [32]byte
	for index := range qs {
		qs[index] = byte(index * 3)
	}
	now := uint64(1794475905000000000)
	reply1, err := authenticator.Reply1(hello, qs, now)
	if err != nil {
		return err
	}
	var qc [32]byte
	copy(qc[:], hello.QC)
	proof := auth.ClientProof(auth.Inputs{
		Secret: helloSecret(), Counter: hello.Counter, QC: qc, QS: qs, DateTimeNS: now,
		Context: auth.Version2Context(auth.ContextInputs{
			Version: hello.Version, ID: hello.ID, Model: hello.Model, Firmware: hello.Firmware,
			Suite: hello.Suite, Cert: []byte{1, 2, 3}, CipherSuite: 4865, Entity: "CLIENT",
		}),
	})
	_, err = authenticator.Reply3(hello, reply1, Reply2{Version: 2, NextDateTimeNS: now + 1, Authed: true, RC: proof[:]})
	return err
}

func TestGoldenHellosDecodeAndAuthenticate(t *testing.T) {
	for _, name := range helloGoldens {
		body := loadGoldenHello(t, name)
		extras, failed := decodeHelloExtras(body)
		if len(failed) != 0 || !reflect.DeepEqual(extras, goldenExtras) {
			t.Fatalf("%s: extras %+v, errors %v", name, extras, failed)
		}
		if err := authenticateHello(t, body); err != nil {
			t.Fatalf("%s: golden hello does not authenticate: %v", name, err)
		}
	}
}

// TestEveryMutatedHelloStillAuthenticates is the §5.4 guarantee: a wrong
// type, a missing field or null in any extra field never fails
// authentication, and only the broken field is lost.
func TestEveryMutatedHelloStillAuthenticates(t *testing.T) {
	for _, name := range helloGoldens {
		golden := loadGoldenHello(t, name)
		var goldenHello Hello
		if err := json.Unmarshal(golden, &goldenHello); err != nil {
			t.Fatal(err)
		}
		for _, mutation := range helloMutations() {
			body := mutation.apply(t, golden)
			var hello Hello
			if err := json.Unmarshal(body, &hello); err != nil {
				t.Fatalf("%s %s: authentication decode failed: %v", name, mutation.name, err)
			}
			if !reflect.DeepEqual(hello, goldenHello) {
				t.Fatalf("%s %s: authentication fields changed: %+v", name, mutation.name, hello)
			}
			if err := authenticateHello(t, body); err != nil {
				t.Fatalf("%s %s: authentication failed: %v", name, mutation.name, err)
			}
			want := goldenExtras
			mutation.extras(&want)
			extras, failed := decodeHelloExtras(body)
			if !reflect.DeepEqual(extras, want) {
				t.Fatalf("%s %s: extras\n got %+v\nwant %+v", name, mutation.name, extras, want)
			}
			if !reflect.DeepEqual([]string(failed), mutation.errors) && (len(failed) != 0 || len(mutation.errors) != 0) {
				t.Fatalf("%s %s: decode errors %v, want %v", name, mutation.name, failed, mutation.errors)
			}
		}
	}
}

// TestMutatedHellosAuthenticateOverWSS runs every mutated hello through
// the real path: the Manager's routing decode, the session's handshake and
// the tolerant pass into Status, with tools/fakeap as the AP.
func TestMutatedHellosAuthenticateOverWSS(t *testing.T) {
	manager := NewManager()
	tlsServer := httptest.NewTLSServer(manager)
	defer tlsServer.Close()
	server, err := NewServer(Options{ID: "02-aa-00-00-00-01", Secret: helloSecret(), CertDER: tlsServer.Certificate().Raw, Config: []byte(`{"Timestamp":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Register("02-aa-00-00-00-01", "", server); err != nil {
		t.Fatal(err)
	}
	url := "wss" + strings.TrimPrefix(tlsServer.URL, "https") + "/ws"
	ctx := context.Background()
	for _, name := range helloGoldens {
		golden := loadGoldenHello(t, name)
		for _, mutation := range helloMutations() {
			before := server.Counters()
			ap, err := fakeap.Connect(ctx, fakeap.Config{URL: url, ID: "02-aa-00-00-00-01", Secret: helloSecret(), HelloJSON: mutation.apply(t, golden)})
			if err != nil {
				t.Fatalf("%s %s: AP did not authenticate: %v", name, mutation.name, err)
			}
			after := waitCounters(t, server, func(c Counters) bool { return c.Connections > before.Connections })
			want := goldenExtras
			mutation.extras(&want)
			if status := server.Status(); !reflect.DeepEqual(status.Hello, want) || !status.Connected {
				t.Fatalf("%s %s: status hello\n got %+v\nwant %+v", name, mutation.name, status.Hello, want)
			}
			if got := after.DecodeErrors["hello"] - before.DecodeErrors["hello"]; got != uint64(len(mutation.errors)) {
				t.Fatalf("%s %s: counted %d hello decode errors, want %d", name, mutation.name, got, len(mutation.errors))
			}
			_ = ap.Close()
		}
	}
	if pending := manager.Pending(); len(pending) != 0 {
		t.Fatalf("authenticated AP left in the pending list: %+v", pending)
	}
}

func waitCounters(t *testing.T, server *Server, ready func(Counters) bool) Counters {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		counters := server.Counters()
		if ready(counters) {
			return counters
		}
		if time.Now().After(deadline) {
			t.Fatalf("counters never became ready: %+v", counters)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHelloExtrasNonObjectIsOneError(t *testing.T) {
	for _, body := range []string{`[]`, `"x"`, `null`, `{`, ``} {
		extras, failed := decodeHelloExtras([]byte(body))
		if !reflect.DeepEqual(extras, HelloExtras{}) || len(failed) != 1 || failed[0] != "hello" {
			t.Fatalf("%q: %+v %v", body, extras, failed)
		}
	}
}

func TestReply3WireFormatUnchanged(t *testing.T) {
	body, err := json.Marshal(Reply3{Version: 2, NextCounter: 43, Authed: true})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"version":2,"nextcounter":43,"authed":true,"features":{"protobuf_telemetry":false,"small_telemetry_header":false}}`
	if string(body) != want {
		t.Fatalf("Reply3 = %s", body)
	}
}

func TestRebootDetection(t *testing.T) {
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	previous := HelloExtras{BootCount: 75, Uptime: 3600}
	cases := []struct {
		name     string
		current  HelloExtras
		after    time.Duration
		rebooted bool
		known    bool
	}{
		{"reconnect, uptime advanced", HelloExtras{BootCount: 75, Uptime: 3900}, 5 * time.Minute, false, true},
		{"reconnect, within slack", HelloExtras{BootCount: 75, Uptime: 3880}, 5 * time.Minute, false, true},
		{"boot count rose", HelloExtras{BootCount: 76, Uptime: 4000}, 5 * time.Minute, true, true},
		{"uptime went back", HelloExtras{BootCount: 75, Uptime: 200}, 5 * time.Minute, true, true},
		{"uptime too low for the gap", HelloExtras{Uptime: 3700}, time.Hour, true, true},
		{"no facts", HelloExtras{}, time.Minute, false, false},
	}
	for _, c := range cases {
		rebooted, known := rebootDetected(previous, at, c.current, at.Add(c.after))
		if rebooted != c.rebooted || known != c.known {
			t.Errorf("%s: rebooted %t known %t", c.name, rebooted, known)
		}
	}
	if rebooted, known := rebootDetected(HelloExtras{}, time.Time{}, HelloExtras{BootCount: 1}, at); rebooted || known {
		t.Error("first hello must not count as a reboot")
	}
}

func TestHelloSummaryNamesRebootFacts(t *testing.T) {
	summary := goldenExtras.summary()
	for _, want := range []string{"boot count 75", "uptime 420s", "connect uptime 410s", "2026-01-02T03:04:05Z", "reason unknown", "warm boot", "local IP 192.0.2.10"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary %q lacks %q", summary, want)
		}
	}
}
