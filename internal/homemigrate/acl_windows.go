//go:build windows

package homemigrate

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// RedoEnvACL replaces the DACL of the copied .env with a protected descriptor:
// FULL control for the current user and SYSTEM, nothing else, inheritance
// disabled (the "P" in the SDDL). This is the ACL-redo half of the task-846
// credential copy: the file must not silently inherit looser ACEs from the
// destination directory tree, and any stale grantee from the old location is
// dropped wholesale rather than merged.
//
// Report wording (交付报告口径)：重做方式 = 写完后以 SetNamedSecurityInfo
// 显式替换保护型 DACL（当前用户 SID + SYSTEM，FILE_ALL_ACCESS，OICI，
// PROTECTED_DACL_SECURITY_INFORMATION），不继承、不合并旧 ACL。
func RedoEnvACL(path string) error {
	dacl, err := currentUserProtectedDACL()
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil,
		dacl,
		nil,
	); err != nil {
		return fmt.Errorf("redo .env ACL: %w", err)
	}
	return nil
}

// currentUserProtectedDACL builds the ACL: current user + SYSTEM, full
// control, inheritable by children (OICI).
func currentUserProtectedDACL() (*windows.ACL, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, fmt.Errorf("open process token: %w", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("token user: %w", err)
	}
	sddl := fmt.Sprintf("D:P(A;OICI;FA;;;%s)(A;OICI;FA;;;SY)", user.User.Sid.String())
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("parse SDDL %q: %w", sddl, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return nil, err
	}
	return dacl, nil
}

// EnvACLOnDisk is the test probe: it reports the SDDL form of path's security
// descriptor and whether the DACL is protected from inheritance.
func EnvACLOnDisk(path string) (sddl string, protected bool, err error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "", false, err
	}
	if sd == nil {
		return "", false, fmt.Errorf("no security descriptor on %s", path)
	}
	control, _, err := sd.Control()
	if err != nil {
		return "", false, err
	}
	return sd.String(), control&windows.SE_DACL_PROTECTED != 0, nil
}
