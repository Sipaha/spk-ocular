package engine_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
	"github.com/spk/spk-ocular/internal/providers/compose/enginefake"
)

func newClient(t *testing.T, cfg engine.Config) *engine.Client {
	t.Helper()
	c, err := engine.New(cfg)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

func engineError(t *testing.T, err error) *engine.Error {
	t.Helper()
	require.Error(t, err)
	var e *engine.Error
	require.True(t, errors.As(err, &e), "not an *engine.Error: %v", err)
	return e
}

func TestUnsupportedSchemesSayWhy(t *testing.T) {
	for host, class := range map[string]provider.ErrorClass{
		"ssh://me@box":                   provider.ClassUnsupported,
		"npipe:////./pipe/docker_engine": provider.ClassUnsupported,
		"fd://":                          provider.ClassUnsupported,
		"not a url":                      provider.ClassInvalid,
		"unix://":                        provider.ClassInvalid,
		"tcp://":                         provider.ClassInvalid,
	} {
		_, err := engine.New(engine.Config{Host: host})
		assert.Equal(t, class, engineError(t, err).Class, host)
	}
	_, err := engine.New(engine.Config{Host: "ssh://me@box"})
	assert.Contains(t, err.Error(), "dial-stdio")
}

func TestRedirectIsRefused(t *testing.T) {
	f := enginefake.New(t)
	f.AddHook(enginefake.Redirect("/info", "/elsewhere"))
	f.AddHook(enginefake.Body("/elsewhere", []byte(`{"Name":"followed"}`)))
	c := newClient(t, engine.Config{Host: f.Host()})

	_, err := c.Info(context.Background())
	assert.Equal(t, provider.ClassUnsupported, engineError(t, err).Class)
	assert.Contains(t, err.Error(), "redirect")
	assert.Equal(t, 0, f.Count("/elsewhere"), "the redirect was followed")
	assert.Equal(t, 1, f.Count("/info"))
}

func TestARequestGoesOnTheWireOnce(t *testing.T) {
	t.Run("5xx", func(t *testing.T) {
		f := enginefake.New(t)
		f.AddHook(enginefake.Fail("/info", http.StatusServiceUnavailable, "daemon is busy"))
		c := newClient(t, engine.Config{Host: f.Host()})
		_, err := c.Info(context.Background())
		e := engineError(t, err)
		assert.Equal(t, provider.ClassUnavailable, e.Class)
		assert.Equal(t, 503, e.Status)
		assert.Equal(t, "daemon is busy", e.Message)
		assert.Equal(t, 1, f.Count("/info"))
	})
	t.Run("timeout", func(t *testing.T) {
		f := enginefake.New(t)
		f.AddHook(enginefake.StallHeaders("/info", nil))
		c := newClient(t, engine.Config{Host: f.Host(), RequestTimeout: 150 * time.Millisecond})
		start := time.Now()
		_, err := c.Info(context.Background())
		assert.Less(t, time.Since(start), 3*time.Second)
		assert.Equal(t, provider.ClassUnavailable, engineError(t, err).Class)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, 1, f.Count("/info"))
	})
	t.Run("a reused connection hung up after the request", func(t *testing.T) {
		// net/http resends an idempotent request when a reused keep-alive
		// connection dies before the answer — after it was written.
		f := enginefake.New(t)
		c := newClient(t, engine.Config{Host: f.Host()})
		_, err := c.Info(context.Background()) // ping + info: an idle keep-alive connection
		require.NoError(t, err)
		f.AddHook(enginefake.HangUp("/containers/json"))
		_, err = c.ListContainers(context.Background(), nil)
		assert.Equal(t, provider.ClassUnavailable, engineError(t, err).Class)
		assert.Equal(t, 1, f.Count("/containers/json"))
	})
}

