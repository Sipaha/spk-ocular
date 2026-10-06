package compose

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

func q(kind string, scope string) provider.Query {
	if scope == "" {
		return provider.Query{Kind: kind, Scope: core.ScopeSel{Mode: core.ScopeAll}}
	}
	return provider.Query{Kind: kind, Scope: core.ScopeSel{Mode: core.ScopeOne, Name: scope}}
}

func rowsOf(w *World, qu provider.Query) []core.Row {
	rows, _ := projectRows(w, qu, t0)
	return rows
}

func ids(rows []core.Row) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func cell(t *testing.T, kind string, r core.Row, col string) core.Cell {
	t.Helper()
	for i, c := range kindByID(t, kind).Columns {
		if c.ID == col {
			require.Less(t, i, len(r.Cells))
			return r.Cells[i]
		}
	}
	require.Failf(t, "no column", "%s.%s", kind, col)
	return core.Cell{}
}

func onlyRow(t *testing.T, w *World, qu provider.Query) core.Row {
	t.Helper()
	rows := rowsOf(w, qu)
	require.Len(t, rows, 1)
	return rows[0]
}

// A container's key is its full id; its name is only its title: a rename
// changes the title, not the row; a reused name is another row.
func TestContainerIdentity(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	r := onlyRow(t, world(ctr(id, "p-web-1", "p", "web")), q(KindContainers, ""))
	assert.Equal(t, id, r.ID)
	assert.Equal(t, core.Ref{Provider: ProviderID, Scope: "p", Kind: KindContainers, Name: id, UID: id, Title: "p-web-1"}, r.Ref)
	assert.Equal(t, "p-web-1", cell(t, KindContainers, r, "name").Text)

	renamed := onlyRow(t, world(ctr(id, "renamed", "p", "web")), q(KindContainers, ""))
	assert.Equal(t, r.ID, renamed.ID)
	assert.Equal(t, r.Ref.Name, renamed.Ref.Name)
	assert.Equal(t, "renamed", renamed.Ref.Title)

	reused := onlyRow(t, world(ctr("fedcba", "p-web-1", "p", "web")), q(KindContainers, ""))
	assert.NotEqual(t, r.ID, reused.ID)
	assert.Equal(t, "fedcba", reused.Ref.Name)
}

// All resources includes standalone containers; named project scopes do not.
func TestStandaloneContainersKeepProjectIsolation(t *testing.T) {
	w := world(ctr("a", "plain", "", ""), ctr("b", "p-web-1", "p", "web"))
	assert.Equal(t, []string{"a", "b"}, ids(rowsOf(w, q(KindContainers, ""))))
	assert.Equal(t, []string{"b"}, ids(rowsOf(w, q(KindContainers, "p"))))
	assert.Empty(t, rowsOf(w, q(KindContainers, "missing")))
	assert.Empty(t, rowsOf(w, q(KindContainers, ""))[0].Ref.Scope)
	assert.Equal(t, []string{"p/web"}, ids(rowsOf(w, q(KindServices, ""))))
	assert.Equal(t, []core.Scope{{Name: "p"}}, scopesOf(w))
}

func TestContainerCells(t *testing.T) {
	c := ctr("a", "p-web-2", "p", "web", number("2"), exited(3, t0), image("sha256:i1", "nginx:1.27"), func(c *engine.ContainerInspect) {
		c.RestartCount = 4
		c.NetworkSettings.Ports = map[string][]engine.PortBinding{
			"443/tcp":  {{HostIP: "127.0.0.1", HostPort: "8443"}},
			"80/tcp":   {{HostIP: "0.0.0.0", HostPort: "8080"}, {HostIP: "::", HostPort: "8080"}},
			"9000/tcp": nil, // exposed, not published
		}
	}, health("unhealthy"))
	r := onlyRow(t, world(c), q(KindContainers, "p"))
	assert.Equal(t, "p", cell(t, KindContainers, r, "project").Text)
	assert.Equal(t, "web", cell(t, KindContainers, r, "service").Text)
	assert.Equal(t, "2", cell(t, KindContainers, r, "number").Text)
	assert.Equal(t, 2.0, *cell(t, KindContainers, r, "number").Num)
	assert.Equal(t, "exited (3)", cell(t, KindContainers, r, "status").Text)
	assert.Equal(t, "unhealthy", cell(t, KindContainers, r, "health").Text)
	assert.Equal(t, 4.0, *cell(t, KindContainers, r, "restarts").Num)
	assert.Equal(t, "nginx:1.27", cell(t, KindContainers, r, "image").Text)
	assert.Equal(t, "0.0.0.0:8080→80/tcp, [::]:8080→80/tcp, 127.0.0.1:8443→443/tcp", cell(t, KindContainers, r, "ports").Text)
	assert.Equal(t, t0.Add(-time.Hour).UnixMilli(), cell(t, KindContainers, r, "age").Time)
	assert.Equal(t, core.HealthError, r.Health.State)
	assert.Equal(t, "Exited", r.Health.Reason)
}

