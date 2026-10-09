package desktop

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// copyContainerDownload stages a complete export privately. Stream errors or
// unsafe entries discard the staging tree and leave existing destinations alone.
func copyContainerDownload(ctx context.Context, directory, name string, stream func(context.Context, io.Writer) error) (string, error) {
	rootSource := name == "/" || name == "."
	if rootSource {
		name = "root"
	}
	if !safeDownloadPath(name) || strings.Contains(name, "/") {
		return "", errors.New("source name cannot be represented safely on this computer")
	}
	parent, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer parent.Close()
	if _, err = parent.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
		if err == nil {
			err = fmt.Errorf("destination already exists: %s", name)
		}
		return "", err
	}
	stage, err := os.MkdirTemp(directory, ".ocular-file-download-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err = os.Chmod(stage, 0700); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(stage)
	if err != nil {
		return "", err
	}
	defer root.Close()
	reader, writer := io.Pipe()
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() { e := stream(child, writer); _ = writer.CloseWithError(e); result <- e }()
	err = extractContainerDownload(root, reader, name, rootSource)
	if err != nil {
		cancel()
		_ = reader.CloseWithError(err)
	} else { // Drain padding and wait for the remote exit status before publishing.
		_, err = io.Copy(io.Discard, reader)
	}
	_ = reader.Close()
	remoteErr := <-result
	if err != nil {
		return "", err
	}
	if remoteErr != nil {
		return "", remoteErr
	}
	info, err := root.Lstat(name)
	if err != nil {
		return "", fmt.Errorf("download contains no source entry: %w", err)
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return "", errors.New("source is not a regular file or directory")
	}
	if err = publishDownload(parent, directory, filepath.Join(filepath.Base(stage), name), name); err != nil {
		return "", err
	}

	return filepath.Join(directory, name), nil
}

func safeDownloadPath(p string) bool {
	if !fs.ValidPath(p) || p == "." || !filepath.IsLocal(p) || strings.ContainsAny(p, "\\:\x00") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9') {
			return false
		}
	}
	return true
}

func extractContainerDownload(root *os.Root, input io.Reader, name string, rootSource bool) error {
	tr := tar.NewReader(input)
	seen := map[string]byte{}
	type link struct {
		name, target string
		symbolic     bool
	}
	var links []link
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		p := strings.TrimPrefix(h.Name, "./")
		p = strings.TrimSuffix(p, "/")
		if p == "." || p == "" {
			p = name
		} else if rootSource {
			p = name + "/" + p
		}
		if !safeDownloadPath(p) || (p != name && !strings.HasPrefix(p, name+"/")) {
			return fmt.Errorf("unsafe archive path: %q", h.Name)
		}
		if len(seen) >= 100000 {
			return errors.New("download exceeds 100000 entries")
		}
		if _, exists := seen[p]; exists {
			return fmt.Errorf("duplicate archive path: %q", p)
		}
		for ancestor := path.Dir(p); ancestor != "."; ancestor = path.Dir(ancestor) {
			if kind, exists := seen[ancestor]; exists && kind != tar.TypeDir {
				return fmt.Errorf("non-directory archive parent: %q", ancestor)
			}
		}
		seen[p] = h.Typeflag
		if err = root.MkdirAll(filepath.FromSlash(path.Dir(p)), 0700); err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err = root.MkdirAll(filepath.FromSlash(p), 0700); err != nil {
				return err
			}
		case tar.TypeReg:
			f, e := root.OpenFile(filepath.FromSlash(p), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return e
			}
			_, e = io.Copy(f, tr)
			if e == nil {
				e = f.Sync()
			}
			closeErr := f.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink, tar.TypeLink:
			target := h.Linkname
			if h.Typeflag == tar.TypeSymlink {
				if strings.HasPrefix(target, "/") || strings.ContainsAny(target, "\\:\x00") {
					return fmt.Errorf("unsafe symbolic link: %q", p)
				}
				target = path.Clean(path.Join(path.Dir(p), target))
			} else {
				target = strings.TrimPrefix(target, "./")
				if rootSource {
					target = path.Join(name, target)
				}
			}
			if !safeDownloadPath(target) || (target != name && !strings.HasPrefix(target, name+"/")) {
				return fmt.Errorf("link leaves downloaded folder: %q", p)
			}
			links = append(links, link{p, target, h.Typeflag == tar.TypeSymlink})
		default:
			return fmt.Errorf("unsupported archive entry (devices, sockets and FIFOs are not copied): %q", p)
		}
	}
	// Links are created last: no archive write can follow a symlink.
	for _, l := range links {
		if l.symbolic {
			relative, err := filepath.Rel(filepath.Dir(filepath.FromSlash(l.name)), filepath.FromSlash(l.target))
			if err != nil {
				return err
			}
			if err = root.Symlink(relative, filepath.FromSlash(l.name)); err != nil {
				return err
			}
		} else {
			kind, exists := seen[l.target]
			if !exists || kind != tar.TypeReg {
				return fmt.Errorf("hard link target is not a downloaded regular file: %q", l.target)
			}
			if err := root.Link(filepath.FromSlash(l.target), filepath.FromSlash(l.name)); err != nil {
				return err
			}
		}
	}
	return nil
}
