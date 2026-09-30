package engine_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
	"github.com/spk/spk-ocular/internal/providers/compose/enginefake"
)

// A container inspect as Docker 29 sends it (trimmed), with fields the
// client does not decode (Env, Args) that Raw must keep.
const webInspect = `{
  "Id": "c0ffee01",
  "Created": "2026-09-30T10:00:00.123456789Z",
  "Path": "/bin/sh", "Args": ["-c", "httpd -f"],
  "State": {
    "Status": "running", "Running": true, "Paused": false, "Restarting": false,
    "OOMKilled": false, "Dead": false, "Pid": 42, "ExitCode": 0, "Error": "",
    "StartedAt": "2026-09-30T10:00:01.5Z", "FinishedAt": "0001-01-01T00:00:00Z",
    "Health": {"Status": "unhealthy", "FailingStreak": 3, "Log": [
      {"Start": "2026-09-30T10:01:00Z", "End": "2026-09-30T10:01:01Z", "ExitCode": 1, "Output": "curl: (7) refused\n"}
    ]}
  },
  "Image": "sha256:abc",
  "Name": "/fixture-web-1",
  "RestartCount": 2,
  "HostConfig": {
    "NetworkMode": "fixture_default",
    "PortBindings": {"80/tcp": [{"HostIp": "127.0.0.1", "HostPort": "8080"}]},
    "RestartPolicy": {"Name": "unless-stopped", "MaximumRetryCount": 0},
    "LogConfig": {"Type": "json-file", "Config": {}}
  },
  "Mounts": [{"Type": "volume", "Name": "fixture_data", "Source": "/var/lib/docker/volumes/fixture_data/_data",
              "Destination": "/data", "Driver": "local", "Mode": "z", "RW": true, "Propagation": ""}],
  "Config": {
    "Hostname": "c0ffee01", "Tty": false, "Env": ["SECRET=value"], "Image": "busybox:latest",
    "Labels": {"com.docker.compose.project": "fixture", "com.docker.compose.service": "web"}
  },
  "NetworkSettings": {
    "Ports": {"80/tcp": [{"HostIp": "127.0.0.1", "HostPort": "8080"}], "443/tcp": null},
    "Networks": {"fixture_default": {"NetworkID": "net1", "EndpointID": "ep1", "Gateway": "172.18.0.1",
      "IPAddress": "172.18.0.2", "IPPrefixLen": 16, "MacAddress": "02:42:ac:12:00:02",
      "Aliases": ["fixture-web-1", "web"], "DNSNames": ["fixture-web-1", "web", "c0ffee01"]}}
  }
}`

