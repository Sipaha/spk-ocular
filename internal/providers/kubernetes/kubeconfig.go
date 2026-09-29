package kubernetes

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// maxExtraFileSize skips big files in ~/.kube: a kubeconfig is a few KB, and
// reading arbitrary large files on the startup path is not free.
const maxExtraFileSize = 1 << 20

// Sources are the kubeconfig files to read.
//
// Primary follows kubectl exactly: the KUBECONFIG list (merged, first wins),
// else ~/.kube/config. Extra are the other regular files directly in ~/.kube
// (Lens-style): people keep per-cluster kubeconfigs there without adding them
// to KUBECONFIG. Extras are standalone — each is resolved on its own.
type Sources struct {
	Primary []string
	Extra   []string
	// KubeDir is ~/.kube ("" when the home dir is unknown).
	KubeDir string
}

// ResolveSources computes Sources from the environment (getenv) and home dir.
func ResolveSources(getenv func(string) string, home string) Sources {
	var s Sources
	if home != "" {
		s.KubeDir = filepath.Join(home, ".kube")
	}
	seen := map[string]bool{}
	for _, p := range filepath.SplitList(getenv("KUBECONFIG")) {
		if p == "" {
			continue
		}
		p = absClean(p)
		if !seen[p] {
			seen[p] = true
			s.Primary = append(s.Primary, p)
		}
	}
	if len(s.Primary) == 0 && s.KubeDir != "" {
		p := filepath.Join(s.KubeDir, "config")
		s.Primary = []string{p}
		seen[p] = true
	}
	if s.KubeDir == "" {
		return s
	}
	entries, err := os.ReadDir(s.KubeDir)
	if err != nil {
		return s
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		p := filepath.Join(s.KubeDir, name)
		if !isRegular(e, p) {
			continue // dirs (cache, http-cache), sockets, dangling links
		}
		if seen[p] || samePrimary(p, s.Primary) {
			continue
		}
		s.Extra = append(s.Extra, p)
	}
	sort.Strings(s.Extra)
	return s
}

