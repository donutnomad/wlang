package wflang_test

import (
	"testing"

	"github.com/wflang/wflang/wflang"
)

// §12.2: Formatter 必须产生稳定 key 顺序。
func TestFormatJSON_StableKeyOrder(t *testing.T) {
	src := []byte(`{"b":1,"a":2,"c":3}`)
	out, err := wflang.FormatJSON(src)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	got := string(out)
	want := "{\n  \"a\": 2,\n  \"b\": 1,\n  \"c\": 3\n}"
	if got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}
