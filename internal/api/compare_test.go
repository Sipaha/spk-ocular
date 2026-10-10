package api

import (
	"context"
	"github.com/spk/spk-ocular/internal/provider"
	"strings"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/providers/synthetic"
	"github.com/stretchr/testify/require"
)

func TestComparisonNormalizationPreservesMeaningAndHidesOnlyExplicitFields(t *testing.T) {
	text := `apiVersion: apps/v1
kind: Deployment
metadata:
  uid: u
  resourceVersion: "9007199254740993"
  generation: 2
  name: app
  namespace: prod
  creationTimestamp: now
  annotations:
    deployment.kubernetes.io/revision: "7"
    description: useful
  labels:
    app: web
spec:
  replicas: 3
  template:
    metadata:
      annotations:
        useful: kept
    spec:
      containers:
      - name: first
        image: app:1
      - name: second
        image: sidecar:2
status:
  readyReplicas: 2
`
	side, err := normalizeComparison(text, true, false)
	require.NoError(t, err)
	require.NotContains(t, side.YAML, "resourceVersion:")
	require.NotContains(t, side.YAML, "status:")
	require.Contains(t, side.FullYAML, `"9007199254740993"`)
	require.Contains(t, side.FullYAML, "readyReplicas: 2")
	for _, term := range []string{"name: app", "namespace: prod", "replicas: 3", "description: useful", "useful: kept", `deployment.kubernetes.io/revision: "7"`} {
		require.Contains(t, side.YAML, term)
	}
	require.Less(t, strings.Index(side.YAML, "name: first"), strings.Index(side.YAML, "name: second"))
	require.ElementsMatch(t, []string{"status", "metadata.uid", "metadata.resourceVersion", "metadata.generation", "metadata.creationTimestamp"}, side.Omitted)
	reordered, err := normalizeComparison("b: 2\na: 9007199254740993\n", false, false)
	require.NoError(t, err)
	require.Equal(t, "a: 9007199254740993\nb: 2\n", reordered.YAML)
	require.Equal(t, reordered.YAML, reordered.FullYAML)
}

func TestComparisonExcludesSecretValuesAndMasksInEveryMode(t *testing.T) {
	for _, text := range []string{"apiVersion: v1\nkind: Secret\ndata:\n  token: TOPSECRET\nstringData:\n  password: SECRET\nbinaryData:\n  hidden: HIDDEN\nmetadata:\n  name: credentials\n", "data:\n  token: MASKED\nname: safe\n"} {
		side, err := normalizeComparison(text, true, true)
		require.NoError(t, err)
		require.True(t, side.ValuesExcluded)
		for _, value := range []string{"TOPSECRET", "SECRET", "HIDDEN", "MASKED", "\ndata:", "\nstringData:", "\nbinaryData:"} {
			require.NotContains(t, side.YAML, value)
			require.NotContains(t, side.FullYAML, value)
		}
	}
}

func TestComparisonRejectsAmbiguousAndUnboundedYAML(t *testing.T) {
	for _, text := range []string{"a: 1\na: 2\n", "a: 1\n---\nb: 2\n", "- item\n", "a: &ref [1,2]\nb: *ref\n", "a: [", "", "a: " + strings.Repeat("x", comparisonBytes), strings.Repeat("# line\n", comparisonLines+1) + "a: 1\n", "a: " + strings.Repeat("[", 70) + "0" + strings.Repeat("]", 70)} {
		_, err := normalizeComparison(text, true, false)
		require.Error(t, err)
	}
}

