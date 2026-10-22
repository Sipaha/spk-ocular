package agentapi

import (
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestWindowsMCPProfileAndCredentialHaveProtectedOwnerACL(t *testing.T) {
	profile := t.TempDir()
	listener, lock, _, err := openEndpoint(profile)
	require.NoError(t, err)
	defer func() { _ = listener.Close(); _ = lock.Unlock() }()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	for _, name := range []string{profile, filepath.Join(profile, "mcp.json"), filepath.Join(profile, "mcp.lock")} {
		descriptor, err := windows.GetNamedSecurityInfo(name, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		require.NoError(t, err)
		control, _, err := descriptor.Control()
		require.NoError(t, err)
		require.NotZero(t, control&windows.SE_DACL_PROTECTED)
		acl, _, err := descriptor.DACL()
		require.NoError(t, err)
		require.NotNil(t, acl)
		require.Equal(t, uint16(1), acl.AceCount)
		var ace *windows.ACCESS_ALLOWED_ACE
		require.NoError(t, windows.GetAce(acl, 0, &ace))
		require.Equal(t, uint8(windows.ACCESS_ALLOWED_ACE_TYPE), ace.Header.AceType)
		require.True(t, (*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(user.User.Sid))
	}
}
