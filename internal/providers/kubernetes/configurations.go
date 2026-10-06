package kubernetes

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/gofrs/flock"
	"github.com/spk/spk-ocular/internal/privatefs"
	"golang.org/x/crypto/argon2"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// ConfigRequest is UI-only. Passwords and YAML must never enter logs or preferences.
type ConfigRequest struct {
	TargetRevision  string   `json:"targetRevision,omitempty"`
	Target          string   `json:"target,omitempty"`
	ContentRevision string   `json:"contentRevision,omitempty"`
	Command         string   `json:"command"`
	Paths           []string `json:"paths,omitempty"`
	Name            string   `json:"name,omitempty"`
	YAML            string   `json:"yaml,omitempty"`
	Password        string   `json:"password,omitempty"`
	ID              string   `json:"id,omitempty"`
	Expect          string   `json:"expect,omitempty"`
}
type ConfigCandidate struct {
	Path     string   `json:"path"`
	Contexts []string `json:"contexts"`
	Selected bool     `json:"selected"`
	Problem  string   `json:"problem,omitempty"`
}
type ConfigEntry struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Path     string `json:"path,omitempty"`
	Revision string `json:"revision"`
}
type ConfigState struct {
	ResetRevision   string `json:"resetRevision,omitempty"`
	TargetName      string `json:"targetName,omitempty"`
	TargetRevision  string `json:"targetRevision,omitempty"`
	EntryID         string `json:"entryId,omitempty"`
	ContentRevision string `json:"contentRevision,omitempty"`
	// YAML exists only in the explicit inspect response, never in status or persistence.
	YAML        string            `json:"yaml,omitempty"`
	Initialized bool              `json:"initialized"`
	Encrypted   bool              `json:"encrypted"`
	Locked      bool              `json:"locked"`
	Candidates  []ConfigCandidate `json:"candidates"`
	Entries     []ConfigEntry     `json:"entries"`
}
type linkedConfig struct {
	Name    string
	Path    string
	Primary bool
	Order   int
}
type sealedConfig struct {
	Contexts []string `json:",omitempty"`
	ID       string
	Name     string
	Data     []byte
}
type targetName struct {
	SourceID string
	Name     string
}
type configDisk struct {
	TargetNames map[string]targetName
	Version     int
	Initialized bool
	Links       []linkedConfig
	Salt        []byte
	Verify      []byte
	Entries     []sealedConfig
}
type configurations struct {
	mu   sync.Mutex
	path string
	disk configDisk
	key  []byte
	raw  []byte // compare before saving: another process must not be overwritten
}

// WithConfigurations opts the production provider into explicit imports. Tests
// of kubectl loading can still construct a provider with injected raw sources.
func (p *Provider) WithConfigurations(dir string) (*Provider, error) {
	m := &configurations{path: filepath.Join(dir, "kubeconfigs.json"), disk: configDisk{Version: 1}}
	b, err := os.ReadFile(m.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read configuration registry: %w", err)
	}
	if err == nil {
		if len(b) > 16<<20 || json.Unmarshal(b, &m.disk) != nil || m.disk.Version != 1 {
			return nil, errors.New("invalid configuration registry")
		}
		if (len(m.disk.Salt) != 0 && len(m.disk.Salt) != 16) || (len(m.disk.Salt) == 0 && (len(m.disk.Verify) > 0 || len(m.disk.Entries) > 0)) {
			return nil, errors.New("invalid encrypted configuration registry")
		}
		m.raw = b
	}
	p.configs = m
	p.configChanged = make(chan struct{}, 1)
	return p, nil
}

