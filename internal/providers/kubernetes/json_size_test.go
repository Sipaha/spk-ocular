package kubernetes

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncodedJSONSizeMatchesMarshal(t *testing.T) {
	strings := []string{"", "plain ASCII", "\"\\\b\f\n\r\t\x00\x1f\x7f", "<script>&<>/", "中文 русский 日本語 😀", "\u2028\u2029", string([]byte{0xff, 0xfe, 0xc0, 0xaf, 0xe2, 0x80})}
	values := []any{nil, true, false, int64(-12345), 1e-9, 1e30, json.Number("-2.5e10"), []any{}, map[string]any{}, []byte{0, 1, 2}, map[string]any(nil), []any(nil)}
	for _, s := range strings {
		values = append(values, s, map[string]any{s: []any{s, nil, true, int64(-2), map[string]any{"value": s}}})
	}
	for _, v := range values {
		want, err := json.Marshal(v)
		require.NoError(t, err)
		got, err := encodedJSONSize(v)
		require.NoError(t, err)
		require.Equal(t, len(want), got)
	}
	for i := range 128 {
		v := realisticPod(i).Object
		want, err := json.Marshal(v)
		require.NoError(t, err)
		got, err := encodedJSONSize(v)
		require.NoError(t, err)
		require.Equal(t, len(want), got)
	}
	_, err := encodedJSONSize(math.NaN())
	require.Error(t, err)
	cyclic := map[string]any{}
	cyclic["cycle"] = cyclic
	_, err = encodedJSONSize(cyclic)
	require.Error(t, err)
}
func FuzzEncodedJSONSize(f *testing.F) {
	for _, seed := range []string{`null`, `{"a":[1,true,"<>&\u2028"]}`, `"中文 😀"`, `[]`, `{"empty":{}}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		var v any
		if json.Unmarshal(b, &v) != nil {
			return
		}
		want, err := json.Marshal(v)
		if err != nil {
			return
		}
		got, err := encodedJSONSize(v)
		require.NoError(t, err)
		require.Equal(t, len(want), got)
	})
}
