package agentapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gofrs/flock"
	"github.com/spk/spk-ocular/internal/privatefs"
)

var ErrOtherInstance = errors.New("another SPK Ocular instance serves MCP access")

type endpointConfig struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func newToken() string {
	var data [32]byte
	// crypto/rand.Read cannot fail on supported Go platforms.
	_, _ = rand.Read(data[:])
	return hex.EncodeToString(data[:])
}

func openEndpoint(profile string) (net.Listener, *flock.Flock, endpointConfig, error) {
	var empty endpointConfig
	if profile == "" {
		return nil, nil, empty, errors.New("MCP profile is required")
	}
	if err := privatefs.EnsureDir(profile); err != nil {
		return nil, nil, empty, err
	}
	lockPath := filepath.Join(profile, "mcp.lock")
	// Create with an owner-only DACL on Windows, before flock opens it.
	if file, err := privatefs.CreateNew(lockPath); err == nil {
		_ = file.Close()
	} else if !errors.Is(err, os.ErrExist) {
		return nil, nil, empty, err
	}
	info, statErr := os.Lstat(lockPath)
	if statErr != nil {
		return nil, nil, empty, statErr
	}
	if !info.Mode().IsRegular() {
		return nil, nil, empty, errors.New("MCP profile lock must be a regular file")
	}
	lock := flock.New(lockPath, flock.SetPermissions(0o600))
	ok, err := lock.TryLock()
	if err != nil {
		return nil, nil, empty, err
	}
	if !ok {
		return nil, nil, empty, ErrOtherInstance
	}
	fail := func(err error) (net.Listener, *flock.Flock, endpointConfig, error) {
		_ = lock.Unlock()
		return nil, nil, empty, err
	}
	configPath := filepath.Join(profile, "mcp.json")
	config := endpointConfig{}
	var data []byte
	info, err = os.Lstat(configPath)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fail(errors.New("MCP connection configuration must be a regular file"))
		}
		file, openErr := os.Open(configPath)
		if openErr != nil {
			return fail(openErr)
		}
		data, err = io.ReadAll(io.LimitReader(file, 4097))
		closeErr := file.Close()
		if err != nil {
			return fail(err)
		}
		if closeErr != nil {
			return fail(closeErr)
		}
	}
	address := "127.0.0.1:0"
	if err == nil {
		if len(data) > 4096 || json.Unmarshal(data, &config) != nil {
			return fail(errors.New("invalid MCP connection configuration"))
		}
		u, parseErr := url.Parse(config.URL)
		if parseErr != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Path != "/mcp" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return fail(errors.New("invalid MCP endpoint configuration"))
		}
		port, portErr := strconv.Atoi(u.Port())
		token, tokenErr := hex.DecodeString(config.Token)
		if portErr != nil || port < 1 || port > 65535 || tokenErr != nil || len(token) != 32 {
			return fail(errors.New("invalid MCP endpoint or credential configuration"))
		}
		address = u.Host
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail(err)
	}
	ln, err := net.Listen("tcp4", address)
	if err != nil {
		return fail(fmt.Errorf("MCP loopback port unavailable: %w", err))
	}
	if config.URL == "" {
		config = endpointConfig{URL: "http://" + ln.Addr().String() + "/mcp", Token: newToken()}
		encoded, _ := json.Marshal(config)
		tmp, err := privatefs.CreateTemp(profile, "mcp-config-*")
		if err != nil {
			_ = ln.Close()
			return fail(err)
		}
		tmpPath := tmp.Name()
		defer func() { _ = os.Remove(tmpPath) }()
		_, writeErr := tmp.Write(encoded)
		if writeErr == nil {
			writeErr = tmp.Sync()
		}
		closeErr := tmp.Close()
		if writeErr == nil {
			writeErr = closeErr
		}
		if writeErr == nil {
			writeErr = os.Rename(tmpPath, configPath)
		}
		if writeErr != nil {
			_ = ln.Close()
			return fail(writeErr)
		}
	}
	return ln, lock, config, nil
}
