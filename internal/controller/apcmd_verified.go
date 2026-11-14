package controller

// apCmdVerified lists the firmware versions on which the ap-cmd-rx wire
// format (apcmd.go) has been verified on a live AP, with what was
// verified. It is empty in v1: the format was recovered statically from
// 0.15 ep-control and has not been run on an AP yet. Add a version only
// with a recorded run in docs/verification.md (design §8.8).
var apCmdVerified = map[string]string{}

// APCmdVerified reports whether ap-cmd-rx commands have been verified on
// the firmware version.
func APCmdVerified(firmware string) bool {
	_, ok := apCmdVerified[firmware]
	return ok
}
