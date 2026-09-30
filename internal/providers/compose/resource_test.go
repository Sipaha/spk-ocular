package compose

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

func get(t *testing.T, w *World, ref core.Ref) *core.Resource {
	t.Helper()
	res, err := resourceOf(w, ref, t0)
	require.NoError(t, err)
	return res
}

func fact(res *core.Resource, key string) (string, bool) {
	for _, f := range res.Facts {
		if f.Key == key {
			return f.Value, true
		}
	}
	return "", false
}

type rel struct {
	typ, kind, name, uid, title string
	inert                       bool
}

func rels(res *core.Resource) []rel {
	out := []rel{}
	for _, r := range res.Relations {
		out = append(out, rel{r.Type, r.Ref.Kind, r.Ref.Name, r.Ref.UID, r.Ref.Title, r.Inert})
	}
	return out
}

func errClass(err error) provider.ErrorClass {
	var pe *provider.Error
	if errors.As(err, &pe) {
		return pe.Class
	}
	return ""
}

func fixtureWorld() *World {
	return world(
		ctr("c1", "p-web-1", "p", "web",
			image("sha256:web", "web:latest"),
			onNet("p_default", "n-p", "172.18.0.2"),
			onNet("gone-net", "n-gone", "10.9.0.2"), // not in the networks feed
			mountVol("p_data", "/data"),
			mountVol("missing", "/missing"),
			restarted(2, t0.Add(-3*time.Minute)),
			health("healthy", check(1, "boom", t0.Add(-time.Minute)), check(0, "fine\n", t0.Add(-30*time.Second))),
			func(c *engine.ContainerInspect) {
				c.HostConfig.RestartPolicy = engine.RestartPolicy{Name: "on-failure", MaximumRetryCount: 5}
				c.HostConfig.LogConfig.Type = "json-file"
				c.NetworkSettings.Ports = map[string][]engine.PortBinding{"80/tcp": {{HostIP: "127.0.0.1", HostPort: "8080"}}}
				c.Mounts = append(c.Mounts, engine.Mount{Type: "bind", Source: "/src", Destination: "/app", RW: false})
			},
			raw(`{"Id":"c1","Name":"/p-web-1","Config":{"Env":["A=1"]},"State":{"Status":"running"}}`)),
		ctr("c2", "p-web-2", "p", "web", number("2"), exited(0, t0.Add(-time.Hour)), image("sha256:web", "web:latest")),
		ctr("c3", "p-web-run-1", "p", "web", oneoff(), image("sha256:web", "web:latest")),
		ctr("x1", "plain", "", "", image("sha256:web", "web:latest"), mountVol("p_data", "/d")),
		network("n-p", "p_default", "p"),
		vol("p_data", "p", "2026-09-30T10:00:00Z"),
		img("sha256:web", "web:latest", "web:1"),
	)
}

