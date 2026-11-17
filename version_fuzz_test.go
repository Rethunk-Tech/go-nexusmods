package nexusmods

import "testing"

// FuzzNewerFile holds that a file is never newer than itself, and two files cannot each be newer than the other.
func FuzzNewerFile(f *testing.F) {
	f.Add("2.0.0", int64(1), "1.9.0", int64(9))
	f.Add("1.0.0", int64(9), "1.0.0", int64(1))
	f.Add("not a version", int64(5), "1.0", int64(1))
	f.Add("", int64(0), "", int64(0))
	f.Fuzz(func(t *testing.T, a string, aID int64, b string, bID int64) {
		got := NewerFile(a, aID, b, bID)
		if a == b && aID == bID && got {
			t.Fatalf("a file is newer than itself: %q %d", a, aID)
		}
		if got && NewerFile(b, bID, a, aID) {
			t.Fatalf("both ways newer: %q %d vs %q %d", a, aID, b, bID)
		}
	})
}
