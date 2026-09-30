package kubernetes

import (
	"context"
	"encoding/json"
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// actionWriter sends an action's write as exactly one request. client-go
// resends a request answered 5xx/429 with Retry-After (up to 10 times) —
// for a write that may already have been applied (a restart would roll out
// twice); whether to try again is decided only by RunAction, on proof.
type actionWriter interface {
	patch(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, pt types.PatchType, data []byte, sub string) error
	delete(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, opts metav1.DeleteOptions) error
	// editPatch sends an edit (a JSON merge patch) with strict field
	// validation, as a dry run or for real; the answer is the object.
	editPatch(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, data []byte, dryRun bool) ([]byte, error)
}

// restWriter: the dynamic client's REST setup, requests with MaxRetries(0).
type restWriter struct{ c rest.Interface }

func newRESTWriter(cfg *rest.Config) (*restWriter, error) {
	c := dynamic.ConfigFor(cfg)
	c.GroupVersion = nil
	c.APIPath = "/"
	rc, err := rest.UnversionedRESTClientFor(c)
	if err != nil {
		return nil, err
	}
	return &restWriter{c: rc}, nil
}

func objectPath(gvr schema.GroupVersionResource, ns, name, sub string) []string {
	p := []string{"/api", gvr.Version}
	if gvr.Group != "" {
		p = []string{"/apis", gvr.Group, gvr.Version}
	}
	if ns != "" {
		p = append(p, "namespaces", ns)
	}
	p = append(p, gvr.Resource, name)
	if sub != "" {
		p = append(p, sub)
	}
	return p
}

func (w *restWriter) patch(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, pt types.PatchType, data []byte, sub string) error {
	return w.c.Patch(pt).AbsPath(objectPath(gvr, ns, name, sub)...).Body(data).MaxRetries(0).Do(ctx).Error()
}

// editManager names the editor's writes in managedFields (kubectl edit's
// are "kubectl-edit").
const editManager = "spk-ocular"

func (w *restWriter) editPatch(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, data []byte, dryRun bool) ([]byte, error) {
	r := w.c.Patch(types.MergePatchType).AbsPath(objectPath(gvr, ns, name, "")...).Param("fieldValidation", "Strict").Param("fieldManager", editManager)
	if dryRun {
		r = r.Param("dryRun", "All")
	}
	res := r.Body(data).MaxRetries(0).Do(ctx)
	// Error, not Raw: only Error reads a refusal's Status (message, causes)
	// from the body; Raw gives the bare "the server rejected our request".
	if err := res.Error(); err != nil {
		return nil, err
	}
	return res.Raw()
}

func (w *restWriter) delete(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, opts metav1.DeleteOptions) error {
	opts.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "DeleteOptions"}
	body, err := json.Marshal(opts)
	if err != nil {
		return err
	}
	return w.c.Delete().AbsPath(objectPath(gvr, ns, name, "")...).SetHeader("Content-Type", "application/json").Body(body).MaxRetries(0).Do(ctx).Error()
}

// dynWriter writes through a dynamic client (fake clients in tests: no
// transport, no retries).
type dynWriter struct{ dyn dynamic.Interface }

func (w dynWriter) patch(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, pt types.PatchType, data []byte, sub string) error {
	var subs []string
	if sub != "" {
		subs = []string{sub}
	}
	_, err := w.dyn.Resource(gvr).Namespace(ns).Patch(ctx, name, pt, data, metav1.PatchOptions{}, subs...)
	return err
}

func (w dynWriter) editPatch(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, data []byte, dryRun bool) ([]byte, error) {
	opts := metav1.PatchOptions{FieldValidation: "Strict", FieldManager: editManager}
	if dryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	u, err := w.dyn.Resource(gvr).Namespace(ns).Patch(ctx, name, types.MergePatchType, data, opts)
	if err != nil {
		return nil, err
	}
	return u.MarshalJSON()
}

func (w dynWriter) delete(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, opts metav1.DeleteOptions) error {
	return w.dyn.Resource(gvr).Namespace(ns).Delete(ctx, name, opts)
}

// ambiguous: an answer that does not say whether a write was applied — a
// server error, a timeout, an unavailable server or gateway. (Too many
// requests is a refusal before the write.)
func ambiguous(err error) bool {
	if apierrors.IsTooManyRequests(err) {
		return false
	}
	var se apierrors.APIStatus
	if !errors.As(err, &se) {
		return false
	}
	return apierrors.IsInternalError(err) || apierrors.IsServerTimeout(err) || apierrors.IsTimeout(err) ||
		apierrors.IsServiceUnavailable(err) || apierrors.IsUnexpectedServerError(err) || se.Status().Code >= 500
}