func TestContainerDetails(t *testing.T) {
	w := fixtureWorld()
	// The network's inspect sees an endpoint the container's inspect does not.
	w.Networks["n-late"] = network("n-late", "late", "")
	w.Networks["n-late"].Containers = map[string]engine.NetworkEndpoint{"c1": {Name: "p-web-1", IPv4Address: "10.5.0.3/16"}}

	res := get(t, w, core.Ref{Kind: KindContainers, Name: "c1", UID: "c1"})
	assert.Equal(t, core.Ref{Provider: ProviderID, Scope: "p", Kind: KindContainers, Name: "c1", UID: "c1", Title: "p-web-1"}, res.Ref)
	assert.Equal(t, core.HealthWarning, res.Health.State)
	assert.Equal(t, "RestartedByPolicy", res.Health.Reason)
	for key, want := range map[string]string{
		"Id":                                "c1",
		"Name":                              "p-web-1",
		"Project":                           "p",
		"Service":                           "web",
		"Number":                            "1",
		"Image":                             "web:latest (web)",
		"State":                             "running",
		"Health check":                      "healthy",
		"Started":                           t0.Add(-3 * time.Minute).Format(time.RFC3339),
		"Restart policy":                    "on-failure (max 5)",
		"Restarts":                          "2",
		"Ports":                             "127.0.0.1:8080→80/tcp",
		"Networks":                          "gone-net: 10.9.0.2\nlate: 10.5.0.3\np_default: 172.18.0.2",
		"Mounts":                            "volume p_data → /data\nvolume missing → /missing\nbind /src → /app (ro)",
		"Log driver":                        "json-file",
		"Last health check (as of reading)": t0.Add(-30*time.Second).Format(time.RFC3339) + ", exit 0: fine",
		"created":                           t0.Add(-time.Hour).Format(time.RFC3339),
	} {
		got, ok := fact(res, key)
		if assert.True(t, ok, "no fact %s", key) {
			assert.Equal(t, want, got, key)
		}
	}
	_, ok := fact(res, "Finished")
	assert.False(t, ok, "running: not finished")
	labels, _ := fact(res, "labels")
	assert.Contains(t, labels, LabelProject+"=p")

	// YAML is the whole inspect answer as sent.
	assert.Equal(t, "Config:\n  Env:\n  - A=1\nId: c1\nName: /p-web-1\nState:\n  Status: running\n", res.YAML)

	assert.Equal(t, []rel{
		{"owner", KindServices, "p/web", "p/web", "web", false},
		{"uses", KindNetworks, "n-gone", "n-gone", "gone-net", true},
		{"uses", KindNetworks, "n-late", "n-late", "late", false},
		{"uses", KindNetworks, "n-p", "n-p", "p_default", false},
		{"uses", KindVolumes, "missing", "", "", true},
		{"uses", KindVolumes, "p_data", "p_data@2026-09-30T10:00:00Z", "", false},
		{"uses", KindImages, "sha256:web", "sha256:web", "web:latest", false},
	}, rels(res))
}

// A one-off belongs to the project but not to the service.
func TestOneOffContainerHasNoServiceOwner(t *testing.T) {
	res := get(t, fixtureWorld(), core.Ref{Kind: KindContainers, Name: "c3"})
	for _, r := range res.Relations {
		assert.NotEqual(t, "owner", r.Type)
	}
	v, _ := fact(res, "One-off")
	assert.Equal(t, "yes", v)
}

// Without the networks/volumes/images feeds, a container's relations are
// still openable by their keys (the volume without its UID).
func TestContainerRelationsWithoutTheOtherFeeds(t *testing.T) {
	w := without(fixtureWorld(), FeedNetworks, FeedVolumes, FeedImages)
	res := get(t, w, core.Ref{Kind: KindContainers, Name: "c1"})
	assert.Equal(t, []rel{
		{"owner", KindServices, "p/web", "p/web", "web", false},
		{"uses", KindNetworks, "n-gone", "n-gone", "gone-net", false},
		{"uses", KindNetworks, "n-p", "n-p", "p_default", false},
		{"uses", KindVolumes, "missing", "", "", false},
		{"uses", KindVolumes, "p_data", "", "", false},
		{"uses", KindImages, "sha256:web", "sha256:web", "web:latest", false},
	}, rels(res))
}

func TestServiceDetails(t *testing.T) {
	res := get(t, fixtureWorld(), core.Ref{Kind: KindServices, Name: "p/web", UID: "p/web"})
	assert.Equal(t, core.Ref{Provider: ProviderID, Scope: "p", Kind: KindServices, Name: "p/web", UID: "p/web", Title: "web"}, res.Ref)
	assert.Equal(t, []rel{
		{"owns", KindContainers, "c1", "c1", "p-web-1", false},
		{"owns", KindContainers, "c2", "c2", "p-web-2", false},
	}, rels(res), "members only, not the one-off")
	for key, want := range map[string]string{
		"Project":    "p",
		"Service":    "web",
		"Running":    "1/2",
		"Status":     "RestartedByPolicy",
		"Containers": "p-web-1: running\np-web-2: exited (0)",
		"Images":     "web:latest",
	} {
		got, _ := fact(res, key)
		assert.Equal(t, want, got, key)
	}
	assert.Contains(t, res.YAML, "service: web")
	assert.Contains(t, res.YAML, "name: p-web-2")
	assert.Equal(t, core.HealthWarning, res.Health.State)
}

