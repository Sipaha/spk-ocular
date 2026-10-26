package kubernetes

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spk/spk-ocular/internal/krr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

// SetupKRR discovers candidates only. Pod reachability, authentication and
// historical cluster membership cannot be inferred from a Service definition.
func (s *session) SetupKRR(ctx context.Context) (krr.Setup, error) {
	out := krr.Setup{Sources: []string{}, Complete: true}
	next := ""
	seen := map[string]bool{}
	for page := 0; page < 4; page++ {
		list, e := s.dyn.Resource(schema.GroupVersionResource{Version: "v1", Resource: "services"}).List(ctx, metav1.ListOptions{Limit: 100, Continue: next})
		if e != nil {
			out.Complete = false
			return out, nil
		}
		if len(list.Items) > 100 {
			out.Complete = false
			return out, nil
		}
		for _, item := range list.Items {
			name, ns := item.GetName(), item.GetNamespace()
			app := item.GetLabels()["app.kubernetes.io/name"]
			if app == "" {
				app = item.GetLabels()["app"]
			}
			if app != "prometheus" && app != "prometheus-server" && !strings.Contains(name, "prometheus") {
				continue
			}
			if strings.Contains(name, "operator") || strings.Contains(name, "alertmanager") || strings.Contains(name, "adapter") || str(item.Object, "spec", "type") == "ExternalName" || item.GetDeletionTimestamp() != nil {
				continue
			}
			if len(validation.IsDNS1035Label(name)) != 0 || len(validation.IsDNS1123Label(ns)) != 0 || item.GetUID() == "" {
				continue
			}
			for _, p := range slice(item.Object, "spec", "ports") {
				port, found, e := unstructured.NestedInt64(p, "port")
				ok := found && e == nil && port > 0 && port <= 65535
				if protocol := str(p, "protocol"); protocol != "" && protocol != "TCP" {
					continue
				}
				if !ok {
					continue
				}
				pn := str(p, "name")
				if pn != "web" && pn != "http" && pn != "http-web" && pn != "prometheus" && port != 9090 {
					continue
				}
				url := fmt.Sprintf("http://%s.%s.svc:%d", name, ns, port)
				if !seen[url] {
					if len(out.Sources) >= 32 {
						out.Complete = false
						sort.Strings(out.Sources)
						return out, nil
					}
					out.Sources = append(out.Sources, url)
					seen[url] = true
				}
			}
		}
		more := list.GetContinue()
		if more == "" {
			sort.Strings(out.Sources)
			return out, nil
		}
		if more == next {
			out.Complete = false
			return out, nil
		}
		next = more
	}
	out.Complete = false
	sort.Strings(out.Sources)
	return out, nil
}
