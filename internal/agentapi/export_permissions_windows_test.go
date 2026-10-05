package agentapi

import (
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
	"testing"
	"unsafe"
)

func assertPrivateExport(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	require.NoError(t, err)
	control, _, err := sd.Control()
	require.NoError(t, err)
	require.NotZero(t, control&windows.SE_DACL_PROTECTED)
	acl, _, err := sd.DACL()
	require.NoError(t, err)
	require.NotNil(t, acl)
	require.Equal(t, uint16(1), acl.AceCount)
	var ace *windows.ACCESS_ALLOWED_ACE
	require.NoError(t, windows.GetAce(acl, 0, &ace))
	require.Equal(t, uint8(windows.ACCESS_ALLOWED_ACE_TYPE), ace.Header.AceType)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	require.True(t, (*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(user.User.Sid))
}
