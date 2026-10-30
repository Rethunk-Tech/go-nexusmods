package nexusmods

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

var archiveName = regexp.MustCompile(`(?i)^(.+?)-(\d+)-([0-9a-z]+(?:-[0-9a-z]+)*)-(\d{9,})\.(zip|7z|rar)$`)

// ArchiveName is what a Nexus download's file name carries.
type ArchiveName struct {
	// Name is the mod's name as written in the file name, where Nexus has turned spaces into underscores.
	Name  string
	ModID int
	// Version is the file's version with its dots written as '-', as in the file name ("2-8-2").
	Version string
	// Timestamp is the Unix time Nexus stamped on the download.
	Timestamp int64
	// Ext is the archive extension, lower case, without the dot.
	Ext string
}

// ParseArchiveName reads the name Nexus gives a downloaded archive, "<name>-<mod id>-<version>-<timestamp>.<ext>"
// with ext zip, 7z or rar. A directory part is ignored. ok is false for any other name or a mod id of 0. A name
// that itself holds "-<digits>-" is read at its first such span, since the pattern cannot tell them apart.
func ParseArchiveName(file string) (ArchiveName, bool) {
	m := archiveName.FindStringSubmatch(path.Base(strings.ReplaceAll(file, `\`, "/")))
	if m == nil {
		return ArchiveName{}, false
	}
	id, err := strconv.Atoi(m[2])
	if err != nil || id <= 0 {
		return ArchiveName{}, false
	}
	ts, err := strconv.ParseInt(m[4], 10, 64)
	if err != nil {
		return ArchiveName{}, false
	}
	return ArchiveName{Name: m[1], ModID: id, Version: m[3], Timestamp: ts, Ext: strings.ToLower(m[5])}, true
}
