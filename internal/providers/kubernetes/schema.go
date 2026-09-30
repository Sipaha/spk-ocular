package kubernetes

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/spk/spk-ocular/internal/core"
)

// A discovered kind's columns are the server's (Table) and hold for a
// schema epoch: an immutable snapshot a view, its cache and its rows share.
// An answer with other columns starts the next epoch; views of the old one
// end "schema changed" and are opened again (docs/plans P8, decision 2).

// tableSchema is one epoch of a discovered resource's columns.
type tableSchema struct {
	gvr        schema.GroupVersionResource
	namespaced bool
	epoch      uint64
	// table: rows come as server-side Tables; false — the plain format
	// (the server cannot answer a Table): Name, Namespace, Age.
	table bool
	// server: the columns as served (compared with every answer's).
	server []metav1.TableColumnDefinition
	// age: server columns (by index) that are the object's creation time
	// by known provenance, shown as live ages.
	age map[int]bool
	// shown: server column index per shown column after Name (and
	// Namespace); cols: the descriptor's columns.
	shown []int
	cols  []core.Column
	// def: the kind as this epoch shows it (projection by these columns).
	def *kindDef
	// retired: the epoch ended (views of it end; late answers are dropped).
	retired atomic.Bool
}

// matches: an answer's columns are this epoch's.
func (s *tableSchema) matches(cols []metav1.TableColumnDefinition) bool {
	if !s.table || len(cols) != len(s.server) {
		return false
	}
	for i, c := range cols {
		o := s.server[i]
		if c.Name != o.Name || c.Type != o.Type || c.Format != o.Format || c.Priority != o.Priority {
			return false
		}
	}
	return true
}

// same: the other schema shows the same thing (the epoch can stay).
func (s *tableSchema) same(o *tableSchema) bool {
	if s.table != o.table || s.namespaced != o.namespaced || len(s.age) != len(o.age) {
		return false
	}
	for i := range s.age {
		if !o.age[i] {
			return false
		}
	}
	return !s.table || s.matches(o.server)
}

// newTableSchema builds an epoch's schema. cols nil with table false: the
// plain format. age: server columns known to be the creation time.
func newTableSchema(base *kindDef, epoch uint64, table bool, cols []metav1.TableColumnDefinition, age map[int]bool) *tableSchema {
	s := &tableSchema{gvr: base.gvr, namespaced: base.namespaced, epoch: epoch, table: table, server: cols, age: age}
	if s.age == nil {
		s.age = map[int]bool{}
	}
	s.cols = []core.Column{colName}
	if s.namespaced {
		s.cols = append(s.cols, colNS)
	}
	if !table {
		s.cols = append(s.cols, colAge)
	} else {
		ids := map[string]bool{"name": true, "namespace": true}
		named := false
		for i, c := range cols {
			if c.Priority != 0 { // wide: facts of the details
				continue
			}
			if c.Format == "name" && !named { // the first name column is Name itself
				named = true
				continue
			}
			col := core.Column{ID: columnID(c.Name, ids), Title: c.Name, Type: core.ColText}
			switch {
			case s.age[i]:
				col.Type, col.Width = core.ColAge, colAge.Width
			case c.Type == "integer" || c.Type == "number":
				col.Type = core.ColNumber
			}
			s.shown = append(s.shown, i)
			s.cols = append(s.cols, col)
		}
	}
	d := *base
	d.desc.Columns = s.cols
	d.keep = genericKeep
	d.project = s.project
	d.schema = s
	s.def = &d
	return s
}

// columnID: a stable id from the column's name, unique in the table.
func columnID(name string, taken map[string]bool) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	id := strings.TrimSuffix(b.String(), "-")
	if id == "" {
		id = "column"
	}
	for n := 2; taken[id]; n++ {
		id = strings.TrimSuffix(id, "-"+strconv.Itoa(n-1)) + "-" + strconv.Itoa(n)
	}
	taken[id] = true
	return id
}