func TestProjectDetails(t *testing.T) {
	res := get(t, fixtureWorld(), core.Ref{Kind: KindProjects, Name: "p"})
	assert.Equal(t, core.Ref{Provider: ProviderID, Kind: KindProjects, Name: "p", UID: "p"}, res.Ref)
	assert.Equal(t, []rel{
		{"owns", KindServices, "p/web", "p/web", "web", false},
		{"owns", KindContainers, "c3", "c3", "p-web-run-1", false},
		{"owns", KindNetworks, "n-p", "n-p", "p_default", false},
		{"owns", KindVolumes, "p_data", "p_data@2026-09-30T10:00:00Z", "", false},
	}, rels(res))
	v, _ := fact(res, "Containers")
	assert.Equal(t, "3", v)
	assert.Contains(t, res.YAML, "project: p")

	// A project only a volume keeps.
	only := get(t, world(vol("v", "lonely", "")), core.Ref{Kind: KindProjects, Name: "lonely"})
	assert.Equal(t, []rel{{"owns", KindVolumes, "v", "v@", "", false}}, rels(only))
}

// Users of a network: Compose containers openable; others are named, not
// openable (a kind the provider does not show).
func TestNetworkDetails(t *testing.T) {
	w := fixtureWorld()
	w.Networks["n-p"].Containers = map[string]engine.NetworkEndpoint{
		"c1":      {Name: "p-web-1"},
		"x1":      {Name: "plain"},        // in the World, no Compose labels
		"foreign": {Name: "someone-else"}, // not observed at all
	}
	w.Networks["n-p"].Raw = []byte(`{"Id":"n-p","Name":"p_default"}`)
	res := get(t, w, core.Ref{Kind: KindNetworks, Name: "n-p", UID: "n-p"})
	assert.Equal(t, core.Ref{Provider: ProviderID, Scope: "p", Kind: KindNetworks, Name: "n-p", UID: "n-p", Title: "p_default"}, res.Ref)
	assert.Equal(t, []rel{
		{"used-by", KindContainers, "c1", "c1", "p-web-1", false},
		{"used-by", KindContainers, "x1", "x1", "plain", true},
		{"used-by", KindContainers, "foreign", "foreign", "someone-else", true},
	}, rels(res))
	assert.Equal(t, "Id: n-p\nName: p_default\n", res.YAML)
	v, _ := fact(res, "Driver")
	assert.Equal(t, "bridge", v)
}

func TestVolumeDetailsAndIdentity(t *testing.T) {
	w := fixtureWorld()
	ref := core.Ref{Kind: KindVolumes, Name: "p_data", UID: "p_data@2026-09-30T10:00:00Z"}
	res := get(t, w, ref)
	assert.Equal(t, core.Ref{Provider: ProviderID, Scope: "p", Kind: KindVolumes, Name: "p_data", UID: "p_data@2026-09-30T10:00:00Z"}, res.Ref)
	assert.Equal(t, []rel{
		{"used-by", KindContainers, "c1", "c1", "p-web-1", false},
		{"used-by", KindContainers, "x1", "x1", "plain", true},
	}, rels(res))
	v, _ := fact(res, "Identity")
	assert.Equal(t, messageTexts["volume.identity"], v, "the details say the limitation")
	assert.Contains(t, res.YAML, "Name: p_data")

	// Re-created in another second: the old ref is gone.
	w.Volumes["p_data"] = vol("p_data", "p", "2026-09-30T10:00:07Z")
	_, err := resourceOf(w, ref, t0)
	assert.Equal(t, provider.ClassGone, errClass(err))
	// In the same second: indistinguishable, the current one opens
	// (a documented limitation, decision 4).
	w.Volumes["p_data"] = vol("p_data", "p", "2026-09-30T10:00:00Z")
	_, err = resourceOf(w, ref, t0)
	assert.NoError(t, err)
	// Without a UID (a relation named by a container), the current one.
	_, err = resourceOf(w, core.Ref{Kind: KindVolumes, Name: "p_data"}, t0)
	assert.NoError(t, err)
}

