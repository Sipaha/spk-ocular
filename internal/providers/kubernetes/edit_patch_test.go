package kubernetes

import (
	"strings"
	"testing"
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
