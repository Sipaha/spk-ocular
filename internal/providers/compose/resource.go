package compose

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

// maxRelated caps each relation list (as Kubernetes does): a network
// used by thousands of containers must not turn the details into a table.
const maxRelated = 200

// Relation types beyond the generic ones: a container uses networks,
// volumes and an image (many-to-many, not ownership); they are used by it.
const (
	relUses   = "uses"
	relUsedBy = "used-by"
)

// resourceOf is the details of the object ref names in w (by its key,
// ref.Name): health, facts, YAML (the inspect answer as sent; services
// and projects — a summary of their members) and relations. A ref whose
// UID no longer matches the object with its key (a volume re-created in
// another second) is gone; an absent key is not found. The kind's primary
// feed (kindFeeds(kind)[0]) must be in w. Refs carry Provider; Target is
// the caller's (resourceWithTarget).
func resourceOf(w *World, ref core.Ref, now time.Time) (*core.Resource, error) {
	feeds := kindFeeds(ref.Kind)
	if feeds == nil {
		return nil, provider.Said(provider.ClassUnsupported, msg("error.unknownKind", "kind", ref.Kind))
	}
	if !w.Has[feeds[0]] {
		return nil, provider.Said(provider.ClassInternal, msg("error.notObserved", "feed", feeds[0].String()))
	}
	x := newIndex(w)
	var res *core.Resource
	switch ref.Kind {
	case KindContainers:
		if c := w.Containers[ref.Name]; c != nil {
			res = x.containerResource(c, now)
		}
	case KindServices:
		if s := x.byService[ref.Name]; s != nil {
			res = x.serviceResource(s, now)
		}
	case KindProjects:
		for _, p := range x.projectNames() {
			if p == ref.Name {
				res = x.projectResource(p, now)
			}
		}
	case KindNetworks:
		if n := w.Networks[ref.Name]; n != nil {
			res = x.networkResource(n)
		}
	case KindVolumes:
		if v := w.Volumes[ref.Name]; v != nil {
			res = x.volumeResource(v)
		}
	case KindImages:
		if im := w.Images[ref.Name]; im != nil {
			res = x.imageResource(im)
		}
	}
	if res == nil {
		return nil, provider.Said(provider.ClassNotFound, msg("error.notFound", "kind", ref.Kind, "name", shown(ref)))
	}
	if ref.Scope != "" && res.Ref.Scope != ref.Scope {
		return nil, notInScope(ref)
	}
	if ref.UID != "" && ref.UID != res.Ref.UID {
		// The same key, another object: never shown as the one asked for.
		return nil, provider.Said(provider.ClassGone, msg("error.gone", "kind", ref.Kind, "name", shown(ref)))
	}
	sortRelations(res.Relations)
	res.Relations, res.RelationsTruncated = capRelations(res.Relations)
	if res.Relations == nil {
		res.Relations = []core.Relation{}
	}
	return res, nil
}

// resourceWithTarget stamps the target id into the resource's refs (its
// own and its relations'), in place, and returns it.
func resourceWithTarget(target string, r *core.Resource) *core.Resource {
	r.Ref.Target = target
	for i := range r.Relations {
		r.Relations[i].Ref.Target = target
	}
	return r
}

func shown(ref core.Ref) string {
	if ref.Title != "" {
		return ref.Title
	}
	return ref.Name
}

// Containers.

