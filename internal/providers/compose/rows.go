package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

// Projections are pure functions over a World (world.go): no network, no
// locks, no mutation of the World. Identity (decision 4):
//   - container: Row.ID = Ref.Name = Ref.UID = full id, Ref.Title = name;
//   - service: "<project>/<service>" (logical, while ≥ 1 non one-off
//     container), Ref.Title = the service name;
//   - project (a scope): its name; exists while a container, network or
//     volume has its label;
//   - network: its Engine id, Ref.Title = its name;
//   - volume: Ref.Name = its name (the Engine key), Ref.UID = Row.ID =
//     "<name>@<CreatedAt as sent>" (best effort: a volume re-created within
//     the same second is not told apart);
//   - image: its id, Ref.Title = its first tag or short id.
//
// Refs carry Provider; Target is the caller's (withTarget).

// kindFeeds are the feeds the rows and the details of a kind read (the
// dependency table of decision 2). Container details also read networks
// when they are there (connections seen from the network's side);
// volumes and images only name their relations without them.
func kindFeeds(kind string) []Feed {
	switch kind {
	case KindProjects:
		return []Feed{FeedContainers, FeedNetworks, FeedVolumes}
	case KindServices, KindContainers:
		return []Feed{FeedContainers}
	case KindNetworks:
		return []Feed{FeedNetworks, FeedContainers}
	case KindVolumes:
		return []Feed{FeedVolumes, FeedContainers}
	case KindImages:
		return []Feed{FeedImages, FeedContainers}
	}
	return nil
}

