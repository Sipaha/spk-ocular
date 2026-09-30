package kubernetes

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustParse(t *testing.T, text string) map[string]any {
	t.Helper()
	v, err := parseEditDoc(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return v
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEditParseRefusesWhatIsNotOneObject(t *testing.T) {
	for name, tc := range map[string]struct{ text, want string }{
		"two documents":    {"a: 1\n---\nb: 2\n", "one document"},
		"empty":            {"# only a comment\n", "empty"},
		"list root":        {"- a\n- b\n", "mapping"},
		"scalar root":      {"hello\n", "mapping"},
		"duplicate key":    {"metadata:\n  name: a\n  name: b\n", "duplicate key \"name\""},
		"anchor":           {"a: &x 1\nb: *x\n", "anchors"},
		"merge key":        {"base: {x: 1}\nc:\n  <<: {x: 2}\n", "anchors"},
		"non-string key":   {"1: a\n", "key"},
		"int out of range": {"n: 9223372036854775808\n", "out of range"},
		"infinity":         {"n: .inf\n", "not a finite number"},
		"binary tag":       {"b: !!binary aGk=\n", "tag"},
		"syntax":           {"a: [1, 2\n", "line"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseEditDoc(tc.text)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to say %q", err, tc.want)
			}
		})
	}
}

func TestEditParseKeepsNumbersExactly(t *testing.T) {
	v := mustParse(t, "a: 9007199254740993\nb: -9223372036854775808\nc: 9223372036854775807\nd: 1.5\ne: 0x10\nf: 007\n")
	got := toJSON(t, v)
	want := `{"a":9007199254740993,"b":-9223372036854775808,"c":9223372036854775807,"d":1.5,"e":16,"f":7}`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestEditParseScalarsAreYAML12(t *testing.T) {
	// YAML 1.2: yes/on are strings; a timestamp stays its text; quoted stays a string.
	got := toJSON(t, mustParse(t, "a: yes\nb: on\nc: true\nd: null\ne: 2024-01-02T03:04:05Z\nf: \"1\"\ng: ~\nh:\n"))
	want := `{"a":"yes","b":"on","c":true,"d":null,"e":"2024-01-02T03:04:05Z","f":"1","g":null,"h":null}`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestEditParseDepthIsBounded(t *testing.T) {
	text := strings.Repeat("[", 150) + strings.Repeat("]", 150)
	if _, err := parseEditDoc("a: " + text + "\n"); err == nil || !strings.Contains(err.Error(), "deep") {
		t.Fatalf("err = %v", err)
	}
}

func TestMergePatchIsRFC7386AndExact(t *testing.T) {
	orig := mustParse(t, "a: 1\nb: {x: 1, y: 2}\nl: [1, 2]\nn: 9007199254740992\nkeep: same\n")
	edited := mustParse(t, "a: 1\nb: {x: 1, z: 3}\nl: [1, 2, 3]\nn: 9007199254740993\nkeep: same\nnew: {q: null}\n")
	got := toJSON(t, mergePatch(orig, edited))
	want := `{"b":{"y":null,"z":3},"l":[1,2,3],"n":9007199254740993,"new":{"q":null}}`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if p := mergePatch(orig, orig); len(p) != 0 {
		t.Fatalf("no change must be an empty patch, got %v", p)
	}
	// A key removed whole is null; a map replaced by a scalar is the scalar.
	got = toJSON(t, mergePatch(mustParse(t, "a: {x: 1}\nb: 2\n"), mustParse(t, "a: 5\n")))
	if got != `{"a":5,"b":null}` {
		t.Fatalf("got %s", got)
	}
}

func TestEditParseKeepsDecimalLiteralsExactly(t *testing.T) {
	// Through float64 9007199254740993.0 would be …992: the literal is sent
	// as written, in JSON's form.
	// (An untagged 1e400 is a string in YAML: yaml.v3 resolves it so.)
	v := mustParse(t, "a: 9007199254740993.0\nb: +1.5\nc: .5\nd: -.5e3\ne: 1.\nf: 007.25\ng: 1E+300\nh: 1.e5\ni: !!float 3\n")
	got := toJSON(t, v)
	want := `{"a":9007199254740993.0,"b":1.5,"c":0.5,"d":-0.5e3,"e":1,"f":7.25,"g":1E+300,"h":1e5,"i":3}`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	for _, text := range []string{"n: -.INF\n", "n: .NaN\n", "n: !!float 0x10\n", "n: !!float abc\n", "n: !!float 1e400\n", "n: !!float -2e308\n"} {
		if _, err := parseEditDoc(text); err == nil {
			t.Fatalf("%q: accepted", text)
		}
	}
}