// save refuses stale state while holding an OS lock, then atomically replaces
// the owner-only registry. Ciphertext is never decrypted for persistence.
func (m *configurations) save(d configDisk) error {
	if err := privatefs.EnsureDir(filepath.Dir(m.path)); err != nil {
		return err
	}
	lock := flock.New(m.path + ".lock")
	acquired, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !acquired {
		return errors.New("configuration registry is being changed by another process")
	}
	defer lock.Close()

	current, err := os.ReadFile(m.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot read configuration registry")
	}
	if string(current) != string(m.raw) {
		return errors.New("configuration registry changed in another process; restart Ocular before saving")
	}
	b, err := json.Marshal(d)
	if len(b) > 16<<20 {
		return errors.New("configuration storage limit reached")
	}
	if err != nil {
		return err
	}
	if err = privatefs.EnsureDir(filepath.Dir(m.path)); err != nil {
		return err
	}
	f, err := privatefs.CreateTemp(filepath.Dir(m.path), ".configs-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err = f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, m.path); err != nil {
		return err
	}
	m.disk = d
	m.raw = b
	return nil
}
func deriveConfigKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
}
func sealConfig(key, plain []byte, id string) ([]byte, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plain, []byte("ocular-kubeconfig-v1:"+id)), nil
}
func openConfig(key, data []byte, id string) ([]byte, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	if len(data) < g.NonceSize() {
		return nil, errors.New("invalid ciphertext")
	}
	return g.Open(nil, data[:g.NonceSize()], data[g.NonceSize():], []byte("ocular-kubeconfig-v1:"+id))
}
func readConfigurationBytes(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("file is unavailable")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxExtraFileSize+1))
	if err != nil || len(b) > maxExtraFileSize {
		return nil, errors.New("configuration exceeds 1 MiB or cannot be read")
	}
	return b, nil
}