func TestContainersListAndInspectDecode(t *testing.T) {
	f := enginefake.New(t)
	f.PutContainerJSON("c0ffee01", []byte(webInspect))
	f.PutContainer(engine.ContainerInspect{
		ID: "beef02", Name: "/other", Created: engine.TimeOf(time.Now()),
		State:  engine.ContainerState{Status: "exited", ExitCode: 3},
		Config: engine.ContainerConfig{Image: "alpine", Labels: map[string]string{"com.docker.compose.project": "other"}},
	})
	c := newClient(t, engine.Config{Host: f.Host()})
	ctx := context.Background()

	all, err := c.ListContainers(ctx, nil)
	require.NoError(t, err)
	require.Len(t, all, 2)
	list, err := c.ListContainers(ctx, engine.Filters{"label": {"com.docker.compose.project=fixture"}})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "c0ffee01", list[0].ID)
	assert.Equal(t, []string{"/fixture-web-1"}, list[0].Names)
	assert.Equal(t, "running", list[0].State)
	assert.Equal(t, "web", list[0].Labels["com.docker.compose.service"])
	reqs := f.Requests()
	last := reqs[len(reqs)-1]
	assert.Equal(t, "1", last.Query.Get("all"))
	var sent map[string][]string
	require.NoError(t, json.Unmarshal([]byte(last.Query.Get("filters")), &sent))
	assert.Equal(t, map[string][]string{"label": {"com.docker.compose.project=fixture"}}, sent)

	ci, err := c.InspectContainer(ctx, "c0ffee01")
	require.NoError(t, err)
	assert.Equal(t, "/fixture-web-1", ci.Name)
	assert.Equal(t, time.Date(2026, 9, 30, 10, 0, 0, 123456789, time.UTC), ci.Created.UTC())
	assert.Equal(t, "sha256:abc", ci.Image)
	assert.Equal(t, 2, ci.RestartCount)
	assert.True(t, ci.State.Running)
	assert.Equal(t, "running", ci.State.Status)
	assert.Equal(t, time.Date(2026, 9, 30, 10, 0, 1, 5e8, time.UTC), ci.State.StartedAt.UTC())
	assert.True(t, ci.State.FinishedAt.IsZero(), "0001-01-01 is never")
	require.NotNil(t, ci.State.Health)
	assert.Equal(t, "unhealthy", ci.State.Health.Status)
	assert.Equal(t, 3, ci.State.Health.FailingStreak)
	require.Len(t, ci.State.Health.Log, 1)
	assert.Equal(t, "curl: (7) refused\n", ci.State.Health.Log[0].Output)
	assert.Equal(t, 1, ci.State.Health.Log[0].ExitCode)
	assert.Equal(t, "busybox:latest", ci.Config.Image)
	assert.Equal(t, "c0ffee01", ci.Config.Hostname)
	assert.False(t, ci.Config.Tty)
	assert.Equal(t, "fixture", ci.Config.Labels["com.docker.compose.project"])
	assert.Equal(t, []engine.PortBinding{{HostIP: "127.0.0.1", HostPort: "8080"}}, ci.HostConfig.PortBindings["80/tcp"])
	assert.Equal(t, "unless-stopped", ci.HostConfig.RestartPolicy.Name)
	assert.Equal(t, "json-file", ci.HostConfig.LogConfig.Type)
	assert.Contains(t, ci.NetworkSettings.Ports, "443/tcp")
	assert.Nil(t, ci.NetworkSettings.Ports["443/tcp"])
	assert.Equal(t, "net1", ci.NetworkSettings.Networks["fixture_default"].NetworkID)
	assert.Equal(t, "172.18.0.2", ci.NetworkSettings.Networks["fixture_default"].IPAddress)
	require.Len(t, ci.Mounts, 1)
	assert.Equal(t, engine.Mount{Type: "volume", Name: "fixture_data", Source: "/var/lib/docker/volumes/fixture_data/_data",
		Destination: "/data", Driver: "local", Mode: "z", RW: true}, ci.Mounts[0])
	// the whole answer is kept for details
	assert.JSONEq(t, webInspect, string(ci.Raw))

	other, err := c.InspectContainer(ctx, "beef02")
	require.NoError(t, err)
	assert.Nil(t, other.State.Health, "no healthcheck")
	assert.Equal(t, 3, other.State.ExitCode)

	_, err = c.InspectContainer(ctx, "nope")
	assert.True(t, engine.IsNotFound(err))
	assert.Contains(t, err.Error(), "No such container: nope")
}

func TestInfoDecodes(t *testing.T) {
	f := enginefake.New(t)
	at := time.Date(2026, 9, 30, 12, 0, 0, 987654321, time.FixedZone("MSK", 3*3600))
	f.SetInfo(engine.Info{ID: "ABCD", Name: "dind", ServerVersion: "29.3.1", OSType: "linux", SystemTime: engine.TimeOf(at)})
	c := newClient(t, engine.Config{Host: f.Host()})
	info, err := c.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "ABCD", info.ID)
	assert.Equal(t, "dind", info.Name)
	assert.Equal(t, "29.3.1", info.ServerVersion)
	assert.Equal(t, "linux", info.OSType)
	assert.True(t, at.Equal(info.SystemTime.Time), "the daemon's clock with nanoseconds")

	p, err := c.Ping(context.Background())
	require.NoError(t, err)
	assert.Equal(t, engine.Ping{APIVersion: "1.54", OSType: "linux", Version: "1.54"}, p)
}

