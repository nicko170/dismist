// Package auth implements the cryptography of the Mist ep-auth version 2
// handshake between an AP and its controller: the context hash that binds
// a proof to the AP's identity, the TLS certificate and cipher suite, and
// the OCRA-style HMAC proofs both sides exchange.
//
// The controller side (internal/controller) uses ServerProof to answer the
// AP's hello and to check the AP's proof; tools/fakeap uses ClientProof to
// play the AP. Owned by workstream B (design §8.1). The proofs are frozen:
// a change here breaks authentication of every real AP, so every function
// is pinned by an independent test vector.
package auth
