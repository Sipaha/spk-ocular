package kubernetes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/spk/spk-ocular/internal/core"
)

var widgetsDef = discoveredDef(apiResource{Group: "ocular.dev", Version: "v1", Resource: "widgets", Kind: "Widget", Namespaced: true, Verbs: lw})

// The server's columns: Name is the name column, Namespace is added for
// namespaced kinds, wide ones are not shown, numbers are numbers, ids are
// unique.
func TestTableSchemaColumns(t *testing.T) {
	cols := []metav1.TableColumnDefinition{
		{Name: "Name", Type: "string", Format: "name"},
		{Name: "Size", Type: "integer"},
		{Name: "Status", Type: "string"},
		{Name: "status", Type: "string"},
		{Name: "Detail", Type: "string", Priority: 1},
		{Name: "Since", Type: "date"},
		{Name: "Ratio", Type: "number"},
	}
	s := newTableSchema(widgetsDef, 3, true, cols, map[int]bool{5: true})
	var ids, types []string
	for _, c := range s.cols {
		ids = append(ids, c.ID)
		types = append(types, string(c.Type))
	}
	assert.Equal(t, []string{"name", "namespace", "size", "status", "status-2", "since", "ratio"}, ids)
	assert.Equal(t, []string{"text", "text", "number", "text", "text", "age", "number"}, types)
	assert.True(t, s.cols[1].ScopeColumn)
	assert.Equal(t, s.cols, s.def.desc.Columns)
	assert.Same(t, s, s.def.schema)

	u := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "alpha", "namespace": "ns", "creationTimestamp": "2026-09-30T10:00:00Z"},
		cellsField: []any{"alpha", float64(5), "Running", nil, "wide", "40s", "0.5"},
	}}
	cells, _, _ := s.project(u, u.GetCreationTimestamp().Time)
	assert.Equal(t, "alpha", cells[0].Text)
	assert.Equal(t, "ns", cells[1].Text)
	assert.Equal(t, 5.0, *cells[2].Num)
	assert.Equal(t, "5", cells[2].Text)
	assert.Equal(t, "Running", cells[3].Text)
	assert.Equal(t, core.Cell{}, cells[4], "no value is empty, not 0 or false")
	assert.Equal(t, u.GetCreationTimestamp().UnixMilli(), cells[5].Time, "a known creation time ticks")
	assert.Equal(t, 0.5, *cells[6].Num)

	assert.True(t, s.matches(cols))
	moved := append([]metav1.TableColumnDefinition{cols[1], cols[0]}, cols[2:]...)
	assert.False(t, s.matches(moved), "reordered columns are another schema")
	assert.False(t, s.matches(cols[:3]))
	assert.False(t, s.same(newTableSchema(widgetsDef, 4, true, cols, nil)), "another provenance is another schema")
	assert.True(t, s.same(newTableSchema(widgetsDef, 4, true, cols, map[int]bool{5: true})))
}

func TestTableCells(t *testing.T) {
	assert.Equal(t, core.Cell{}, tableCell("string", nil))
	assert.Equal(t, "true", tableCell("boolean", true).Text)
	assert.Equal(t, "false", tableCell("boolean", false).Text)
	assert.Equal(t, "7", tableCell("string", float64(7)).Text)
	assert.Nil(t, tableCell("string", float64(7)).Num)
	assert.Equal(t, 3.0, *tableCell("integer", "3").Num)
	assert.Equal(t, "n/a", tableCell("integer", "n/a").Text)
	assert.Equal(t, "5m", tableCell("date", "5m").Text, "a server duration stays text")
}

// The plain format: Name, Namespace, Age from metadata.
func TestPlainSchema(t *testing.T) {
	s := newTableSchema(widgetsDef, 1, false, nil, nil)
	assert.Equal(t, []core.Column{colName, colNS, colAge}, s.cols)
	u := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "a", "namespace": "ns", "creationTimestamp": "2026-09-30T10:00:00Z"}}}
	cells, h, _ := s.project(u, u.GetCreationTimestamp().Time)
	assert.Equal(t, []core.Cell{core.TextCell("a"), core.TextCell("ns"), createdCell(u)}, cells)
	assert.Equal(t, core.HealthUnknown, h.State)
	assert.False(t, s.matches([]metav1.TableColumnDefinition{{Name: "Name"}}))
}

