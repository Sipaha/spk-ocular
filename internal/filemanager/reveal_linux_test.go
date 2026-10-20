package filemanager

import (
	"bufio"
	"context"
	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/require"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type managerFixture struct {
	mu      sync.Mutex
	calls   chan []string
	wait    chan struct{}
	started chan struct{}
}

func (m *managerFixture) ShowItems(uris []string, startup string) *dbus.Error {
	m.mu.Lock()
	wait := m.wait
	m.mu.Unlock()
	if m.started != nil {
		m.started <- struct{}{}
	}
	if wait != nil {
		<-wait
	}
	m.calls <- append(uris, startup)
	return nil
}
func TestLinuxShowItemsSelectsExactEscapedURIAndAcknowledgesFailure(t *testing.T) {
	dir, err := os.MkdirTemp(os.Getenv("TMPDIR"), "fm")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(dir) }()
	config := filepath.Join(dir, "bus.conf")
	require.NoError(t, os.WriteFile(config, []byte(`<busconfig><type>session</type><listen>unix:path=`+filepath.Join(dir, "bus")+`</listen><auth>EXTERNAL</auth><policy context="default"><allow send_destination="*"/><allow receive_sender="*"/><allow own="*"/></policy></busconfig>`), 0600))
	cmd := exec.Command("dbus-daemon", "--nofork", "--print-address=1", "--config-file="+config)
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	scan := bufio.NewScanner(out)
	require.True(t, scan.Scan())
	address := scan.Text()
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	conn, err := dbus.Connect(address)
	require.NoError(t, err)
	defer conn.Close()
	m := &managerFixture{calls: make(chan []string, 2), started: make(chan struct{}, 2)}
	require.NoError(t, conn.Export(m, "/org/freedesktop/FileManager1", "org.freedesktop.FileManager1"))
	reply, err := conn.RequestName("org.freedesktop.FileManager1", dbus.NameFlagDoNotQueue)
	require.NoError(t, err)
	require.Equal(t, dbus.RequestNameReplyPrimaryOwner, reply)
	path := filepath.Join(dir, "name with spaces;$(no),#?.txt")
	require.NoError(t, Reveal(t.Context(), path))
	require.Equal(t, []string{(&url.URL{Scheme: "file", Path: path}).String(), ""}, <-m.calls)
	m.mu.Lock()
	m.wait = make(chan struct{})
	m.mu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	<-m.started
	result := make(chan error, 1)
	go func() { result <- Reveal(ctx, path) }()
	select {
	case <-m.started:
	case <-time.After(2 * time.Second):
		close(m.wait)
		t.Fatal("ShowItems was not received")
	}
	cancel()
	require.Error(t, <-result)
	close(m.wait)
	<-m.calls

	require.NoError(t, conn.Export(nil, "/org/freedesktop/FileManager1", "org.freedesktop.FileManager1"))
	require.ErrorContains(t, Reveal(t.Context(), path), "could not select")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	require.ErrorContains(t, Reveal(t.Context(), path), "requires")
}
