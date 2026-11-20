//go:build windows

package sessionapi

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func prepareSourceDirectory(dir string, owner *SourceDirectoryOwner) error {
	if owner != nil {
		return errors.New("Unix source directory ownership is unsupported on Windows")
	}
	return os.MkdirAll(dir, 0o700)
}

func secureAcceptedDirectory(dir string, serviceDirectory bool) error {
	if !serviceDirectory {
		return nil
	}
	// This directory is below the service's own LocalAppData. Give inherited
	// full access to LocalSystem and administrators before creating any files.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func verifyAcceptedAccess(info os.FileInfo, owner *SourceDirectoryOwner) error { return nil }
func syncAcceptedDirectory(root *os.Root) error                                { return nil }