func TestBodyOverTheLimitIsAnError(t *testing.T) {
	f := enginefake.New(t)
	big := `[{"Id":"` + strings.Repeat("a", 2000) + `"}]`
	f.AddHook(enginefake.Body("/containers/json", []byte(big)))
	f.PutContainerJSON("c1", []byte(`{"Id":"c1","Name":"/`+strings.Repeat("n", 2000)+`"}`))
	c := newClient(t, engine.Config{Host: f.Host(), Limits: engine.Limits{ListBytes: 1024, InspectBytes: 1024}})

	_, err := c.ListContainers(context.Background(), nil)
	assert.Contains(t, engineError(t, err).Message, "larger than 1024 bytes")
	_, err = c.InspectContainer(context.Background(), "c1")
	assert.Contains(t, engineError(t, err).Message, "larger than 1024 bytes")

	// at the limit is fine
	c2 := newClient(t, engine.Config{Host: f.Host(), Limits: engine.Limits{ListBytes: int64(len(big))}})
	_, err = c2.ListContainers(context.Background(), nil)
	require.NoError(t, err)
}

func TestHangingStreamHeadersEndByTheDeadline(t *testing.T) {
	f := enginefake.New(t)
	f.AddHook(enginefake.StallHeaders("/events", nil))
	c := newClient(t, engine.Config{Host: f.Host(), HeaderTimeout: 150 * time.Millisecond})
	start := time.Now()
	_, err := c.Events(context.Background(), time.Time{}, nil)
	assert.Less(t, time.Since(start), 3*time.Second)
	assert.Equal(t, provider.ClassUnavailable, engineError(t, err).Class)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestAHangingStreamBodyIsCutByCancelNotByTheDeadline(t *testing.T) {
	f := enginefake.New(t)
	c := newClient(t, engine.Config{Host: f.Host(), RequestTimeout: 100 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := c.Events(ctx, time.Time{}, nil)
	require.NoError(t, err)
	defer s.Close()
	f.WaitEventSubscribers(t, 1)

	time.Sleep(400 * time.Millisecond) // well past both deadlines
	f.Emit(engine.Event{Type: "container", Action: "start", Actor: engine.EventActor{ID: "c1"}})
	ev, err := s.Next()
	require.NoError(t, err)
	assert.Equal(t, "c1", ev.Actor.ID)

	done := make(chan error, 1)
	go func() { _, err := s.Next(); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("the idle stream ended by itself: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, provider.ClassUnavailable, engine.ClassOf(err))
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not end the stream")
	}
}

func TestAPIVersionNegotiation(t *testing.T) {
	t.Run("1.40 is unsupported and nothing else is sent", func(t *testing.T) {
		f := enginefake.New(t, enginefake.WithAPIVersion("1.40"))
		c := newClient(t, engine.Config{Host: f.Host()})
		_, err := c.Info(context.Background())
		e := engineError(t, err)
		assert.Equal(t, provider.ClassUnsupported, e.Class)
		assert.Contains(t, e.Message, "1.40")
		assert.Equal(t, 0, f.Count("/info"))
	})
	for daemon, want := range map[string]string{"1.41": "1.41", "1.47": "1.47", "1.54": "1.54", "1.56": "1.54", "2.0": "1.54"} {
		t.Run(daemon, func(t *testing.T) {
			f := enginefake.New(t, enginefake.WithAPIVersion(daemon))
			c := newClient(t, engine.Config{Host: f.Host()})
			_, err := c.Info(context.Background())
			require.NoError(t, err)
			reqs := f.Requests()
			require.Len(t, reqs, 2)
			assert.Equal(t, "/_ping", reqs[0].Path)
			assert.Equal(t, "", reqs[0].Version)
			assert.Equal(t, "/info", reqs[1].Path)
			assert.Equal(t, want, reqs[1].Version)
			assert.Equal(t, want, c.APIVersion())
		})
	}
	t.Run("the version is negotiated once", func(t *testing.T) {
		f := enginefake.New(t)
		c := newClient(t, engine.Config{Host: f.Host()})
		for range 3 {
			_, err := c.Info(context.Background())
			require.NoError(t, err)
		}
		assert.Equal(t, 1, f.Count("/_ping"))
	})
	t.Run("concurrent first requests share one ping", func(t *testing.T) {
		f := enginefake.New(t)
		release := make(chan struct{})
		f.AddHook(enginefake.StallHeaders("/_ping", release))
		c := newClient(t, engine.Config{Host: f.Host()})
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for range 8 {
			wg.Go(func() {
				_, err := c.Info(context.Background())
				errs <- err
			})
		}
		waitFor(t, func() bool { return f.Count("/_ping") >= 1 })
		time.Sleep(100 * time.Millisecond) // let the others arrive
		close(release)
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		assert.Equal(t, 1, f.Count("/_ping"))
		assert.Equal(t, 8, f.Count("/info"))
	})
	t.Run("a failed ping is not cached", func(t *testing.T) {
		f := enginefake.New(t)
		var failed atomic.Bool
		f.AddHook(func(w http.ResponseWriter, r *http.Request, p string) bool {
			if p == "/_ping" && failed.CompareAndSwap(false, true) {
				return enginefake.Fail("/_ping", 500, "starting")(w, r, p)
			}
			return false
		})
		c := newClient(t, engine.Config{Host: f.Host()})
		_, err := c.Info(context.Background())
		assert.Equal(t, provider.ClassUnavailable, engineError(t, err).Class)
		assert.Equal(t, 0, f.Count("/info"), "a request without a version was sent")
		_, err = c.Info(context.Background())
		require.NoError(t, err)
		assert.Equal(t, 2, f.Count("/_ping"))
		assert.Equal(t, 1, f.Count("/info"))
	})
	t.Run("a daemon without Api-Version", func(t *testing.T) {
		f := enginefake.New(t)
		f.AddHook(enginefake.Body("/_ping", []byte("OK")))
		c := newClient(t, engine.Config{Host: f.Host()})
		_, err := c.Info(context.Background())
		assert.Equal(t, provider.ClassUnsupported, engineError(t, err).Class)
	})
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in 5s")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestErrorClasses(t *testing.T) {
	f := enginefake.New(t)
	c := newClient(t, engine.Config{Host: f.Host()})
	for _, tc := range []struct {
		status int
		body   string
		class  provider.ErrorClass
		msg    string
	}{
		{400, `{"message":"bad parameter"}`, provider.ClassInvalid, "bad parameter"},
		{401, `{"message":"who are you"}`, provider.ClassForbidden, "who are you"},
		{403, `{"message":"authorization denied by plugin"}`, provider.ClassForbidden, "authorization denied by plugin"},
		{404, `{"message":"No such container: x"}`, provider.ClassNotFound, "No such container: x"},
		{409, `{"message":"is already in progress"}`, provider.ClassConflict, "is already in progress"},
		{500, `{"message":"internal"}`, provider.ClassUnavailable, "internal"},
		{502, "Bad Gateway from a proxy\n", provider.ClassUnavailable, "Bad Gateway from a proxy"},
		{503, "", provider.ClassUnavailable, "503 Service Unavailable"},
	} {
		f.ClearHooks()
		f.AddHook(func(w http.ResponseWriter, _ *http.Request, p string) bool {
			if p != "/containers/x/json" {
				return false
			}
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
			return true
		})
		_, err := c.InspectContainer(context.Background(), "x")
		e := engineError(t, err)
		assert.Equal(t, tc.class, e.Class, tc.status)
		assert.Equal(t, tc.status, e.Status)
		assert.Equal(t, tc.msg, e.Message)
		assert.Equal(t, tc.class, engine.ClassOf(err))
	}
	assert.True(t, engine.IsNotFound(&engine.Error{Class: provider.ClassNotFound}))

	// no daemon at all
	gone := newClient(t, engine.Config{Host: "unix://" + filepath.Join(os.TempDir(), "no-such-engine.sock")})
	_, err := gone.Info(context.Background())
	e := engineError(t, err)
	assert.Equal(t, provider.ClassUnavailable, e.Class)
	assert.Equal(t, 0, e.Status)
}

func TestAnErrorBodyIsBounded(t *testing.T) {
	f := enginefake.New(t)
	f.AddHook(enginefake.Fail("/info", 500, strings.Repeat("x", 10000)))
	c := newClient(t, engine.Config{Host: f.Host(), Limits: engine.Limits{ErrorBytes: 100}})
	_, err := c.Info(context.Background())
	assert.LessOrEqual(t, len(engineError(t, err).Message), 100)
}

// ---- proxies ----

func clearProxyEnv(t *testing.T) {
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(k, "")
	}
}

// proxyEngine is an HTTP proxy that answers as an Engine itself.
func proxyEngine(t *testing.T) (*httptest.Server, *atomic.Int32, *atomic.Value) {
	var n atomic.Int32
	var host atomic.Value
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		host.Store(r.URL.Host)
		if r.URL.Path == "/_ping" {
			w.Header().Set("Api-Version", "1.54")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"Name": "via-proxy"})
	}))
	t.Cleanup(s.Close)
	return s, &n, &host
}

