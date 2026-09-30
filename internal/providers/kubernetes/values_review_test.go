package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// Every way out of a value plan or read is an authored projection: the
// server's (or a transport's) strings never pass, whatever they carry.
func TestValueOutputsNeverCarryServerStrings(t *testing.T) {
	leak := errors.New("server says " + marker + " " + marker64)
	t.Run("the pods lookup refused", func(t *testing.T) {
		s, w := valueSession(t, secret("3", map[string]string{"k": "a"}, nil))
		w.dyn.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", leak)
		})
		_, b := listed(t, s)
		plan, _, err := s.PrepareValueEdit(context.Background(), setReq(b, "k", "z"))
		require.NoError(t, err)
		require.NotNil(t, plan.Consumers)
		assert.False(t, plan.Consumers.Known)
		assert.NotEmpty(t, plan.Consumers.Why, "unknown says why")
		noMarker(t, "consumers", plan)
	})
	t.Run("the rights check failed", func(t *testing.T) {
		s, w := valueSession(t, secret("3", map[string]string{"k": "a"}, nil))
		w.dyn.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "selfsubjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "selfsubjectaccessreviews"}, "", leak)
		})
		_, b := listed(t, s)
		plan, _, err := s.PrepareValueEdit(context.Background(), setReq(b, "k", "z"))
		require.NoError(t, err)
		assert.Equal(t, core.RightsUnknown, plan.Rights.State)
		assert.NotEmpty(t, plan.Rights.Reason)
		noMarker(t, "rights", plan)
	})
	t.Run("the authorizer's evaluation error", func(t *testing.T) {
		s, w := valueSession(t, secret("3", map[string]string{"k": "a"}, nil))
		w.dyn.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "selfsubjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, &unstructured.Unstructured{Object: map[string]any{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview",
				"status": map[string]any{"allowed": false, "evaluationError": leak.Error()}}}, nil
		})
		_, b := listed(t, s)
		plan, _, err := s.PrepareValueEdit(context.Background(), setReq(b, "k", "z"))
		require.NoError(t, err)
		assert.Equal(t, core.RightsUnknown, plan.Rights.State)
		noMarker(t, "evaluation error", plan)
	})
	t.Run("the authorizer's denial reason", func(t *testing.T) {
		s, w := valueSession(t, secret("3", map[string]string{"k": "a"}, nil))
		w.dyn.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "selfsubjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, &unstructured.Unstructured{Object: map[string]any{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview",
				"status": map[string]any{"allowed": false, "denied": true, "reason": leak.Error()}}}, nil
		})
		_, b := listed(t, s)
		plan, grant, err := s.PrepareValueEdit(context.Background(), setReq(b, "k", "z"))
		require.NoError(t, err)
		assert.Equal(t, core.RightsDenied, plan.Rights.State)
		assert.Equal(t, "you may not patch secrets in ns", plan.Rights.Reason)
		assert.Nil(t, grant)
		noMarker(t, "denial", plan)
	})
	t.Run("a read failing without a status", func(t *testing.T) {
		s, w := valueSession(t, secret("3", map[string]string{"k": "a"}, nil))
		ref := secRef
		ref.UID = "uid-s"
		_, b := listed(t, s)
		w.dyn.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, leak
		})
		_, _, err := s.Values(context.Background(), secRef)
		require.Error(t, err)
		noMarker(t, "values", err)
		_, err = s.RevealValue(context.Background(), ref, "k")
		require.Error(t, err)
		noMarker(t, "reveal", err)
		_, _, err = s.PrepareValueEdit(context.Background(), setReq(b, "k", "z"))
		require.Error(t, err)
		noMarker(t, "prepare", err)
	})
}

// waitingDyn's Secret GET answers only when its context ends (a stuck
// transport that honours cancellation).
type waitingDyn struct{ dynamic.Interface }

func (d waitingDyn) Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return waitingRes{d.Interface.Resource(gvr)}
}

type waitingRes struct {
	dynamic.NamespaceableResourceInterface
}

func (r waitingRes) Namespace(ns string) dynamic.ResourceInterface {
	return waitingNsRes{r.NamespaceableResourceInterface.Namespace(ns)}
}

type waitingNsRes struct{ dynamic.ResourceInterface }

func (r waitingNsRes) Get(ctx context.Context, _ string, _ metav1.GetOptions, _ ...string) (*unstructured.Unstructured, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// Reads of values end with the session: nothing is read or returned over a
// closed connection.
func TestValueReadsEndWithTheSession(t *testing.T) {
	for name, read := range map[string]func(s *session) error{
		"values": func(s *session) error { _, _, err := s.Values(context.Background(), secRef); return err },
		"reveal": func(s *session) error {
			ref := secRef
			ref.UID = "uid-s"
			_, err := s.RevealValue(context.Background(), ref, "k")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSession("ctx", "h", waitingDyn{valueClient()}, false)
			t.Cleanup(s.Close)
			done := make(chan error, 1)
			go func() { done <- read(s) }()
			time.Sleep(20 * time.Millisecond)
			s.cancel()
			select {
			case err := <-done:
				assertClass(t, err, provider.ClassUnavailable)
			case <-time.After(2 * time.Second):
				t.Fatal("the read outlived its session")
			}
		})
	}
}

// A checked review's After is the size the server's dry run leaves, not
// the size typed.
func TestReviewValueAfterIsTheDryRunResultSize(t *testing.T) {
	t.Run("admission lengthens the value", func(t *testing.T) {
		s, w := valueSession(t, secret("3", map[string]string{"k": "a"}, nil))
		_, b := listed(t, s)
		w.dryAnswer = func(o map[string]any) { o["data"].(map[string]any)["k"] = b64("admission-long-value") }
		plan, _, err := s.PrepareValueEdit(context.Background(), setReq(b, "k", "new"))
		require.NoError(t, err)
		require.True(t, plan.Checked)
		assert.Equal(t, len("admission-long-value"), plan.After)
	})
	t.Run("admission removes the key", func(t *testing.T) {
		s, w := valueSession(t, secret("3", map[string]string{"k": "a"}, nil))
		_, b := listed(t, s)
		w.dryAnswer = func(o map[string]any) { delete(o["data"].(map[string]any), "k") }
		plan, _, err := s.PrepareValueEdit(context.Background(), setReq(b, "k", "new"))
		require.NoError(t, err)
		require.True(t, plan.Checked)
		assert.Equal(t, -1, plan.After)
	})
}

// Comparing key sets is linear: a Secret of many small keys is compared at once.
func TestDifferingIsLinear(t *testing.T) {
	const n = 40_000
	a := make([]provider.ValuePrint, n)
	for i := range a {
		a[i] = provider.ValuePrint{Key: fmt.Sprintf("k-%06d", i), Present: true, Print: "p"}
	}
	b := append([]provider.ValuePrint{}, a...)
	b[n/2].Print = "q"
	start := time.Now()
	d := differing(a, b)
	assert.Equal(t, []string{fmt.Sprintf("k-%06d", n/2)}, d)
	assert.Less(t, time.Since(start), 2*time.Second)
}
