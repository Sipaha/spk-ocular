package kubernetes

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spk/spk-ocular/internal/core"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const argoGroup = "argoproj.io"
const argoLimit = 2000
const argoBytes = 8 << 20

var argoResources = []struct{ resource, kind string }{{"applications", "Application"}, {"applicationsets", "ApplicationSet"}, {"appprojects", "AppProject"}, {"events", "Event"}, {"services", "Service"}}
var argoURLs = regexp.MustCompile(`https?://[^\s<>"']+`)

// ArgoCD reads controller observations, never Git, repo/cluster Secrets, Helm
// values, generator credentials or remote destination clusters.
func (s *session) ArgoCD(parent context.Context, scope core.ScopeSel) (core.ArgoSnapshot, error) {
	ctx, release, err := s.inventoryContext(parent)
	if err != nil {
		return core.ArgoSnapshot{}, err
	}
	defer release()
	out := core.ArgoSnapshot{Servers: []core.Ref{}, Objects: []core.ArgoObject{}, Events: []core.ArgoEvent{}, Coverage: []core.ArgoCoverage{}, Discovery: s.cat.snap().state}
	spaces := []string{""}
	if scope.Mode != core.ScopeAll {
		spaces = scope.SelectedNames()
	}
	if len(spaces) > 128 {
		spaces = spaces[:128]
		out.Truncated = true
	}
	count, bytes, pages := 0, 0, 0
	seen := map[string]bool{}
	for _, source := range argoResources {
		for _, ns := range spaces {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			gvr := schema.GroupVersionResource{Group: argoGroup, Version: "v1alpha1", Resource: source.resource}
			if source.kind == "Event" || source.kind == "Service" {
				gvr = schema.GroupVersionResource{Version: "v1", Resource: source.resource}
			} else if d := s.kind(argoGroup + "/" + source.resource); d != nil {
				gvr = d.gvr
			}
			state := "ready"
			cursor := inventoryCursor{}
			job, done := context.WithTimeout(ctx, 15*time.Second)
			for page := 0; ; page++ {
				if count >= argoLimit || bytes >= argoBytes || pages >= 2048 || page >= 256 {
					out.Truncated = true
					state = "partial"
					break
				}
				pages++
				options := metav1.ListOptions{Limit: 50, Continue: cursor.token}
				if source.kind == "Service" {
					options.LabelSelector = "app.kubernetes.io/name=argocd-server"
				}
				list, e := s.dyn.Resource(gvr).Namespace(ns).List(job, options)
				if e != nil {
					class, _ := classify(e)
					state = string(class)
					if apierrors.IsNotFound(e) && source.kind != "Event" && source.kind != "Service" {
						state = "missing"
						if st, ok := e.(apierrors.APIStatus); ok && st.Status().Details != nil && st.Status().Details.Kind == "namespaces" {
							state = "not_found"
						}
					}
					break
				}
				for i := range list.Items {
					u := &list.Items[i]
					if !scope.Contains(u.GetNamespace()) || u.GetUID() == "" {
						continue
					}
					raw, e := json.Marshal(u.Object)
					if e != nil {
						out.Truncated = true
						state = "partial"
						continue
					}
					bytes += len(raw)
					if len(raw) > 1<<20 || bytes > argoBytes || count >= argoLimit {
						out.Truncated = true
						state = "partial"
						break
					}
					key := gvr.String() + "/" + string(u.GetUID())
					if seen[key] {
						out.Truncated = true
						state = "partial"
						continue
					}
					seen[key] = true
					count++
					switch source.kind {
					case "Service":
						if u.GetLabels()["app.kubernetes.io/name"] == "argocd-server" {
							out.Servers = append(out.Servers, core.Ref{Provider: ProviderID, Target: s.target, Kind: "services", Scope: u.GetNamespace(), Name: u.GetName(), UID: string(u.GetUID())})
						}
					case "Event":
						if event, ok := s.argoEvent(u); ok {
							out.Events = append(out.Events, event)
						}
					default:
						object := s.argoObject(u, source.kind)
						out.Objects = append(out.Objects, object)
						out.Truncated = out.Truncated || object.Truncated
					}
				}
				more, repeated := cursor.advance(list.GetContinue())
				if repeated {
					out.Truncated = true
					state = "partial"
				}
				if !more || out.Truncated && state == "partial" {
					break
				}
			}
			done()
			out.Coverage = append(out.Coverage, core.ArgoCoverage{Kind: source.kind, Scope: ns, State: state})
		}
	}
	current := map[string]bool{}
	for _, object := range out.Objects {
		current[argoRefKey(object.Ref)] = true
	}
	for i := range out.Events {
		out.Events[i].Openable = current[argoRefKey(out.Events[i].Ref)]
	}
	sort.Slice(out.Objects, func(i, j int) bool {
		a, b := out.Objects[i], out.Objects[j]
		return a.Kind+"/"+a.Ref.Scope+"/"+a.Ref.Name < b.Kind+"/"+b.Ref.Scope+"/"+b.Ref.Name
	})
	sort.Slice(out.Events, func(i, j int) bool { return out.Events[i].At > out.Events[j].At })
	if len(out.Events) > 500 {
		out.Events = out.Events[:500]
		out.Truncated = true
	}
	out.CapturedAt = s.now().UnixMilli()
	return out, nil
}
func argoText(text string) string { return argoBound(argoURLs.ReplaceAllStringFunc(text, argoURL)) }
func argoURL(text string) string {
	if !strings.Contains(text, "://") {
		if at := strings.LastIndex(text, "@"); at >= 0 && strings.Contains(text[at+1:], ":") {
			text = text[at+1:]
		}
	}
	u, err := url.Parse(text)
	if err != nil {
		if !strings.Contains(text, "://") && strings.Contains(text, ":") && !strings.ContainsAny(text, "\r\n\t ") {
			return argoBound(strings.Split(strings.Split(text, "?")[0], "#")[0])
		}
		return "[invalid URL]"
	}
	if u.Opaque != "" && (u.Scheme == "https" || u.Scheme == "http") {
		return "[invalid URL]"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return argoBound(u.String())
}
func argoBound(value string) string {
	if len(value) > 512 {
		value = value[:509]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
		value += "…"
	}
	return value
}
func argoRefKey(ref core.Ref) string {
	return ref.Kind + "/" + ref.Scope + "/" + ref.Name + "/" + ref.UID
}

func argoString(o map[string]any, path ...string) string { return argoText(str(o, path...)) }
func argoSyncStatus(value string) string {
	if value == "Synced" || value == "OutOfSync" {
		return value
	}
	return "Unknown"
}
func argoHealthStatus(value string) string {
	switch value {
	case "Healthy", "Progressing", "Degraded", "Suspended", "Missing":
		return value
	}
	return "Unknown"
}
func argoOperationStatus(value string) string {
	switch value {
	case "Succeeded", "Running", "Failed", "Error", "Terminating":
		return value
	}
	return "Unknown"
}
func (s *session) argoObject(u *unstructured.Unstructured, kind string) core.ArgoObject {
	o := u.Object
	ref := core.Ref{Provider: ProviderID, Target: s.target, Kind: argoGroup + "/" + map[string]string{"Application": "applications", "ApplicationSet": "applicationsets", "AppProject": "appprojects"}[kind], Scope: u.GetNamespace(), Name: u.GetName(), UID: string(u.GetUID())}
	out := core.ArgoObject{Ref: ref, Kind: kind, Sync: argoSyncStatus(str(o, "status", "sync", "status")), Health: argoHealthStatus(str(o, "status", "health", "status")), Operation: argoOperationStatus(str(o, "status", "operationState", "phase")), Project: argoString(o, "spec", "project"), Description: argoString(o, "spec", "description"), Sources: []core.ArgoSource{}, Conditions: []core.ArgoCondition{}, Resources: []core.ArgoResource{}, Generators: []string{}, CreatedAt: u.GetCreationTimestamp().UnixMilli()}
	if ts := timeAt(o, "status", "reconciledAt"); !ts.IsZero() {
		out.ReconciledAt = ts.UnixMilli()
	}
	if u.GetCreationTimestamp().Time.IsZero() {
		out.CreatedAt = 0
	}
	out.OperationMessage = argoString(o, "status", "operationState", "message")
	out.Truncated = len(slice(o, "spec", "sources")) > 32 || len(slice(o, "status", "conditions")) > 32 || len(slice(o, "status", "resources")) > 500 || len(slice(o, "spec", "generators")) > 32
	out.Destination = argoURL(str(o, "spec", "destination", "server"))
	if out.Destination == "" {
		out.Destination = argoString(o, "spec", "destination", "name")
	}
	out.DestinationNamespace = argoString(o, "spec", "destination", "namespace")
	server := str(o, "spec", "destination", "server")
	out.LocalDestination = server == "https://kubernetes.default.svc" || server == "https://kubernetes.default.svc/" || server != "" && strings.TrimRight(server, "/") == strings.TrimRight(s.conn.cfg.Host, "/")
	sources := slice(o, "spec", "sources")
	revisions := strs(o, "status", "sync", "revisions")
	if len(sources) == 0 {
		if source, ok := fieldAt(o, "spec", "source").(map[string]any); ok {
			sources = []map[string]any{source}
			revisions = []string{str(o, "status", "sync", "revision")}
		}
	}
	for i, source := range sources {
		if i >= 32 {
			break
		}
		revision := ""
		if i < len(revisions) {
			revision = argoText(revisions[i])
		}
		out.Sources = append(out.Sources, core.ArgoSource{Repository: argoURL(str(source, "repoURL")), Path: argoString(source, "path"), Chart: argoString(source, "chart"), TargetRevision: argoString(source, "targetRevision"), ObservedRevision: revision})
	}
	for _, c := range slice(o, "status", "conditions") {
		if len(out.Conditions) >= 32 {
			break
		}
		out.Conditions = append(out.Conditions, core.ArgoCondition{Type: argoString(c, "type"), Message: argoString(c, "message")})
	}
	for _, r := range slice(o, "status", "resources") {
		if len(out.Resources) >= 500 {
			break
		}
		out.Resources = append(out.Resources, core.ArgoResource{Group: argoString(r, "group"), Version: argoString(r, "version"), Kind: argoString(r, "kind"), Name: argoString(r, "name"), Namespace: argoString(r, "namespace"), Sync: argoSyncStatus(str(r, "status")), Health: argoHealthStatus(str(r, "health", "status")), Message: argoString(r, "health", "message")})
	}
	if kind == "AppProject" {
		for i, repo := range strs(o, "spec", "sourceRepos") {
			if i >= 32 {
				out.Truncated = true
				break
			}
			out.Sources = append(out.Sources, core.ArgoSource{Repository: argoURL(repo)})
		}
	}
	for _, g := range slice(o, "spec", "generators") {
		for key := range g {
			if len(out.Generators) >= 32 {
				out.Truncated = true
			}
			if len(out.Generators) < 32 {
				out.Generators = append(out.Generators, argoText(key))
			}
		}
	}
	sort.Strings(out.Generators)
	return out
}
func (s *session) argoEvent(u *unstructured.Unstructured) (core.ArgoEvent, bool) {
	o := u.Object
	version := str(o, "involvedObject", "apiVersion")
	kind := str(o, "involvedObject", "kind")
	if !strings.HasPrefix(version, argoGroup+"/") || (kind != "Application" && kind != "ApplicationSet" && kind != "AppProject") {
		return core.ArgoEvent{}, false
	}
	at := timeAt(o, "lastTimestamp")
	if at.IsZero() {
		at = timeAt(o, "eventTime")
	}
	if at.IsZero() {
		at = u.GetCreationTimestamp().Time
	}
	atMillis := int64(0)
	if !at.IsZero() {
		atMillis = at.UnixMilli()
	}
	return core.ArgoEvent{ID: string(u.GetUID()), Ref: core.Ref{Provider: ProviderID, Target: s.target, Kind: argoGroup + "/" + map[string]string{"Application": "applications", "ApplicationSet": "applicationsets", "AppProject": "appprojects"}[kind], Scope: str(o, "involvedObject", "namespace"), Name: str(o, "involvedObject", "name"), UID: str(o, "involvedObject", "uid")}, Type: argoString(o, "type"), Reason: argoString(o, "reason"), Message: argoString(o, "message"), At: atMillis, Count: i64(o, "count")}, true
}