func TestAUnixSocketIsNeverProxied(t *testing.T) {
	clearProxyEnv(t)
	p, n, _ := proxyEngine(t)
	t.Setenv("HTTP_PROXY", p.URL)
	t.Setenv("HTTPS_PROXY", p.URL)
	f := enginefake.New(t)
	c := newClient(t, engine.Config{Host: f.Host()})
	info, err := c.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "fake-engine", info.Name)
	assert.Equal(t, int32(0), n.Load())
	for _, r := range f.Requests() {
		// a proxied request carries an absolute URI (the unix dialer would
		// still reach the socket, so the proxy itself sees nothing)
		assert.True(t, strings.HasPrefix(r.RequestURI, "/"), r.RequestURI)
	}
}

func TestRequestsHaveNoBody(t *testing.T) {
	// the non-replayable empty body must not turn into a chunked GET
	f := enginefake.New(t)
	c := newClient(t, engine.Config{Host: f.Host()})
	_, err := c.Info(context.Background())
	require.NoError(t, err)
	for _, r := range f.Requests() {
		assert.Empty(t, r.TransferEncoding, r.Path)
		assert.Equal(t, int64(0), r.ContentLength, r.Path)
		assert.Equal(t, "spk-ocular", r.Header.Get("User-Agent"))
	}
}

