package api

import (
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"encoding/base64"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAtomicRenameProgramsMatchSourcesAndNeedNoInterpreter(t *testing.T) {
	for _, p := range []struct {
		arch, encoded, sourceHash string
		machine                   elf.Machine
	}{{"amd64", renameHelperAMD64, renameHelperAMD64SourceSHA256, elf.EM_X86_64}, {"arm64", renameHelperARM64, renameHelperARM64SourceSHA256, elf.EM_AARCH64}} {
		t.Run(p.arch, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join("renamehelper", p.arch+".S"))
			require.NoError(t, err)
			hash := sha256.Sum256(source)
			require.Equal(t, p.sourceHash, hex.EncodeToString(hash[:]), "regenerate after changing assembler source")
			image, err := base64.StdEncoding.DecodeString(p.encoded)
			require.NoError(t, err)
			program, err := elf.NewFile(bytes.NewReader(image))
			require.NoError(t, err)
			require.Equal(t, p.machine, program.Machine)
			require.Equal(t, elf.ELFCLASS64, program.Class)
			stack := false
			for _, segment := range program.Progs {
				require.NotEqual(t, elf.PT_INTERP, segment.Type)
				if segment.Type == elf.PT_GNU_STACK {
					stack = true
					require.Zero(t, segment.Flags&elf.PF_X)
				}
			}
			require.True(t, stack)
		})
	}
}
func TestAtomicRenameKernelRejectsDestinationWithoutAnyShellPrecheck(t *testing.T) {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("Linux kernel helper; both ELF architectures are validated separately")
	}
	encoded := renameHelperAMD64
	if runtime.GOARCH == "arm64" {
		encoded = renameHelperARM64
	}
	image, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	root := t.TempDir()
	program := filepath.Join(root, "rename")
	require.NoError(t, os.WriteFile(program, image, 0700))
	source, destination := filepath.Join(root, "source"), filepath.Join(root, "destination")
	require.NoError(t, os.WriteFile(source, []byte("source data"), 0600))
	require.NoError(t, os.WriteFile(destination, []byte("concurrent destination"), 0600))
	err = exec.CommandContext(t.Context(), program, source, destination).Run()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, int(syscall.EEXIST), exit.ExitCode())
	data, err := os.ReadFile(destination)
	require.NoError(t, err)
	require.Equal(t, "concurrent destination", string(data))
	data, err = os.ReadFile(source)
	require.NoError(t, err)
	require.Equal(t, "source data", string(data))
	require.NoError(t, os.Remove(destination))
	require.NoError(t, exec.CommandContext(t.Context(), program, source, destination).Run())
	_, err = os.Stat(source)
	require.True(t, os.IsNotExist(err))
	data, err = os.ReadFile(destination)
	require.NoError(t, err)
	require.Equal(t, "source data", string(data))
}
