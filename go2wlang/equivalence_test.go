package go2wlang_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/wflang/wflang/go2wlang"
	"github.com/wflang/wflang/registry"
	"github.com/wflang/wflang/types"
	"github.com/wflang/wflang/wflang"
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