func (x *index) containerResource(c *engine.ContainerInspect, now time.Time) *core.Resource {
	h, _ := containerHealth(c, now)
	st := c.State
	f := facts{}
	f.add("Id", c.ID)
	f.add("Name", containerName(c))
	f.add("Project", projectOf(c))
	f.add("Service", c.Config.Labels[LabelService])
	f.add("Number", c.Config.Labels[LabelNumber])
	if isOneoff(c) {
		f.add("One-off", "yes")
	}
	f.add("Image", c.Config.Image+" ("+shortID(c.Image)+")")
	f.add("State", statusText(c))
	f.add("Health check", healthcheckStatus(c))
	f.addTime("Started", st.StartedAt.Time)
	if !st.Running {
		f.addTime("Finished", st.FinishedAt.Time)
	}
	if st.Status == "exited" || st.Status == "dead" {
		f.add("Exit code", strconv.Itoa(st.ExitCode))
	}
	f.add("Error", st.Error)
	if st.OOMKilled {
		f.add("OOM killed", "yes")
	}
	f.add("Restart policy", restartPolicy(c.HostConfig.RestartPolicy))
	f.add("Restarts", strconv.Itoa(c.RestartCount))
	f.add("Ports", strings.Join(publishedPorts(c), "\n"))
	var nets []string
	for _, l := range x.links[c.ID] {
		if l.ip != "" {
			nets = append(nets, l.name+": "+l.ip)
		} else {
			nets = append(nets, l.name)
		}
	}
	f.add("Networks", strings.Join(nets, "\n"))
	f.add("Mounts", strings.Join(mounts(c.Mounts), "\n"))
	f.add("Log driver", c.HostConfig.LogConfig.Type)
	if st.Health != nil && len(st.Health.Log) > 0 {
		last := st.Health.Log[len(st.Health.Log)-1]
		v := fmt.Sprintf("exit %d", last.ExitCode)
		if !last.End.IsZero() {
			v = last.End.UTC().Format(time.RFC3339) + ", " + v
		}
		if out := bounded(strings.TrimSpace(last.Output), maxEvidence); out != "" {
			v += ": " + out
		}
		f.add("Last health check (as of reading)", v)
	}
	f.addTime("created", c.Created.Time)
	f.labels(c.Config.Labels)

	r := &core.Resource{Ref: containerRef(c), Health: h, Facts: f.list, YAML: inspectYAML(c.Raw, c)}
	if s := c.Config.Labels[LabelService]; projectOf(c) != "" && s != "" && !isOneoff(c) {
		r.Relations = append(r.Relations, core.Relation{Type: "owner", Ref: serviceRef(projectOf(c), s)})
	}
	for _, l := range x.links[c.ID] {
		r.Relations = append(r.Relations, x.networkRelation(l))
	}
	seen := map[string]bool{}
	for _, m := range c.Mounts {
		if m.Type != "volume" || m.Name == "" || seen[m.Name] {
			continue
		}
		seen[m.Name] = true
		r.Relations = append(r.Relations, x.volumeRelation(m.Name))
	}
	if c.Image != "" {
		r.Relations = append(r.Relations, x.imageRelation(c))
	}
	return r
}

// The objects a container uses: openable when observed; named, not
// openable, when their feed is observed and they are not in it (removed,
// or the link could not be pinned down); without their feed, openable by
// their key (a volume then without its UID: the current one).

func (x *index) networkRelation(l netLink) core.Relation {
	ref := core.Ref{Provider: ProviderID, Kind: KindNetworks, Name: l.id, UID: l.id, Title: l.name}
	if l.id == "" {
		ref.Name = l.name
		return core.Relation{Type: relUses, Ref: ref, Inert: true}
	}
	if !x.w.Has[FeedNetworks] {
		return core.Relation{Type: relUses, Ref: ref}
	}
	if n := x.w.Networks[l.id]; n != nil {
		return core.Relation{Type: relUses, Ref: networkRef(n)}
	}
	return core.Relation{Type: relUses, Ref: ref, Inert: true}
}

func (x *index) volumeRelation(name string) core.Relation {
	ref := core.Ref{Provider: ProviderID, Kind: KindVolumes, Name: name}
	if !x.w.Has[FeedVolumes] {
		return core.Relation{Type: relUses, Ref: ref}
	}
	if v := x.w.Volumes[name]; v != nil {
		return core.Relation{Type: relUses, Ref: volumeRef(v)}
	}
	return core.Relation{Type: relUses, Ref: ref, Inert: true}
}

