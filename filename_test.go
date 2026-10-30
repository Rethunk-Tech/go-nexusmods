package nexusmods

import "testing"

func TestParseArchiveName(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want ArchiveName
		ok   bool
	}{
		{"ContentPatcher-1915-2-8-2-1700000000.zip", ArchiveName{"ContentPatcher", 1915, "2-8-2", 1700000000, "zip"}, true},
		{"A_Mod-Name-42-1-0-1700000001.7z", ArchiveName{"A_Mod-Name", 42, "1-0", 1700000001, "7z"}, true},
		{"Mod-1234-1-0-0-beta-1700000000.zip", ArchiveName{"Mod", 1234, "1-0-0-beta", 1700000000, "zip"}, true},
		{`C:\Downloads\Mod-7-1-1700000000.RAR`, ArchiveName{"Mod", 7, "1", 1700000000, "rar"}, true},
		{"/home/u/Downloads/Mod-7-1-1700000000.zip", ArchiveName{"Mod", 7, "1", 1700000000, "zip"}, true},
		{"Mod-0-1-1700000000.zip", ArchiveName{}, false},
		{"Mod-7-1-12345.zip", ArchiveName{}, false},
		{"Mod-7-1-1700000000.tar", ArchiveName{}, false},
		{"random.zip", ArchiveName{}, false},
	} {
		got, ok := ParseArchiveName(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: got %+v %v, want %+v %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
