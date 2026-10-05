package privatefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestWindowsPrivateFilesAndDirectoryHaveProtectedOwnerACL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	require.NoError(t, EnsureDir(dir))
	f, err := CreateTemp(dir, "logs-*.log")
	require.NoError(t, err)
	_, err = f.WriteString("private log")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	for _, path := range []string{dir, f.Name()} {
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
		require.True(t, ace.Mask&windows.GENERIC_ALL != 0 || ace.Mask&0x001f01ff == 0x001f01ff)
	}
	_, err = CreateNew(f.Name())
	require.ErrorIs(t, err, os.ErrExist)
	require.True(t, strings.HasSuffix(f.Name(), ".log"))
	data, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	require.Equal(t, "private log", string(data))
}
