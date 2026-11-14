package auth

// ClientProof is the AP's proof of its secret (AuthReply2.RC). It is the
// same HMAC as ServerProof with the two challenges swapped: in.QC is the
// AP's challenge (from its hello), in.QS the controller's (from its reply),
// and in.Context the version 2 context for Entity "CLIENT".
func ClientProof(in Inputs) [32]byte {
	in.QC, in.QS = in.QS, in.QC
	return ServerProof(in)
}
