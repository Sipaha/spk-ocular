package kubernetes

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watchDebounce folds an editor's write/rename/chmod burst (and kubectl's
// config.lock dance) into one re-discovery.
const watchDebounce = 300 * time.Millisecond

// Watch calls onChange after kubeconfig files change. It watches the
// directories that hold the source files — not the files — so atomic
// rename-writes (editors, `kubectl config`) are seen. A missing directory is
// awaited by watching its parent for that one name. inotify only: no polling.
func (p *Provider) Watch(ctx context.Context, onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	// dir -> accepted base names (nil: any name in the dir)
	var filters map[string]map[string]bool
	rewatch := func() {
		want := watchPlan(p.sources())
		for dir := range filters {
			if _, ok := want[dir]; !ok {
				_ = w.Remove(dir)
			}
		}
		for dir := range want {
			if _, ok := filters[dir]; !ok {
				if err := w.Add(dir); err != nil {
					slog.Debug("kubeconfig watch: cannot watch dir", "dir", dir, "err", err)
				}
			}
		}
		filters = want
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
			names, watched := filters[filepath.Dir(ev.Name)]
			if !watched || (names != nil && !names[filepath.Base(ev.Name)]) {
				continue
			}
			timer.Reset(watchDebounce)
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			slog.Warn("kubeconfig watch error", "err", err)
		case <-timer.C:
			rewatch() // a created ~/.kube or KUBECONFIG dir is watched from now on
			onChange()
		}
	}
}

// watchPlan maps each directory to watch to the names that matter in it.
// Existing source dirs accept any name (a new extra file in ~/.kube counts);
// for a missing dir, its existing parent is watched for that dir's name only.
func watchPlan(src Sources) map[string]map[string]bool {
	plan := map[string]map[string]bool{}
	addDir := func(dir string) {
		if dir == "" {
			return
		}
		if isDir(dir) {
			plan[dir] = nil
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir || !isDir(parent) {
			return
		}
		if names, ok := plan[parent]; ok && names == nil {
			return // already watching everything there
		}
		if plan[parent] == nil {
			plan[parent] = map[string]bool{}
		}
		plan[parent][filepath.Base(dir)] = true
	}
	for _, f := range src.Primary {
		addDir(filepath.Dir(f))
	}
	addDir(src.KubeDir)
	// A parent filter added before the same dir was planned as "any name"
	// must not narrow it.
	for dir := range plan {
		if isDir(dir) && containsSourceDir(src, dir) {
			plan[dir] = nil
		}
	}
	return plan
}

func containsSourceDir(src Sources, dir string) bool {
	if src.KubeDir == dir {
		return true
	}
	for _, f := range src.Primary {
		if filepath.Dir(f) == dir {
			return true
		}
	}
	return false
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
