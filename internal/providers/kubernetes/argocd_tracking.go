package kubernetes

import (
	"github.com/spk/spk-ocular/internal/core"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"strings"
)

// A copied, non-self-referencing annotation never establishes ownership.
func argoTracking(u *unstructured.Unstructured) *core.ArgoTracking {
	gvk := u.GroupVersionKind()
	value := u.GetAnnotations()["argocd.argoproj.io/tracking-id"]
	method, app := "label", u.GetLabels()["app.kubernetes.io/instance"]
	if value != "" {
		parts := strings.Split(value, ":")
		if len(parts) != 3 || parts[1] != gvk.Group+"/"+gvk.Kind || (u.GetNamespace() != "" && parts[2] != u.GetNamespace()+"/"+u.GetName()) {
			return nil
		}
		identity := strings.Split(parts[2], "/")
		if len(identity) != 2 || identity[1] != u.GetName() {
			return nil
		}
		app, method = parts[0], "annotation"
	}
	if app == "" || len(app) > 512 || strings.ContainsAny(app, "/ :\t\r\n") {
		return nil
	}
	return &core.ArgoTracking{Application: app, Method: method, Group: gvk.Group, Kind: gvk.Kind, Installation: argoBound(u.GetAnnotations()["argocd.argoproj.io/installation-id"])}
}
