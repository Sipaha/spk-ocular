package kubernetes

import (
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/spk/spk-ocular/internal/core"
)

// addressWarnAfter: a LoadBalancer / Ingress without an address this long is a problem.
const addressWarnAfter = 5 * time.Minute

var servicesKind = &kindDef{
	desc: core.KindDescriptor{
		ID: "services", Title: "Services", Group: "Network", Scoped: true,
		Columns: []core.Column{colName, colNS,
			{ID: "type", Title: "Type", Type: core.ColText, Width: 110},
			{ID: "clusterip", Title: "Cluster IP", Type: core.ColText, Width: 130},
			{ID: "externalip", Title: "External IP", Type: core.ColText},
			{ID: "ports", Title: "Ports", Type: core.ColText},
			colAge},
	},
	gvr:        schema.GroupVersionResource{Version: "v1", Resource: "services"},
	namespaced: true,
	keep: fields{
		"spec":   fields{"type": true, "clusterIP": true, "externalIPs": true, "ports": true, "selector": true, "externalName": true},
		"status": fields{"loadBalancer": true},
	},
	project: func(u *unstructured.Unstructured, now time.Time) ([]core.Cell, core.Health, time.Time) {
		o := u.Object
		typ := nonEmpty(str(o, "spec", "type"), "ClusterIP")
		ext := lbAddresses(o)
		ext = append(ext, strs(o, "spec", "externalIPs")...)
		if typ == "ExternalName" {
			ext = append(ext, str(o, "spec", "externalName"))
		}
		cells := []core.Cell{
			core.TextCell(u.GetName()), core.TextCell(u.GetNamespace()), core.TextCell(typ),
			core.TextCell(str(o, "spec", "clusterIP")), core.TextCell(strings.Join(ext, ",")),
			core.TextCell(servicePorts(o)), createdCell(u),
		}
		if typ == "LoadBalancer" && len(lbAddresses(o)) == 0 {
			h, next := awaitingAddress(u, now, "the load balancer has no address yet")
			return cells, h, next
		}
		return cells, core.Health{State: core.HealthOK}, time.Time{}
	},
}

func lbAddresses(o map[string]any) []string {
	var out []string
	for _, in := range slice(o, "status", "loadBalancer", "ingress") {
		if ip := str(in, "ip"); ip != "" {
			out = append(out, ip)
		} else if h := str(in, "hostname"); h != "" {
			out = append(out, h)
		}
	}
	return out
}

func servicePorts(o map[string]any) string {
	var parts []string
	for _, p := range slice(o, "spec", "ports") {
		s := fmt.Sprint(i64(p, "port"))
		if np := i64(p, "nodePort"); np != 0 {
			s += fmt.Sprintf(":%d", np)
		}
		parts = append(parts, s+"/"+nonEmpty(str(p, "protocol"), "TCP"))
	}
	return strings.Join(parts, ",")
}

// awaitingAddress: progressing for a while, then a warning (time-based).
func awaitingAddress(u *unstructured.Unstructured, now time.Time, msg string) (core.Health, time.Time) {
	created := u.GetCreationTimestamp().Time
	if now.Sub(created) >= addressWarnAfter {
		return core.HealthFrom([]core.Issue{{State: core.HealthWarning, Reason: "NoAddress", Message: msg}}), time.Time{}
	}
	return core.HealthFrom([]core.Issue{{State: core.HealthProgressing, Reason: "Pending", Message: msg}}), created.Add(addressWarnAfter)
}

var ingressesKind = &kindDef{
	desc: core.KindDescriptor{
		ID: "networking.k8s.io/ingresses", Title: "Ingresses", Group: "Network", Scoped: true,
		Columns: []core.Column{colName, colNS,
			{ID: "class", Title: "Class", Type: core.ColText, Width: 110},
			{ID: "hosts", Title: "Hosts", Type: core.ColText},
			{ID: "address", Title: "Address", Type: core.ColText},
			{ID: "ports", Title: "Ports", Type: core.ColText, Width: 80},
			colAge},
	},
	gvr:        schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
	namespaced: true,
	keep: fields{
		"spec":   fields{"ingressClassName": true, "rules": true, "tls": true, "defaultBackend": true},
		"status": fields{"loadBalancer": true},
	},
	project: func(u *unstructured.Unstructured, now time.Time) ([]core.Cell, core.Health, time.Time) {
		o := u.Object
		var hosts []string
		for _, r := range slice(o, "spec", "rules") {
			hosts = append(hosts, nonEmpty(str(r, "host"), "*"))
		}
		ports := "80"
		if len(slice(o, "spec", "tls")) > 0 {
			ports = "80,443"
		}
		addr := lbAddresses(o)
		cells := []core.Cell{
			core.TextCell(u.GetName()), core.TextCell(u.GetNamespace()), core.TextCell(str(o, "spec", "ingressClassName")),
			core.TextCell(strings.Join(hosts, ",")), core.TextCell(strings.Join(addr, ",")), core.TextCell(ports), createdCell(u),
		}
		if len(addr) == 0 {
			h, next := awaitingAddress(u, now, "the ingress controller has not assigned an address")
			return cells, h, next
		}
		return cells, core.Health{State: core.HealthOK}, time.Time{}
	},
}
