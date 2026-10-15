package kubernetes

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func TestInventoryListDecoderMatchesUpstream(t *testing.T) {
	base := dynamic.ConfigFor(&rest.Config{}).NegotiatedSerializer.SupportedMediaTypes()[0].Serializer
	decoder := inventoryJSON{base}
	for _, data := range []string{
		`{"apiVersion":"v1","kind":"PodList","metadata":{"resourceVersion":"123","continue":"next","remainingItemCount":9},"items":[{"metadata":{"name":"a","uid":"uid-a"},"spec":{"big":9007199254740993,"decimal":1.5}},{"apiVersion":"custom/v2","kind":"Other","metadata":{"name":"b"}}]}`,
		`{"apiVersion":"v1","kind":"PodList","items":[{"kind":"Existing"},{"apiVersion":"custom/v2"},null]}`,
		`{"kind":"List","items":[]}`,
		`{"kind":"List","items":null}`,
		`{"kind":"List"}`,
		`{"apiVersion":"v1","kind":"Status","status":"Failure","code":403}`,
		`{"kind":"List","items":{}}`,
		`{"kind":"List","items":["invalid"]}`,
		`{"apiVersion":42,"kind":"List","items":[]}`,
		`{"apiVersion":"a/b/c","kind":"List","items":[]}`,
		`{"apiVersion":"v1","Kind":"List","items":[]}`,
		`{"apiVersion":"v1","APIVERSION":42,"kind":"List","items":[]}`,
		`{"items":[]}`,
		`{"kind":"List","items":[`,
	} {
		t.Run(data, func(t *testing.T) {
			want, got := &unstructured.UnstructuredList{}, &unstructured.UnstructuredList{}
			_, wantGVK, wantErr := base.Decode([]byte(data), nil, want)
			_, gotGVK, gotErr := decoder.Decode([]byte(data), nil, got)
			if wantErr != nil {
				require.Error(t, gotErr)
				return
			}
			require.NoError(t, gotErr)
			require.Equal(t, wantGVK, gotGVK)
			require.Equal(t, want, got)
		})
	}
	// Ordinary GETs and Status decoding keep the original serializer path.
	want, got := &unstructured.Unstructured{}, &unstructured.Unstructured{}
	data := []byte(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"a"}}`)
	_, _, err := base.Decode(data, nil, want)
	require.NoError(t, err)
	_, _, err = decoder.Decode(data, nil, got)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestInventoryClientRetainsRequestAndErrorContracts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/namespaces/team/pods", r.URL.Path)
		require.Equal(t, "spk-ocular-decoder-test", r.Header.Get("User-Agent"))
		require.Equal(t, "100", r.URL.Query().Get("limit"))
		require.Equal(t, "metadata.name=chosen", r.URL.Query().Get("fieldSelector"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("continue") == "forbidden" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"Forbidden","message":"fixture denial","code":403}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"apiVersion":"v1","kind":"PodList","metadata":{"resourceVersion":"10","continue":"next","remainingItemCount":2},"items":[{"metadata":{"name":"chosen"},"spec":{"count":9007199254740993}}]}`)
	}))
	defer srv.Close()
	client, err := inventoryClient(budgetConfig(&rest.Config{Host: srv.URL, UserAgent: "spk-ocular-decoder-test"}))
	require.NoError(t, err)
	resource := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).Namespace("team")
	options := metav1.ListOptions{Limit: 100, Continue: "previous", FieldSelector: "metadata.name=chosen"}
	list, err := resource.List(context.Background(), options)
	require.NoError(t, err)
	require.Equal(t, "10", list.GetResourceVersion())
	require.Equal(t, "next", list.GetContinue())
	require.EqualValues(t, 2, *list.GetRemainingItemCount())
	require.Equal(t, "Pod", list.Items[0].GetKind())
	count, found, err := unstructured.NestedInt64(list.Items[0].Object, "spec", "count")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(9007199254740993), count)
	options.Continue = "forbidden"
	_, err = resource.List(context.Background(), options)
	require.True(t, apierrors.IsForbidden(err), "%v", err)
}