// A service is logical: "<project>/<service>", while at least one of its
// (non one-off) containers exists; recreated whole, it is the same row.
func TestServiceIsLogicalAndOneOffsAreNotMembers(t *testing.T) {
	w := world(
		ctr("a", "p-web-1", "p", "web"),
		ctr("b", "p-web-2", "p", "web", number("2"), exited(0, t0)),
		ctr("c", "p-web-run-1", "p", "web", oneoff(), exited(1, t0)),
	)
	r := onlyRow(t, w, q(KindServices, "p"))
	assert.Equal(t, "p/web", r.ID)
	assert.Equal(t, core.Ref{Provider: ProviderID, Scope: "p", Kind: KindServices, Name: "p/web", UID: "p/web", Title: "web"}, r.Ref)
	assert.Equal(t, "web", cell(t, KindServices, r, "name").Text)
	assert.Equal(t, "1/2", cell(t, KindServices, r, "running").Text, "the one-off is not counted")
	assert.Equal(t, 0.5, *cell(t, KindServices, r, "running").Num)
	assert.Equal(t, core.HealthOK, r.Health.State, "the failed one-off does not make the service red")
	assert.Equal(t, "Running", cell(t, KindServices, r, "status").Text)
	assert.Equal(t, "web:latest", cell(t, KindServices, r, "images").Text)
	assert.ElementsMatch(t, []string{"a", "b", "c"}, ids(rowsOf(w, q(KindContainers, "p"))), "the one-off is a container of the project")

	// Only one-offs left: no service.
	assert.Empty(t, rowsOf(world(ctr("c", "p-web-run-1", "p", "web", oneoff())), q(KindServices, "")))

	// Removed whole, then recreated with other containers: the same row key.
	again := onlyRow(t, world(ctr("x", "p-web-1", "p", "web"), ctr("y", "p-web-2", "p", "web", number("2"))), q(KindServices, "p"))
	assert.Equal(t, r.ID, again.ID)
	assert.NotEqual(t, r.Rev, again.Rev, "other members")
}

func TestServiceStatusAndImages(t *testing.T) {
	w := world(
		ctr("a", "p-job-1", "p", "job", exited(0, t0), image("sha256:1", "job:2")),
		ctr("b", "p-job-2", "p", "job", exited(0, t0), image("sha256:0", "job:1")),
	)
	r := onlyRow(t, w, q(KindServices, ""))
	assert.Equal(t, "Completed", cell(t, KindServices, r, "status").Text)
	assert.Equal(t, "0/2", cell(t, KindServices, r, "running").Text)
	assert.Equal(t, "job:1, job:2", cell(t, KindServices, r, "images").Text)
}

// A project exists while a container, network or volume has its label.
func TestProjectsAreScopesFromEveryFeed(t *testing.T) {
	w := world(
		ctr("a", "b-web-1", "b", "web", label(LabelWorkingDir, "/src/b"), label(LabelConfigFiles, "/src/b/compose.yaml")),
		ctr("o", "b-web-run-1", "b", "web", oneoff(), exited(0, t0)),
		network("n1", "c_default", "c"),
		vol("a_data", "a", "2026-09-30T10:00:00Z"),
		ctr("x", "plain", "", ""),
	)
	assert.Equal(t, []core.Scope{{Name: "a"}, {Name: "b"}, {Name: "c"}}, scopesOf(w))
	rows := rowsOf(w, provider.Query{Kind: KindProjects, Scope: core.ScopeSel{Mode: core.ScopeNone}})
	assert.Equal(t, []string{"a", "b", "c"}, ids(rows))
	b := rows[1]
	assert.Equal(t, core.Ref{Provider: ProviderID, Kind: KindProjects, Name: "b", UID: "b"}, b.Ref)
	assert.Equal(t, "1", cell(t, KindProjects, b, "services").Text)
	assert.Equal(t, "2", cell(t, KindProjects, b, "containers").Text)
	assert.Equal(t, "1", cell(t, KindProjects, b, "running").Text)
	assert.Equal(t, "/src/b", cell(t, KindProjects, b, "workingDir").Text)
	assert.Equal(t, "/src/b/compose.yaml", cell(t, KindProjects, b, "configFiles").Text)
	assert.Equal(t, "0", cell(t, KindProjects, rows[0], "containers").Text, "only a volume")

	// The volume goes: so does its project.
	delete(w.Volumes, "a_data")
	assert.Equal(t, []core.Scope{{Name: "b"}, {Name: "c"}}, scopesOf(w))
	assert.NotNil(t, scopesOf(world()), "never nil")
}