// Age is the creation time only by known provenance: an exact built-in
// resource's "Age", or the CRD's own column path; never the description.
func TestAgeProvenance(t *testing.T) {
	cols := []metav1.TableColumnDefinition{{Name: "Name", Format: "name"}, {Name: "Since", Type: "date"},
		{Name: "Age", Type: "date", Description: "CreationTimestamp is a timestamp representing the server time"}}
	gr := func(g, r string) schema.GroupResource { return schema.GroupResource{Group: g, Resource: r} }
	assert.Equal(t, map[int]bool{2: true}, ageColumns(gr("batch", "jobs"), cols, crdColumns{}))
	assert.Equal(t, map[int]bool{2: true}, ageColumns(gr("", "persistentvolumeclaims"), cols, crdColumns{}))
	assert.Empty(t, ageColumns(gr("networking.k8s.io", "foos"), cols, crdColumns{}), "a CRD in a built-in group's name")
	assert.Empty(t, ageColumns(gr("gateway.networking.k8s.io", "gateways"), cols, crdColumns{}))
	assert.Empty(t, ageColumns(gr("ocular.dev", "widgets"), cols, crdColumns{}), "the CRD not read: server text")
	assert.Equal(t, map[int]bool{2: true}, ageColumns(gr("ocular.dev", "gadgets"), cols, crdColumns{found: true}), "no printer columns: the default Age")
	assert.Equal(t, map[int]bool{1: true}, ageColumns(gr("ocular.dev", "widgets"), cols,
		crdColumns{found: true, printer: map[string]string{"Since": ".metadata.creationTimestamp", "Age": ".status.lastSeen"}}))

	crd := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"versions": []any{
		map[string]any{"name": "v1beta1", "additionalPrinterColumns": []any{map[string]any{"name": "Old", "jsonPath": ".metadata.creationTimestamp"}}},
		map[string]any{"name": "v1", "additionalPrinterColumns": []any{map[string]any{"name": "Since", "jsonPath": ".metadata.creationTimestamp"}}},
		map[string]any{"name": "v2"},
	}}}}
	assert.Equal(t, crdColumns{found: true, printer: map[string]string{"Since": ".metadata.creationTimestamp"}}, crdColumnsOf(crd, "v1"))
	assert.Equal(t, crdColumns{found: true, printer: map[string]string{}}, crdColumnsOf(crd, "v2"))
	assert.Equal(t, crdColumns{}, crdColumnsOf(crd, "v3"))
}

func TestGenericHealth(t *testing.T) {
	obj := func(gen int64, deleting bool, conds ...map[string]any) *unstructured.Unstructured {
		var cs []any
		for _, c := range conds {
			cs = append(cs, c)
		}
		u := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "x", "generation": gen}, "status": map[string]any{"conditions": cs}}}
		if deleting {
			now := metav1.Now()
			u.SetDeletionTimestamp(&now)
		}
		return u
	}
	c := func(typ, status string, og ...int64) map[string]any {
		m := map[string]any{"type": typ, "status": status, "reason": typ + status}
		if len(og) > 0 {
			m["observedGeneration"] = float64(og[0])
		}
		return m
	}
	cases := []struct {
		name  string
		u     *unstructured.Unstructured
		state core.HealthState
	}{
		{"nothing known", obj(1, false), core.HealthUnknown},
		{"deleting", obj(1, true, c("Ready", "True")), core.HealthTerminating},
		{"ready", obj(2, false, c("Ready", "True", 2)), core.HealthOK},
		{"not ready", obj(1, false, c("Ready", "False")), core.HealthError},
		{"ready unknown", obj(1, false, c("Ready", "Unknown")), core.HealthProgressing},
		{"failed over ready", obj(1, false, c("Ready", "True"), c("Failed", "True")), core.HealthError},
		{"degraded over ready, any order", obj(1, false, c("Degraded", "True"), c("Ready", "True")), core.HealthError},
		{"stalled over ready", obj(1, false, c("Ready", "True"), c("Stalled", "True")), core.HealthWarning},
		{"available without ready", obj(1, false, c("Available", "False")), core.HealthError},
		{"stale ready says nothing", obj(3, false, c("Ready", "True", 2)), core.HealthProgressing},
		{"stale failure says nothing", obj(3, false, c("Failed", "True", 2), c("Ready", "True")), core.HealthOK},
		{"no observedGeneration is current", obj(3, false, c("Ready", "False")), core.HealthError},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.state, genericHealth(tc.u).State, tc.name)
	}
	h := genericHealth(obj(1, false, c("Ready", "False")))
	assert.Equal(t, "ReadyFalse", h.Reason)
}