// isRegular follows a symlink: a linked kubeconfig counts as a file.
func isRegular(e fs.DirEntry, p string) bool {
	if e.Type().IsRegular() {
		return true
	}
	if e.Type()&fs.ModeSymlink == 0 {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func absClean(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return filepath.Clean(p)
}

// samePrimary catches a primary file reached through another path (a
// symlinked KUBECONFIG pointing into ~/.kube).
func samePrimary(p string, primary []string) bool {
	st, err := os.Stat(p)
	if err != nil {
		return false
	}
	for _, q := range primary {
		if qs, err := os.Stat(q); err == nil && os.SameFile(st, qs) {
			return true
		}
	}
	return false
}

// kubeContext is one discovered context with everything needed to build a
// client for it later (Files + Name), and display-safe facts. It never holds
// credentials.
type kubeContext struct {
	ID        string   // unique target id
	Name      string   // context name inside Files
	Files     []string // loading precedence for this context
	DefinedIn string   // the file that defines the context (first wins)
	Cluster   string
	Server    string
	User      string
	Namespace string
	Auth      string // auth method summary: "exec: yc", "token", ...
	Current   bool
}

type problem struct{ Source, Message string }

type loaded struct {
	Contexts []kubeContext
	Problems []problem
}

// load reads Sources. A missing primary file is not a problem (kubectl
// ignores it too); an unreadable one is, and the rest stay usable. Extra
// files that are not kubeconfigs are skipped silently — ~/.kube holds other
// things too.
func load(src Sources) loaded {
	var out loaded
	merged := clientcmdapi.NewConfig()
	var primaryFiles []string
	definedIn := map[string]string{}
	currentSet := false
	for _, f := range src.Primary {
		cfg, err := clientcmd.LoadFromFile(f)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			out.Problems = append(out.Problems, problem{Source: f, Message: cleanErr(err, f)})
			continue
		}
		primaryFiles = append(primaryFiles, f)
		for name := range cfg.Contexts {
			if _, ok := definedIn[name]; !ok {
				definedIn[name] = f
			}
		}
		mergeFirstWins(merged, cfg)
		if !currentSet && cfg.CurrentContext != "" {
			merged.CurrentContext = cfg.CurrentContext
			currentSet = true
		}
	}
	taken := map[string]bool{}
	for _, name := range sortedKeys(merged.Contexts) {
		kc := describe(name, merged)
		kc.ID = name
		kc.Files = primaryFiles
		kc.DefinedIn = definedIn[name]
		kc.Current = name == merged.CurrentContext
		taken[kc.ID] = true
		out.Contexts = append(out.Contexts, kc)
	}
	for _, f := range src.Extra {
		st, err := os.Stat(f)
		if err != nil || st.Size() > maxExtraFileSize {
			continue
		}
		cfg, err := clientcmd.LoadFromFile(f)
		if err != nil || len(cfg.Contexts) == 0 {
			continue
		}
		for _, name := range sortedKeys(cfg.Contexts) {
			kc := describe(name, cfg)
			kc.ID = uniqueID(name, filepath.Base(f), taken)
			kc.Files = []string{f}
			kc.DefinedIn = f
			taken[kc.ID] = true
			out.Contexts = append(out.Contexts, kc)
		}
	}
	sort.SliceStable(out.Contexts, func(i, j int) bool {
		return strings.ToLower(out.Contexts[i].ID) < strings.ToLower(out.Contexts[j].ID)
	})
	return out
}

func uniqueID(name, file string, taken map[string]bool) string {
	if !taken[name] {
		return name
	}
	id := fmt.Sprintf("%s (%s)", name, file)
	for i := 2; taken[id]; i++ {
		id = fmt.Sprintf("%s (%s #%d)", name, file, i)
	}
	return id
}

// mergeFirstWins adds src's entries missing from dst — kubectl's rule for
// the KUBECONFIG list: the first file to define a name wins.
func mergeFirstWins(dst, src *clientcmdapi.Config) {
	for k, v := range src.Contexts {
		if _, ok := dst.Contexts[k]; !ok {
			dst.Contexts[k] = v
		}
	}
	for k, v := range src.Clusters {
		if _, ok := dst.Clusters[k]; !ok {
			dst.Clusters[k] = v
		}
	}
	for k, v := range src.AuthInfos {
		if _, ok := dst.AuthInfos[k]; !ok {
			dst.AuthInfos[k] = v
		}
	}
}

func describe(name string, cfg *clientcmdapi.Config) kubeContext {
	c := cfg.Contexts[name]
	kc := kubeContext{Name: name}
	if c == nil {
		return kc
	}
	kc.Cluster, kc.User, kc.Namespace = c.Cluster, c.AuthInfo, c.Namespace
	if cl := cfg.Clusters[c.Cluster]; cl != nil {
		kc.Server = cl.Server
	}
	kc.Auth = authSummary(cfg.AuthInfos[c.AuthInfo])
	return kc
}

// authSummary names the auth method without any secret material.
func authSummary(a *clientcmdapi.AuthInfo) string {
	switch {
	case a == nil:
		return ""
	case a.Exec != nil:
		return "exec: " + filepath.Base(a.Exec.Command)
	case a.AuthProvider != nil:
		return "auth-provider: " + a.AuthProvider.Name
	case a.Token != "" || a.TokenFile != "":
		return "token"
	case len(a.ClientCertificateData) > 0 || a.ClientCertificate != "":
		return "client certificate"
	case a.Username != "":
		return "basic"
	}
	return ""
}

// cleanErr keeps a parse error short: clientcmd prefixes the file path, which
// the UI already shows as the problem's source.
func cleanErr(err error, file string) string {
	msg := err.Error()
	msg = strings.TrimPrefix(msg, "error loading config file \""+file+"\": ")
	return msg
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