func TestNetworksVolumesImagesDecode(t *testing.T) {
	f := enginefake.New(t)
	f.PutNetworkJSON("net1", []byte(`{"Name":"fixture_default","Id":"net1","Created":"2026-09-30T10:00:00.5Z",
		"Scope":"local","Driver":"bridge","Internal":false,"Attachable":false,"IPAM":{"Driver":"default"},
		"Labels":{"com.docker.compose.project":"fixture"},
		"Containers":{"c0ffee01":{"Name":"fixture-web-1","EndpointID":"ep1","MacAddress":"m","IPv4Address":"172.18.0.2/16","IPv6Address":""}}}`))
	f.PutNetwork(engine.Network{ID: "net2", Name: "ocular-ext-net", Driver: "bridge", Scope: "local"})
	f.PutVolumeJSON("fixture_data", []byte(`{"CreatedAt":"2026-09-30T10:00:00Z","Driver":"local",
		"Labels":{"com.docker.compose.project":"fixture"},"Mountpoint":"/var/lib/docker/volumes/fixture_data/_data",
		"Name":"fixture_data","Options":null,"Scope":"local"}`))
	f.PutVolume(engine.Volume{Name: "ocular-ext-vol", Driver: "local", Scope: "local"})
	f.PutImageJSON("sha256:abc", []byte(`{"Id":"sha256:abc","RepoTags":["busybox:latest"],"RepoDigests":["busybox@sha256:d"],
		"Created":"2026-01-01T00:00:00Z","Size":4400000,"Os":"linux","Architecture":"amd64",
		"Config":{"Labels":{"org.opencontainers.image.title":"busybox"},"Env":["PATH=/bin"]}}`))
	c := newClient(t, engine.Config{Host: f.Host()})
	ctx := context.Background()

	nets, err := c.ListNetworks(ctx, engine.Filters{"label": {"com.docker.compose.project"}})
	require.NoError(t, err)
	require.Len(t, nets, 1)
	assert.Equal(t, "fixture_default", nets[0].Name)
	assert.Nil(t, nets[0].Raw, "a list item is not an inspect")
	n, err := c.InspectNetwork(ctx, "net1")
	require.NoError(t, err)
	assert.Equal(t, "bridge", n.Driver)
	assert.Equal(t, "local", n.Scope)
	assert.Equal(t, time.Date(2026, 9, 30, 10, 0, 0, 5e8, time.UTC), n.Created.UTC())
	assert.Equal(t, "fixture-web-1", n.Containers["c0ffee01"].Name)
	assert.Contains(t, string(n.Raw), `"IPAM"`)

	vols, err := c.ListVolumes(ctx, nil)
	require.NoError(t, err)
	require.Len(t, vols.Volumes, 2)
	v, err := c.InspectVolume(ctx, "fixture_data")
	require.NoError(t, err)
	assert.Equal(t, "2026-09-30T10:00:00Z", v.CreatedAt, "kept as text")
	assert.Equal(t, "/var/lib/docker/volumes/fixture_data/_data", v.Mountpoint)
	assert.Equal(t, "fixture", v.Labels["com.docker.compose.project"])
	assert.Contains(t, string(v.Raw), `"Options"`)
	ext, err := c.InspectVolume(ctx, "ocular-ext-vol")
	require.NoError(t, err)
	assert.Empty(t, ext.CreatedAt)

	imgs, err := c.ListImages(ctx, nil)
	require.NoError(t, err)
	require.Len(t, imgs, 1)
	assert.Equal(t, []string{"busybox:latest"}, imgs[0].RepoTags)
	img, err := c.InspectImage(ctx, "sha256:abc")
	require.NoError(t, err)
	assert.Equal(t, int64(4400000), img.Size)
	assert.Equal(t, []string{"busybox@sha256:d"}, img.RepoDigests)
	assert.Equal(t, "busybox", img.Config.Labels["org.opencontainers.image.title"])
	assert.Contains(t, string(img.Raw), `"Env"`)

	_, err = c.InspectVolume(ctx, "gone")
	assert.Equal(t, provider.ClassNotFound, engine.ClassOf(err))
}

func TestVolumeListWarningsArePassedOn(t *testing.T) {
	f := enginefake.New(t)
	f.AddHook(enginefake.Body("/volumes", []byte(`{"Volumes":[],"Warnings":["driver x: timeout"]}`)))
	c := newClient(t, engine.Config{Host: f.Host()})
	l, err := c.ListVolumes(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"driver x: timeout"}, l.Warnings)
}

func TestAnUndecodableAnswerIsAnError(t *testing.T) {
	f := enginefake.New(t)
	f.AddHook(enginefake.Body("/info", []byte(`<html>not json</html>`)))
	c := newClient(t, engine.Config{Host: f.Host()})
	_, err := c.Info(context.Background())
	assert.Equal(t, provider.ClassInternal, engine.ClassOf(err))
}
