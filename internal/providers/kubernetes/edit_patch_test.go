package kubernetes

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var cmID = editIdentity{APIVersion: "v1", Kind: "ConfigMap", Name: "cfg", Namespace: "web", UID: "u1"}

const cmHead = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cfg\n  namespace: web\n  uid: u1\n  resourceVersion: \"7\"\n"

func patchOf(t *testing.T, id editIdentity, orig, edited string) (string, error) {
	t.Helper()
	p, err := buildEditPatch(id, mustParse(t, orig), mustParse(t, edited))
	if err != nil {
		return "", err
	}
	return toJSON(t, p), nil
}

func TestEditPatchKeepsTheObjectsIdentity(t *testing.T) {
	orig := cmHead + "data: {a: \"1\"}\n"
	for name, edited := range map[string]string{
		"renamed":      strings.Replace(orig, "name: cfg", "name: other", 1),
		"moved":        strings.Replace(orig, "namespace: web", "namespace: db", 1),
		"another kind": strings.Replace(orig, "kind: ConfigMap", "kind: Secret", 1),
		"another uid":  strings.Replace(orig, "uid: u1", "uid: u2", 1),
		"uid removed":  strings.Replace(orig, "  uid: u1\n", "", 1),
		"apiVersion":   strings.Replace(orig, "apiVersion: v1", "apiVersion: v2", 1),
		"no metadata":  "apiVersion: v1\nkind: ConfigMap\ndata: {a: \"1\"}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := patchOf(t, cmID, orig, edited); err == nil || !strings.Contains(err.Error(), "another object") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	// The original must be the captured object too (not only equal to the edit).
	other := strings.Replace(orig, "name: cfg", "name: other", 1)
	if _, err := patchOf(t, cmID, other, other); err == nil {
		t.Fatal("an original of another object must be refused")
	}
}

func TestEditPatchIgnoresTheVersionInTheText(t *testing.T) {
	orig := cmHead + "data: {a: \"1\"}\n"
	got, err := patchOf(t, cmID, orig, strings.Replace(orig, `resourceVersion: "7"`, `resourceVersion: "99"`, 1))
	if err != nil || got != `{}` {
		t.Fatalf("got %s, %v", got, err)
	}
}

func TestEditPatchNeverTouchesHiddenPaths(t *testing.T) {
	orig := cmHead + "  annotations: {a: x, b: y}\ndata: {k: v}\n"
	// All shown annotations deleted: only they are, not the hidden last-applied.
	got, err := patchOf(t, cmID, orig, strings.Replace(orig, "  annotations: {a: x, b: y}\n", "", 1))
	if err != nil || got != `{"metadata":{"annotations":{"a":null,"b":null}}}` {
		t.Fatalf("got %s, %v", got, err)
	}
	got, err = patchOf(t, cmID, orig, strings.Replace(orig, "annotations: {a: x, b: y}", "annotations: null", 1))
	if err != nil || got != `{"metadata":{"annotations":{"a":null,"b":null}}}` {
		t.Fatalf("got %s, %v", got, err)
	}
	for name, edited := range map[string]string{
		"status":                orig + "status: {phase: x}\n",
		"managedFields":         strings.Replace(orig, "  uid: u1\n", "  uid: u1\n  managedFields: []\n", 1),
		"last-applied":          strings.Replace(orig, "{a: x, b: y}", "{a: x, b: y, kubectl.kubernetes.io/last-applied-configuration: '{}'}", 1),
		"annotations as a list": strings.Replace(orig, "annotations: {a: x, b: y}", "annotations: []", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := patchOf(t, cmID, orig, edited); err == nil {
				t.Fatal("must be refused")
			}
		})
	}
}