func (x *index) imageRelation(c *engine.ContainerInspect) core.Relation {
	ref := core.Ref{Provider: ProviderID, Kind: KindImages, Name: c.Image, UID: c.Image, Title: c.Config.Image}
	if !x.w.Has[FeedImages] {
		return core.Relation{Type: relUses, Ref: ref}
	}
	if im := x.w.Images[c.Image]; im != nil {
		return core.Relation{Type: relUses, Ref: imageRef(im)}
	}
	return core.Relation{Type: relUses, Ref: ref, Inert: true}
}

// usedBy: observed containers that pass use are openable, whether standalone
// or Compose members. Unobserved network endpoints remain inert.
func (x *index) usedByRelations(use func(c *engine.ContainerInspect) bool) []core.Relation {
	var out []core.Relation
	for _, c := range x.w.Containers {
		if use(c) {
			out = append(out, core.Relation{Type: relUsedBy, Ref: containerRef(c)})
		}
	}
	return out
}

func restartPolicy(p engine.RestartPolicy) string {
	switch {
	case p.Name == "":
		return "no"
	case p.MaximumRetryCount > 0:
		return fmt.Sprintf("%s (max %d)", p.Name, p.MaximumRetryCount)
	}
	return p.Name
}

// mounts: "type source → destination", read-only marked; volumes by name.
func mounts(ms []engine.Mount) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		src := m.Source
		if m.Type == "volume" && m.Name != "" {
			src = m.Name
		}
		s := m.Type + " " + src + " → " + m.Destination
		if !m.RW {
			s += " (ro)"
		}
		out = append(out, s)
	}
	return out
}

// Services and projects: logical objects, their YAML is a summary.

type memberSummary struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	State  string `json:"state"`
	Health string `json:"health,omitempty"`
	Image  string `json:"image"`
}

func summaries(cs []*engine.ContainerInspect) []memberSummary {
	out := make([]memberSummary, 0, len(cs))
	for _, c := range cs {
		out = append(out, memberSummary{ID: c.ID, Name: containerName(c), State: statusText(c), Health: healthcheckStatus(c), Image: c.Config.Image})
	}
	return out
}

func (x *index) serviceResource(s *service, now time.Time) *core.Resource {
	h, _ := membersHealth(s.members, now)
	running := runningCount(s.members)
	f := facts{}
	f.add("Project", s.project)
	f.add("Service", s.name)
	f.add("Running", fmt.Sprintf("%d/%d", running, len(s.members)))
	f.add("Status", groupStatus(h, running))
	var list []string
	for _, c := range s.members {
		list = append(list, containerName(c)+": "+statusText(c))
	}
	f.add("Containers", strings.Join(list, "\n"))
	f.add("Images", strings.Join(memberImages(s.members), "\n"))
	y := toYAML(struct {
		Project    string          `json:"project"`
		Service    string          `json:"service"`
		Containers []memberSummary `json:"containers"`
	}{s.project, s.name, summaries(s.members)})
	r := &core.Resource{Ref: serviceRef(s.project, s.name), Health: h, Facts: f.list, YAML: y}
	for _, c := range s.members {
		r.Relations = append(r.Relations, core.Relation{Type: "owns", Ref: containerRef(c)})
	}
	return r
}

