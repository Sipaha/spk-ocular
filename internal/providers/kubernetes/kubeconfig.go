package kubernetes

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/url"
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
	Encrypted   bool
	Locked      bool
	DisplayName string               // UI alias only; never changes the Kubernetes context or identity
	Config      *clientcmdapi.Config // decrypted snapshot for app-owned configurations only
	ID          string               // stable opaque target id (see primaryID/extraID)
	Name        string               // context name inside Files
	Files       []string             // loading precedence for this context
	DefinedIn   string               // the file that defines the context (first wins)
	Extra       bool                 // from a standalone file in ~/.kube, not kubectl's config
	Hash        string               // configHash: what the context resolves to
	Cluster     string
	Server      string
	User        string
	Namespace   string
	Auth        string // auth method summary: "exec: yc", "token", ...
	Identity    string // what the context points at (core.Target.Identity)
	Current     bool
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
	for _, name := range sortedKeys(merged.Contexts) {
		kc := describe(name, merged)
		kc.ID = primaryID(name)
		kc.Files = primaryFiles
		kc.DefinedIn = definedIn[name]
		kc.Hash = configHash(name, merged, primaryFiles)
		kc.Current = name == merged.CurrentContext
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
			kc.ID = extraID(f, name)
			kc.Files = []string{f}
			kc.DefinedIn = f
			kc.Extra = true
			kc.Hash = configHash(name, cfg, kc.Files)
			out.Contexts = append(out.Contexts, kc)
		}
	}
	sort.SliceStable(out.Contexts, func(i, j int) bool {
		a, b := strings.ToLower(out.Contexts[i].Name), strings.ToLower(out.Contexts[j].Name)
		if a != b {
			return a < b
		}
		if out.Contexts[i].Extra != out.Contexts[j].Extra {
			return !out.Contexts[i].Extra // kubectl's own context before same-named extras
		}
		return out.Contexts[i].ID < out.Contexts[j].ID
	})
	return out
}

// Target ids are stable and never depend on what else is configured: a
// context keeps its id when another file gains a context with the same name
// (a remembered selection must not silently move to another cluster). The
// two domains are disjoint by prefix. Ids are opaque; the UI shows Title.
func primaryID(name string) string { return "kubeconfig:" + name }

func extraID(file, name string) string { return "file:" + file + ":" + name }

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
	cl := cfg.Clusters[c.Cluster]
	if cl != nil {
		kc.Server = cl.Server
	}
	kc.Auth = authSummary(cfg.AuthInfos[c.AuthInfo])
	kc.Identity = identity(cl, c.AuthInfo)
	return kc
}

// maxCAFile bounds the CA file read for an identity (a bundle is KBs).
const maxCAFile = 1 << 20

// identity: the server, the CA it trusts (a digest of its bytes: inline or
// the file's content, so the same CA elsewhere is the same) and the
// auth-info's name — no credential, so rotating one keeps the identity.
func identity(cl *clientcmdapi.Cluster, user string) string {
	if cl == nil {
		return "no cluster | user:" + user
	}
	ca := "system"
	switch {
	case cl.InsecureSkipTLSVerify:
		ca = "insecure"
	case len(cl.CertificateAuthorityData) > 0:
		ca = caDigest(cl.CertificateAuthorityData)
	case cl.CertificateAuthority != "":
		ca = "unreadable " + cl.CertificateAuthority
		path := cl.CertificateAuthority
		if !filepath.IsAbs(path) && cl.LocationOfOrigin != "" {
			path = filepath.Join(filepath.Dir(cl.LocationOfOrigin), path) // as kubectl: relative to its kubeconfig
		}
		if f, err := os.Open(path); err == nil {
			b, err := io.ReadAll(io.LimitReader(f, maxCAFile))
			_ = f.Close()
			if err == nil {
				ca = caDigest(b)
			}
		}
	}
	return withoutUserinfo(cl.Server) + " | ca:" + ca + " | tls-server-name:" + cl.TLSServerName +
		" | proxy:" + withoutUserinfo(cl.ProxyURL) + " | user:" + user
}

// withoutUserinfo drops credentials from a URL (they may sit in a proxy
// URL): the identity is shown and stored; a new password is the same
// target. An unparsable URL is kept only up to any "@".
func withoutUserinfo(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		if i := strings.LastIndex(s, "@"); i >= 0 {
			return "***@" + s[i+1:]
		}
		return s
	}
	if u.User == nil {
		return s
	}
	u.User = nil
	return u.String()
}

func caDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
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