// Scope one: a project's own objects and those its containers use; an
// external network used by two projects is one object shown in both.
func TestScopeOneAndAll(t *testing.T) {
	w := world(
		ctr("a1", "a-web-1", "a", "web", onNet("ext", "n-ext", "10.0.0.2"), onNet("a_default", "n-a", "172.18.0.2"), mountVol("ext-vol", "/data"), image("sha256:shared", "busybox")),
		ctr("b1", "b-web-1", "b", "web", onNet("ext", "n-ext", "10.0.0.3"), mountVol("ext-vol", "/data"), image("sha256:shared", "busybox")),
		ctr("x", "plain", "", "", onNet("other", "n-other", "10.1.0.2"), image("sha256:plain", "alpine")),
		network("n-ext", "ext", ""),
		network("n-a", "a_default", "a"),
		network("n-b", "b_default", "b"), // labelled, unused
		network("n-other", "other", ""),  // used only by a non-Compose container
		vol("ext-vol", "", "2026-09-30T10:00:00Z"),
		vol("b_data", "b", "2026-09-30T10:00:00Z"),
		vol("lonely", "", ""),
		img("sha256:shared", "busybox:latest"),
		img("sha256:plain", "alpine:latest"),
	)
	assert.Equal(t, []string{"a1"}, ids(rowsOf(w, q(KindContainers, "a"))))
	assert.Equal(t, []string{"a1", "b1", "x"}, ids(rowsOf(w, q(KindContainers, ""))))
	assert.Equal(t, []string{"a/web"}, ids(rowsOf(w, q(KindServices, "a"))))

	assert.Equal(t, []string{"n-a", "n-ext"}, ids(rowsOf(w, q(KindNetworks, "a"))))
	assert.Equal(t, []string{"n-b", "n-ext"}, ids(rowsOf(w, q(KindNetworks, "b"))))
	assert.Equal(t, []string{"n-a", "n-b", "n-ext", "n-other"}, ids(rowsOf(w, q(KindNetworks, ""))))
	ext := rowsOf(w, q(KindNetworks, "a"))[1]
	assert.Equal(t, core.Ref{Provider: ProviderID, Kind: KindNetworks, Name: "n-ext", UID: "n-ext", Title: "ext"}, ext.Ref, "not the project's: no scope")
	assert.Equal(t, "2", cell(t, KindNetworks, ext, "containers").Text)
	assert.Equal(t, "", cell(t, KindNetworks, ext, "project").Text)
	assert.Equal(t, "a", rowsOf(w, q(KindNetworks, "a"))[0].Ref.Scope)

	assert.Equal(t, []string{"ext-vol@2026-09-30T10:00:00Z"}, ids(rowsOf(w, q(KindVolumes, "a"))))
	assert.Equal(t, []string{"b_data@2026-09-30T10:00:00Z", "ext-vol@2026-09-30T10:00:00Z"}, ids(rowsOf(w, q(KindVolumes, "b"))))
	assert.Equal(t, []string{"b_data@2026-09-30T10:00:00Z", "ext-vol@2026-09-30T10:00:00Z", "lonely@"}, ids(rowsOf(w, q(KindVolumes, ""))))
	assert.Equal(t, "2", cell(t, KindVolumes, rowsOf(w, q(KindVolumes, "a"))[0], "usedBy").Text)

	// Images have no scope and include standalone-container images.
	none := provider.Query{Kind: KindImages, Scope: core.ScopeSel{Mode: core.ScopeNone}}
	images := rowsOf(w, none)
	require.Equal(t, []string{"sha256:plain", "sha256:shared"}, ids(images))
	im := images[1]
	assert.Equal(t, "1", cell(t, KindImages, images[0], "usedBy").Text)
	assert.Equal(t, "2", cell(t, KindImages, im, "usedBy").Text)
}

