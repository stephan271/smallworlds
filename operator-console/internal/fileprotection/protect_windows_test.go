//go:build windows

package fileprotection

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestSecureFileACLAllowsOnlyCurrentUser(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "launcher-state")
	if err := SecureDirectory(directory); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "launcher.vault.age")
	if err := os.WriteFile(path, []byte("encrypted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SecureFile(path); err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("protected launcher file inherits permissions from its parent")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil {
		t.Fatal("launcher file has no protected DACL")
	}
	if dacl.AceCount != 1 {
		t.Fatalf("launcher file ACE count = %d, want one current-user entry", dacl.AceCount)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !aceSID.Equals(currentUser.User.Sid) {
		t.Fatal("launcher file ACL does not grant its sole entry to the current user")
	}
}
