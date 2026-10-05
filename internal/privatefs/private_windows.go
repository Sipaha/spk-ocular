package privatefs

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// OwnerDescriptor grants only this user's SID access, with no inherited ACEs.
func OwnerDescriptor(inherit bool) (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	flags := ""
	if inherit {
		flags = "OICI"
	}
	return "D:P(A;" + flags + ";GA;;;" + user.User.Sid.String() + ")", nil
}

func EnsureDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	sddl, err := OwnerDescriptor(true)
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// CreateTemp sets the DACL at creation, before any content can be written.
// chmod(0600) alone does not make a file private on Windows.
func CreateTemp(dir, pattern string) (*os.File, error) {
	sddl, err := OwnerDescriptor(false)
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, err
	}
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))
	for range 100 {
		name := pattern
		if i := strings.LastIndexByte(pattern, '*'); i >= 0 {
			name = pattern[:i] + rand.Text() + pattern[i+1:]
		} else {
			name += rand.Text()
		}
		path := filepath.Join(dir, name)
		file, err := createNew(path, &sa)
		if errors.Is(err, windows.ERROR_FILE_EXISTS) {
			continue
		}
		return file, err
	}
	return nil, os.ErrExist
}

func CreateNew(path string) (*os.File, error) {
	descriptor, err := OwnerDescriptor(false)
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString(descriptor)
	if err != nil {
		return nil, err
	}
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))
	return createNew(path, &sa)
}

func createNew(path string, sa *windows.SecurityAttributes) (*os.File, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(ptr, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "create", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}