// projectRows is every row of the view q over w, sorted by row id, and
// the earliest future moment some row's health must be recomputed
// without a new observation (zero: none). Scope one: the project's own
// objects and the networks/volumes its containers use; all/none: every
// Engine object, including standalone/unused ones; images are unscoped.
// q.Name narrows to the object with that key (Ref.Name).
func projectRows(w *World, q provider.Query, now time.Time) (rows []core.Row, next time.Time) {
	x := newIndex(w)
	scope := ""
	if q.Scope.Mode == core.ScopeOne {
		scope = q.Scope.Name
	}
	add := func(r core.Row, n time.Time) {
		if q.Name != "" && r.Ref.Name != q.Name {
			return
		}
		rows = append(rows, r)
		next = earliest(next, n)
	}
	switch q.Kind {
	case KindContainers:
		for _, c := range x.containers {
			if scope == "" || projectOf(c) == scope {
				add(x.containerRow(c, now))
			}
		}
	case KindServices:
		for _, s := range x.services {
			if scope == "" || s.project == scope {
				add(x.serviceRow(s, now))
			}
		}
	case KindProjects:
		for _, p := range x.projectNames() {
			add(x.projectRow(p, now))
		}
	case KindNetworks:
		for _, n := range w.Networks {
			if x.networkIn(n, scope) {
				add(x.networkRow(n), time.Time{})
			}
		}
	case KindVolumes:
		for _, v := range w.Volumes {
			if x.volumeIn(v, scope) {
				add(x.volumeRow(v), time.Time{})
			}
		}
	case KindImages:
		for _, im := range w.Images {
			add(x.imageRow(im), time.Time{})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, next
}

// scopesOf is every project, sorted; never nil.
func scopesOf(w *World) []core.Scope {
	names := newIndex(w).projectNames()
	out := make([]core.Scope, 0, len(names))
	for _, n := range names {
		out = append(out, core.Scope{Name: n})
	}
	return out
}

// withTarget stamps the target id into the rows' refs (in place, before
// they are handed over) and returns them.
func withTarget(target string, rows []core.Row) []core.Row {
	for i := range rows {
		rows[i].Ref.Target = target
	}
	return rows
}

// index is what one projection derives from a World once: Docker
// containers, services, and who uses which network, volume, image.
type index struct {
	w          *World
	containers []*engine.ContainerInspect // all containers, by id
	services   []*service                 // by key
	byService  map[string]*service
	// links of each container to networks, by container id.
	links       map[string][]netLink
	netUsers    map[string][]*engine.ContainerInspect // network id → containers
	volumeUsers map[string][]*engine.ContainerInspect // volume name → containers
	imageUsers  map[string][]*engine.ContainerInspect // image id → containers
}

type service struct {
	key, project, name string
	members            []*engine.ContainerInspect // not one-off, by id
}

// netLink is a container's connection to a network: from its own
// NetworkSettings or from the network's inspect (a connect the container's
// inspect has not seen yet). ID is empty when the network is not known.
type netLink struct {
	id, name, ip string
}

func newIndex(w *World) *index {
	x := &index{w: w, byService: map[string]*service{}, links: map[string][]netLink{},
		netUsers: map[string][]*engine.ContainerInspect{}, volumeUsers: map[string][]*engine.ContainerInspect{},
		imageUsers: map[string][]*engine.ContainerInspect{}}
	for _, c := range w.Containers {
		x.containers = append(x.containers, c)
	}
	sort.Slice(x.containers, func(i, j int) bool { return x.containers[i].ID < x.containers[j].ID })
	for _, c := range x.containers {
		p, s := projectOf(c), c.Config.Labels[LabelService]
		if p != "" && s != "" && !isOneoff(c) {
			key := serviceKey(p, s)
			sv := x.byService[key]
			if sv == nil {
				sv = &service{key: key, project: p, name: s}
				x.byService[key] = sv
				x.services = append(x.services, sv)
			}
			sv.members = append(sv.members, c)
		}
		ls := x.networkLinks(c)
		x.links[c.ID] = ls
		for _, l := range ls {
			if l.id != "" {
				x.netUsers[l.id] = append(x.netUsers[l.id], c)
			}
		}
		seen := map[string]bool{}
		for _, m := range c.Mounts {
			if m.Type == "volume" && m.Name != "" && !seen[m.Name] {
				seen[m.Name] = true
				x.volumeUsers[m.Name] = append(x.volumeUsers[m.Name], c)
			}
		}
		if c.Image != "" {
			x.imageUsers[c.Image] = append(x.imageUsers[c.Image], c)
		}
	}
	sort.Slice(x.services, func(i, j int) bool { return x.services[i].key < x.services[j].key })
	return x
}

// networkLinks: the container's networks by its inspect (by id, else by a
// unique name), plus networks whose inspect lists it; sorted by name.
func (x *index) networkLinks(c *engine.ContainerInspect) []netLink {
	var out []netLink
	seen := map[string]bool{}
	for name, ep := range c.NetworkSettings.Networks {
		id := ep.NetworkID
		if id == "" {
			id = x.networkByName(name)
		}
		if id != "" {
			if seen[id] {
				continue
			}
			seen[id] = true
		}
		out = append(out, netLink{id: id, name: name, ip: ep.IPAddress})
	}
	for _, n := range x.w.Networks {
		ep, ok := n.Containers[c.ID]
		if !ok || seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		out = append(out, netLink{id: n.ID, name: n.Name, ip: strings.SplitN(ep.IPv4Address, "/", 2)[0]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].name != out[j].name {
			return out[i].name < out[j].name
		}
		return out[i].id < out[j].id
	})
	return out
}

// networkByName is the id of the only network with this name ("" when
// none or several).
func (x *index) networkByName(name string) string {
	id := ""
	for _, n := range x.w.Networks {
		if n.Name == name {
			if id != "" {
				return ""
			}
			id = n.ID
		}
	}
	return id
}

// projectNames: every project a container, network or volume is labelled
// with, sorted.
func (x *index) projectNames() []string {
	set := map[string]bool{}
	for _, c := range x.containers {
		if project := projectOf(c); project != "" {
			set[project] = true
		}
	}
	for _, n := range x.w.Networks {
		if p := n.Labels[LabelProject]; p != "" {
			set[p] = true
		}
	}
	for _, v := range x.w.Volumes {
		if p := v.Labels[LabelProject]; p != "" {
			set[p] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// usedBy: some of the containers belong to the project ("": any).
func usedBy(cs []*engine.ContainerInspect, project string) bool {
	if project == "" {
		return len(cs) > 0
	}
	for _, c := range cs {
		if projectOf(c) == project {
			return true
		}
	}
	return false
}

// networkIn: labelled with the project or used by its containers
// (project "": all Engine networks).
func (x *index) networkIn(n *engine.Network, project string) bool {
	p := n.Labels[LabelProject]
	return project == "" || p == project || usedBy(x.netUsers[n.ID], project)
}

func (x *index) volumeIn(v *engine.Volume, project string) bool {
	p := v.Labels[LabelProject]
	return project == "" || p == project || usedBy(x.volumeUsers[v.Name], project)
}

func projectOf(c *engine.ContainerInspect) string { return c.Config.Labels[LabelProject] }

func isOneoff(c *engine.ContainerInspect) bool { return c.Config.Labels[LabelOneoff] == "True" }

func serviceKey(project, service string) string { return project + "/" + service }

func volumeUID(v *engine.Volume) string { return v.Name + "@" + v.CreatedAt }

// imageTitle is the first tag, else the short id.
func imageTitle(im *engine.ImageInspect) string {
	if tags := realTags(im.RepoTags); len(tags) > 0 {
		return tags[0]
	}
	return shortID(im.ID)
}

// realTags drops the "<none>:<none>" placeholder.
func realTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t != "" && t != "<none>:<none>" {
			out = append(out, t)
		}
	}
	return out
}

// Refs.

func containerRef(c *engine.ContainerInspect) core.Ref {
	return core.Ref{Provider: ProviderID, Scope: projectOf(c), Kind: KindContainers, Name: c.ID, UID: c.ID, Title: containerName(c)}
}

func serviceRef(project, name string) core.Ref {
	key := serviceKey(project, name)
	return core.Ref{Provider: ProviderID, Scope: project, Kind: KindServices, Name: key, UID: key, Title: name}
}

func projectRef(name string) core.Ref {
	return core.Ref{Provider: ProviderID, Kind: KindProjects, Name: name, UID: name}
}

func networkRef(n *engine.Network) core.Ref {
	return core.Ref{Provider: ProviderID, Scope: n.Labels[LabelProject], Kind: KindNetworks, Name: n.ID, UID: n.ID, Title: n.Name}
}

func volumeRef(v *engine.Volume) core.Ref {
	return core.Ref{Provider: ProviderID, Scope: v.Labels[LabelProject], Kind: KindVolumes, Name: v.Name, UID: volumeUID(v)}
}

func imageRef(im *engine.ImageInspect) core.Ref {
	return core.Ref{Provider: ProviderID, Kind: KindImages, Name: im.ID, UID: im.ID, Title: imageTitle(im)}
}

// Rows.

func (x *index) containerRow(c *engine.ContainerInspect, now time.Time) (core.Row, time.Time) {
	h, next := containerHealth(c, now)
	cells := []core.Cell{
		core.TextCell(containerName(c)),
		core.TextCell(projectOf(c)),
		core.TextCell(c.Config.Labels[LabelService]),
		numText(c.Config.Labels[LabelNumber]),
		core.TextCell(statusText(c)),
		textOrNull(healthcheckStatus(c)),
		count(c.RestartCount),
		core.TextCell(c.Config.Image),
		textOrNull(strings.Join(publishedPorts(c), ", ")),
		timeCell(c.Created.Time),
		{}, {}, // metrics come from GetMetrics
	}
	return core.Row{ID: c.ID, Rev: x.containerRev(c), Ref: containerRef(c), Cells: cells, Health: h}, next
}

func (x *index) serviceRow(s *service, now time.Time) (core.Row, time.Time) {
	h, next := membersHealth(s.members, now)
	running := runningCount(s.members)
	cells := []core.Cell{
		core.TextCell(s.name),
		core.TextCell(s.project),
		ratio(running, len(s.members)),
		core.TextCell(groupStatus(h, running)),
		core.TextCell(strings.Join(memberImages(s.members), ", ")),
		{}, {}, // metrics come from GetMetrics
	}
	return core.Row{ID: s.key, Rev: x.membersRev(s.members), Ref: serviceRef(s.project, s.name), Cells: cells, Health: h}, next
}

// groupStatus: the health's reason, else Running (some member runs) or
// Completed (none: all exited 0, or some being removed).
func groupStatus(h core.Health, running int) string {
	switch {
	case h.Reason != "":
		return h.Reason
	case running > 0:
		return "Running"
	}
	return "Completed"
}

// project is what a project row and details read.
type project struct {
	name                    string
	containers              []*engine.ContainerInspect // one-offs too, by id
	members                 []*engine.ContainerInspect // not one-off, by id
	services                []*service
	networks                []*engine.Network // labelled with it, by id
	volumes                 []*engine.Volume  // labelled with it, by name
	workingDir, configFiles string
}

func (x *index) project(name string) *project {
	p := &project{name: name}
	for _, c := range x.containers {
		if projectOf(c) != name {
			continue
		}
		p.containers = append(p.containers, c)
		if !isOneoff(c) {
			p.members = append(p.members, c)
		}
		if p.workingDir == "" {
			p.workingDir = c.Config.Labels[LabelWorkingDir]
		}
		if p.configFiles == "" {
			p.configFiles = c.Config.Labels[LabelConfigFiles]
		}
	}
	for _, s := range x.services {
		if s.project == name {
			p.services = append(p.services, s)
		}
	}
	for _, n := range x.w.Networks {
		if n.Labels[LabelProject] == name {
			p.networks = append(p.networks, n)
		}
	}
	sort.Slice(p.networks, func(i, j int) bool { return p.networks[i].ID < p.networks[j].ID })
	for _, v := range x.w.Volumes {
		if v.Labels[LabelProject] == name {
			p.volumes = append(p.volumes, v)
		}
	}
	sort.Slice(p.volumes, func(i, j int) bool { return p.volumes[i].Name < p.volumes[j].Name })
	return p
}

// projectRow: its health is its services' (one-offs are not members).
func (x *index) projectRow(name string, now time.Time) (core.Row, time.Time) {
	p := x.project(name)
	h, next := membersHealth(p.members, now)
	cells := []core.Cell{
		core.TextCell(name),
		count(len(p.services)),
		count(len(p.containers)),
		count(runningCount(p.containers)),
		textOrNull(p.workingDir),
		textOrNull(p.configFiles),
	}
	return core.Row{ID: name, Rev: x.projectRev(p), Ref: projectRef(name), Cells: cells, Health: h}, next
}

func (x *index) networkRow(n *engine.Network) core.Row {
	users := x.netUsers[n.ID]
	cells := []core.Cell{
		core.TextCell(n.Name),
		textOrNull(n.Labels[LabelProject]),
		core.TextCell(n.Driver),
		core.TextCell(n.Scope),
		count(len(users)),
		timeCell(n.Created.Time),
	}
	return core.Row{ID: n.ID, Rev: x.networkRev(n), Ref: networkRef(n), Cells: cells, Health: core.Health{State: core.HealthOK}}
}

func (x *index) volumeRow(v *engine.Volume) core.Row {
	cells := []core.Cell{
		core.TextCell(v.Name),
		textOrNull(v.Labels[LabelProject]),
		core.TextCell(v.Driver),
		count(len(x.volumeUsers[v.Name])),
		timeCell(parseTime(v.CreatedAt)),
	}
	return core.Row{ID: volumeUID(v), Rev: x.volumeRev(v), Ref: volumeRef(v), Cells: cells, Health: core.Health{State: core.HealthOK}}
}

func (x *index) imageRow(im *engine.ImageInspect) core.Row {
	tags := realTags(im.RepoTags)
	tagText := ""
	if len(tags) > 0 {
		tagText = tags[0]
		if len(tags) > 1 {
			tagText += " +" + strconv.Itoa(len(tags)-1)
		}
	}
	cells := []core.Cell{
		textOrNull(tagText),
		core.TextCell(shortID(im.ID)),
		core.NumCell(float64(im.Size), ""),
		count(len(x.imageUsers[im.ID])),
		timeCell(im.Created.Time),
	}
	return core.Row{ID: im.ID, Rev: x.imageRev(im), Ref: imageRef(im), Cells: cells, Health: core.Health{State: core.HealthOK}}
}

// Revisions: a hash of what the details show — the inspect answer (Raw,
// else the decoded object) plus derived relations.

// containerRev: the inspect without the health check log and failing
// streak (both change with every probe; the details show them "as of
// reading"), plus the networks it is connected to as seen from either
// side.
func (x *index) containerRev(c *engine.ContainerInspect) string {
	var nets []string
	for _, l := range x.links[c.ID] {
		nets = append(nets, "net:"+l.id+"/"+l.name+"/"+l.ip)
	}
	return revOf(canonical(c.Raw, c, dropProbeNoise), nets...)
}

// membersRev: the containers' ids and revisions (in the given order).
func (x *index) membersRev(cs []*engine.ContainerInspect) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, c.ID+"="+x.containerRev(c))
	}
	return revOf(nil, parts...)
}

