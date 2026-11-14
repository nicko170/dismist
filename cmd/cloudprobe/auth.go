package main

import "github.com/nicko170/dismist/internal/auth"

func clientProof(secret [32]byte, counter uint64, qc, qs, context [32]byte, datetimeNS uint64) [32]byte {
	return auth.ServerProof(auth.Inputs{
		Secret: secret, Counter: counter, QC: qs, QS: qc,
		Context: context, DateTimeNS: datetimeNS,
	})
}
