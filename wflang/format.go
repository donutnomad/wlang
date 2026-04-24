package wflang

import (
	"bytes"
	"encoding/json"
	"sort"

	werr "github.com/wflang/wflang/errors"
)

// FormatJSON pretty-prints a wflang JSON document with 2-space indent and
// stable key ordering (LANGUAGE.md §12.2). Objects are recursively sorted by
// key; arrays keep their original order. The output is always valid JSON.
func FormatJSON(src []byte) ([]byte, error) {
	var tree any
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	if err := dec.Decode(&tree); err != nil {
		return nil, werr.Newf(werr.CodeJSONDecode, "format: %v", err)
	}
	var buf bytes.Buffer
	if err := writeSorted(&buf, tree, 0); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeSorted(buf *bytes.Buffer, v any, indent int) error {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			buf.WriteString("{}")
			return nil
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
			writeIndent(buf, indent+1)
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteString(": ")
			if err := writeSorted(buf, x[k], indent+1); err != nil {
				return err
			}
		}
		buf.WriteByte('\n')
		writeIndent(buf, indent)
		buf.WriteByte('}')
	case []any:
		if len(x) == 0 {
			buf.WriteString("[]")
			return nil
		}
		buf.WriteByte('[')
		for i, el := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
			writeIndent(buf, indent+1)
			if err := writeSorted(buf, el, indent+1); err != nil {
				return err
			}
		}
		buf.WriteByte('\n')
		writeIndent(buf, indent)
		buf.WriteByte(']')
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return werr.Newf(werr.CodeJSONDecode, "format: %v", err)
		}
		buf.Write(b)
	}
	return nil
}

func writeIndent(buf *bytes.Buffer, n int) {
	for range n {
		buf.WriteString("  ")
	}
}