func (x *index) projectResource(name string, now time.Time) *core.Resource {
	p := x.project(name)
	h, _ := membersHealth(p.members, now)
	f := facts{}
	f.add("Services", strconv.Itoa(len(p.services)))
	f.add("Containers", strconv.Itoa(len(p.containers)))
	f.add("Running", strconv.Itoa(runningCount(p.containers)))
	f.add("Working dir", p.workingDir)
	f.add("Config files", p.configFiles)
	var nets, vols, svcs []string
	for _, n := range p.networks {
		nets = append(nets, n.Name)
	}
	for _, v := range p.volumes {
		vols = append(vols, v.Name)
	}
	for _, s := range p.services {
		svcs = append(svcs, s.name)
	}
	f.add("Networks", strings.Join(nets, "\n"))
	f.add("Volumes", strings.Join(vols, "\n"))
	y := toYAML(struct {
		Project     string          `json:"project"`
		WorkingDir  string          `json:"workingDir,omitempty"`
		ConfigFiles string          `json:"configFiles,omitempty"`
		Services    []string        `json:"services"`
		Containers  []memberSummary `json:"containers"`
		Networks    []string        `json:"networks"`
		Volumes     []string        `json:"volumes"`
	}{name, p.workingDir, p.configFiles, nonNil(svcs), summaries(p.containers), nonNil(nets), nonNil(vols)})
	r := &core.Resource{Ref: projectRef(name), Health: h, Facts: f.list, YAML: y}
	for _, s := range p.services {
		r.Relations = append(r.Relations, core.Relation{Type: "owns", Ref: serviceRef(s.project, s.name)})
	}
	for _, c := range p.containers {
		if isOneoff(c) {
			r.Relations = append(r.Relations, core.Relation{Type: "owns", Ref: containerRef(c)})
		}
	}
	for _, n := range p.networks {
		r.Relations = append(r.Relations, core.Relation{Type: "owns", Ref: networkRef(n)})
	}
	for _, v := range p.volumes {
		r.Relations = append(r.Relations, core.Relation{Type: "owns", Ref: volumeRef(v)})
	}
	return r
}

// Networks, volumes, images.

func (x *index) networkResource(n *engine.Network) *core.Resource {
	f := facts{}
	f.add("Id", n.ID)
	f.add("Name", n.Name)
	f.add("Driver", n.Driver)
	f.add("Scope", n.Scope)
	f.add("Internal", yesNo(n.Internal))
	f.add("Attachable", yesNo(n.Attachable))
	f.add("Project", n.Labels[LabelProject])
	f.add("Containers", strconv.Itoa(len(x.netUsers[n.ID])))
	f.addTime("created", n.Created.Time)
	f.labels(n.Labels)
	r := &core.Resource{Ref: networkRef(n), Health: core.Health{State: core.HealthOK}, Facts: f.list, YAML: inspectYAML(n.Raw, n)}
	users := map[string]bool{}
	for _, c := range x.netUsers[n.ID] {
		users[c.ID] = true
	}
	r.Relations = x.usedByRelations(func(c *engine.ContainerInspect) bool {
		if users[c.ID] {
			return true
		}
		if _, listed := n.Containers[c.ID]; listed {
			return true
		}
		for _, l := range x.networkLinks(c) { // fallback to the container's observed links
			if l.id == n.ID {
				return true
			}
		}
		return false
	})
	// Endpoints of containers the World does not have (not observed).
	for id, ep := range n.Containers {
		if x.w.Containers[id] == nil {
			r.Relations = append(r.Relations, core.Relation{Type: relUsedBy, Inert: true,
				Ref: core.Ref{Provider: ProviderID, Kind: KindContainers, Name: id, UID: id, Title: ep.Name}})
		}
	}
	return r
}

func (x *index) volumeResource(v *engine.Volume) *core.Resource {
	f := facts{}
	f.add("Name", v.Name)
	f.add("Driver", v.Driver)
	f.add("Scope", v.Scope)
	f.add("Mountpoint", v.Mountpoint)
	f.add("Project", v.Labels[LabelProject])
	f.add("Created at", v.CreatedAt)
	f.add("Used by", strconv.Itoa(len(x.volumeUsers[v.Name])))
	f.add("Identity", msg("volume.identity").Text)
	f.labels(v.Labels)
	r := &core.Resource{Ref: volumeRef(v), Health: core.Health{State: core.HealthOK}, Facts: f.list, YAML: inspectYAML(v.Raw, v)}
	r.Relations = x.usedByRelations(func(c *engine.ContainerInspect) bool {
		for _, m := range c.Mounts {
			if m.Type == "volume" && m.Name == v.Name {
				return true
			}
		}
		return false
	})
	return r
}