// genericKeep: what a discovered kind's cache keeps besides metadata —
// the row's cells and what health reads.
var genericKeep = fields{
	cellsField: true,
	"status":   fields{"conditions": fields{"type": true, "status": true, "reason": true, "message": true, "observedGeneration": true}},
}

func (s *tableSchema) project(u *unstructured.Unstructured, _ time.Time) ([]core.Cell, core.Health, time.Time) {
	cells := make([]core.Cell, 0, len(s.cols))
	cells = append(cells, core.TextCell(u.GetName()))
	if s.namespaced {
		cells = append(cells, core.TextCell(u.GetNamespace()))
	}
	if !s.table {
		cells = append(cells, createdCell(u))
	} else {
		raw, _ := u.Object[cellsField].([]any)
		for _, i := range s.shown {
			if s.age[i] {
				cells = append(cells, createdCell(u))
				continue
			}
			var v any
			if i < len(raw) {
				v = raw[i]
			}
			cells = append(cells, tableCell(s.server[i].Type, v))
		}
	}
	return cells, genericHealth(u), time.Time{}
}

// tableCell: numbers are numbers, booleans their words, no value empty
// (not 0, not false); the rest the server's text as is.
func tableCell(typ string, v any) core.Cell {
	switch t := v.(type) {
	case nil:
		return core.Cell{}
	case float64:
		if typ == "integer" || typ == "number" {
			return core.NumCell(t, strconv.FormatFloat(t, 'f', -1, 64))
		}
		return core.TextCell(strconv.FormatFloat(t, 'f', -1, 64))
	case int64:
		return tableCell(typ, float64(t))
	case bool:
		return core.TextCell(strconv.FormatBool(t))
	case string:
		if typ == "integer" || typ == "number" {
			if n, err := strconv.ParseFloat(t, 64); err == nil {
				return core.NumCell(n, t)
			}
		}
		return core.TextCell(t)
	}
	return core.TextCell(fmt.Sprint(v))
}

// genericHealth reads the common conditions (decision 2): deletion →
// terminating; a condition whose explicit observedGeneration is behind the
// object's generation is stale and says nothing (the controller has not
// looked yet: progressing). Failed/Degraded True → error; Ready (else
// Available) False → error, Unknown → progressing, True → ok; Stalled True
// → warning over ok; nothing known → neutral (unknown). The order of the
// conditions does not matter.
func genericHealth(u *unstructured.Unstructured) core.Health {
	if u.GetDeletionTimestamp() != nil {
		return core.HealthFrom([]core.Issue{{State: core.HealthTerminating, Reason: "Terminating"}})
	}
	gen := u.GetGeneration()
	conds := map[string]map[string]any{}
	stale := false
	for _, c := range slice(u.Object, "status", "conditions") {
		t := str(c, "type")
		if t == "" {
			continue
		}
		if _, ok := c["observedGeneration"]; ok && gen > 0 && i64(c, "observedGeneration") < gen {
			stale = true
			continue
		}
		conds[t] = c
	}
	is := func(t, status string) (map[string]any, bool) {
		c, ok := conds[t]
		return c, ok && str(c, "status") == status
	}
	issue := func(state core.HealthState, c map[string]any, fallback string) core.Health {
		reason := str(c, "reason")
		if reason == "" {
			reason = fallback
		}
		return core.HealthFrom([]core.Issue{{State: state, Reason: reason, Message: str(c, "message")}})
	}
	for _, t := range []string{"Failed", "Degraded"} {
		if c, ok := is(t, "True"); ok {
			return issue(core.HealthError, c, t)
		}
	}
	ready := "Ready"
	if _, ok := conds[ready]; !ok {
		ready = "Available"
	}
	if c, ok := conds[ready]; ok {
		switch str(c, "status") {
		case "False":
			return issue(core.HealthError, c, "Not"+ready)
		case "True":
			if s, ok := is("Stalled", "True"); ok {
				return issue(core.HealthWarning, s, "Stalled")
			}
			return core.Health{State: core.HealthOK}
		default:
			return issue(core.HealthProgressing, c, ready+"Unknown")
		}
	}
	if s, ok := is("Stalled", "True"); ok {
		return issue(core.HealthWarning, s, "Stalled")
	}
	if stale {
		return core.HealthFrom([]core.Issue{{State: core.HealthProgressing, Reason: "ObservedGenerationBehind", Message: "waiting for the controller to observe the latest change"}})
	}
	return core.Health{State: core.HealthUnknown}
}