// Query.Name narrows a view to one object by its key (the details follow
// it), not by its displayed name.
func TestQueryNameNarrowsToTheKey(t *testing.T) {
	w := world(
		ctr("a", "p-web-1", "p", "web", onNet("p_default", "n1", ""), mountVol("p_data", "/d"), image("sha256:i", "web")),
		ctr("b", "p-db-1", "p", "db"),
		network("n1", "p_default", "p"), network("n2", "p_other", "p"),
		vol("p_data", "p", "2026-09-30T10:00:00Z"), vol("p_more", "p", ""),
		img("sha256:i", "web:latest"), img("sha256:j", "db:latest"),
	)
	w.Containers["b"].Image = "sha256:j"
	for _, c := range []struct{ kind, name, want string }{
		{KindContainers, "a", "a"},
		{KindContainers, "p-web-1", ""},
		{KindServices, "p/db", "p/db"},
		{KindServices, "db", ""},
		{KindNetworks, "n2", "n2"},
		{KindNetworks, "p_other", ""},
		{KindVolumes, "p_more", "p_more@"},
		{KindImages, "sha256:j", "sha256:j"},
		{KindProjects, "p", "p"},
	} {
		qu := q(c.kind, "")
		qu.Name = c.name
		if c.want == "" {
			assert.Empty(t, rowsOf(w, qu), "%s %s", c.kind, c.name)
		} else {
			assert.Equal(t, []string{c.want}, ids(rowsOf(w, qu)), "%s %s", c.kind, c.name)
		}
	}
	// Narrowing and scope together.
	qu := q(KindContainers, "other")
	qu.Name = "a"
	assert.Empty(t, rowsOf(w, qu))
}

// Images: the id is the key; a tag moved to another image changes the
// rows' tags, not their ids.
func TestTagMovedToAnotherImage(t *testing.T) {
	a := ctr("a", "p-web-1", "p", "web", image("sha256:old", "web:latest"))
	b := ctr("b", "p-web-2", "p", "web", image("sha256:new", "web:latest"))
	before := world(a, b, img("sha256:old", "web:latest", "web:1"), img("sha256:new"))
	after := world(a, b, img("sha256:old", "web:1"), img("sha256:new", "web:latest"))
	none := provider.Query{Kind: KindImages, Scope: core.ScopeSel{Mode: core.ScopeNone}}
	rb, ra := rowsOf(before, none), rowsOf(after, none)
	assert.Equal(t, ids(rb), ids(ra))
	assert.Equal(t, []string{"sha256:new", "sha256:old"}, ids(ra))
	assert.Equal(t, "", cell(t, KindImages, rb[0], "tags").Text)
	assert.Equal(t, "web:latest +1", cell(t, KindImages, rb[1], "tags").Text)
	assert.Equal(t, "web:latest", cell(t, KindImages, ra[0], "tags").Text)
	assert.Equal(t, "web:latest", ra[0].Ref.Title)
	assert.Equal(t, "new", rb[0].Ref.Title, "no tag: the short id")
	assert.Equal(t, "web:1", cell(t, KindImages, ra[1], "tags").Text)
	assert.NotEqual(t, rb[0].Rev, ra[0].Rev)
	assert.Equal(t, core.Ref{Provider: ProviderID, Kind: KindImages, Name: "sha256:new", UID: "sha256:new", Title: "web:latest"}, ra[0].Ref)
	assert.Equal(t, float64(1<<20), *cell(t, KindImages, ra[0], "size").Num)
	assert.Equal(t, "new", cell(t, KindImages, ra[0], "id").Text)
}

// A volume has no immutable id: UID = "<name>@<CreatedAt as sent>".
func TestVolumeIdentity(t *testing.T) {
	r := onlyRow(t, world(vol("p_data", "p", "2026-09-30T10:00:00+03:00")), q(KindVolumes, "p"))
	assert.Equal(t, "p_data@2026-09-30T10:00:00+03:00", r.ID)
	assert.Equal(t, core.Ref{Provider: ProviderID, Scope: "p", Kind: KindVolumes, Name: "p_data", UID: "p_data@2026-09-30T10:00:00+03:00"}, r.Ref)
	assert.Equal(t, time.Date(2026, 9, 30, 7, 0, 0, 0, time.UTC).UnixMilli(), cell(t, KindVolumes, r, "age").Time)
	noTime := onlyRow(t, world(vol("p_data", "p", "")), q(KindVolumes, "p"))
	assert.Equal(t, "p_data@", noTime.ID)
	assert.Equal(t, core.Cell{}, cell(t, KindVolumes, noTime, "age"))
}

