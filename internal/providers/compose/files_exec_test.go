package compose

import (
	"bytes"
	"context"
	"encoding/binary"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRawExecSeparatesFramesAndPreservesBytes(t *testing.T) {
	var input bytes.Buffer
	for _, f := range []struct {
		stream byte
		text   string
	}{{1, "a\r\n\x00"}, {2, "permission denied\n"}, {1, "b\n"}} {
		h := make([]byte, 8)
		h[0] = f.stream
		binary.BigEndian.PutUint32(h[4:], uint32(len(f.text)))
		input.Write(h)
		input.WriteString(f.text)
	}
	var out, stderr bytes.Buffer
	require.NoError(t, copyExecOutput(&input, &out, &stderr))
	require.Equal(t, "a\r\n\x00b\n", out.String())
	require.Equal(t, "permission denied\n", stderr.String())
	require.Error(t, copyExecOutput(bytes.NewReader([]byte{1}), &out, &stderr))
}

func TestDindRawFileReadWrite(t *testing.T) {
	host := dindHost(t)
	id := strings.TrimSpace(dindDocker(t, host, "run", "-d", "--label", "ocular.test=files", "busybox:latest", "sleep", "infinity"))
	t.Cleanup(func() { dindDocker(t, host, "rm", "-f", id) })
	s := dindSession(t, host)
	c, err := s.cl.InspectContainer(t.Context(), id)
	require.NoError(t, err)
	ref := ctrRef(c)
	run := func(argv []string, input string) (string, string) {
		t.Helper()
		dindVerify(t, host)
		h, err := s.PrepareExec(t.Context(), ref, provider.ExecRequest{Instance: id, Command: argv})
		require.NoError(t, err)
		defer h.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		var out, stderr bytes.Buffer
		st, err := h.Run(ctx, provider.Terminal{Raw: true, Stdin: strings.NewReader(input), Stdout: &out, Stderr: &stderr})
		require.NoError(t, err)
		require.Equal(t, provider.ExitStatus{Known: true}, st, stderr.String())
		return out.String(), stderr.String()
	}
	text := "a: привет\r\nb: 42\n"
	run([]string{"sh", "-c", `head -c "$1" > /config.yaml`, "files", strconv.Itoa(len(text))}, text)
	got, _ := run([]string{"cat", "/config.yaml"}, "")
	require.Equal(t, text, got)
	got, stderr := run([]string{"sh", "-c", `printf 'out\n'; printf 'err\n' >&2`}, "")
	require.Equal(t, "out\n", got)
	require.Equal(t, "err\n", stderr)
}
