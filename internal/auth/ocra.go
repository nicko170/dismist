package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
)

type Inputs struct {
	Secret     [32]byte
	Counter    uint64
	QC         [32]byte
	QS         [32]byte
	Context    [32]byte
	DateTimeNS uint64
}

func ServerProof(in Inputs) [32]byte {
	var payload [178]byte
	payload[0] = 1
	binary.BigEndian.PutUint64(payload[2:10], in.Counter)
	copy(payload[10:42], in.QC[:])
	copy(payload[42:74], in.QS[:])
	copy(payload[138:170], in.Context[:])
	binary.BigEndian.PutUint64(payload[170:178], in.DateTimeNS)
	mac := hmac.New(sha256.New, in.Secret[:])
	_, _ = mac.Write(payload[:])
	var proof [32]byte
	copy(proof[:], mac.Sum(nil))
	return proof
}