func (x *index) projectRev(p *project) string {
	parts := []string{x.membersRev(p.containers)}
	for _, n := range p.networks {
		parts = append(parts, "net:"+n.ID+"/"+n.Name)
	}
	for _, v := range p.volumes {
		parts = append(parts, "vol:"+volumeUID(v))
	}
	return revOf(nil, parts...)
}

func (x *index) networkRev(n *engine.Network) string {
	return revOf(canonical(n.Raw, n, nil), containerIDs(x.netUsers[n.ID])...)
}

func (x *index) volumeRev(v *engine.Volume) string {
	return revOf(canonical(v.Raw, v, nil), containerIDs(x.volumeUsers[v.Name])...)
}

func (x *index) imageRev(im *engine.ImageInspect) string {
	return revOf(canonical(im.Raw, im, nil), containerIDs(x.imageUsers[im.ID])...)
}

func dropProbeNoise(m map[string]any) {
	st, _ := m["State"].(map[string]any)
	if h, ok := st["Health"].(map[string]any); ok {
		delete(h, "Log")
		delete(h, "FailingStreak")
	}
}

// canonical is the inspect answer (Raw; without it, the decoded object)
// re-encoded with sorted keys after drop.
func canonical(raw []byte, decoded any, drop func(map[string]any)) []byte {
	if len(raw) == 0 {
		b, err := json.Marshal(decoded)
		if err != nil {
			return nil
		}
		raw = b
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw // not an object: hashed as sent
	}
	if drop != nil {
		drop(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return b
}

// revOf hashes a body and derived parts (short hex).
func revOf(body []byte, parts ...string) string {
	h := sha256.New()
	h.Write(body)
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// Cells and texts.

// statusText: the Engine state, with the exit code once exited.
func statusText(c *engine.ContainerInspect) string {
	if c.State.Status == "exited" || c.State.Status == "dead" {
		return fmt.Sprintf("%s (%d)", c.State.Status, c.State.ExitCode)
	}
	return c.State.Status
}

func healthcheckStatus(c *engine.ContainerInspect) string {
	if c.State.Health == nil {
		return ""
	}
	return c.State.Health.Status
}

// publishedPorts: "host:port→container/proto", by container port, then text.
func publishedPorts(c *engine.ContainerInspect) []string {
	type port struct {
		n    int
		text string
	}
	var ps []port
	seen := map[string]bool{}
	for key, binds := range c.NetworkSettings.Ports {
		n, _ := strconv.Atoi(strings.SplitN(key, "/", 2)[0])
		for _, b := range binds {
			if b.HostPort == "" {
				continue
			}
			ip := b.HostIP
			if ip == "" {
				ip = "0.0.0.0"
			}
			t := net.JoinHostPort(ip, b.HostPort) + "→" + key
			if !seen[t] {
				seen[t] = true
				ps = append(ps, port{n, t})
			}
		}
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].n != ps[j].n {
			return ps[i].n < ps[j].n
		}
		return ps[i].text < ps[j].text
	})
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.text)
	}
	return out
}

