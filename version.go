package nexusmods

import "github.com/Rethunk-Tech/go-semverish"

// NewerFile reports whether file a (version a, file id aID) is newer than file b of the same mod: by version where
// both parse and differ, else the later upload, which has the higher file id. Nexus file ids grow with each upload,
// so the id breaks a tie when an author re-uploads a fix under the same version.
func NewerFile(a string, aID int64, b string, bID int64) bool {
	if c, ok := semverish.Compare(a, b); ok && c != 0 {
		return c > 0
	}
	return aID > bID
}
