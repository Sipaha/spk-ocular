package kubernetes

import (
	"context"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestRollingPodChoicesUseOwnedReplicaSetsAndReuseListing(t *testing.T) {
	dep := rolloutDeployment("10", "v2", nil)
	old := revisionRS("web-old1", "rs-old", "uid-web", "193", "v1", nil)
	current := revisionRS("web-new1", "rs-new", "uid-web", "194", "v2", nil)
	foreign := revisionRS("web-outs", "rs-out", "another-deployment", "999", "v2", nil)
	ready := webPod("old-ready", "p-old", "rs-old", 10)
	ready.Object["status"].(map[string]any)["containerStatuses"] = []any{map[string]any{"name": "app", "ready": true, "state": map[string]any{"running": map[string]any{}}}}
	starting := webPod("new-starting", "p-new", "rs-new", 20, notReady)
	starting.Object["status"].(map[string]any)["containerStatuses"] = []any{map[string]any{"name": "app", "ready": false, "state": map[string]any{"waiting": map[string]any{"reason": "ContainerCreating"}}}}
	s, _ := execSessionFor(t, nil, dep, old, current, foreign, ready, starting, webPod("outsider", "p-out", "rs-out", 30))
	ref := webRef()
	ref.UID = "uid-web"
	info, err := s.LogInfo(context.Background(), ref)
	require.NoError(t, err)
	require.Len(t, info.Instances, 2)
	assert.Equal(t, "old-ready", info.Instances[0].Title, "retain ready-first ordering")
	assert.Equal(t, []core.Message{msg("instance.previous", "revision", "193"), msg("instance.ready"), msg("instance.containers", "ready", "1", "total", "1"), msg("instance.state", "state", "Running")}, info.Instances[0].Details)
	assert.Equal(t, []core.Message{msg("instance.current", "revision", "194"), msg("instance.notReady"), msg("instance.containers", "ready", "0", "total", "1"), msg("instance.state", "state", "ContainerCreating")}, info.Instances[1].Details)
	assert.Equal(t, "p-new", info.Instances[1].Ref.UID)
	actions := s.conn.dyn.(*dynamicfake.FakeDynamicClient).Actions()
	require.Len(t, actions, 3, "one Deployment GET and the existing ReplicaSet/Pod lists only")
	assert.Equal(t, "get", actions[0].GetVerb())
	assert.Equal(t, "replicasets", actions[1].GetResource().Resource)
	assert.Equal(t, "pods", actions[2].GetResource().Resource)
	exec, err := s.ExecInfo(context.Background(), ref)
	require.NoError(t, err)
	require.Len(t, exec.Instances, 1, "not-yet-running containers are not executable")
	assert.Equal(t, info.Instances[0].Details, exec.Instances[0].Details)
}

func TestPodChoiceRevisionUsesTemplateDuringRollbackNotHighestNumber(t *testing.T) {
	p := webPod("pod", "p", "rs", 1)
	// An older ReplicaSet can already match a requested rollback before its
	// revision annotation is increased by the controller.
	dep := rolloutDeployment("11", "v1", nil)
	rs := revisionRS("web-old1", "rs", "uid-web", "193", "v1", nil)
	assert.Equal(t, msg("instance.current", "revision", "193"), podChoiceDetails(p, dep, rs)[0])
	dep = rolloutDeployment("12", "not-created-yet", nil)
	assert.Equal(t, msg("instance.previous", "revision", "193"), podChoiceDetails(p, dep, rs)[0])
	delete(rs.Object["spec"].(map[string]any), "template")
	assert.Equal(t, msg("instance.revision", "revision", "193"), podChoiceDetails(p, dep, rs)[0], "missing templates cannot classify current/previous")
	rs.SetAnnotations(map[string]string{revisionKey: "bad"})
	assert.Equal(t, msg("instance.ready"), podChoiceDetails(p, dep, rs)[0], "invalid revision is omitted")
	assert.Equal(t, msg("instance.ready"), podChoiceDetails(p, nil, nil)[0], "non-Deployment pods have no inferred revision")
}

func TestPodChoiceShowsReadinessSeparatelyFromStatusAndDeletion(t *testing.T) {
	p := webPod("pod", "p", "rs", 1, notReady)
	p.Object["status"].(map[string]any)["containerStatuses"] = []any{
		map[string]any{"name": "app", "ready": true, "state": map[string]any{"running": map[string]any{}}},
		map[string]any{"name": "stale-container", "ready": true},
	}
	d := podChoiceDetails(p, nil, nil)
	assert.Equal(t, msg("instance.notReady"), d[0], "Pod readiness gates may fail even with ready containers")
	assert.Equal(t, msg("instance.containers", "ready", "1", "total", "1"), d[1])
	assert.Equal(t, msg("instance.state", "state", "Running"), d[2])
	p.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})
	assert.Equal(t, msg("instance.state", "state", "Terminating"), podChoiceDetails(p, nil, nil)[2])
}
