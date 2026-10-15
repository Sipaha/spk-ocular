package kubernetes

import (
	"encoding/json"
	"errors"
	"strconv"
	"unicode/utf8"
)

// encodedJSONSize counts the same bytes as json.Marshal without sorting map
// keys, allocating the encoded object, or retaining its contents. Unstructured
// API objects contain maps/slices/primitives; unusual typed values use Marshal.
func encodedJSONSize(v any) (int, error) { return jsonValueSize(v, 0) }
func jsonValueSize(v any, depth int) (int, error) {
	if depth > 10000 {
		return 0, errors.New("object exceeds the JSON nesting limit")
	}
	switch x := v.(type) {
	case nil:
		return 4, nil
	case bool:
		if x {
			return 4, nil
		}
		return 5, nil
	case string:
		return jsonStringSize(x), nil
	case int64:
		var buffer [32]byte
		return len(strconv.AppendInt(buffer[:0], x, 10)), nil
	case int:
		var buffer [32]byte
		return len(strconv.AppendInt(buffer[:0], int64(x), 10)), nil
	case map[string]any:
		if x == nil {
			return 4, nil
		}
		n := 2
		for k, value := range x {
			size, err := jsonValueSize(value, depth+1)
			if err != nil {
				return 0, err
			}
			n += jsonStringSize(k) + 1 + size + 1
		}
		if len(x) > 0 {
			n--
		}
		return n, nil
	case []any:
		if x == nil {
			return 4, nil
		}
		n := 2
		for _, value := range x {
			size, err := jsonValueSize(value, depth+1)
			if err != nil {
				return 0, err
			}
			n += size + 1
		}
		if len(x) > 0 {
			n--
		}
		return n, nil
	default:
		b, err := json.Marshal(v)
		return len(b), err
	}
}
func jsonStringSize(s string) int {
	n := 2
	for i := 0; i < len(s); {
		b := s[i]
		if b < utf8.RuneSelf {
			switch b {
			case '\\', '"', '\b', '\f', '\n', '\r', '\t':
				n += 2
			case '<', '>', '&':
				n += 6
			default:
				if b < 0x20 {
					n += 6
				} else {
					n++
				}
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 || r == '\u2028' || r == '\u2029' {
			n += 6
		} else {
			n += size
		}
		i += size
	}
	return n
}
