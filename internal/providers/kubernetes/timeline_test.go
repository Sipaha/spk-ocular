package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

func timelineFixture(name, uid, namespace string) *unstructured.Unstructured {
	return mk("v1", "Event", namespace, name, uid, map[string]any{
		"involvedObject": map[string]any{"apiVersion": "v1", "kind": "Pod", "namespace": namespace, "name": "api-0", "uid": "old-pod"},
		"firstTimestamp": "2026-10-10T01:00:00Z", "lastTimestamp": "2026-10-10T01:05:00Z",
		"type": "Warning", "reason": "BackOff", "message": "Back-off restarting failed container", "count": int64(3),
		"series": map[string]any{"count": int64(9), "lastObservedTime": "2026-10-10T01:06:00Z"},
	})
}
func TestTimelineScopeOwnershipAndCumulativeTimes(t *testing.T) {
	pod := mk("v1", "Pod", "blue", "api-0", "new-pod", map[string]any{"metadata": map[string]any{"ownerReferences": []any{map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "api", "uid": "rs", "controller": true}}}, "spec": map[string]any{"private": "PRIVATE-VALUE"}})
	owner := mk("apps/v1", "ReplicaSet", "blue", "api", "rs", nil)
	client := fullFake(pod, owner, timelineFixture("a", "a", "blue"), timelineFixture("b", "b", "green"))
	s := newSession("t", "h", client, false)
	defer s.Close()
	out, err := s.Timeline(context.Background(), core.ScopeSel{Mode: core.ScopeSome, Names: []string{"blue"}})
	require.NoError(t, err)
	require.Len(t, out.Events, 1)
	e := out.Events[0]
	assert.Equal(t, int64(9), e.Count)
	assert.Equal(t, time.Date(2026, 10, 10, 1, 6, 0, 0, time.UTC).UnixMilli(), e.LastAt)
	assert.Equal(t, time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC).UnixMilli(), e.FirstAt)
	assert.False(t, e.TimeFallback)
	assert.True(t, e.Openable)
	assert.Equal(t, "old-pod", e.Subject.UID)
	_, err = s.Get(context.Background(), e.Subject)
	require.Error(t, err)
	require.Len(t, out.Resources, 2)
	for _, r := range out.Resources {
		if r.Ref.UID == "new-pod" {
			assert.Equal(t, "rs", r.OwnerUID)
		}
	}
	b, err := json.Marshal(out)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "PRIVATE-VALUE")
	for _, a := range client.Actions() {
		assert.Equal(t, "blue", a.GetNamespace())
		assert.NotContains(t, []string{"secrets", "configmaps"}, a.GetResource().Resource)
	}
	client.ClearActions()
	empty, err := s.Timeline(context.Background(), core.ScopeSel{Mode: core.ScopeSome, Names: []string{}})
	require.NoError(t, err)
	assert.Empty(t, empty.Events)
	assert.Empty(t, empty.Resources)
	assert.Empty(t, client.Actions())
}
func TestTimelineTimeFallbackUnknownKindsAndMessageLimit(t *testing.T) {
	s := newSession("t", "h", fullFake(), false)
	defer s.Close()
	u := timelineFixture("event", "uid", "blue")
	u.Object["message"] = strings.Repeat("界", 1000)
	u.Object["involvedObject"] = map[string]any{"apiVersion": "unknown/v1", "kind": "Unknown", "name": "x"}
	u.Object["firstTimestamp"] = "2026-10-10T02:00:00Z"
	e := s.timelineEvent(u)
	assert.True(t, e.TimeFallback)
	assert.Equal(t, e.LastAt, e.FirstAt)
	assert.False(t, e.Openable)
	assert.True(t, e.MessageTruncated)
	assert.LessOrEqual(t, len(e.Message), timelineMessageLimit)
	assert.True(t, utf8.ValidString(e.Message))
	delete(u.Object, "firstTimestamp")
	delete(u.Object, "lastTimestamp")
	delete(u.Object, "series")
	u.Object["eventTime"] = "2026-10-10T01:01:00.123456Z"
	e = s.timelineEvent(u)
	assert.False(t, e.TimeFallback)
	assert.Equal(t, e.FirstAt, e.LastAt)
	assert.Positive(t, e.LastAt)
	delete(u.Object, "eventTime")
	u.SetCreationTimestamp(metav1.Time{})
	e = s.timelineEvent(u)
	assert.True(t, e.TimeFallback)
	assert.Zero(t, e.FirstAt)
	assert.Zero(t, e.LastAt)
}
func TestTimelinePartialPaginationCancellationAndCap(t *testing.T) {
	client := fullFake()
	page := 0
	client.PrependReactor("list", "events", func(k8stesting.Action) (bool, runtime.Object, error) {
		page++
		u := timelineFixture(fmt.Sprint(page), fmt.Sprint(page), "blue")
		list := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*u}}
		if page == 1 {
			list.SetContinue("next")
		}
		return true, list, nil
	})
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", fmt.Errorf("private error"))
	})
	s := newSession("t", "h", client, false)
	defer s.Close()
	scope := core.ScopeSel{Mode: core.ScopeOne, Name: "blue"}
	out, err := s.Timeline(context.Background(), scope)
	require.NoError(t, err)
	require.Len(t, out.Events, 2)
	assert.Equal(t, 2, page)
	assert.False(t, out.Truncated)
	assert.Contains(t, out.Problems, core.GraphProblem{Kind: "pods", Scope: "blue", Class: "forbidden"})
	b, _ := json.Marshal(out)
	assert.NotContains(t, string(b), "private error")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.Timeline(ctx, scope)
	require.ErrorIs(t, err, context.Canceled)
	client.PrependReactor("list", "events", func(k8stesting.Action) (bool, runtime.Object, error) {
		items := make([]unstructured.Unstructured, timelineEventLimit+1)
		for i := range items {
			items[i] = *timelineFixture(fmt.Sprint(i), fmt.Sprint(i), "blue")
		}
		return true, &unstructured.UnstructuredList{Items: items}, nil
	})
	out, err = s.Timeline(context.Background(), scope)
	require.NoError(t, err)
	assert.Len(t, out.Events, timelineEventLimit)
	assert.True(t, out.Truncated)
}
func TestTimelineOwnershipRequestsMetadataOnly(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		assert.Equal(t, "/api/v1/namespaces/blue/pods", r.URL.Path)
		assert.Contains(t, r.Header.Get("Accept"), "as=PartialObjectMetadataList")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"meta.k8s.io/v1","kind":"PartialObjectMetadataList","metadata":{},"items":[{"apiVersion":"meta.k8s.io/v1","kind":"PartialObjectMetadata","metadata":{"name":"api-0","namespace":"blue","uid":"pod"}}]}`))
	}))
	defer server.Close()
	client := fullFake(timelineFixture("event", "event", "blue"))
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		t.Fatal("raw Pod list requested")
		return true, nil, nil
	})
	s := newSession("t", "h", client, false)
	defer s.Close()
	s.cat = newCatalog(s.ctx, newKindRegistry(eventsKind, podsKind), nil)
	var err error
	s.graphMetadata, err = metadata.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	out, err := s.Timeline(context.Background(), core.ScopeSel{Mode: core.ScopeOne, Name: "blue"})
	require.NoError(t, err)
	assert.Equal(t, 1, requests)
	require.Len(t, out.Resources, 1)
	assert.Equal(t, "pod", out.Resources[0].Ref.UID)
}
