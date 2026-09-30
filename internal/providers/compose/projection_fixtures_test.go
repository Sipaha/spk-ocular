package compose

import (
	"time"

	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

// Worlds for projection tests are built directly: engine types are plain
// structs, Raw is a small JSON literal where a test needs it.

var t0 = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

type ctrOpt func(*engine.ContainerInspect)

// ctr is a running Compose container of project/service, number 1.
func ctr(id, name, project, service string, opts ...ctrOpt) *engine.ContainerInspect {
	c := &engine.ContainerInspect{
		ID:      id,
		Name:    "/" + name,
		Created: engine.TimeOf(t0.Add(-time.Hour)),
		Image:   "sha256:img-" + service,
		State:   engine.ContainerState{Status: "running", Running: true, StartedAt: engine.TimeOf(t0.Add(-time.Hour))},
		Config: engine.ContainerConfig{Image: service + ":latest", Labels: map[string]string{
			LabelProject: project, LabelService: service, LabelNumber: "1", LabelOneoff: "False",
		}},
	}
	if project == "" {
		c.Config.Labels = nil
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func status(s string) ctrOpt {
	return func(c *engine.ContainerInspect) {
		c.State.Status = s
		c.State.Running = s == "running" || s == "paused" || s == "restarting"
		c.State.Paused = s == "paused"
		c.State.Restarting = s == "restarting"
		c.State.Dead = s == "dead"
	}
}

func exited(code int, at time.Time) ctrOpt {
	return func(c *engine.ContainerInspect) {
		status("exited")(c)
		c.State.ExitCode = code
		c.State.FinishedAt = engine.TimeOf(at)
	}
}

func health(s string, log ...engine.HealthResult) ctrOpt {
	return func(c *engine.ContainerInspect) { c.State.Health = &engine.Health{Status: s, Log: log} }
}

func check(code int, out string, at time.Time) engine.HealthResult {
	return engine.HealthResult{Start: engine.TimeOf(at.Add(-time.Second)), End: engine.TimeOf(at), ExitCode: code, Output: out}
}

func restarted(count int, startedAt time.Time) ctrOpt {
	return func(c *engine.ContainerInspect) {
		c.RestartCount = count
		c.State.StartedAt = engine.TimeOf(startedAt)
	}
}

func number(n string) ctrOpt {
	return func(c *engine.ContainerInspect) { c.Config.Labels[LabelNumber] = n }
}

func oneoff() ctrOpt {
	return func(c *engine.ContainerInspect) { c.Config.Labels[LabelOneoff] = "True" }
}

func label(k, v string) ctrOpt {
	return func(c *engine.ContainerInspect) { c.Config.Labels[k] = v }
}

func image(id, ref string) ctrOpt {
	return func(c *engine.ContainerInspect) { c.Image, c.Config.Image = id, ref }
}

// onNet attaches the container to a network (by name and id).
func onNet(name, id, ip string) ctrOpt {
	return func(c *engine.ContainerInspect) {
		if c.NetworkSettings.Networks == nil {
			c.NetworkSettings.Networks = map[string]engine.EndpointSettings{}
		}
		c.NetworkSettings.Networks[name] = engine.EndpointSettings{NetworkID: id, IPAddress: ip}
	}
}

func mountVol(name, dest string) ctrOpt {
	return func(c *engine.ContainerInspect) {
		c.Mounts = append(c.Mounts, engine.Mount{Type: "volume", Name: name, Destination: dest, RW: true})
	}
}

func raw(s string) ctrOpt {
	return func(c *engine.ContainerInspect) { c.Raw = []byte(s) }
}

func network(id, name, project string) *engine.Network {
	n := &engine.Network{ID: id, Name: name, Driver: "bridge", Scope: "local", Created: engine.TimeOf(t0.Add(-2 * time.Hour))}
	if project != "" {
		n.Labels = map[string]string{LabelProject: project, LabelNetwork: "default"}
	}
	return n
}

func vol(name, project, createdAt string) *engine.Volume {
	v := &engine.Volume{Name: name, Driver: "local", CreatedAt: createdAt, Scope: "local", Mountpoint: "/var/lib/docker/volumes/" + name + "/_data"}
	if project != "" {
		v.Labels = map[string]string{LabelProject: project, LabelVolume: name}
	}
	return v
}

func img(id string, tags ...string) *engine.ImageInspect {
	return &engine.ImageInspect{ID: id, RepoTags: tags, Size: 1 << 20, Created: engine.TimeOf(t0.Add(-24 * time.Hour))}
}

// world holds every feed.
func world(objs ...any) *World {
	w := &World{
		Containers: map[string]*engine.ContainerInspect{},
		Networks:   map[string]*engine.Network{},
		Volumes:    map[string]*engine.Volume{},
		Images:     map[string]*engine.ImageInspect{},
	}
	for i := range w.Has {
		w.Has[i] = true
	}
	for _, o := range objs {
		switch x := o.(type) {
		case *engine.ContainerInspect:
			w.Containers[x.ID] = x
		case *engine.Network:
			w.Networks[x.ID] = x
		case *engine.Volume:
			w.Volumes[x.Name] = x
		case *engine.ImageInspect:
			w.Images[x.ID] = x
		default:
			panic("world: unknown object")
		}
	}
	return w
}

// without drops feeds from a world (not leased: absent, not empty).
func without(w *World, feeds ...Feed) *World {
	for _, f := range feeds {
		w.Has[f] = false
		switch f {
		case FeedContainers:
			w.Containers = nil
		case FeedNetworks:
			w.Networks = nil
		case FeedVolumes:
			w.Volumes = nil
		case FeedImages:
			w.Images = nil
		}
	}
	return w
}