func TestTCPUsesTheEnvironmentProxyLikeTheCLI(t *testing.T) {
	clearProxyEnv(t)
	p, n, host := proxyEngine(t)
	t.Setenv("HTTP_PROXY", p.URL)
	c := newClient(t, engine.Config{Host: "tcp://docker.test:2375"})
	info, err := c.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "via-proxy", info.Name)
	assert.Equal(t, int32(2), n.Load())
	assert.Equal(t, "docker.test:2375", host.Load())

	t.Setenv("NO_PROXY", "docker.test")
	direct := newClient(t, engine.Config{Host: "tcp://docker.test:2375", RequestTimeout: 3 * time.Second})
	_, err = direct.Info(context.Background())
	assert.Equal(t, provider.ClassUnavailable, engineError(t, err).Class)
	assert.Equal(t, int32(2), n.Load(), "NO_PROXY was not honored")
}

// ---- TLS ----

type pki struct {
	caPEM, serverCert, serverKey, clientCert, clientKey []byte
	dir                                                 string
}

func newPKI(t *testing.T) *pki {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	ca, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)
	leaf := func(serial int64, usage x509.ExtKeyUsage, ips []net.IP) ([]byte, []byte) {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		tpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "leaf"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, IPAddresses: ips,
		}
		der, err := x509.CreateCertificate(rand.Reader, tpl, ca, &k.PublicKey, caKey)
		require.NoError(t, err)
		kder, err := x509.MarshalECPrivateKey(k)
		require.NoError(t, err)
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
			pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})
	}
	p := &pki{caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), dir: t.TempDir()}
	p.serverCert, p.serverKey = leaf(2, x509.ExtKeyUsageServerAuth, []net.IP{net.ParseIP("127.0.0.1")})
	p.clientCert, p.clientKey = leaf(3, x509.ExtKeyUsageClientAuth, nil)
	for name, b := range map[string][]byte{"ca.pem": p.caPEM, "cert.pem": p.clientCert, "key.pem": p.clientKey} {
		require.NoError(t, os.WriteFile(filepath.Join(p.dir, name), b, 0o600))
	}
	return p
}