// builtinAge: the exact built-in resources of kube-apiserver whose printer
// has an "Age" column of the creation time. Not a group's name: a CRD may
// serve another resource in a built-in group's name.
var builtinAge = map[schema.GroupResource]bool{}

func init() {
	for group, resources := range map[string][]string{
		"":                             {"endpoints", "limitranges", "persistentvolumeclaims", "persistentvolumes", "podtemplates", "replicationcontrollers", "resourcequotas", "serviceaccounts"},
		"admissionregistration.k8s.io": {"mutatingwebhookconfigurations", "validatingwebhookconfigurations", "validatingadmissionpolicies", "validatingadmissionpolicybindings", "mutatingadmissionpolicies", "mutatingadmissionpolicybindings"},
		"apiregistration.k8s.io":       {"apiservices"},
		"apps":                         {"controllerrevisions"},
		"autoscaling":                  {"horizontalpodautoscalers"},
		"batch":                        {"cronjobs", "jobs"},
		"certificates.k8s.io":          {"certificatesigningrequests", "clustertrustbundles"},
		"coordination.k8s.io":          {"leases", "leasecandidates"},
		"discovery.k8s.io":             {"endpointslices"},
		"flowcontrol.apiserver.k8s.io": {"flowschemas", "prioritylevelconfigurations"},
		"networking.k8s.io":            {"ingressclasses", "networkpolicies", "ipaddresses", "servicecidrs"},
		"node.k8s.io":                  {"runtimeclasses"},
		"policy":                       {"poddisruptionbudgets"},
		"rbac.authorization.k8s.io":    {"clusterrolebindings", "clusterroles", "rolebindings", "roles"},
		"resource.k8s.io":              {"deviceclasses", "resourceclaims", "resourceclaimtemplates", "resourceslices"},
		"scheduling.k8s.io":            {"priorityclasses"},
		"storage.k8s.io":               {"csidrivers", "csinodes", "csistoragecapacities", "storageclasses", "volumeattachments", "volumeattributesclasses"},
		"storagemigration.k8s.io":      {"storageversionmigrations"},
		"internal.apiserver.k8s.io":    {"storageversions"},
	} {
		for _, r := range resources {
			builtinAge[schema.GroupResource{Group: group, Resource: r}] = true
		}
	}
}

// crdColumns: what a CRD says of its columns in one version — printer
// columns by name with their JSONPath; none: the server's default (Name,
// Age of the creation time).
type crdColumns struct {
	found   bool
	printer map[string]string // column name → JSONPath
}

// ageColumns: the server columns that are the creation time by known
// provenance (see builtinAge, crdColumns); a column's description is no
// proof (a CRD author writes it).
func ageColumns(gr schema.GroupResource, cols []metav1.TableColumnDefinition, crd crdColumns) map[int]bool {
	out := map[int]bool{}
	for i, c := range cols {
		switch {
		case builtinAge[gr]:
			if c.Name == "Age" {
				out[i] = true
			}
		case crd.found && len(crd.printer) == 0:
			if c.Name == "Age" {
				out[i] = true
			}
		case crd.found:
			if crd.printer[c.Name] == ".metadata.creationTimestamp" {
				out[i] = true
			}
		}
	}
	return out
}
