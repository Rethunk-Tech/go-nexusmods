package nexusmods

import "testing"

func FuzzParseArchiveName(f *testing.F) {
	f.Add("ContentPatcher-1915-2-8-2-1700000000.zip")
	f.Add("A_Mod-Name-42-1-0-1700000001.7z")
	f.Add("Mod-1234-1-0-0-beta-1700000000.zip")
	f.Add(`C:\Downloads\Mod-7-1-1700000000.RAR`)
	f.Add("/home/u/Downloads/Mod-7-1-1700000000.zip")
	f.Add("Mod-0-1-1700000000.zip")
	f.Add("Mod-7-1-12345.zip")
	f.Add("Mod-7-1-1700000000.tar")
	f.Add("random.zip")
	f.Add("")
	f.Fuzz(func(t *testing.T, name string) {
		got, ok := ParseArchiveName(name)
		if !ok {
			return
		}
		if got.Name == "" || got.ModID <= 0 || got.Version == "" || got.Timestamp < 100_000_000 ||
			(got.Ext != "zip" && got.Ext != "7z" && got.Ext != "rar") {
			t.Fatalf("accepted invalid archive name %q: %+v", name, got)
		}
	})
}
