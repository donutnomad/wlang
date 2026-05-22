package go2wlang_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donutnomad/wlang/go2wlang"
	"github.com/donutnomad/wlang/wflang"
)

func TestTranslateFileRuleFunction(t *testing.T) {
	src := []byte(`package rules

import "example.com/demo"

func Rule(user demo.User, scores []int64) demo.Report {
	total := int64(0)
	for i, score := range scores {
		if score > 0 {
			total = total + score
		} else {
			continue
		}
		_ = i
	}
	risk, err := demo.Score(user, total)
	status := "normal"
	if risk >= 10 {
		status = "high"
	}
	return demo.Report{Risk: risk, Error: err, Status: status}
}
`)
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	if _, err := eng.CompileJSON(out); err != nil {
		t.Fatalf("generated JSON should parse and typecheck: %v\n%s", err, out)
	}
	pseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("pseudo: %v", err)
	}
	got := string(pseudo)
	for _, needle := range []string{
		"import demo",
		"let total = 0",
		"foreach score, i in scores {",
		"if score > 0 {",
		"let risk, err = demo.Score(user, total)",
		"return struct demo.Report {",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestTranslateFileConcurrencySubset(t *testing.T) {
	src := []byte(`package rules

import "example.com/demo"

func Concurrent(input string) string {
	ch := make(chan string, 1)
	defer demo.Close(input)
	go demo.Publish(input)
	go func() {
		ch <- input
	}()
	select {
	case msg, ok := <-ch:
		if ok {
			return msg
		}
	default:
		return "idle"
	}
	return "done"
}
`)
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Concurrent"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	pseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("pseudo: %v", err)
	}
	got := string(pseudo)
	for _, needle := range []string{
		"let ch = chan<string>(1)",
		"defer demo.Close(input)",
		"routine demo.Publish(input)",
		"routine {",
		"ch.send(ch, input)",
		"select {",
		"recv ch -> msg, ok {",
		"default {",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestTranslateFileCurrentPackageStructLiteralArgument(t *testing.T) {
	src := []byte(`package rules

import "example.com/api"

type Args struct{ Name string }

func Rule(booker api.Booker, input string) api.Result {
	return booker.Book(Args{Name: input})
}
`)
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	pseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("pseudo: %v", err)
	}
	got := string(pseudo)
	for _, needle := range []string{
		"return booker.Book(struct rules.Args {",
		"Name: input",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestTranslateFileCurrentPackageStructLiteralUsesLocalPackageName(t *testing.T) {
	src := []byte(`package rules

import "example.com/api"

type Args struct{ Name string }

func Rule(booker api.Booker, input string) api.Result {
	return booker.Book(Args{Name: input})
}
`)
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Rule", LocalPackageName: "approval"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	pseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("pseudo: %v", err)
	}
	got := string(pseudo)
	if !strings.Contains(got, "return booker.Book(struct approval.Args {") {
		t.Fatalf("missing local package struct literal in:\n%s", got)
	}
}

func TestTranslateFileClosureCompensationSubset(t *testing.T) {
	src := []byte(`package orders

import (
	temporal "example.com/temporal"
	workflow "example.com/workflow"
)

type FailureReason struct {
	FailedStep string
	Message    string
	Type       string
}

type OrderInput struct {
	OrderID string
}

type ReserveResult struct {
	ID string
}

type MarkFailedInput struct {
	OrderID    string
	ReserveID  string
	FailedBy   string
	Reason     string
	ReasonType string
}

func OrderWorkflow(ctx workflow.Context, runner workflow.Runner, input OrderInput) (err error) {
	var compensations []func(workflow.Context, FailureReason) error
	failedStep := ""
	reserve := ReserveResult{ID: ""}

	defer func() {
		if err == nil {
			return
		}

		reason := BuildFailureReason(failedStep, err)

		for i := len(compensations) - 1; i >= 0; i-- {
			compErr := compensations[i](ctx, reason)
			if compErr != nil {
				err = temporal.NewApplicationError(
					"workflow failed and compensation failed",
					"CompensationFailed",
					compErr,
				)
				return
			}
		}
	}()

	failedStep = "step1_reserve"
	reserve = workflow.Reserve(ctx, input.OrderID)
	compensations = append(compensations, func(ctx workflow.Context, reason FailureReason) error {
		return workflow.MarkReserveFailed(ctx, MarkFailedInput{
			OrderID:    input.OrderID,
			ReserveID:  reserve.ID,
			FailedBy:   reason.FailedStep,
			Reason:     reason.Message,
			ReasonType: reason.Type,
		})
	})

	failedStep = "step10_pay"
	err = runner.Pay(ctx, input.OrderID)
	if err != nil {
		return err
	}

	return nil
}
`)
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "OrderWorkflow"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	if _, err := eng.CompileJSON(out); err != nil {
		t.Fatalf("generated JSON should parse and typecheck: %v\n%s", err, out)
	}
	got := string(out)
	for _, needle := range []string{
		`"named": "err"`,
		`"fn": {`,
		`"call": {`,
		`"arr.push":`,
		`"arr.get":`,
		`"arr.len":`,
		`"NewApplicationError":`,
		`"Pay":`,
		`"MarkReserveFailed":`,
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestTranslateFileMapBuiltinsUseMapNamespace(t *testing.T) {
	src := []byte(`package rules

func Rule(labels map[string]int64) int64 {
	val, ok := labels["primary"]
	if ok {
		labels["copy"] = val
	} else {
		delete(labels, "primary")
	}
	return labels["copy"]
}
`)
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	got := string(out)
	for _, needle := range []string{
		`"map.get"`,
		`"map.set"`,
		`"map.del"`,
		`"map.value"`,
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
	for _, legacy := range []string{`"m.get"`, `"m.set"`, `"m.del"`, `"m.value"`} {
		if strings.Contains(got, legacy) {
			t.Fatalf("found legacy %q in:\n%s", legacy, got)
		}
	}
}

func TestTranslateFileFunctionValueParameterCall(t *testing.T) {
	src := []byte(`package rules

func Rule(fn func(string) string, input string) string {
	return fn(input)
}
`)
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	got := string(out)
	for _, needle := range []string{
		`"call":`,
		`"var": "fn"`,
		`"var": "input"`,
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestTranslateFileAppendMultipleValues(t *testing.T) {
	src := []byte(`package rules

func Rule() []int64 {
	var xs []int64
	xs = append(xs, 1, 2)
	return xs
}
`)
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	got := string(out)
	if strings.Count(got, `"arr.push"`) != 2 {
		t.Fatalf("want two arr.push calls in:\n%s", got)
	}
}

func TestTranslateFileRejectsUnsupportedClosureAndAppendShapes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "function literal parameter must be named",
			src: `package rules
func Rule() {
	f := func(string) error { return nil }
	_ = f
}
			`,
			want: "function literal parameters must be named",
		},
		{
			name: "function literal parameter type unsupported",
			src: `package rules
func Rule() {
	f := func(ch chan string) { _ = ch }
	_ = f
}
`,
			want: "function parameter type is not supported",
		},
		{
			name: "function literal result type unsupported",
			src: `package rules
func Rule() {
	f := func() chan string { return nil }
	_ = f
}
`,
			want: "function result type is not supported",
		},
		{
			name: "append target mismatch",
			src: `package rules
func Rule(xs []int64, ys []int64) {
	xs = append(ys, 1)
}
`,
			want: "append target must match assignment target",
		},
		{
			name: "append target must be identifier",
			src: `package rules
func Rule(xs []int64) {
	xs = append(xs[:], 1)
}
`,
			want: "append target must match assignment target",
		},
		{
			name: "append requires value",
			src: `package rules
func Rule(xs []int64) {
	xs = append(xs)
}
`,
			want: "append requires a slice and at least one value",
		},
		{
			name: "append item expression unsupported",
			src: `package rules
func Rule(xs []int64) {
	xs = append(xs, make([]int64, 1)[0])
}
`,
			want: "make supports only channel types",
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

func TestTranslateFileFunctionLiteralNamedResults(t *testing.T) {
	src := []byte(`package rules

func Rule() {
	f := func() (err error) { return nil }
	_ = f
}
`)
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `"returns":`) || !strings.Contains(got, `"error"`) {
		t.Fatalf("missing named error return in:\n%s", got)
	}
}

func TestTranslateFileUnsupportedReportsDiagnostic(t *testing.T) {
	src := []byte(`package rules

func Rule() int64 {
	goto done
done:
	return 1
}
`)
	_, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Rule"})
	if err == nil {
		t.Fatal("expected unsupported diagnostic")
	}
	var diag *go2wlang.DiagnosticError
	if !errors.As(err, &diag) {
		t.Fatalf("err = %T %[1]v, want DiagnosticError", err)
	}
	if diag.Line == 0 || diag.Column == 0 {
		t.Fatalf("missing position in diagnostic: %+v", diag)
	}
	if !strings.Contains(diag.Node, "BranchStmt") {
		t.Fatalf("node = %q, want BranchStmt", diag.Node)
	}
}

func TestTranslateFileMissingFunction(t *testing.T) {
	src := []byte(`package rules

func Other() int64 { return 1 }
`)
	_, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Rule"})
	if err == nil {
		t.Fatal("expected missing function error")
	}
	if !strings.Contains(err.Error(), "function Rule not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestExampleApprovalRuleTranslates(t *testing.T) {
	path := filepath.Join("examples", "approval_rule.go")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "ApprovalRule"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	pseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("pseudo: %v", err)
	}
	got := string(pseudo)
	for _, needle := range []string{
		"import audit",
		"import notify",
		"import policy",
		"defer audit.Close(\"approval-rule\")",
		"let normalized = policy.Normalize(user)",
		"let risk, riskErr = scorer.Score(normalized, total)",
		"let saveErr = store.Save(decision)",
		"routine notify.Publish(status)",
		"let events = chan<string>(1)",
		"select {",
		"recv events -> msg, ok {",
		"return decision",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestExampleOrderWorkflowTranslates(t *testing.T) {
	path := filepath.Join("examples", "order_workflow.go")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "OrderWorkflow"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	got := string(out)
	for _, needle := range []string{
		`"named": "err"`,
		`"arr.push"`,
		`"arr.get"`,
		`"arr.len"`,
		`"fn"`,
		`"call"`,
		`"zero"`,
		`"out"`,
		`"symbol": "workflow.Reserve"`,
		`"Get"`,
		`"NewApplicationError"`,
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}

func TestExampleFeatureShowcaseTranslates(t *testing.T) {
	path := filepath.Join("examples", "feature_showcase.go")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	out, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "FeatureShowcase"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}
	pseudo, err := wflang.FormatPseudoCode(out)
	if err != nil {
		t.Fatalf("pseudo: %v", err)
	}
	got := string(pseudo)
	for _, needle := range []string{
		"if n > 0 {",
		"let pair = struct examples.ShowcaseItem {",
		"pair.Name = call symbol examples.Identity(pair.Name)",
		"scores[0] = n",
		"let val, ok = labels[\"primary\"]",
		"labels[\"copy\"] = val",
		"delete(labels, \"primary\")",
		"let part = scores[0:1]",
		"let copied = copy(scores, keyed)",
		"let s, typeOK = type.assert.ok(input, \"string\")",
		"if type.is(input, \"string\") {",
		"let x = type.assert(input, \"string\")",
		"let p = ptr.new(\"int64\")",
		"let deref = ptr.deref(value)",
		"let mask = bit.not(val)",
		"let z = complex(1, 2)",
		"let realPart = real(z)",
		"let imagPart = imag(z)",
		"routine {",
		"let msg = \"ready\"",
		"select {",
	} {
		if !strings.Contains(got, needle) {
			t.Fatalf("missing %q in:\n%s", needle, got)
		}
	}
}