func TestResourceComparisonIsUIOnlyAndPinsObjectAndConnection(t *testing.T) {
	s, _ := newService(t, synthetic.New())
	ctx := UIContext(context.Background())
	_, err := s.ListTargets(ctx)
	require.NoError(t, err)
	req := CompareRequest{Left: CompareSelection{Ref: core.Ref{Provider: synthetic.ID, Target: synthetic.Target, Kind: synthetic.CrateKind, Scope: "blue", Name: "crate-7f3a", UID: "crate-7f3a"}, ConfigRev: "stale", ConnectionID: 1}}
	req.Right = req.Left
	_, err = s.CompareResources(context.Background(), req)
	require.Error(t, err)
	_, err = s.CompareResources(ctx, req)
	require.Error(t, err)
	_, err = s.ConnectTarget(ctx, synthetic.ID, synthetic.Target)
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	// Use actual returned identity; synthetic Crates intentionally do not enforce
	// the requested UID themselves, so the comparison must reject a replacement.
	resource, err := s.GetResource(ctx, core.Ref{Provider: synthetic.ID, Target: synthetic.Target, Kind: synthetic.CrateKind, Scope: "blue", Name: "crate-7f3a"})
	require.NoError(t, err)
	view, err := s.ListTargets(ctx)
	require.NoError(t, err)
	var target core.Target
	for _, group := range view.Groups {
		for _, candidate := range group.Targets {
			if candidate.Provider == synthetic.ID && candidate.ID == synthetic.Target {
				target = candidate
			}
		}
	}
	req.Left = CompareSelection{Ref: resource.Ref, ConfigRev: target.ConfigRev, ConnectionID: target.Connection.ID}
	req.Right = req.Left
	result, err := s.CompareResources(ctx, req)
	require.NoError(t, err)
	require.Equal(t, result.Left.YAML, result.Right.YAML)
	require.NotZero(t, result.Left.CapturedAt)
	req.Right.Ref.UID = "replaced"
	_, err = s.CompareResources(ctx, req)
	require.Error(t, err)
	req.Right = req.Left
	req.Right.ConfigRev = "stale"
	_, err = s.CompareResources(ctx, req)
	require.Error(t, err)
	req.Right = req.Left
	req.Right.ConnectionID++
	_, err = s.CompareResources(ctx, req)
	require.Error(t, err)
	req.Right = req.Left
	req.Right.Ref.UID = ""
	_, err = s.CompareResources(ctx, req)
	require.Error(t, err)
}

// A controlled read exercises cancellation and configuration replacement while
// the first object is in flight, without touching any live connection.
type comparisonFixtureProvider struct {
	*openable
	read func(context.Context, core.Ref) (*core.Resource, error)
}
type comparisonFixtureSession struct {
	*fakeSession
	read func(context.Context, core.Ref) (*core.Resource, error)
}

func (p *comparisonFixtureProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	sess, err := p.openable.Open(ctx, target)
	if err != nil {
		return nil, err
	}
	return &comparisonFixtureSession{fakeSession: sess.(*fakeSession), read: p.read}, nil
}
func (s *comparisonFixtureSession) Get(ctx context.Context, ref core.Ref) (*core.Resource, error) {
	return s.read(ctx, ref)
}

func TestComparisonRejectsChangedConfigurationAndCancellationDuringRead(t *testing.T) {
	for _, mode := range []string{"configuration", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			started, release := make(chan struct{}, 1), make(chan struct{})
			p := &comparisonFixtureProvider{openable: newOpenable("a")}
			p.read = func(ctx context.Context, ref core.Ref) (*core.Resource, error) {
				select {
				case started <- struct{}{}:
				default:
				}
				select {
				case <-release:
					return &core.Resource{Ref: ref, YAML: "name: p\n"}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			s, _ := newService(t, p)
			ctx, cancel := context.WithCancel(UIContext(context.Background()))
			defer cancel()
			_, err := s.ListTargets(ctx)
			require.NoError(t, err)
			_, err = s.ConnectTarget(ctx, "k", "a")
			require.NoError(t, err)
			connection := awaitConnection(t, s, "connected")
			selection := CompareSelection{Ref: core.Ref{Provider: "k", Target: "a", Kind: "pods", Scope: "default", Name: "p", UID: "one"}, ConfigRev: targetRev(t, s, "a"), ConnectionID: connection.ID}
			done := make(chan error, 1)
			go func() {
				_, err := s.CompareResources(ctx, CompareRequest{Left: selection, Right: selection})
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("read did not start")
			}
			if mode == "configuration" {
				p.setHash("a", "h2")
				close(release)
			} else {
				cancel()
			}
			select {
			case err := <-done:
				require.Error(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("obsolete comparison did not end")
			}
		})
	}
}
