package desktop

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type downloadEntry struct {
	name, link string
	kind       byte
	data       []byte
}

func downloadArchive(t *testing.T, entries ...downloadEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, e := range entries {
		size := int64(0)
		if e.kind == tar.TypeReg {
			size = int64(len(e.data))
		}
		require.NoError(t, w.WriteHeader(&tar.Header{Name: e.name, Linkname: e.link, Typeflag: e.kind, Size: size, Mode: 0644}))
		if size > 0 {
			_, err := w.Write(e.data)
			require.NoError(t, err)
		}
	}
	require.NoError(t, w.Close())
	return b.Bytes()
}
func archiveStream(b []byte) func(context.Context, io.Writer) error {
	return func(_ context.Context, w io.Writer) error { _, err := w.Write(b); return err }
}
func TestContainerDownloadBinaryAndFolder(t *testing.T) {
	destination := t.TempDir()
	binary := bytes.Repeat([]byte{0, 255, 13, 10, 128}, 600000)
	archive := downloadArchive(t, downloadEntry{name: "./binary.dat", kind: tar.TypeReg, data: binary})
	got, err := copyContainerDownload(t.Context(), destination, "binary.dat", archiveStream(archive))
	require.NoError(t, err)
	actual, err := os.ReadFile(got)
	require.NoError(t, err)
	require.Equal(t, binary, actual)
	_, err = copyContainerDownload(t.Context(), destination, "binary.dat", archiveStream(archive))
	require.ErrorContains(t, err, "already exists")
	folder := downloadArchive(t, downloadEntry{name: "./folder/", kind: tar.TypeDir}, downloadEntry{name: "./folder/empty/", kind: tar.TypeDir}, downloadEntry{name: "./folder/.hidden", kind: tar.TypeReg, data: []byte("привет")}, downloadEntry{name: "./folder/hard", kind: tar.TypeLink, link: "./folder/.hidden"}, downloadEntry{name: "./folder/link", kind: tar.TypeSymlink, link: ".hidden"})
	got, err = copyContainerDownload(t.Context(), destination, "folder", archiveStream(folder))
	require.NoError(t, err)
	actual, err = os.ReadFile(filepath.Join(got, "hard"))
	require.NoError(t, err)
	require.Equal(t, "привет", string(actual))
	target, err := os.Readlink(filepath.Join(got, "link"))
	require.NoError(t, err)
	require.Equal(t, ".hidden", target)
	info, err := os.Stat(filepath.Join(got, "empty"))
	require.NoError(t, err)
	require.True(t, info.IsDir())
	leftovers, err := filepath.Glob(filepath.Join(destination, ".ocular*"))
	require.NoError(t, err)
	require.Empty(t, leftovers)
}
func TestContainerDownloadRejectsUnsafeArchivesAndFailedStreams(t *testing.T) {
	for _, entry := range []downloadEntry{
		{name: "../escape", kind: tar.TypeReg}, {name: "/absolute", kind: tar.TypeReg}, {name: "./folder/../escape", kind: tar.TypeReg},
		{name: "./folder/link", kind: tar.TypeSymlink, link: "../../escape"}, {name: "./folder/link", kind: tar.TypeSymlink, link: "/etc/passwd"},
		{name: "./folder/pipe", kind: tar.TypeFifo}, {name: "./folder/C:ads", kind: tar.TypeReg}, {name: "./folder/CON.txt", kind: tar.TypeReg},
	} {
		t.Run(entry.name+entry.link, func(t *testing.T) {
			destination := t.TempDir()
			_, err := copyContainerDownload(t.Context(), destination, "folder", archiveStream(downloadArchive(t, entry)))
			require.Error(t, err)
			entries, err := os.ReadDir(destination)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
	t.Run("remote failure", func(t *testing.T) {
		destination := t.TempDir()
		b := downloadArchive(t, downloadEntry{name: "./file", kind: tar.TypeReg, data: []byte("partial")})
		_, err := copyContainerDownload(t.Context(), destination, "file", func(_ context.Context, w io.Writer) error { _, _ = w.Write(b); return errors.New("connection lost") })
		require.ErrorContains(t, err, "connection lost")
		entries, err := os.ReadDir(destination)
		require.NoError(t, err)
		require.Empty(t, entries)
	})
	t.Run("duplicate", func(t *testing.T) {
		destination := t.TempDir()
		e := downloadEntry{name: "./file", kind: tar.TypeReg}
		_, err := copyContainerDownload(t.Context(), destination, "file", archiveStream(downloadArchive(t, e, e)))
		require.ErrorContains(t, err, "duplicate")
	})
	t.Run("symlink parent", func(t *testing.T) {
		destination := t.TempDir()
		_, err := copyContainerDownload(t.Context(), destination, "folder", archiveStream(downloadArchive(t, downloadEntry{name: "./folder/link", kind: tar.TypeSymlink, link: "other"}, downloadEntry{name: "./folder/link/file", kind: tar.TypeReg})))
		require.ErrorContains(t, err, "non-directory")
	})
}
func TestContainerDownloadRoot(t *testing.T) {
	b := downloadArchive(t, downloadEntry{name: "./", kind: tar.TypeDir}, downloadEntry{name: "./root/", kind: tar.TypeDir}, downloadEntry{name: "./root/file", kind: tar.TypeReg, data: []byte("nested")}, downloadEntry{name: "./etc/", kind: tar.TypeDir})
	got, err := copyContainerDownload(t.Context(), t.TempDir(), "/", archiveStream(b))
	require.NoError(t, err)
	actual, err := os.ReadFile(filepath.Join(got, "root", "file"))
	require.NoError(t, err)
	require.Equal(t, "nested", string(actual))
}

func TestContainerDownloadDestinationAppearsWhileStreaming(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(fmt.Sprint(directory), func(t *testing.T) {
			destination := t.TempDir()
			name := "source"
			kind := byte(tar.TypeReg)
			if directory {
				kind = tar.TypeDir
			}
			b := downloadArchive(t, downloadEntry{name: "./source", kind: kind, data: []byte("source")})
			_, err := copyContainerDownload(t.Context(), destination, name, func(_ context.Context, w io.Writer) error {
				if directory {
					require.NoError(t, os.Mkdir(filepath.Join(destination, name), 0700))
					require.NoError(t, os.WriteFile(filepath.Join(destination, name, "keep"), []byte("existing"), 0600))
				} else {
					require.NoError(t, os.WriteFile(filepath.Join(destination, name), []byte("existing"), 0600))
				}
				_, e := w.Write(b)
				return e
			})
			require.Error(t, err)
			p := filepath.Join(destination, name)
			if directory {
				p = filepath.Join(p, "keep")
			}
			actual, e := os.ReadFile(p)
			require.NoError(t, e)
			require.Equal(t, "existing", string(actual))
		})
	}
}
