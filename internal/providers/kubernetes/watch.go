package kubernetes

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watchDebounce folds an editor's write/rename/chmod burst (and kubectl's
// config.lock dance) into one re-discovery.
const watchDebounce = 300 * time.Millisecond

// Watch calls onChange after kubeconfig files change. inotify only, no
// polling. It watches directories, not files, so atomic rename-writes
// (editors, `kubectl config`) are seen; a missing directory is awaited by
// watching its parent for that one name; a symlinked kubeconfig also gets
// its target's directory watched; a watched directory that is removed or
// replaced is re-planned; an inotify queue overflow triggers a re-discovery.
func (p *Provider) Watch(ctx context.Context, onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	var plan watchSet
	installed := map[string]bool{} // dirs inotify actually watches
	rewatch := func() {
		plan = watchPlan(p.sources())
		for dir := range installed {
			if _, ok := plan[dir]; !ok {
				_ = w.Remove(dir) // may already be gone with the dir itself
				delete(installed, dir)
			}
		}
		for dir := range plan {
			if installed[dir] {
				continue
			}
			if err := w.Add(dir); err != nil {
				slog.Debug("kubeconfig watch: cannot watch dir", "dir", dir, "err", err)
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
				delete(installed, ev.Name) // inotify dropped it; re-added when it is back
			}
			if plan.relevant(ev.Name) {
				timer.Reset(watchDebounce)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				timer.Reset(watchDebounce) // events were lost: re-read everything
				continue
			}
			slog.Warn("kubeconfig watch error", "err", err)
		case <-timer.C:
			rewatch() // picks up created/replaced dirs and new symlink targets
			onChange()
		}
	}
}

// watchSet maps a directory to the names that matter in it; a nil set means
// any name.
type watchSet map[string]map[string]bool

func (ws watchSet) add(dir, name string) {
	if names, ok := ws[dir]; ok && names == nil {
		return // already any name
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

// relevant: an event on a watched dir itself (removed, renamed) or on a name
// that matters inside one.
func (ws watchSet) relevant(path string) bool {
	if _, self := ws[path]; self {
		return true
	}
	names, ok := ws[filepath.Dir(path)]
	return ok && (names == nil || names[filepath.Base(path)])
}

// watchPlan: every existing source dir (any name — a new extra file in
// ~/.kube counts), or for a missing one its existing parent for that name;
// plus the target dir of every symlinked source file (that file's name).
func watchPlan(src Sources) watchSet {
	plan := watchSet{}
	addDir := func(dir string) {
		if dir == "" {
			return
		}
		if isDir(dir) {
			plan.add(dir, "")
			return
		}
		if parent := filepath.Dir(dir); parent != dir && isDir(parent) {
			plan.add(parent, filepath.Base(dir))
		}
	}
	addLinkTarget := func(file string) {
		st, err := os.Lstat(file)
		if err != nil || st.Mode()&os.ModeSymlink == 0 {
			return
		}
		if target, err := filepath.EvalSymlinks(file); err == nil {
			plan.add(filepath.Dir(target), filepath.Base(target))
		}
	}
	for _, f := range src.Primary {
		addDir(filepath.Dir(f))
		addLinkTarget(f)
	}
	addDir(src.KubeDir)
	for _, f := range src.Extra {
		addLinkTarget(f)
	}
	// A narrow parent filter must not shadow a dir that is itself a source
	// dir (planned as any name): add() already keeps nil sets nil, but a
	// narrow set added first is widened here.
	for _, f := range src.Primary {
		if isDir(filepath.Dir(f)) {
			plan[filepath.Dir(f)] = nil
		}
	}
	if isDir(src.KubeDir) {
		plan[src.KubeDir] = nil
	}
	return plan
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
