package go2wlang_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/donutnomad/wlang/go2wlang"
	"github.com/donutnomad/wlang/registry"
	"github.com/donutnomad/wlang/types"
	"github.com/donutnomad/wlang/wflang"
)

type eqUser struct {
	Name   string
	Age    int64
	Active bool
}

type eqDecision struct {
	User   eqUser
	Risk   int64
	Status string
	Error  error
	Labels map[string]string
}

type eqScorer struct {
	calls []string
}

func (s *eqScorer) Score(user eqUser, total int64) (int64, error) {
	s.calls = append(s.calls, user.Name)
	risk := total + user.Age
	if user.Active {
		risk += 10
	}
	return risk, nil
}

type eqStore struct {
	saved []eqDecision
}

func (s *eqStore) Save(decision eqDecision) error {
	s.saved = append(s.saved, decision)
	return nil
}

func eqNormalize(user eqUser) eqUser {
	if user.Age < 18 {
		user.Active = false
	}
	return user
}

type eqBookArgs struct {
	Name string
	Risk int64
}

type eqBookResult struct {
	Name    string
	Message string
}

type eqBooker struct {
	booked []eqBookArgs
}

func (b *eqBooker) Book(args eqBookArgs) eqBookResult {
	b.booked = append(b.booked, args)
	return eqBookResult{Name: args.Name, Message: "risk"}
}

func eqPrepareName(user eqUser) string {
	return user.Name + "-prepared"
}

func eqBookRule(user eqUser, booker *eqBooker) eqBookResult {
	name := eqPrepareName(user)
	return booker.Book(eqBookArgs{Name: name, Risk: user.Age})
}

type eqWorkflowContext struct {
	Log      *[]string
	FailComp bool
}

type eqFailureReason struct {
	FailedStep string
	Message    string
	Type       string
}

type eqOrderInput struct {
	OrderID string
}

type eqReserveResult struct {
	ID string
}

type eqMarkFailedInput struct {
	OrderID    string
	ReserveID  string
	FailedBy   string
	Reason     string
	ReasonType string
}

type eqWorkflowRunner struct {
	failPay bool
}

func (r *eqWorkflowRunner) Pay(ctx eqWorkflowContext, orderID string) error {
	*ctx.Log = append(*ctx.Log, "pay:"+orderID)
	if r.failPay {
		return eqAppError{msg: "pay failed", typ: "PayFailed"}
	}
	return nil
}

type eqAppError struct {
	msg string
	typ string
}

func (e eqAppError) Error() string {
	return e.typ + ":" + e.msg
}

func eqBuildFailureReason(step string, err error) eqFailureReason {
	return eqFailureReason{
		FailedStep: step,
		Message:    err.Error(),
		Type:       "application",
	}
}

func eqBuildFailureReasonValue(step string, err error) types.Value {
	return types.NewValue("eqFailureReason", eqBuildFailureReason(step, err))
}

func eqReserve(ctx eqWorkflowContext, orderID string) eqReserveResult {
	*ctx.Log = append(*ctx.Log, "reserve:"+orderID)
	return eqReserveResult{ID: "reserve-" + orderID}
}

func eqMarkReserveFailed(ctx eqWorkflowContext, input eqMarkFailedInput) error {
	*ctx.Log = append(*ctx.Log, "compensate:"+input.OrderID+":"+input.ReserveID+":"+input.FailedBy+":"+input.ReasonType)
	if ctx.FailComp {
		return eqAppError{msg: "compensation failed", typ: "CompensationFailed"}
	}
	return nil
}

func eqNewApplicationError(message, typ string, cause error) error {
	return eqAppError{msg: message + ":" + cause.Error(), typ: typ}
}