func TestEditPatchOfASecretIsMetadataOnly(t *testing.T) {
	id := editIdentity{APIVersion: "v1", Kind: "Secret", Name: "cfg", Namespace: "web", UID: "u1", Secret: true}
	head := strings.Replace(cmHead, "kind: ConfigMap", "kind: Secret", 1)
	orig := head + "data: {token: <3 bytes>, pin: <4 bytes>}\ntype: Opaque\n"
	got, err := patchOf(t, id, orig, strings.Replace(orig, "  uid: u1\n", "  uid: u1\n  labels: {team: a}\n", 1))
	if err != nil || got != `{"metadata":{"labels":{"team":"a"}}}` {
		t.Fatalf("a label: got %s, %v", got, err)
	}
	for name, edited := range map[string]string{
		"a value":              strings.Replace(orig, "token: <3 bytes>", "token: eHl6", 1),
		"same-length value":    strings.Replace(orig, "pin: <4 bytes>", "pin: <5 bytes>", 1),
		"mask moved":           orig + "stringData: {token: <3 bytes>}\n",
		"new stringData":       orig + "stringData: {x: y}\n",
		"a key removed":        strings.Replace(orig, ", pin: <4 bytes>", "", 1),
		"type":                 strings.Replace(orig, "type: Opaque", "type: other", 1),
		"mask into annotation": strings.Replace(orig, "  uid: u1\n", "  uid: u1\n  annotations: {a: <3 bytes>}\n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := patchOf(t, id, orig, edited); err == nil || !strings.Contains(err.Error(), "Secret") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestEditCollisionsAreChangesSinceOpeningThatThePatchOverwrites(t *testing.T) {
	orig := mustParse(t, cmHead+"data: {a: \"1\", b: \"2\"}\nlist: [app]\n")
	// Since opening: someone added a sidecar and changed b; status-only churn elsewhere.
	current := mustParse(t, cmHead+"data: {a: \"1\", b: \"3\"}\nlist: [app, sidecar]\nextra: z\n")
	patch := map[string]any{"list": []any{"app2"}, "data": map[string]any{"a": "9"}}
	got := collisions(patch, orig, current)
	if strings.Join(got, ",") != "list" {
		t.Fatalf("collisions = %v, want [list]", got)
	}
	patch = map[string]any{"data": map[string]any{"b": "4"}}
	if got := collisions(patch, orig, current); strings.Join(got, ",") != "data.b" {
		t.Fatalf("collisions = %v, want [data.b]", got)
	}
	// Deleting what someone else changed is a collision too; untouched changes are not.
	patch = map[string]any{"data": map[string]any{"b": nil}}
	if got := collisions(patch, orig, current); strings.Join(got, ",") != "data.b" {
		t.Fatalf("collisions = %v", got)
	}
	if got := collisions(map[string]any{"data": map[string]any{"a": "5"}}, orig, current); len(got) != 0 {
		t.Fatalf("no collision expected, got %v", got)
	}
}

func TestEditViewHidesWhatTheEditorDoesNotChange(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "ocular.dev/v1", "kind": "Widget",
		"metadata": map[string]any{
			"name": "w", "namespace": "web", "uid": "u1", "resourceVersion": "7",
			"managedFields": []any{map[string]any{"manager": "kubectl"}},
			"annotations":   map[string]any{lastAppliedKey: "{}", "team": "a"},
		},
		"spec":   map[string]any{"big": int64(9007199254740993)},
		"status": map[string]any{"phase": "Ready"},
	}}
	text, err := editView(u, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, "# ") {
		t.Fatalf("no header comment:\n%s", text)
	}
	for _, hidden := range []string{"managedFields", lastAppliedKey, "status", "phase"} {
		if strings.Contains(text, hidden+":") {
			t.Fatalf("%s shown:\n%s", hidden, text)
		}
	}
	doc := mustParse(t, text)
	if got := toJSON(t, doc["spec"]); got != `{"big":9007199254740993}` {
		t.Fatalf("spec = %s", got)
	}
	if got := toJSON(t, doc["metadata"].(map[string]any)["annotations"]); got != `{"team":"a"}` {
		t.Fatalf("annotations = %s", got)
	}
	if u.Object["status"] == nil {
		t.Fatal("the object itself must not change")
	}

	s := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"name": "s", "namespace": "web", "uid": "u2", "annotations": map[string]any{lastAppliedKey: "{}"}},
		"data":     map[string]any{"token": "eHl6"},
	}}
	text, err = editView(s, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "eHl6") || !strings.Contains(text, "<3 bytes>") || strings.Contains(text, "annotations") {
		t.Fatalf("secret view:\n%s", text)
	}
}

// Many decimals come back in one pass: each marker used to be a separate
// ReplaceAll over the whole text (16k decimals took ~6 s).
func TestEditViewManyDecimalsIsLinear(t *testing.T) {
	const n = 40000
	l := make([]any, n)
	for i := range l {
		l[i] = json.Number(strconv.Itoa(i) + ".5")
	}
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "c", "namespace": "web"},
		"spec":     map[string]any{"l": l, "one": json.Number("1.0"), "ten": json.Number("10.25")},
	}}
	start := time.Now()
	text, err := editView(u, false)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("editView of %d decimals took %v", n, d)
	}
	for _, want := range []string{"- 0.5\n", "- 1.5\n", "- 10.5\n", "- 39999.5\n", "one: 1.0\n", "ten: 10.25\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("%q missing", want)
		}
	}
	if strings.Contains(text, "ocularnum") {
		t.Fatal("a marker is left in the text")
	}
}
