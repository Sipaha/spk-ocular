package compose

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watchDebounce folds a burst (docker context create writes a directory,
// then meta.json and the TLS files) into one re-discovery.
const watchDebounce = 300 * time.Millisecond

// Watch calls onChange after the Docker configuration changes: config.json
// (currentContext), contexts' meta.json, their TLS files, DOCKER_CERT_PATH.
// inotify only, no polling. Directories are watched, not files, so atomic
// rename-writes are seen; a directory that does not exist yet is awaited
// through its nearest existing ancestor; new context directories are added
// on the re-plan after each change; an overflow re-reads everything.
func (p *Provider) Watch(ctx context.Context, onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	var plan watchSet
	installed := map[string]bool{}
	rewatch := func() {
		plan = watchPlan(p.env())
		for dir := range installed {
			if _, ok := plan[dir]; !ok {
				_ = w.Remove(dir)
				delete(installed, dir)
			}
		}
		for dir := range plan {
			if installed[dir] {
				continue
			}
			if err := w.Add(dir); err != nil {
				slog.Debug("docker config watch: cannot watch dir", "dir", dir, "err", err)
				continue // retried on the next re-plan
			}
			installed[dir] = true
		}
	}
	rewatch()

	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if _, self := plan[ev.Name]; self && ev.Has(fsnotify.Remove|fsnotify.Rename) {
				delete(installed, ev.Name)
			}
			if plan.relevant(ev.Name) {
				timer.Reset(watchDebounce)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				timer.Reset(watchDebounce)
				continue
			}
			slog.Warn("docker config watch error", "err", err)
		case <-timer.C:
			rewatch() // new context and TLS directories
			onChange()
		}
	}
}

// watchSet maps a directory to the names that matter in it; nil: any name.
type watchSet map[string]map[string]bool

func (ws watchSet) add(dir, name string) {
	if names, ok := ws[dir]; ok && names == nil {
		return
	}
	if name == "" {
		ws[dir] = nil
		return
	}
	if ws[dir] == nil {
		ws[dir] = map[string]bool{}
	}
	ws[dir][name] = true
}

func (ws watchSet) relevant(path string) bool {
	if _, self := ws[path]; self {
		return true
	}
	names, ok := ws[filepath.Dir(path)]
	return ok && (names == nil || names[filepath.Base(path)])
}

// watchPlan: the config dir for config.json and contexts; contexts for meta
// and tls; meta and every context dir in it; tls and every dir below it;
// the certificate dir of the default context. A missing dir is awaited in
// its nearest existing ancestor (for the next name on the way down).
func watchPlan(e Env) watchSet {
	plan := watchSet{}
	await := func(dir string) {
		for d := dir; ; {
			parent := filepath.Dir(d)
			if parent == d {
				return
			}
			if isDir(parent) {
				plan.add(parent, filepath.Base(d))
				return
			}
			d = parent
		}
	}
	// want watches dir for names (none: any), or awaits it.
	want := func(dir string, names ...string) {
		if !isDir(dir) {
			await(dir)
			return
		}
		if len(names) == 0 {
			plan.add(dir, "")
		}
		for _, n := range names {
			plan.add(dir, n)
		}
	}
	contexts := filepath.Join(e.ConfigDir, "contexts")
	want(e.ConfigDir, "config.json", "contexts")
	want(contexts, "meta", "tls")
	want(e.metaDir())
	if entries, err := os.ReadDir(e.metaDir()); err == nil {
		for _, ent := range entries {
			if ent.IsDir() {
				want(filepath.Join(e.metaDir(), ent.Name()))
			}
		}
	}
	want(e.tlsDir())
	_ = filepath.WalkDir(e.tlsDir(), func(path string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			want(path)
		}
		return nil
	})
	if e.CertPath != e.ConfigDir {
		want(e.CertPath)
	} else {
		want(e.ConfigDir, "ca.pem", "cert.pem", "key.pem")
	}
	return plan
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
