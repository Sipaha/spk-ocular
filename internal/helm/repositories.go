package helm

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/privatefs"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/loader"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/registry"
	repo "helm.sh/helm/v4/pkg/repo/v1"
	"oras.land/oras-go/v2/registry/remote/auth"
	"sigs.k8s.io/yaml"
)

const maxDownload = 32 << 20

var repositoryName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

type Settings struct {
	Repositories     []Repository `json:"repositories"`
	Storage          Storage      `json:"storage"`
	HasSQLConnection bool         `json:"hasSQLConnection,omitempty"`
}

// Repositories stores credentials only in a private application-owned file.
type Repositories struct {
	mu  sync.Mutex
	dir string
}

func NewRepositories(dir string) *Repositories { return &Repositories{dir: dir} }
func (m *Repositories) read() (Settings, error) {
	out := Settings{Repositories: []Repository{}, Storage: Storage{Driver: "secret"}}
	if m.dir == "" {
		return out, errors.New("helm settings directory is unavailable")
	}
	b, err := os.ReadFile(filepath.Join(m.dir, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}
func (m *Repositories) Settings() (Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.read()
	s.HasSQLConnection = s.Storage.SQLConnection != ""
	s.Storage.SQLConnection = ""
	for i := range s.Repositories {
		s.Repositories[i].HasPassword = s.Repositories[i].Password != ""
		s.Repositories[i].Password = ""
	}
	return s, err
}
func (m *Repositories) Save(s Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, err := m.read()
	if err != nil {
		return err
	}
	names := map[string]bool{}
	for i := range s.Repositories {
		r := &s.Repositories[i]
		if !repositoryName.MatchString(r.Name) || names[r.Name] {
			return errors.New("repository names must be unique and contain only letters, digits, dot, dash or underscore")
		}
		names[r.Name] = true
		u, err := url.Parse(r.URL)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "oci") {
			return errors.New("repository URL must be HTTP, HTTPS or OCI without embedded credentials, query or fragment")
		}
		if (r.CertFile == "") != (r.KeyFile == "") {
			return errors.New("client certificate and key must be supplied together")
		}
		if r.HasPassword && r.Password == "" {
			for _, prev := range old.Repositories {
				if prev.Name == r.Name && prev.URL == r.URL && prev.Username == r.Username {
					r.Password = prev.Password
				}
			}
		}
		r.HasPassword = false
	}
	switch s.Storage.Driver {
	case "secret", "configmap":
		s.Storage.SQLConnection = ""
	case "sql":
		if s.HasSQLConnection && s.Storage.SQLConnection == "" {
			s.Storage.SQLConnection = old.Storage.SQLConnection
		}
		if s.Storage.SQLConnection == "" {
			return errors.New("SQL connection string is required")
		}
	default:
		return errors.New("unsupported Helm storage driver")
	}
	s.HasSQLConnection = false
	if err = privatefs.EnsureDir(m.dir); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	f, err := privatefs.CreateTemp(m.dir, "settings-*")
	if err != nil {
		return err
	}
	path := f.Name()
	defer func() { _ = os.Remove(path) }()
	if _, err = f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(m.dir, "settings.json"))
}
func (m *Repositories) Storage() (Storage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, e := m.read()
	return s.Storage, e
}
func (m *Repositories) repository(name string) (Repository, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, e := m.read()
	if e != nil {
		return Repository{}, e
	}
	for _, r := range s.Repositories {
		if r.Name == name {
			return r, nil
		}
	}
	return Repository{}, errors.New("repository was removed")
}
func repositoryHTTP(ctx context.Context, r Repository) (*http.Client, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: r.Insecure} // explicit per-repository preference
	if r.CAFile != "" {
		b, e := os.ReadFile(r.CAFile)
		if e != nil {
			return nil, e
		}
		tc.RootCAs = x509.NewCertPool()
		if !tc.RootCAs.AppendCertsFromPEM(b) {
			return nil, errors.New("invalid repository CA")
		}
	}
	if r.CertFile != "" {
		cert, e := tls.LoadX509KeyPair(r.CertFile, r.KeyFile)
		if e != nil {
			return nil, e
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tc
	return &http.Client{Transport: requestTransport{transport, ctx}, Timeout: 60 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		if len(via) > 0 && req.URL.Host != via[0].URL.Host {
			req.Header.Del("Authorization")
		}
		if len(via) > 0 && via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("refusing HTTPS downgrade")
		}
		return nil
	}}, nil
}
func fetch(ctx context.Context, r Repository, address string) ([]byte, error) {
	c, e := repositoryHTTP(ctx, r)
	if e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if e != nil {
		return nil, e
	}
	origin, e := url.Parse(r.URL)
	if e != nil {
		return nil, e
	}
	if req.URL.Host == origin.Host && req.URL.Scheme == origin.Scheme && r.Username != "" {
		req.SetBasicAuth(r.Username, r.Password)
	}
	res, e := c.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("repository returned HTTP %d", res.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, maxDownload+1))
	if len(b) > maxDownload {
		return nil, errors.New("repository response exceeds 32 MiB")
	}
	return b, e
}
func (m *Repositories) index(ctx context.Context, r Repository) (*repo.IndexFile, error) {
	address, e := repo.ResolveReferenceURL(r.URL, "index.yaml")
	if e != nil {
		return nil, e
	}
	b, e := fetch(ctx, r, address)
	if e != nil {
		return nil, e
	}
	var idx repo.IndexFile
	if e = yaml.Unmarshal(b, &idx); e != nil {
		return nil, e
	}
	if idx.APIVersion == "" {
		return nil, errors.New("invalid Helm repository index")
	}
	idx.SortEntries()
	return &idx, nil
}
func (m *Repositories) registry(ctx context.Context, r Repository) (*registry.Client, error) {
	c, e := repositoryHTTP(ctx, r)
	if e != nil {
		return nil, e
	}
	origin, _ := url.Parse(r.URL)
	authorizer := &auth.Client{Client: c, Cache: auth.NewCache(), Credential: func(_ context.Context, host string) (auth.Credential, error) {
		if host != origin.Host {
			return auth.EmptyCredential, nil
		}
		return auth.Credential{Username: r.Username, Password: r.Password}, nil
	}}
	return registry.NewClient(registry.ClientOptHTTPClient(c), registry.ClientOptAuthorizer(*authorizer), registry.ClientOptCredentialsFile(filepath.Join(m.dir, "registry-unused.json")))
}

