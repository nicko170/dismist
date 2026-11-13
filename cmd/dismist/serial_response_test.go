package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
)

const syntheticChallenge = "BQXwwMi0xMS0yMi0zMy00NC01NXxyb290fAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f"

func TestSerialResponseMatchesLegacyVector(t *testing.T) {
	clientNonce := make([]byte, 32)
	for i := range clientNonce {
		clientNonce[i] = byte(i + 32)
	}
	got, err := serialResponse(syntheticChallenge, bytes.NewReader(clientNonce))
	if err != nil {
		t.Fatal(err)
	}
	const want = "BICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj/ME6+1zZ+l45CtgHHWvPyjwBrrFxFJaGaS+uxai1UnnA=="
	if got != want {
		t.Fatalf("response mismatch:\ngot  %s\nwant %s", got, want)
	}
}

func TestSerialResponseAcceptsDelimiterInBinaryNonce(t *testing.T) {
	clientNonce := make([]byte, 32)
	for i := range clientNonce {
		clientNonce[i] = byte(i + 32)
	}
	const challenge = "BQXwwMi1hYS1iYi1jYy1kZC1lZXxyb290fHwBAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f"
	const want = "BICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj+6L6mgqT8iVkLn4r/90U1KiZA9f5rHQKfbNyehWRCZcA=="
	got, err := serialResponse(challenge, bytes.NewReader(clientNonce))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("response mismatch:\ngot  %s\nwant %s", got, want)
	}
}

func TestSerialResponseRejectsMalformedChallenges(t *testing.T) {
	cases := []struct {
		name      string
		challenge string
		message   string
	}{
		{"missing prefix", "AQ==", "prefix"},
		{"bad base64", "B%%%", "base64"},
		{"signed mode", encodedChallenge([]byte("C\x04root")), "C-mode"},
		{"other mode", encodedChallenge([]byte("Z|02-11-22-33-44-55|root|" + string(make([]byte, 32)))), "A-mode"},
		{"other user", encodedChallenge([]byte("A|02-11-22-33-44-55|admin|" + string(make([]byte, 32)))), "root"},
		{"short MAC", encodedChallenge([]byte("A|02-11-22-33-44|root|" + string(make([]byte, 32)))), "MAC"},
		{"colon MAC", encodedChallenge([]byte("A|02:11:22:33:44:55|root|" + string(make([]byte, 32)))), "MAC"},
		{"nonhex MAC", encodedChallenge([]byte("A|02-11-22-33-44-gg|root|" + string(make([]byte, 32)))), "MAC"},
		{"short nonce", encodedChallenge([]byte("A|02-11-22-33-44-55|root|short")), "nonce"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := serialResponse(tc.challenge, bytes.NewReader(make([]byte, 32)))
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("expected %q error, got %v", tc.message, err)
			}
		})
	}
}

func TestSerialResponsePropagatesRandomnessFailure(t *testing.T) {
	_, err := serialResponse(syntheticChallenge, io.LimitReader(bytes.NewReader([]byte{1}), 1))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected short random read error, got %v", err)
	}
}

func TestSerialResponseCLIPrintsOnlyResponse(t *testing.T) {
	var output bytes.Buffer
	if err := run([]string{"ap33", "serial-response", syntheticChallenge}, &output); err != nil {
		t.Fatal(err)
	}
	result := output.String()
	if !strings.HasPrefix(result, "B") || strings.Count(result, "\n") != 1 || !strings.HasSuffix(result, "\n") {
		t.Fatalf("unexpected command output %q", result)
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSuffix(result, "\n")[1:])
	if err != nil || len(raw) != 64 {
		t.Fatalf("invalid encoded response: %v, length %d", err, len(raw))
	}
}

func TestSerialResponseCLIRejectsInvalidArgumentsWithoutOutput(t *testing.T) {
	for _, args := range [][]string{
		{"ap33"},
		{"ap33", "serial-response"},
		{"ap33", "serial-response", syntheticChallenge, "extra"},
		{"ap33", "serial-response", "B%%%"},
	} {
		var output bytes.Buffer
		if err := run(args, &output); err == nil || output.Len() != 0 {
			t.Fatalf("args %q: expected error and no output, got %q, %v", args, output.String(), err)
		}
	}
}

func encodedChallenge(raw []byte) string {
	return "B" + base64.StdEncoding.EncodeToString(raw)
}