func TestImageDetails(t *testing.T) {
	res := get(t, fixtureWorld(), core.Ref{Kind: KindImages, Name: "sha256:web"})
	assert.Equal(t, core.Ref{Provider: ProviderID, Kind: KindImages, Name: "sha256:web", UID: "sha256:web", Title: "web:latest"}, res.Ref)
	assert.Equal(t, []rel{
		{"used-by", KindContainers, "c1", "c1", "p-web-1", false},
		{"used-by", KindContainers, "c2", "c2", "p-web-2", false},
		{"used-by", KindContainers, "c3", "c3", "p-web-run-1", false},
		{"used-by", KindContainers, "x1", "x1", "plain", true},
	}, rels(res))
	v, _ := fact(res, "Tags")
	assert.Equal(t, "web:latest\nweb:1", v)
	v, _ = fact(res, "Size")
	assert.Equal(t, "1.0 MiB (1048576 bytes)", v)
}

func TestNotFoundAndGone(t *testing.T) {
	w := fixtureWorld()
	for _, c := range []struct {
		ref  core.Ref
		want provider.ErrorClass
	}{
		{core.Ref{Kind: KindContainers, Name: "nope"}, provider.ClassNotFound},
		{core.Ref{Kind: KindContainers, Name: "x1"}, provider.ClassNotFound}, // not a Compose container
		{core.Ref{Kind: KindContainers, Name: "c1", UID: "other"}, provider.ClassGone},
		{core.Ref{Kind: KindServices, Name: "p/db"}, provider.ClassNotFound},
		{core.Ref{Kind: KindServices, Name: "p/web", UID: "q/web"}, provider.ClassGone},
		{core.Ref{Kind: KindProjects, Name: "nope"}, provider.ClassNotFound},
		{core.Ref{Kind: KindNetworks, Name: "nope"}, provider.ClassNotFound},
		{core.Ref{Kind: KindVolumes, Name: "nope"}, provider.ClassNotFound},
		{core.Ref{Kind: KindImages, Name: "sha256:nope"}, provider.ClassNotFound},
		{core.Ref{Kind: "pods", Name: "x"}, provider.ClassUnsupported},
	} {
		_, err := resourceOf(w, c.ref, t0)
		assert.Equal(t, c.want, errClass(err), "%v", c.ref)
	}
	// A kind whose feed is not in the World is the caller's mistake, not "not found".
	_, err := resourceOf(without(fixtureWorld(), FeedImages), core.Ref{Kind: KindImages, Name: "sha256:web"}, t0)
	assert.Equal(t, provider.ClassInternal, errClass(err))
}

// Each relation list is capped and says so.
func TestRelationsAreCapped(t *testing.T) {
	w := world(network("n", "big", "p"))
	for i := range maxRelated + 5 {
		id := fmt.Sprintf("c%03d", i)
		w.Containers[id] = ctr(id, "p-web-"+id, "p", "web", onNet("big", "n", ""))
	}
	res := get(t, w, core.Ref{Kind: KindNetworks, Name: "n"})
	assert.Len(t, res.Relations, maxRelated)
	assert.True(t, res.RelationsTruncated)
	assert.False(t, get(t, fixtureWorld(), core.Ref{Kind: KindNetworks, Name: "n-p"}).RelationsTruncated)
}

// The target is the caller's: stamped into the resource and its relations.
func TestResourceWithTarget(t *testing.T) {
	res := resourceWithTarget("context:x", get(t, fixtureWorld(), core.Ref{Kind: KindContainers, Name: "c1"}))
	assert.Equal(t, "context:x", res.Ref.Target)
	for _, r := range res.Relations {
		assert.Equal(t, "context:x", r.Ref.Target)
		assert.Equal(t, ProviderID, r.Ref.Provider)
	}
}

// Details of an exited container: finish time, exit code, the evidence
// of an unhealthy check in its health.
func TestExitedContainerFacts(t *testing.T) {
	w := world(ctr("c", "p-job-1", "p", "job", exited(3, t0.Add(-time.Minute)), func(c *engine.ContainerInspect) {
		c.State.Error = "oops"
		c.State.OOMKilled = true
	}))
	res := get(t, w, core.Ref{Kind: KindContainers, Name: "c"})
	for key, want := range map[string]string{
		"State":      "exited (3)",
		"Finished":   t0.Add(-time.Minute).Format(time.RFC3339),
		"Exit code":  "3",
		"Error":      "oops",
		"OOM killed": "yes",
	} {
		got, _ := fact(res, key)
		assert.Equal(t, want, got, key)
	}
	assert.True(t, strings.HasPrefix(res.YAML, "Config:"), "no Raw: the decoded object")
}
