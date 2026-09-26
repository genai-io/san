package session

import (
	"runtime"
	"strings"
)

// EncodePath turns a directory into the flat name its sessions are stored under.
func EncodePath(path string) string {
	path = strings.TrimRight(path, "/")
	// On Windows, the working directory contains backslashes and a colon
	// (e.g. D:\\go-project\\workspace).  These characters are not valid in
	// directory names under Linux (where the session store lives) and the
	// colon causes filepath.Join to produce an invalid path when the
	// encoded value is used as a subdirectory name.
	if runtime.GOOS == "windows" {
		path = strings.ReplaceAll(path, ":", "-")
		path = strings.ReplaceAll(path, "\\", "-")
	}
	return strings.ReplaceAll(path, "/", "-")
}