func eqOrderWorkflow(ctx eqWorkflowContext, runner *eqWorkflowRunner, input eqOrderInput) (err error) {
	var compensations []func(eqWorkflowContext, eqFailureReason) error
	failedStep := ""
	reserve := eqReserveResult{ID: ""}

	defer func() {
		if err == nil {
			return
		}
		reason := eqBuildFailureReason(failedStep, err)
		for i := len(compensations) - 1; i >= 0; i-- {
			compErr := compensations[i](ctx, reason)
			if compErr != nil {
				err = eqNewApplicationError(
					"workflow failed and compensation failed",
					"CompensationFailed",
					compErr,
				)
				return
			}
		}
	}()

	failedStep = "step1_reserve"
	reserve = eqReserve(ctx, input.OrderID)
	compensations = append(compensations, func(ctx eqWorkflowContext, reason eqFailureReason) error {
		return eqMarkReserveFailed(ctx, eqMarkFailedInput{
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

func eqRule(user eqUser, scores []int64, scorer *eqScorer, store *eqStore) eqDecision {
	normalized := eqNormalize(user)
	total := int64(0)
	for _, score := range scores {
		if score > 0 {
			total = total + score
		} else {
			continue
		}
	}
	risk, riskErr := scorer.Score(normalized, total)
	status := "approved"
	if risk >= 80 {
		status = "review"
	}
	labels := map[string]string{
		"source": "equivalence",
		"status": status,
	}
	decision := eqDecision{
		User:   normalized,
		Risk:   risk,
		Status: status,
		Error:  riskErr,
		Labels: labels,
	}
	saveErr := store.Save(decision)
	decision = eqDecision{
		User:   normalized,
		Risk:   risk,
		Status: status,
		Error:  saveErr,
		Labels: labels,
	}
	return decision
}

func TestTranslatedJSONMatchesOriginalGoBehavior(t *testing.T) {
	src := []byte(`package rules

import policy "example.com/policy"

func Rule(user eqUser, scores []int64, scorer *eqScorer, store *eqStore) policy.eqDecision {
	normalized := policy.eqNormalize(user)
	total := int64(0)
	for _, score := range scores {
		if score > 0 {
			total = total + score
		} else {
			continue
		}
	}
	risk, riskErr := scorer.Score(normalized, total)
	status := "approved"
	if risk >= 80 {
		status = "review"
	}
	labels := map[string]string{
		"source": "equivalence",
		"status": status,
	}
	decision := policy.eqDecision{
		User:   normalized,
		Risk:   risk,
		Status: status,
		Error:  riskErr,
		Labels: labels,
	}
	saveErr := store.Save(decision)
	decision = policy.eqDecision{
		User:   normalized,
		Risk:   risk,
		Status: status,
		Error:  saveErr,
		Labels: labels,
	}
	return decision
}
`)
	user := eqUser{Name: "alice", Age: 42, Active: true}
	scores := []int64{30, -2, 5}
	scoreValue := mustInt64Array(t, scores)

	goScorer := &eqScorer{}
	goStore := &eqStore{}
	want := eqRule(user, scores, goScorer, goStore)

	jsonProgram, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "Rule"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("policy", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "eqNormalize", Impl: eqNormalize, Pure: true},
		},
	}); err != nil {
		t.Fatalf("BindGoPackage: %v", err)
	}
	if err := reg.BindType("policy.eqDecision", reflect.TypeFor[eqDecision](), wflang.BindOptions{}); err != nil {
		t.Fatalf("BindType decision: %v", err)
	}
	if err := reg.AutoBindType((*eqScorer)(nil)); err != nil {
		t.Fatalf("AutoBindType scorer: %v", err)
	}
	if err := reg.AutoBindType((*eqStore)(nil)); err != nil {
		t.Fatalf("AutoBindType store: %v", err)
	}

	runtimeScorer := &eqScorer{}
	runtimeStore := &eqStore{}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON(jsonProgram)
	if err != nil {
		t.Fatalf("CompileJSON: %v\n%s", err, jsonProgram)
	}
	gotV, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{
			"user":   user,
			"scores": scoreValue,
			"scorer": runtimeScorer,
			"store":  runtimeStore,
		},
	})
	if err != nil {
		t.Fatalf("Run generated JSON: %v\n%s", err, jsonProgram)
	}
	got, ok := gotV.Go().(eqDecision)
	if !ok {
		t.Fatalf("result carrier = %T, want eqDecision", gotV.Go())
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("result mismatch\nwant: %#v\ngot:  %#v", want, got)
	}
	if !reflect.DeepEqual(runtimeScorer.calls, goScorer.calls) {
		t.Fatalf("scorer side effects mismatch\nwant: %#v\ngot:  %#v", goScorer.calls, runtimeScorer.calls)
	}
	if !reflect.DeepEqual(runtimeStore.saved, goStore.saved) {
		t.Fatalf("store side effects mismatch\nwant: %#v\ngot:  %#v", goStore.saved, runtimeStore.saved)
	}
}

