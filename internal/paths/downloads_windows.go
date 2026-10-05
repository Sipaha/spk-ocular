package paths

import (
	"golang.org/x/sys/windows"
)

func nativeDownloads() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Downloads, windows.KF_FLAG_DEFAULT)
}
