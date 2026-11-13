package auth

import (
	"crypto/sha256"
	"fmt"
)

type ContextInputs struct {
	Version     int
	ID          string
	Model       string
	Firmware    string
	Suite       int
	Cert        []byte
	CipherSuite uint16
	Entity      string
}

func Version2Context(in ContextInputs) [32]byte {
	text := fmt.Sprintf(
		"START, Version %d, ID %s, Model %s, Firmware %s, Suite %d, Cert %x, CipherSuite %d, Entity %s, END",
		in.Version, in.ID, in.Model, in.Firmware, in.Suite, in.Cert, in.CipherSuite, in.Entity,
	)
	return sha256.Sum256([]byte(text))
}
