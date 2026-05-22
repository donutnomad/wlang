package go2wlang_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/donutnomad/wlang/go2wlang"
)

func TestTranslateFileStaticSymbolsMethodValuesOutAndZero(t *testing.T) {
	src := []byte(`package orders

import "example.com/app/activities"

type ReserveResult struct {
	ID string
}

type Context struct{}

type Worker struct{}

func (Worker) Compensate(ctx Context, input string) error { return nil }

type Future struct{}

func Helper(input string) string { return input }

func ExecuteActivity(ctx Context, activity any, input string) Future { return Future{} }

func (Future) Get(ctx Context, out any) error { return nil }

func Rule(ctx Context, worker Worker, input string) error {
	var reserve ReserveResult
	activity := activities.Reserve
	method := worker.Compensate
	local := Helper
	err := ExecuteActivity(ctx, activity, input).Get(ctx, &reserve)
	if err != nil {
		return err
	}
	_ = method
	_ = local
	return nil
}
`)
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	got := string(out)
	for _, needle := range []string{
		`"zero": "orders.ReserveResult"`,
		`"symbol": "activities.Reserve"`,
		`"method": [`,
		`"var": "worker"`,
		`"Compensate"`,
		`"symbol": "orders.Helper"`,
		`"symbol": "orders.ExecuteActivity"`,
		`"out": "reserve"`,
		`"Get": [`,
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestTranslateFileLocalFunctionVariableShadowsStaticSymbol(t *testing.T) {
	src := []byte(`package rules

func Helper(input string) string { return input }

func Rule(input string) string {
	Helper := func(s string) string { return s }
	return Helper(input)
}
`)
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	got := string(out)
	if strings.Contains(got, `"symbol": "rules.Helper"`) {
		t.Fatalf("local function variable shadow should stay var:\n%s", got)
	}
	if !strings.Contains(got, `"var": "Helper"`) {
		t.Fatalf("missing local function var:\n%s", got)
	}
}

func TestTranslateFileRejectsComplexOutArguments(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "index",
			src: `package rules
func Rule(get func(*string) error, xs []string) error {
	return get(&xs[0])
}
`,
			want: "address-of index is not supported",
		},
		{
			name: "complex",
			src: `package rules
func Rule(get func(*string) error, x string) error {
	return get(&([]string{x}[0]))
}
`,
			want: "address-of expression is not supported",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := go2wlang.TranslateFile([]byte(tc.src), go2wlang.Options{FuncName: "Rule"})
			if err == nil {
				t.Fatalf("want diagnostic containing %q", tc.want)
			}
			var diag *go2wlang.DiagnosticError
			if !errors.As(err, &diag) {
				t.Fatalf("err = %T %[1]v, want DiagnosticError", err)
			}
			if !strings.Contains(diag.Reason, tc.want) {
				t.Fatalf("want %q, got %q", tc.want, diag.Reason)
			}
		})
	}
}