func readConfigCandidate(path string) (*clientcmdapi.Config, error) {
	b, err := readConfigurationBytes(path)
	if err != nil {
		return nil, err
	}
	c, err := clientcmd.Load(b)
	if err != nil {
		return nil, errors.New("invalid kubeconfig")
	}
	return c, nil
}
func (p *Provider) candidates() []ConfigCandidate {
	src := ResolveSources(p.getenv, p.home)
	paths := append(append([]string{}, src.Primary...), src.Extra...)
	// KUBECONFIG replaces kubectl's default file, but the import picker also
	// offers ~/.kube/config if it exists and was not already listed.
	if src.KubeDir != "" {
		f := filepath.Join(src.KubeDir, "config")
		found := false
		for _, v := range paths {
			if v == f {
				found = true
			}
		}
		if !found {
			if _, err := os.Stat(f); err == nil {
				paths = append(paths, f)
			}
		}
	}
	out := []ConfigCandidate{}
	for _, f := range paths {
		c, err := readConfigCandidate(f)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		item := ConfigCandidate{Path: f, Contexts: []string{}}
		if err != nil {
			if _, e := os.Stat(f); errors.Is(e, os.ErrNotExist) {
				continue
			}
			item.Problem = err.Error()
		} else {
			item.Contexts = sortedKeys(c.Contexts)
			if len(c.Contexts) == 0 && len(c.Clusters) == 0 && len(c.AuthInfos) == 0 {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}
func (p *Provider) Configurations(req ConfigRequest) (ConfigState, error) {
	if req.Command == "resolve" {
		source := ""
		displayName := ""
		for _, kc := range p.loadConfigured().Contexts {
			if kc.ID == req.Target {
				source = kc.DefinedIn
				displayName = target(kc).Title
				break
			}
		}
		if source == "" {
			return ConfigState{}, errors.New("connection configuration is no longer available")
		}
		st, err := p.Configurations(ConfigRequest{Command: "status"})
		if err != nil {
			return ConfigState{}, err
		}
		for _, entry := range st.Entries {
			if (entry.Kind == "linked" && entry.Path == source) || (entry.Kind == "stored" && strings.HasPrefix(req.Target, "stored:"+entry.ID+":")) {
				st.EntryID = entry.ID
				st.TargetName = displayName
				p.configs.mu.Lock()
				st.TargetRevision = targetNameRevision(req.Target, entry.ID, p.configs.disk.TargetNames[req.Target])
				p.configs.mu.Unlock()
				return st, nil
			}
		}
		return ConfigState{}, errors.New("connection configuration is no longer registered")
	}
	if req.Command == "rename-target" {
		st, e := p.Configurations(ConfigRequest{Command: "resolve", Target: req.Target})
		if e != nil {
			return ConfigState{}, e
		}
		if st.EntryID != req.ID {
			return ConfigState{}, errors.New("connection configuration changed; reopen rename")
		}
	}
	m := p.configs
	if m == nil {
		return ConfigState{Initialized: true, Candidates: []ConfigCandidate{}, Entries: []ConfigEntry{}}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var err error
	d := m.disk
	switch req.Command {
	case "reset":
		if len(d.Salt) == 0 || req.Expect == "" || req.Expect != configurationRevision(d) {
			return ConfigState{}, errors.New("configuration storage changed; review reset again")
		}
		d.Entries = nil
		d.Salt = nil
		d.Verify = nil
		d.TargetNames = maps.Clone(d.TargetNames)
		for id, alias := range d.TargetNames {
			if strings.HasPrefix(id, "stored:"+alias.SourceID+":") {
				delete(d.TargetNames, id)
			}
		}
		if err = m.save(d); err == nil {
			clear(m.key)
			m.key = nil
		}
	case "inspect":
		link, entry, e := m.selected(req)
		if e != nil {
			return ConfigState{}, e
		}
		var b []byte
		if link != nil {
			b, e = readConfigurationBytes(link.Path)
		} else {
			if len(m.key) == 0 {
				return ConfigState{}, errors.New("unlock configuration storage first")
			}
			b, e = openConfig(m.key, entry.Data, entry.ID)
			if e != nil {
				return ConfigState{}, errors.New("cannot decrypt configuration")
			}
		}
		if e != nil {
			return ConfigState{}, e
		}
		defer clear(b)
		return ConfigState{YAML: string(b), ContentRevision: configurationContentRevision(req.ID, b)}, nil
	case "status", "scan":
	case "import":
		allowed := map[string]bool{}
		for _, c := range p.candidates() {
			if c.Problem == "" {
				allowed[c.Path] = true
			}
		}
		primary := map[string]bool{}
		order := map[string]int{}
		for i, f := range ResolveSources(p.getenv, p.home).Primary {
			order[f] = i
			primary[f] = true
		}
		d.Links = append([]linkedConfig{}, d.Links...)
		for _, f := range req.Paths {
			if !allowed[f] {
				return ConfigState{}, errors.New("selected file is no longer available; scan again")
			}
			exists := false
			for _, link := range d.Links {
				if link.Path == f {
					exists = true
				}
			}
			if !exists {
				d.Links = append(d.Links, linkedConfig{Path: f, Primary: primary[f], Order: order[f]})
			}
		}
		sort.SliceStable(d.Links, func(i, j int) bool {
			if d.Links[i].Primary != d.Links[j].Primary {
				return d.Links[i].Primary
			}
			return d.Links[i].Order < d.Links[j].Order
		})
		d.Initialized = true
		err = m.save(d)
	case "dismiss":
		d.Initialized = true
		err = m.save(d)
	case "setup":
		if len(d.Salt) > 0 {
			return ConfigState{}, errors.New("master password is already configured")
		}
		if req.Password == "" || len(req.Password) > 1024 {
			return ConfigState{}, errors.New("enter a non-empty master password (maximum 1024 bytes)")
		}
		d.Salt = make([]byte, 16)
		if _, err = rand.Read(d.Salt); err != nil {
			return ConfigState{}, err
		}
		key := deriveConfigKey(req.Password, d.Salt)
		d.Verify, err = sealConfig(key, []byte("ocular-configurations"), "verify")
		if err == nil {
			err = m.save(d)
		}
		if err == nil {
			m.key = key
		} else {
			clear(key)
		}
	case "unlock":
		if len(d.Salt) != 16 || len(req.Password) > 1024 {
			return ConfigState{}, errors.New("cannot unlock configuration storage")
		}
		key := deriveConfigKey(req.Password, d.Salt)
		v, e := openConfig(key, d.Verify, "verify")
		if e != nil || string(v) != "ocular-configurations" {
			clear(key)
			return ConfigState{}, errors.New("incorrect master password or damaged storage")
		}
		// Persist only context names for locked rows; credentials remain sealed.
		d.Entries = append([]sealedConfig{}, d.Entries...)
		changed := false
		for i := range d.Entries {
			if len(d.Entries[i].Contexts) != 0 {
				continue
			}
			plain, readErr := openConfig(key, d.Entries[i].Data, d.Entries[i].ID)
			if readErr != nil {
				clear(key)
				return ConfigState{}, errors.New("cannot decrypt configuration")
			}
			d.Entries[i].Contexts = storedContextNames(string(plain))
			clear(plain)
			changed = true
		}
		if changed {
			if e := m.save(d); e != nil {
				clear(key)
				return ConfigState{}, e
			}
		}
		clear(m.key)
		m.key = key
	case "rename-target":
		if _, _, e := m.selected(req); e != nil {
			return ConfigState{}, e
		}
		name := strings.TrimSpace(req.Name)
		if name == "" || len(name) > 128 {
			return ConfigState{}, errors.New("connection name must contain 1 to 128 bytes")
		}
		if req.TargetRevision == "" || req.TargetRevision != targetNameRevision(req.Target, req.ID, m.disk.TargetNames[req.Target]) {
			return ConfigState{}, errors.New("connection name changed; reopen rename")
		}
		d.TargetNames = maps.Clone(d.TargetNames)
		if d.TargetNames == nil {
			d.TargetNames = map[string]targetName{}
		}
		d.TargetNames[req.Target] = targetName{SourceID: req.ID, Name: name}
		err = m.save(d)
	case "rename", "remove":
		if req.Expect == "" {
			return ConfigState{}, errors.New("configuration changed; reopen configuration management")
		}
		name := strings.TrimSpace(req.Name)
		if req.Command == "rename" && (name == "" || len(name) > 128) {
			return ConfigState{}, errors.New("configuration name must contain 1 to 128 bytes")
		}
		found := false
		d.Links = append([]linkedConfig{}, d.Links...)
		d.Entries = append([]sealedConfig{}, d.Entries...)
		for i, link := range d.Links {
			if req.ID != "linked:"+link.Path {
				continue
			}
			if req.Expect != configurationRevision(link) {
				return ConfigState{}, errors.New("configuration changed; reopen configuration management")
			}
			found = true
			if req.Command == "rename" {
				d.Links[i].Name = name
			} else {
				d.Links = append(d.Links[:i], d.Links[i+1:]...)
			}
			break
		}
		for i, entry := range d.Entries {
			if req.ID != entry.ID {
				continue
			}
			if req.Expect != configurationRevision(entry) {
				return ConfigState{}, errors.New("configuration changed; reopen configuration management")
			}
			found = true
			if req.Command == "rename" {
				d.Entries[i].Name = name
			} else {
				d.Entries = append(d.Entries[:i], d.Entries[i+1:]...)
			}
			break
		}
		if !found {
			return ConfigState{}, errors.New("configuration no longer exists")
		}
		if req.Command == "remove" {
			d.TargetNames = maps.Clone(d.TargetNames)
			for id, alias := range d.TargetNames {
				if alias.SourceID == req.ID {
					delete(d.TargetNames, id)
				}
			}
		}
		err = m.save(d)
	case "update":
		link, entry, e := m.selected(req)
		if e != nil {
			return ConfigState{}, e
		}
		if len(req.YAML) > maxExtraFileSize {
			return ConfigState{}, errors.New("configuration exceeds 1 MiB")
		}
		if link != nil {
			if _, e = clientcmd.Load([]byte(req.YAML)); e != nil {
				return ConfigState{}, errors.New("enter valid kubeconfig YAML")
			}
			registry, readErr := os.ReadFile(m.path)
			if readErr != nil || !bytes.Equal(registry, m.raw) {
				return ConfigState{}, errors.New("configuration registry changed; restart Ocular before editing")
			}
			e = updateConfigurationFile(link.Path, req.ID, req.ContentRevision, []byte(req.YAML))
		} else {
			if len(m.key) == 0 {
				return ConfigState{}, errors.New("unlock configuration storage first")
			}
			if e = validateStoredConfiguration(req.YAML); e != nil {
				return ConfigState{}, e
			}
			plain, readErr := openConfig(m.key, entry.Data, entry.ID)
			if readErr != nil {
				return ConfigState{}, errors.New("cannot decrypt configuration")
			}
			revision := configurationContentRevision(req.ID, plain)
			clear(plain)
			if req.ContentRevision == "" || req.ContentRevision != revision {
				return ConfigState{}, errors.New("configuration changed; reopen the editor")
			}
			data, sealErr := sealConfig(m.key, []byte(req.YAML), entry.ID)
			if sealErr != nil {
				return ConfigState{}, sealErr
			}
			d.Entries = append([]sealedConfig{}, d.Entries...)
			for i := range d.Entries {
				if d.Entries[i].ID == entry.ID {
					d.Entries[i].Data = data
					d.Entries[i].Contexts = storedContextNames(req.YAML)
				}
			}
			e = m.save(d)
		}
		if e != nil {
			return ConfigState{}, e
		}
	case "create":
		if len(m.key) == 0 {
			return ConfigState{}, errors.New("unlock configuration storage first")
		}
		name := strings.TrimSpace(req.Name)
		if name == "" || len(name) > 128 {
			return ConfigState{}, errors.New("configuration name must contain 1 to 128 bytes")
		}
		if e := validateStoredConfiguration(req.YAML); e != nil {
			return ConfigState{}, e
		}
		id := rand.Text()
		data, e := sealConfig(m.key, []byte(req.YAML), id)
		if e != nil {
			return ConfigState{}, e
		}
		d.Entries = append(append([]sealedConfig{}, d.Entries...), sealedConfig{ID: id, Name: name, Data: data, Contexts: storedContextNames(req.YAML)})
		d.Initialized = true
		err = m.save(d)
	default:
		return ConfigState{}, errors.New("unknown configuration command")
	}
	if err != nil {
		return ConfigState{}, err
	}
	if req.Command != "status" && req.Command != "scan" {
		select {
		case p.configChanged <- struct{}{}:
		default:
		}
	}
	state := ConfigState{ResetRevision: configurationRevision(m.disk), Initialized: m.disk.Initialized, Encrypted: len(m.disk.Salt) > 0, Locked: len(m.disk.Salt) > 0 && len(m.key) == 0, Candidates: []ConfigCandidate{}, Entries: []ConfigEntry{}}
	if req.Command == "scan" || !state.Initialized {
		state.Candidates = p.candidates()
		for i := range state.Candidates {
			for _, l := range m.disk.Links {
				if l.Path == state.Candidates[i].Path {
					state.Candidates[i].Selected = true
				}
			}
		}
	}
	for _, link := range m.disk.Links {
		name := link.Name
		if name == "" {
			name = filepath.Base(link.Path)
		}
		state.Entries = append(state.Entries, ConfigEntry{ID: "linked:" + link.Path, Name: name, Kind: "linked", Path: link.Path, Revision: configurationRevision(link)})
	}
	for _, entry := range m.disk.Entries {
		state.Entries = append(state.Entries, ConfigEntry{ID: entry.ID, Name: entry.Name, Kind: "stored", Revision: configurationRevision(entry)})
	}
	return state, nil
}
func (p *Provider) loadConfigured() loaded {
	out := load(p.sources())
	m := p.configs
	if m == nil {
		return out
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range out.Contexts {
		kc := &out.Contexts[i]
		for _, link := range m.disk.Links {
			if kc.DefinedIn == link.Path && link.Name != "" {
				kc.DisplayName = link.Name + " / " + kc.Name
			}
		}
	}
	if len(m.key) == 0 {
		for _, entry := range m.disk.Entries {
			names := entry.Contexts
			if len(names) == 0 {
				names = []string{""}
			} // Older records expose a source placeholder until the first unlock.
			for _, name := range names {
				title := entry.Name
				if len(names) > 1 {
					title += " / " + name
				}
				out.Contexts = append(out.Contexts, kubeContext{ID: "stored:" + entry.ID + ":" + name, Name: name, DisplayName: title, DefinedIn: entry.Name, Encrypted: true, Locked: true})
			}
		}
		return applyTargetNames(out, m.disk.TargetNames)
	}
	for _, entry := range m.disk.Entries {
		b, err := openConfig(m.key, entry.Data, entry.ID)
		if err != nil {
			out.Problems = append(out.Problems, problem{Source: entry.Name, Message: "cannot decrypt configuration"})
			continue
		}
		c, err := clientcmd.Load(b)
		clear(b)
		if err != nil {
			out.Problems = append(out.Problems, problem{Source: entry.Name, Message: "invalid stored configuration"})
			continue
		}
		for _, name := range sortedKeys(c.Contexts) {
			kc := describe(name, c)
			kc.Encrypted = true
			kc.DisplayName = entry.Name
			if len(c.Contexts) > 1 {
				kc.DisplayName += " / " + name
			}
			kc.ID = "stored:" + entry.ID + ":" + name
			kc.DefinedIn = entry.Name
			kc.Hash = configHash(name, c, nil)
			kc.Config = c
			out.Contexts = append(out.Contexts, kc)
		}
	}
	return applyTargetNames(out, m.disk.TargetNames)
}

// CloseConfigurations drops the process-local key after all sessions close.
func (p *Provider) CloseConfigurations() {
	if p.configs != nil {
		p.configs.mu.Lock()
		defer p.configs.mu.Unlock()
		clear(p.configs.key)
		p.configs.key = nil
	}
}

func configurationRevision(value any) string {
	b, _ := json.Marshal(value)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// selected is called under the registry lock and accepts only a reviewed registered entry.
func (m *configurations) selected(req ConfigRequest) (*linkedConfig, *sealedConfig, error) {
	for _, link := range m.disk.Links {
		if req.ID == "linked:"+link.Path && req.Expect == configurationRevision(link) {
			return &link, nil, nil
		}
	}
	for _, entry := range m.disk.Entries {
		if req.ID == entry.ID && req.Expect == configurationRevision(entry) {
			return nil, &entry, nil
		}
	}
	return nil, nil, errors.New("configuration changed; reopen configuration management")
}

// ConfigurationFile resolves a registered external source for the UI file-manager action.
func (p *Provider) ConfigurationFile(req ConfigRequest) (string, error) {
	if p.configs == nil {
		return "", errors.New("configuration management is unavailable")
	}
	m := p.configs
	m.mu.Lock()
	defer m.mu.Unlock()
	link, _, err := m.selected(req)
	if err != nil {
		return "", err
	}
	if link == nil {
		return "", errors.New("encrypted configurations have no source file")
	}
	st, err := os.Stat(link.Path)
	if err != nil || !st.Mode().IsRegular() {
		return "", errors.New("source file is unavailable")
	}
	return filepath.Abs(link.Path)
}

func validateStoredConfiguration(text string) error {
	if len(text) > maxExtraFileSize {
		return errors.New("configuration exceeds 1 MiB")
	}
	c, e := clientcmd.Load([]byte(text))
	if e != nil || len(c.Contexts) == 0 {
		return errors.New("enter a valid kubeconfig with at least one context")
	}
	if e = clientcmd.Validate(*c); e != nil {
		return errors.New("kubeconfig contains incomplete or invalid contexts")
	}
	// Pasted configs have no source directory. Reject ambiguous relative paths;
	// preserve absolute references and exec helpers, used only on Connect.
	for _, cl := range c.Clusters {
		if cl.CertificateAuthority != "" && !filepath.IsAbs(cl.CertificateAuthority) {
			return errors.New("use embedded CA data or an absolute certificate-authority path")
		}
	}
	for _, a := range c.AuthInfos {
		if a.Exec != nil && strings.ContainsAny(a.Exec.Command, "/\\") && !filepath.IsAbs(a.Exec.Command) {
			return errors.New("use an absolute credential-helper path or a command available on PATH")
		}
		for _, f := range []string{a.ClientCertificate, a.ClientKey, a.TokenFile} {
			if f != "" && !filepath.IsAbs(f) {
				return errors.New("use embedded credentials or absolute certificate/key/token paths")
			}
		}
	}
	return nil
}

func configurationContentRevision(id string, b []byte) string {
	mac := hmac.New(sha256.New, printKey)
	_, _ = mac.Write([]byte(id + "\x00"))
	_, _ = mac.Write(b)
	return hex.EncodeToString(mac.Sum(nil))
}

// External source edits are explicit, revision-checked and atomically replaced.
// Resolve symlinks so editing never replaces the link itself.
func updateConfigurationFile(source, id, expect string, next []byte) error {
	path, err := filepath.EvalSymlinks(source)
	if err != nil {
		return errors.New("source file is unavailable")
	}
	lock := flock.New(filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".ocular.lock"))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		return errors.New("source file is busy or cannot be locked")
	}
	defer func() { _ = lock.Unlock() }()
	old, err := readConfigurationBytes(path)
	if err != nil {
		return err
	}
	defer clear(old)
	if expect == "" || expect != configurationContentRevision(id, old) {
		return errors.New("source file changed; reopen the editor")
	}
	st, err := os.Stat(path)
	if err != nil {
		return errors.New("source file is unavailable")
	}
	tmp, err := privatefs.CreateTemp(filepath.Dir(path), ".ocular-config-*")
	if err != nil {
		return errors.New("cannot save source file")
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	if _, err = tmp.Write(next); err == nil {
		err = tmp.Sync()
	}
	if err == nil {
		err = tmp.Chmod(st.Mode().Perm())
	}
	if err == nil {
		err = tmp.Close()
	}
	if err != nil {
		return errors.New("cannot save source file")
	}
	now, err := readConfigurationBytes(path)
	if err != nil {
		return err
	}
	revision := configurationContentRevision(id, now)
	clear(now)
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil || resolved != path || revision != expect {
		return errors.New("source file changed; reopen the editor")
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return errors.New("cannot replace source file")
	}
	return nil
}

func applyTargetNames(out loaded, names map[string]targetName) loaded {
	for i := range out.Contexts {
		kc := &out.Contexts[i]
		if alias, ok := names[kc.ID]; ok && (alias.SourceID == "linked:"+kc.DefinedIn || strings.HasPrefix(kc.ID, "stored:"+alias.SourceID+":")) {
			kc.DisplayName = alias.Name
		}
	}
	return out
}
func targetNameRevision(id, source string, alias targetName) string {
	return configurationRevision(struct {
		ID, Source string
		Alias      targetName
	}{id, source, alias})
}

func storedContextNames(yaml string) []string {
	c, err := clientcmd.Load([]byte(yaml))
	if err != nil {
		return nil
	}
	return sortedKeys(c.Contexts)
}