// OCI repositories identify a chart path; OCI has no standardized chart search.
func (m *Repositories) Catalog(ctx context.Context, name string) ([]Chart, error) {
	r, e := m.repository(name)
	if e != nil {
		return nil, e
	}
	out := []Chart{}
	if strings.HasPrefix(r.URL, "oci://") {
		c, e := m.registry(ctx, r)
		if e != nil {
			return nil, e
		}
		ref := strings.TrimPrefix(strings.TrimRight(r.URL, "/"), "oci://")
		tags, e := c.Tags(ref)
		if e != nil {
			return nil, e
		}
		for _, v := range tags {
			out = append(out, Chart{ChartRef: ChartRef{Repository: r.Name, Name: ref[strings.LastIndex(ref, "/")+1:], Version: v}})
		}
		return out, nil
	}
	idx, e := m.index(ctx, r)
	if e != nil {
		return nil, e
	}
	for name, versions := range idx.Entries {
		for _, v := range versions {
			if v == nil || v.Metadata == nil {
				continue
			}
			out = append(out, Chart{ChartRef: ChartRef{Repository: r.Name, Name: name, Version: v.Version}, Description: v.Description, AppVersion: v.AppVersion, Deprecated: v.Deprecated})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (m *Repositories) Load(ctx context.Context, ref ChartRef) (chart.Charter, Chart, error) {
	if ref.Version == "" {
		return nil, Chart{}, errors.New("select an exact chart version")
	}
	r, e := m.repository(ref.Repository)
	if e != nil {
		return nil, Chart{}, e
	}
	var data []byte
	if strings.HasPrefix(r.URL, "oci://") {
		c, err := m.registry(ctx, r)
		if err != nil {
			return nil, Chart{}, err
		}
		result, err := c.Pull(strings.TrimPrefix(strings.TrimRight(r.URL, "/"), "oci://") + ":" + strings.ReplaceAll(ref.Version, "+", "_"))
		if err != nil {
			return nil, Chart{}, err
		}
		data = result.Chart.Data
	} else {
		idx, err := m.index(ctx, r)
		if err != nil {
			return nil, Chart{}, err
		}
		v, err := idx.Get(ref.Name, ref.Version)
		if err != nil {
			return nil, Chart{}, err
		}
		if v.Version != ref.Version || len(v.URLs) == 0 {
			return nil, Chart{}, errors.New("exact chart version is unavailable")
		}
		address, err := repo.ResolveReferenceURL(r.URL, v.URLs[0])
		if err != nil {
			return nil, Chart{}, err
		}
		data, err = fetch(ctx, r, address)
		if err != nil {
			return nil, Chart{}, err
		}
		if v.Digest != "" {
			sum := sha256.Sum256(data)
			if !strings.EqualFold(strings.TrimPrefix(v.Digest, "sha256:"), hex.EncodeToString(sum[:])) {
				return nil, Chart{}, errors.New("chart archive digest does not match repository index")
			}
		}
	}
	if len(data) > maxDownload {
		return nil, Chart{}, errors.New("chart exceeds 32 MiB")
	}
	if err := checkArchiveBudget(data); err != nil {
		return nil, Chart{}, err
	}
	raw, e := loader.LoadArchive(bytes.NewReader(data))
	if e != nil {
		return nil, Chart{}, e
	}
	ch, ok := raw.(*chartv2.Chart)
	if !ok || ch.Metadata == nil {
		return nil, Chart{}, errors.New("unsupported chart format")
	}
	if ch.Metadata.Name != ref.Name || ch.Metadata.Version != ref.Version {
		return nil, Chart{}, errors.New("downloaded chart does not match the selected name and version")
	}
	out := Chart{ChartRef: ref, Description: ch.Metadata.Description, AppVersion: ch.Metadata.AppVersion, Deprecated: ch.Metadata.Deprecated}
	for _, f := range ch.Raw {
		if f.Name == "values.yaml" {
			out.Values = string(f.Data)
		}
	}
	for _, f := range ch.Files {
		if strings.EqualFold(f.Name, "README.md") {
			out.Readme = string(f.Data)
		}
	}
	if out.Values == "" {
		b, e := yaml.Marshal(ch.Values)
		if e != nil {
			return nil, Chart{}, e
		}
		out.Values = string(b)
	}
	return raw, out, nil
}

func SettingsDirectory(base string) string {
	if base == "" {
		return ""
	}
	return filepath.Join(base, "helm")
}

// Bound expansion before the SDK allocates individual files (including nested
// archives, which its loader independently bounds).
func checkArchiveBudget(data []byte) error {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer r.Close()
	n, err := io.Copy(io.Discard, io.LimitReader(r, maxDownload+1))
	if err != nil {
		return err
	}
	if n > maxDownload {
		return errors.New("expanded chart exceeds 32 MiB")
	}
	return nil
}
