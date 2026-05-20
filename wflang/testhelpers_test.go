package wflang_test

import (
	"strings"
	"testing"

	"github.com/wflang/wflang/types"
)

// unwrap1 returns the business (non-error) value of a host-call result.
//
// Under the Go-style error model (LANGUAGE.md §9.1.1), a host call with
// signature `(T, error)` returns tuple<T,error>; `(T)` without error returns T
// directly. Tests that previously expected a single value can wrap their
// program result with unwrap1 to get the first element regardless of which
// shape applied. If the call carries a non-nil error, unwrap1 fails the test.
func unwrap1(t *testing.T, v types.Value) any {
	t.Helper()
	name := v.TypeName()
	if !strings.HasPrefix(name, "tuple<") {
		return v.Go()
	}
	parts, ok := v.Go().([]any)
	if !ok {
		t.Fatalf("unwrap1: expected []any tuple carrier, got %T", v.Go())
	}
	if len(parts) == 0 {
		t.Fatalf("unwrap1: empty tuple")
	}
	if strings.HasSuffix(name, ",error>") || name == "tuple<error>" {
		if parts[len(parts)-1] != nil {
			t.Fatalf("unwrap1: host call returned error: %v", parts[len(parts)-1])
		}
	}
	return parts[0]
}

// unwrapErr returns the error component (or nil) of a host-call tuple result.
func unwrapErr(t *testing.T, v types.Value) any {
	t.Helper()
	name := v.TypeName()
	if !strings.HasPrefix(name, "tuple<") {
		return nil
	}
	parts, _ := v.Go().([]any)
	if len(parts) == 0 {
		return nil
	}
	if strings.HasSuffix(name, ",error>") || name == "tuple<error>" {
		return parts[len(parts)-1]
	}
	return nil
}