func runningCount(cs []*engine.ContainerInspect) int {
	n := 0
	for _, c := range cs {
		if c.State.Status == "running" {
			n++
		}
	}
	return n
}

// memberImages: the distinct image references the containers run, sorted.
func memberImages(cs []*engine.ContainerInspect) []string {
	set := map[string]bool{}
	for _, c := range cs {
		if c.Config.Image != "" {
			set[c.Config.Image] = true
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func containerIDs(cs []*engine.ContainerInspect) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func count(n int) core.Cell { return core.NumCell(float64(n), strconv.Itoa(n)) }

func ratio(n, total int) core.Cell {
	f := 0.0
	if total > 0 {
		f = float64(n) / float64(total)
	}
	return core.NumCell(f, fmt.Sprintf("%d/%d", n, total))
}

// numText is a number label as a number cell; not a number: its text.
func numText(s string) core.Cell {
	if n, err := strconv.Atoi(s); err == nil {
		return core.NumCell(float64(n), s)
	}
	return textOrNull(s)
}

// textOrNull: an empty text is "no value".
func textOrNull(s string) core.Cell {
	if s == "" {
		return core.Cell{}
	}
	return core.TextCell(s)
}

// timeCell: an unknown time is "no value".
func timeCell(t time.Time) core.Cell {
	if t.IsZero() {
		return core.Cell{}
	}
	return core.TimeCell(t.UnixMilli())
}

// parseTime reads an Engine timestamp kept as text (a volume's CreatedAt).
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() <= 1 {
		return time.Time{}
	}
	return t
}
