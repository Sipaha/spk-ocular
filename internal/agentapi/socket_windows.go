package agentapi

import (
	"errors"
	"net"
	"sync"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/spk/spk-ocular/internal/privatefs"
)

var ErrOtherInstance = errors.New("another SPK Ocular instance serves agent access")

type socket struct {
	ln   net.Listener
	once sync.Once
}

// winio reserves the first pipe instance, rejects remote clients, and creates
// every instance with this protected DACL. There is no world-accessible socket
// or unauthenticated TCP fallback. Closing releases the OS-owned pipe name.
func listen(path, _ string) (*socket, error) {
	descriptor, err := privatefs.OwnerDescriptor(false)
	if err != nil {
		return nil, err
	}
	ln, err := winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: descriptor, InputBufferSize: 65536, OutputBufferSize: 65536})
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_PIPE_BUSY) {
		return nil, ErrOtherInstance
	}
	if err != nil {
		return nil, err
	}
	return &socket{ln: ln}, nil
}

func (s *socket) close() { s.once.Do(func() { _ = s.ln.Close() }) }