func TestTranslatedJSONMatchesStructLiteralArgumentBehavior(t *testing.T) {
	src := []byte(`package rules

import policy "example.com/policy"

type eqBookArgs struct {
	Name string
	Risk int64
}

func Rule(user eqUser, booker *eqBooker) policy.eqBookResult {
	name := policy.eqPrepareName(user)
	return booker.Book(eqBookArgs{Name: name, Risk: user.Age})
}
`)
	user := eqUser{Name: "alice", Age: 42, Active: true}
	goBooker := &eqBooker{}
	want := eqBookRule(user, goBooker)

	jsonProgram, err := go2wlang.TranslateFile(src, go2wlang.Options{
		FuncName:         "Rule",
		LocalPackageName: "policy",
	})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("policy", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "eqPrepareName", Impl: eqPrepareName, Pure: true},
		},
	}); err != nil {
		t.Fatalf("BindGoPackage: %v", err)
	}
	if err := reg.BindType("policy.eqBookArgs", reflect.TypeFor[eqBookArgs](), wflang.BindOptions{}); err != nil {
		t.Fatalf("BindType args: %v", err)
	}
	if err := reg.BindType("policy.eqBookResult", reflect.TypeFor[eqBookResult](), wflang.BindOptions{}); err != nil {
		t.Fatalf("BindType result: %v", err)
	}
	if err := reg.AutoBindType((*eqBooker)(nil)); err != nil {
		t.Fatalf("AutoBindType booker: %v", err)
	}

	runtimeBooker := &eqBooker{}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON(jsonProgram)
	if err != nil {
		t.Fatalf("CompileJSON: %v\n%s", err, jsonProgram)
	}
	gotV, err := prog.Run(context.Background(), wflang.RunOptions{
		Vars: map[string]any{
			"user":   user,
			"booker": runtimeBooker,
		},
	})
	if err != nil {
		t.Fatalf("Run generated JSON: %v\n%s", err, jsonProgram)
	}
	got, ok := gotV.Go().(eqBookResult)
	if !ok {
		t.Fatalf("result carrier = %T, want eqBookResult", gotV.Go())
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("result mismatch\nwant: %#v\ngot:  %#v", want, got)
	}
	if !reflect.DeepEqual(runtimeBooker.booked, goBooker.booked) {
		t.Fatalf("booker side effects mismatch\nwant: %#v\ngot:  %#v", goBooker.booked, runtimeBooker.booked)
	}
}

