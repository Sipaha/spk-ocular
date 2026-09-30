package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Docker contexts, resolved the way the docker CLI 29 resolves them:
//   - the config dir is DOCKER_CONFIG, else ~/.docker;
//   - contexts are <cfg>/contexts/meta/<sha256(name)>/meta.json plus the
//     built-in "default" (DOCKER_HOST with its TLS variables, else the
//     local socket);
//   - the current one: DOCKER_HOST set → "default"; else DOCKER_CONTEXT;
//     else config.json's currentContext; else "default".
//
// Only local files are read (Discover is on the startup path).

const (
	defaultContext = "default"
	defaultHost    = "unix:///var/run/docker.sock"
)

// Env is what discovery reads from the environment.
type Env struct {
	ConfigDir string // DOCKER_CONFIG, else ~/.docker
	Host      string // DOCKER_HOST
	Context   string // DOCKER_CONTEXT
	TLSVerify bool   // DOCKER_TLS_VERIFY non-empty
	TLS       bool   // DOCKER_TLS non-empty (TLS without verification)
	CertPath  string // DOCKER_CERT_PATH, else the config dir
}

// ResolveEnv reads the docker CLI's variables.
func ResolveEnv(getenv func(string) string, home string) Env {
	e := Env{
		ConfigDir: getenv("DOCKER_CONFIG"),
		Host:      getenv("DOCKER_HOST"),
		Context:   getenv("DOCKER_CONTEXT"),
		TLSVerify: getenv("DOCKER_TLS_VERIFY") != "",
		TLS:       getenv("DOCKER_TLS") != "",
		CertPath:  getenv("DOCKER_CERT_PATH"),
	}
	if e.ConfigDir == "" {
		e.ConfigDir = filepath.Join(home, ".docker")
	}
	if e.CertPath == "" {
		e.CertPath = e.ConfigDir
	}
	return e
}

func (e Env) metaDir() string { return filepath.Join(e.ConfigDir, "contexts", "meta") }
func (e Env) tlsDir() string  { return filepath.Join(e.ConfigDir, "contexts", "tls") }

// tlsFiles is a context's TLS material, read into memory: the session and
// ConfigHash use the same bytes. Never leaves Go.
type tlsFiles struct {
	CA, Cert, Key []byte
	SkipVerify    bool
}

// dockerContext is one resolved context.
type dockerContext struct {
	Name        string
	Description string
	Host        string
	TLS         *tlsFiles // nil: no TLS
	// TLSError: the context's TLS material is unusable (half a pair, an
	// unreadable file); the session refuses — never falls back to plaintext.
	TLSError string
	// Source: the meta.json it came from ("" for the built-in default).
	Source  string
	Current bool
	Hash    string
}

type problem struct{ Source, Message string }

type loaded struct {
	Contexts []dockerContext
	Problems []problem
}

type metaFile struct {
	Name     string         `json:"Name"`
	Metadata map[string]any `json:"Metadata"`
	// Endpoints: only "docker" matters.
	Endpoints map[string]struct {
		Host          string `json:"Host"`
		SkipTLSVerify bool   `json:"SkipTLSVerify"`
	} `json:"Endpoints"`
}

// contextDir is the CLI's directory name of a context: sha256 of its name.
func contextDir(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

// load reads every context; a broken meta.json is a problem, the others
// stay visible.
func load(e Env) loaded {
	var l loaded
	l.Contexts = append(l.Contexts, defaultOf(e))
	entries, err := os.ReadDir(e.metaDir())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		l.Problems = append(l.Problems, problem{Source: e.metaDir(), Message: err.Error()})
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		path := filepath.Join(e.metaDir(), ent.Name(), "meta.json")
		c, err := readMeta(e, path, ent.Name())
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) { // a directory being written: its meta comes later
				l.Problems = append(l.Problems, problem{Source: path, Message: err.Error()})
			}
			continue
		}
		if c.Name == defaultContext {
			l.Problems = append(l.Problems, problem{Source: path, Message: `the name "default" is reserved for the built-in context`})
			continue
		}
		l.Contexts = append(l.Contexts, c)
	}
	sort.SliceStable(l.Contexts[1:], func(i, j int) bool { return l.Contexts[i+1].Name < l.Contexts[j+1].Name })
	current := currentName(e)
	found := false
	for i := range l.Contexts {
		if l.Contexts[i].Name == current {
			l.Contexts[i].Current, found = true, true
		}
	}
	if !found {
		// A current context that does not exist: the CLI fails; the list
		// says so and nothing is marked current.
		l.Problems = append(l.Problems, problem{Source: currentSource(e), Message: fmt.Sprintf("the current context %q does not exist", current)})
	}
	for i := range l.Contexts {
		l.Contexts[i].Hash = hashOf(l.Contexts[i])
	}
	return l
}

