//go:build windows

package sessionapi

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
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

func (s FileSourceStore) verifyAcceptedAccess(file *os.File, _ os.FileInfo) error {
	if s.ProtectedDirectory == "" {
		return nil
	}
	handle := windows.Handle(file.Fd())
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.NumberOfLinks != 1 {
		return errors.New("accepted configuration has unexpected links")
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	return verifyServiceConfigurationDescriptor(sd)
}

func verifyServiceConfigurationDescriptor(sd *windows.SECURITY_DESCRIPTOR) error {
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if !serviceConfigurationPrincipal(owner) {
		return errors.New("accepted configuration has an unexpected owner")
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if dacl == nil || dacl.AceCount == 0 {
		return errors.New("accepted configuration has no service ACL")
	}
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
			!serviceConfigurationPrincipal((*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
			return errors.New("accepted configuration grants access outside the service ACL")
		}
	}
	return nil
}

func serviceConfigurationPrincipal(sid *windows.SID) bool {
	return sid != nil && (sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid))
}

func syncAcceptedDirectory(root *os.Root) error { return nil }