// Rev covers what the details show, not the health check log (it changes
// with every probe); derived relations (a network connect) are in it.
func TestRevIgnoresTheHealthLogButNotAStatusChange(t *testing.T) {
	const inspect = `{"Id":"a","Name":"/p-web-1","State":{"Status":"running","Running":true,"Health":{"Status":"healthy","FailingStreak":0,"Log":[%s]}},"Config":{"Labels":{"com.docker.compose.project":"p","com.docker.compose.service":"web"}}}`
	mk := func(status, log string, opts ...ctrOpt) *World {
		c := ctr("a", "p-web-1", "p", "web", append([]ctrOpt{health(status), raw(fmt.Sprintf(inspect, log))}, opts...)...)
		return world(c, network("n1", "p_default", "p"))
	}
	rev := func(w *World) string { return onlyRow(t, w, q(KindContainers, "p")).Rev }
	base := rev(mk("healthy", `{"ExitCode":0,"Output":"ok"}`))
	assert.NotEmpty(t, base)
	assert.Equal(t, base, rev(mk("healthy", `{"ExitCode":0,"Output":"ok"},{"ExitCode":0,"Output":"ok again"}`)), "a new probe result")

	changed := world(ctr("a", "p-web-1", "p", "web", health("healthy"), raw(`{"Id":"a","Name":"/p-web-1","State":{"Status":"paused","Running":true,"Paused":true,"Health":{"Status":"healthy","Log":[]}}}`)), network("n1", "p_default", "p"))
	assert.NotEqual(t, base, rev(changed), "a status change")

	// Connected to a network (seen from the network's inspect only).
	connected := mk("healthy", `{"ExitCode":0,"Output":"ok"}`)
	connected.Networks["n1"].Containers = map[string]engine.NetworkEndpoint{"a": {Name: "p-web-1"}}
	assert.NotEqual(t, base, rev(connected), "a derived relation")

	// Without Raw: the decoded object, still without the log.
	s1 := rev(world(ctr("a", "n", "p", "web", health("healthy", check(0, "x", t0)))))
	s2 := rev(world(ctr("a", "n", "p", "web", health("healthy", check(0, "x", t0), check(0, "y", t0.Add(time.Second))))))
	assert.Equal(t, s1, s2)
	assert.NotEqual(t, s1, rev(world(ctr("a", "n", "p", "web", health("unhealthy", check(0, "x", t0))))))
}

// next is the earliest deadline of any row of the view.
func TestRowsNextIsTheEarliestDeadline(t *testing.T) {
	w := world(
		ctr("a", "p-web-1", "p", "web", restarted(1, t0.Add(-8*time.Minute))),
		ctr("b", "p-db-1", "p", "db", restarted(3, t0.Add(-1*time.Minute))),
	)
	for _, kind := range []string{KindContainers, KindServices, KindProjects} {
		_, next := projectRows(w, q(kind, ""), t0)
		assert.Equal(t, t0.Add(2*time.Minute), next, kind)
	}
	_, next := projectRows(w, q(KindNetworks, ""), t0)
	assert.True(t, next.IsZero())
}

// The caller stamps the target into refs (rows and details).
func TestWithTarget(t *testing.T) {
	rows := withTarget("context:x", rowsOf(world(ctr("a", "n", "p", "web")), q(KindContainers, "")))
	assert.Equal(t, "context:x", rows[0].Ref.Target)
}

// The feeds each kind reads (decision 2's dependency table): the caller
// leases them; a feed not leased is absent from the World, and a view
// over it has no rows.
func TestKindFeeds(t *testing.T) {
	assert.Equal(t, []Feed{FeedContainers, FeedNetworks, FeedVolumes}, kindFeeds(KindProjects))
	assert.Equal(t, []Feed{FeedContainers}, kindFeeds(KindServices))
	assert.Equal(t, []Feed{FeedContainers}, kindFeeds(KindContainers))
	assert.Equal(t, []Feed{FeedNetworks, FeedContainers}, kindFeeds(KindNetworks))
	assert.Equal(t, []Feed{FeedVolumes, FeedContainers}, kindFeeds(KindVolumes))
	assert.Equal(t, []Feed{FeedImages, FeedContainers}, kindFeeds(KindImages))
	assert.Nil(t, kindFeeds("pods"))

	w := without(world(ctr("a", "n", "p", "web"), img("sha256:img-web")), FeedImages)
	assert.Empty(t, rowsOf(w, q(KindImages, "")))
	assert.Len(t, rowsOf(w, q(KindContainers, "")), 1)
}
