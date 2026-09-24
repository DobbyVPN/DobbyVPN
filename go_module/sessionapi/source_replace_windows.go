//go:build windows

package sessionapi

import "golang.org/x/sys/windows"

func replaceSourceFile(temporary, destination string) error {
	temporaryPath, err := windows.UTF16PtrFromString(temporary)
	if err != nil {
		return err
	}
	destinationPath, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		temporaryPath,
		destinationPath,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}