func TestTranslatedJSONMatchesClosureCompensationBehavior(t *testing.T) {
	src := []byte(`package orders

import (
	temporal "example.com/temporal"
	workflow "example.com/workflow"
)

type eqFailureReason struct {
	FailedStep string
	Message    string
	Type       string
}

type eqOrderInput struct {
	OrderID string
}

type eqReserveResult struct {
	ID string
}

type eqMarkFailedInput struct {
	OrderID    string
	ReserveID  string
	FailedBy   string
	Reason     string
	ReasonType string
}

func OrderWorkflow(ctx workflow.Context, runner *eqWorkflowRunner, input eqOrderInput) (err error) {
	var compensations []func(workflow.Context, eqFailureReason) error
	failedStep := ""
	reserve := eqReserveResult{ID: ""}

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
	compensations = append(compensations, func(ctx workflow.Context, reason eqFailureReason) error {
		return workflow.MarkReserveFailed(ctx, eqMarkFailedInput{
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
	jsonProgram, err := go2wlang.TranslateFile(src, go2wlang.Options{FuncName: "OrderWorkflow"})
	if err != nil {
		t.Fatalf("TranslateFile: %v", err)
	}

	reg := wflang.DefaultRegistry()
	if err := reg.BindGoPackage("orders", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "BuildFailureReason", Impl: eqBuildFailureReasonValue},
		},
	}); err != nil {
		t.Fatalf("BindGoPackage orders: %v", err)
	}
	if err := reg.BindGoPackage("workflow", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "Reserve", Impl: eqReserve},
			{GoName: "MarkReserveFailed", Impl: eqMarkReserveFailed},
		},
	}); err != nil {
		t.Fatalf("BindGoPackage workflow: %v", err)
	}
	if err := reg.BindGoPackage("temporal", registry.PackageSpec{
		Functions: []registry.FuncSpec{
			{GoName: "NewApplicationError", Impl: eqNewApplicationError},
		},
	}); err != nil {
		t.Fatalf("BindGoPackage temporal: %v", err)
	}
	for name, typ := range map[string]reflect.Type{
		"eqFailureReason":          reflect.TypeFor[eqFailureReason](),
		"orders.eqReserveResult":   reflect.TypeFor[eqReserveResult](),
		"orders.eqMarkFailedInput": reflect.TypeFor[eqMarkFailedInput](),
	} {
		if err := reg.BindType(name, typ, wflang.BindOptions{}); err != nil {
			t.Fatalf("BindType %s: %v", name, err)
		}
	}
	if err := reg.AutoBindType((*eqWorkflowRunner)(nil)); err != nil {
		t.Fatalf("AutoBindType runner: %v", err)
	}

	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON(jsonProgram)
	if err != nil {
		t.Fatalf("CompileJSON: %v\n%s", err, jsonProgram)
	}

	for _, tc := range []struct {
		name     string
		failPay  bool
		failComp bool
	}{
		{name: "success skips compensation"},
		{name: "pay failure with compensation success", failPay: true},
		{name: "pay failure with compensation failure", failPay: true, failComp: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := eqOrderInput{OrderID: "A100"}
			goLog := []string{}
			goCtx := eqWorkflowContext{Log: &goLog, FailComp: tc.failComp}
			goErr := eqOrderWorkflow(goCtx, &eqWorkflowRunner{failPay: tc.failPay}, input)

			runtimeLog := []string{}
			runtimeCtx := eqWorkflowContext{Log: &runtimeLog, FailComp: tc.failComp}
			gotV, err := prog.Run(context.Background(), wflang.RunOptions{
				Vars: map[string]any{
					"ctx":    types.NewValue("workflow.Context", runtimeCtx),
					"runner": &eqWorkflowRunner{failPay: tc.failPay},
					"input":  input,
				},
			})
			if err != nil {
				t.Fatalf("Run generated JSON: %v\n%s", err, jsonProgram)
			}
			var gotErr error
			if gotV.Go() != nil {
				var ok bool
				gotErr, ok = gotV.Go().(error)
				if !ok {
					t.Fatalf("result carrier = %T, want error or nil", gotV.Go())
				}
			}
			if errorMessage(goErr) != errorMessage(gotErr) {
				t.Fatalf("error mismatch\nwant: %s\ngot:  %s", errorMessage(goErr), errorMessage(gotErr))
			}
			if !reflect.DeepEqual(runtimeLog, goLog) {
				t.Fatalf("side effects mismatch\nwant: %#v\ngot:  %#v", goLog, runtimeLog)
			}
		})
	}
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func mustInt64Array(t *testing.T, values []int64) types.Value {
	t.Helper()
	items := make([]types.Value, len(values))
	for i, v := range values {
		items[i] = types.NewValue(types.TInt64, v)
	}
	out, err := types.NewArray(types.TInt64, items)
	if err != nil {
		t.Fatalf("NewArray: %v", err)
	}
	return out
}