func (p *pki) file(name string) string { return filepath.Join(p.dir, name) }

// mtlsEngine: a TLS fake that requires a client certificate of p's CA.
func (p *pki) mtlsEngine(t *testing.T, clientAuth tls.ClientAuthType) *enginefake.Engine {
	pair, err := tls.X509KeyPair(p.serverCert, p.serverKey)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(p.caPEM))
	return enginefake.New(t, enginefake.WithTCP(&tls.Config{
		Certificates: []tls.Certificate{pair}, ClientAuth: clientAuth, ClientCAs: pool, MinVersion: tls.VersionTLS12,
	}))
}

func TestTLSIncompleteOrUnreadableConfigIsAnError(t *testing.T) {
	p := newPKI(t)
	for name, tc := range map[string]engine.TLSConfig{
		"cert without key":      {CAFile: p.file("ca.pem"), CertFile: p.file("cert.pem")},
		"key without cert":      {CAFile: p.file("ca.pem"), KeyFile: p.file("key.pem")},
		"cert data without key": {CA: p.caPEM, Cert: p.clientCert},
		"unreadable CA":         {CAFile: p.file("missing.pem")},
		"unreadable key":        {CertFile: p.file("cert.pem"), KeyFile: p.file("missing.pem")},
		"CA without a cert":     {CA: []byte("not pem")},
		"mismatched pair":       {Cert: p.clientCert, Key: p.serverKey},
		"both file and data":    {CAFile: p.file("ca.pem"), CA: p.caPEM},
	} {
		c, err := engine.New(engine.Config{Host: "tcp://127.0.0.1:1", TLS: &tc})
		assert.Nil(t, c, name)
		assert.Equal(t, provider.ClassInvalid, engineError(t, err).Class, name)
	}
}

func TestMutualTLSWithFilesAndWithData(t *testing.T) {
	p := newPKI(t)
	f := p.mtlsEngine(t, tls.RequireAndVerifyClientCert)
	for name, cfg := range map[string]engine.TLSConfig{
		"files": {CAFile: p.file("ca.pem"), CertFile: p.file("cert.pem"), KeyFile: p.file("key.pem")},
		"data":  {CA: p.caPEM, Cert: p.clientCert, Key: p.clientKey},
	} {
		c := newClient(t, engine.Config{Host: f.Host(), TLS: &cfg})
		info, err := c.Info(context.Background())
		require.NoError(t, err, name)
		assert.Equal(t, "fake-engine", info.Name, name)
	}
	// no client certificate: the server refuses the handshake
	c := newClient(t, engine.Config{Host: f.Host(), TLS: &engine.TLSConfig{CA: p.caPEM}})
	_, err := c.Info(context.Background())
	assert.Equal(t, provider.ClassUnavailable, engineError(t, err).Class)
	// an unknown CA is not trusted
	other := newPKI(t)
	c = newClient(t, engine.Config{Host: f.Host(), TLS: &engine.TLSConfig{CA: other.caPEM, Cert: p.clientCert, Key: p.clientKey}})
	_, err = c.Info(context.Background())
	assert.Equal(t, provider.ClassUnavailable, engineError(t, err).Class)
	// … unless verification is skipped (the context's choice)
	c = newClient(t, engine.Config{Host: f.Host(), TLS: &engine.TLSConfig{Cert: p.clientCert, Key: p.clientKey, SkipVerify: true}})
	_, err = c.Info(context.Background())
	require.NoError(t, err)
}

func TestTLSNeverFallsBackToPlaintext(t *testing.T) {
	p := newPKI(t)
	plain := enginefake.New(t, enginefake.WithTCP(nil))
	c := newClient(t, engine.Config{Host: plain.Host(), TLS: &engine.TLSConfig{CA: p.caPEM, Cert: p.clientCert, Key: p.clientKey}})
	_, err := c.Info(context.Background())
	assert.Equal(t, provider.ClassUnavailable, engineError(t, err).Class)
	assert.Empty(t, plain.Requests(), "a request went out in plaintext")
}