func readMeta(e Env, path, dir string) (dockerContext, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return dockerContext{}, err
	}
	var m metaFile
	if err := json.Unmarshal(b, &m); err != nil {
		return dockerContext{}, fmt.Errorf("not a context: %w", err)
	}
	if m.Name == "" {
		return dockerContext{}, errors.New("not a context: no name")
	}
	if contextDir(m.Name) != dir {
		return dockerContext{}, fmt.Errorf("context %q is stored under the wrong directory", m.Name)
	}
	ep, ok := m.Endpoints["docker"]
	if !ok || ep.Host == "" {
		return dockerContext{}, fmt.Errorf("context %q has no Docker endpoint", m.Name)
	}
	c := dockerContext{Name: m.Name, Host: ep.Host, Source: path}
	if d, ok := m.Metadata["Description"].(string); ok {
		c.Description = d
	}
	// TLS material in <cfg>/contexts/tls/<dir>/docker/{ca,cert,key}.pem.
	tlsDir := filepath.Join(e.tlsDir(), dir, "docker")
	files, terr := readTLS(tlsDir)
	switch {
	case terr != nil:
		c.TLSError = terr.Error()
	case files != nil:
		files.SkipVerify = ep.SkipTLSVerify
		c.TLS = files
	case ep.SkipTLSVerify && strings.HasPrefix(ep.Host, "tcp://"):
		c.TLS = &tlsFiles{SkipVerify: true}
	}
	return c, nil
}

// defaultOf is the built-in context: DOCKER_HOST (TLS as its variables
// say), else the local socket.
func defaultOf(e Env) dockerContext {
	c := dockerContext{Name: defaultContext, Host: defaultHost, Description: "Current DOCKER_HOST based configuration"}
	if e.Host != "" {
		c.Host = e.Host
	}
	if e.TLSVerify || e.TLS {
		files, err := readTLS(e.CertPath)
		switch {
		case err != nil:
			c.TLSError = err.Error()
		case files == nil && e.TLSVerify:
			// Verification with no CA of its own trusts the system roots, like the CLI.
			c.TLS = &tlsFiles{}
		case files == nil:
			c.TLS = &tlsFiles{SkipVerify: true}
		default:
			files.SkipVerify = !e.TLSVerify
			c.TLS = files
		}
	}
	return c
}

// readTLS reads ca.pem, cert.pem and key.pem of dir. None of them: nil (no
// TLS material). Half a client pair or an unreadable file: an error — the
// session refuses rather than going without a certificate.
func readTLS(dir string) (*tlsFiles, error) {
	read := func(name string) ([]byte, error) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("cannot read %s: %w", filepath.Join(dir, name), err)
		}
		if len(b) == 0 {
			return nil, fmt.Errorf("%s is empty", filepath.Join(dir, name))
		}
		return b, nil
	}
	ca, err := read("ca.pem")
	if err != nil {
		return nil, err
	}
	cert, err := read("cert.pem")
	if err != nil {
		return nil, err
	}
	key, err := read("key.pem")
	if err != nil {
		return nil, err
	}
	switch {
	case ca == nil && cert == nil && key == nil:
		return nil, nil
	case (cert == nil) != (key == nil):
		return nil, fmt.Errorf("incomplete TLS pair in %s: cert.pem and key.pem come together", dir)
	}
	return &tlsFiles{CA: ca, Cert: cert, Key: key}, nil
}

// currentName: DOCKER_HOST → default; DOCKER_CONTEXT; config.json; default.
func currentName(e Env) string {
	if e.Host != "" {
		return defaultContext
	}
	if e.Context != "" {
		return e.Context
	}
	if n := configCurrent(e); n != "" {
		return n
	}
	return defaultContext
}

func currentSource(e Env) string {
	if e.Context != "" {
		return "DOCKER_CONTEXT"
	}
	return filepath.Join(e.ConfigDir, "config.json")
}

// configCurrent reads only currentContext of config.json (the file also
// holds registry credentials: nothing else is kept or reported).
func configCurrent(e Env) string {
	b, err := os.ReadFile(filepath.Join(e.ConfigDir, "config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		CurrentContext string `json:"currentContext"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	return cfg.CurrentContext
}

// hashOf covers everything a session is built from — the endpoint and the
// TLS bytes (secret: never sent; the API shows an HMAC of it).
func hashOf(c dockerContext) string {
	h := sha256.New()
	field := func(b []byte) {
		_, _ = fmt.Fprintf(h, "%d:", len(b))
		_, _ = h.Write(b)
	}
	field([]byte(c.Host))
	field([]byte(c.TLSError))
	if c.TLS != nil {
		field([]byte("tls"))
		field(c.TLS.CA)
		field(c.TLS.Cert)
		field(c.TLS.Key)
		field([]byte(fmt.Sprint(c.TLS.SkipVerify)))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// endpointShown is the endpoint without anything secret: a URL's user info
// is dropped (tcp://user:pass@host is not a Docker form, but never show it).
func endpointShown(host string) string {
	u, err := url.Parse(host)
	if err != nil || u.User == nil {
		return host
	}
	u.User = nil
	return u.String()
}