func (x *index) imageResource(im *engine.ImageInspect) *core.Resource {
	f := facts{}
	f.add("Id", im.ID)
	f.add("Tags", strings.Join(realTags(im.RepoTags), "\n"))
	f.add("Digests", strings.Join(im.RepoDigests, "\n"))
	f.add("Size", bytesText(im.Size))
	if im.Os != "" {
		f.add("Platform", strings.TrimSuffix(im.Os+"/"+im.Architecture, "/"))
	}
	f.add("Used by", strconv.Itoa(len(x.imageUsers[im.ID])))
	f.addTime("created", im.Created.Time)
	f.labels(im.Config.Labels)
	r := &core.Resource{Ref: imageRef(im), Health: core.Health{State: core.HealthOK}, Facts: f.list, YAML: inspectYAML(im.Raw, im)}
	r.Relations = x.usedByRelations(func(c *engine.ContainerInspect) bool { return c.Image == im.ID })
	return r
}

// Facts.

type facts struct{ list []core.Detail }

// add keeps a non-empty value.
func (f *facts) add(key, value string) {
	if value != "" {
		f.list = append(f.list, core.Detail{Key: key, Value: value})
	}
}

func (f *facts) addTime(key string, t time.Time) {
	if !t.IsZero() {
		f.add(key, t.UTC().Format(time.RFC3339))
	}
}

func (f *facts) labels(l map[string]string) {
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+l[k])
	}
	f.add("labels", strings.Join(parts, "\n"))
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// bytesText: "1.0 MiB (1048576 bytes)".
func bytesText(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d bytes", n)
	}
	v, exp := float64(n)/unit, 0
	for v >= unit && exp < 4 {
		v /= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB (%d bytes)", v, "KMGTP"[exp], n)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// inspectYAML is the inspect answer as sent (Raw), else the decoded
// object, as YAML.
func inspectYAML(raw []byte, decoded any) string {
	if len(raw) == 0 {
		b, err := json.Marshal(decoded)
		if err != nil {
			return ""
		}
		raw = b
	}
	y, err := yaml.JSONToYAML(raw)
	if err != nil {
		return string(raw) // JSON is YAML too
	}
	return string(y)
}

func toYAML(v any) string {
	y, err := yaml.Marshal(v)
	if err != nil {
		return ""
	}
	return string(y)
}

// sortRelations: by type (owner, owns, uses, used-by), kind in navigation
// order, shown name, key; each (type, kind) list is capped by the caller.
func sortRelations(rs []core.Relation) {
	typeOrder := map[string]int{"owner": 0, "owns": 1, relUses: 2, relUsedBy: 3}
	kindOrder := map[string]int{}
	for i, k := range kindDescriptors() {
		kindOrder[k.ID] = i
	}
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if typeOrder[a.Type] != typeOrder[b.Type] {
			return typeOrder[a.Type] < typeOrder[b.Type]
		}
		if kindOrder[a.Ref.Kind] != kindOrder[b.Ref.Kind] {
			return kindOrder[a.Ref.Kind] < kindOrder[b.Ref.Kind]
		}
		if shown(a.Ref) != shown(b.Ref) {
			return shown(a.Ref) < shown(b.Ref)
		}
		return a.Ref.Name < b.Ref.Name
	})
}

// capRelations keeps at most maxRelated relations of each (type, kind)
// in order; truncated says some were dropped.
func capRelations(rs []core.Relation) (out []core.Relation, truncated bool) {
	n := map[string]int{}
	out = rs[:0]
	for _, r := range rs {
		k := r.Type + "\x00" + r.Ref.Kind
		if n[k] == maxRelated {
			truncated = true
			continue
		}
		n[k]++
		out = append(out, r)
	}
	return out, truncated
}
