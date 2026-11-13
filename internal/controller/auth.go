package controller

import (
	"crypto/hmac"
	"errors"
	"math"

	"github.com/nicko170/dismist/internal/auth"
)

var ErrAuthentication = errors.New("AP authentication failed")

type Hello struct {
	Version  int    `json:"version"`
	ID       string `json:"id"`
	AuthType string `json:"AuthType"`
	Model    string `json:"model"`
	Firmware string `json:"firmware"`
	Suite    int    `json:"suite"`
	Counter  uint64 `json:"counter"`
	QC       []byte `json:"QC"`
	ENV      string `json:"ENV"`
}

type Reply1 struct {
	Version    int    `json:"version"`
	RS         []byte `json:"RS"`
	Suite      int    `json:"suite"`
	DateTimeNS uint64 `json:"datetime_ns"`
	QS         []byte `json:"QS"`
	ENV        string `json:"ENV"`
	ServerName string `json:"ServerName"`
}

type Reply2 struct {
	Version        int    `json:"version"`
	NextDateTimeNS uint64 `json:"nextdatetime_ns"`
	Authed         bool   `json:"authed"`
	RC             []byte `json:"RC"`
}

type Features struct {
	ProtobufTelemetry    bool `json:"protobuf_telemetry"`
	SmallTelemetryHeader bool `json:"small_telemetry_header"`
}

type Reply3 struct {
	Version     int      `json:"version"`
	NextCounter uint64   `json:"nextcounter"`
	Authed      bool     `json:"authed"`
	Features    Features `json:"features"`
}

type Authenticator struct {
	ID          string
	Secret      [32]byte
	CertDER     []byte
	CipherSuite uint16
	ServerName  string
}

func (a Authenticator) context(hello Hello, entity string) [32]byte {
	return auth.Version2Context(auth.ContextInputs{
		Version: hello.Version, ID: hello.ID, Model: hello.Model,
		Firmware: hello.Firmware, Suite: hello.Suite, Cert: a.CertDER,
		CipherSuite: a.CipherSuite, Entity: entity,
	})
}

func (a Authenticator) Reply1(hello Hello, qs [32]byte, datetimeNS uint64) (Reply1, error) {
	if hello.Version != 2 || hello.ID != a.ID || hello.AuthType != "ap" ||
		hello.Suite != 1 || hello.Model == "" || hello.Firmware == "" || len(hello.QC) != 32 {
		return Reply1{}, ErrAuthentication
	}
	var qc [32]byte
	copy(qc[:], hello.QC)
	proof := auth.ServerProof(auth.Inputs{
		Secret: a.Secret, Counter: hello.Counter, QC: qc, QS: qs,
		Context: a.context(hello, "SERVER"), DateTimeNS: datetimeNS,
	})
	return Reply1{
		Version: 2, RS: proof[:], Suite: 1, DateTimeNS: datetimeNS,
		QS: append([]byte(nil), qs[:]...), ENV: hello.ENV, ServerName: a.ServerName,
	}, nil
}

func (a Authenticator) Reply3(hello Hello, reply1 Reply1, reply2 Reply2) (Reply3, error) {
	if reply2.Version != 2 || !reply2.Authed || hello.Counter == math.MaxUint64 ||
		reply2.NextDateTimeNS != reply1.DateTimeNS+1 || len(reply2.RC) != 32 || len(reply1.QS) != 32 {
		return Reply3{}, ErrAuthentication
	}
	var qc, qs [32]byte
	copy(qc[:], hello.QC)
	copy(qs[:], reply1.QS)
	expected := auth.ServerProof(auth.Inputs{
		Secret: a.Secret, Counter: hello.Counter, QC: qs, QS: qc,
		Context: a.context(hello, "CLIENT"), DateTimeNS: reply1.DateTimeNS,
	})
	if !hmac.Equal(expected[:], reply2.RC) {
		return Reply3{}, ErrAuthentication
	}
	return Reply3{Version: 2, NextCounter: hello.Counter + 1, Authed: true}, nil
}
