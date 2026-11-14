package telemetry

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/nicko170/dismist/internal/telemetry/pbtest"
)

// TestPBTestReproducesCapturedShape checks that pbtest encodes exactly the
// bytes of the captured-shape fixture in decode_test.go, so synthetic
// frames built with it are what the AP sends.
func TestPBTestReproducesCapturedShape(t *testing.T) {
	want, _ := hex.DecodeString("500a479001b7039a0107415033332d5757a2011a617066772d302e382e32313735382d686f747069652d623565618202191a0602aa000000042205776c302e312a085465737453534944120e61702d73746174732d66756c6c2d1a1130322d61612d30302d30302d30302d3033")
	vap := pbtest.New().Raw(3, []byte{0x02, 0xaa, 0, 0, 0, 0x04}).String(4, "wl0.1").String(5, "TestSSID")
	stats := pbtest.New().Varint(18, 439).String(19, "AP33-WW").String(20, "apfw-0.8.21758-hotpie-b5ea").Message(32, vap)
	got := pbtest.Frame("ap-stats-full-", "02-aa-00-00-00-03", stats)
	if !bytes.Equal(got, want) {
		t.Fatalf("pbtest frame differs:\n got %x\nwant %x", got, want)
	}
	decoded, err := Decode(pbtest.Gzip(got))
	if err != nil || decoded.UptimeSeconds != 439 || len(decoded.VAPs) != 1 {
		t.Fatalf("gzip pbtest frame: %+v %v", decoded, err)
	}
}

func TestPBTestScalarEncodings(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want string
	}{
		{"zigzag -62", pbtest.New().Zigzag(8, -62).Bytes(), "407b"},
		{"zigzag 0", pbtest.New().Zigzag(8, 0).Bytes(), "4000"},
		{"double", pbtest.New().Double(60, 1.5).Bytes(), "e103000000000000f83f"},
		{"float", pbtest.New().Float(1, 0.5).Bytes(), "0d0000003f"},
		{"packed", pbtest.New().PackedVarint(79, 10, 300).Bytes(), "fa04030aac02"},
		{"bool", pbtest.New().Bool(21, true).Bytes(), "a80101"},
		{"negative int", pbtest.New().Int(1, -1).Bytes(), "08ffffffffffffffffff01"},
	}
	for _, c := range cases {
		if hex.EncodeToString(c.got) != c.want {
			t.Errorf("%s = %x, want %s", c.name, c.got, c.want)
		}
	}
	fields, err := scan(pbtest.New().Zigzag(8, -62).Fixed64(9, 7).Fixed32(10, 3).Bytes())
	if err != nil || len(fields) != 3 || zigzag(fields[0].value) != -62 {
		t.Fatalf("decoder does not read pbtest scalars: %+v %v", fields, err)
	}
}
