package runtime

import (
	"context"
	"sync"
	"testing"

	"github.com/donutnomad/wlang/compiler"
	werr "github.com/donutnomad/wlang/errors"
)

// awaitHandle blocks on the routineHandle bound to name in exec's scope.
func awaitHandle(t *testing.T, exec *Executor, name string) {
	t.Helper()
	v, ok := exec.Scope().Lookup(name)
	if !ok {
		t.Fatalf("handle %q not found in scope", name)
	}
	h, ok := v.Go().(*RoutineHandle)
	if !ok {
		t.Fatalf("%q is %s, want routineHandle", name, v.TypeName())
	}
	if _, err := h.Wait(context.Background()); err != nil {
		t.Fatalf("routine %q failed: %v", name, err)
	}
}

// Two concurrent body-form routines running the identical body must be
// distinguishable through StepInfo.RoutineID, and main-line statements must
// keep RoutineID 0.
func TestStepInfoRoutineIDSeparatesConcurrentInstances(t *testing.T) {
	prog, err := compiler.ParseProgram([]byte(`[
		{"let":{"a":{"routine":{"do":[
			{"let":{"n":{"literal":{"type":"int64","value":"1"}}}},
			{"return":{"var":"n"}}
		]}}}},
		{"let":{"b":{"routine":{"do":[
			{"let":{"n":{"literal":{"type":"int64","value":"1"}}}},
			{"return":{"var":"n"}}
		]}}}}
	]`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var mu sync.Mutex
	// byRoutine maps routineID -> statement paths observed under it.
	byRoutine := map[uint64][]string{}

	exec := NewExecutor(context.Background(), NewScope(), nil, nil, Budget{})
	exec.OnStep = func(info StepInfo) error {
		mu.Lock()
		defer mu.Unlock()
		byRoutine[info.RoutineID] = append(byRoutine[info.RoutineID], info.Node.Path())
		return nil
	}
	if _, err := exec.RunProgram(prog); err != nil {
		t.Fatalf("run: %v", err)
	}
	awaitHandle(t, exec, "a")
	awaitHandle(t, exec, "b")

	mu.Lock()
	defer mu.Unlock()

	// One bucket for the main line (the two `let` statements), plus one bucket
	// per routine instance. The two routines must not share a bucket.
	if len(byRoutine) != 3 {
		t.Fatalf("want 3 routineID buckets (main + 2 instances), got %d: %v",
			len(byRoutine), byRoutine)
	}
	if got := len(byRoutine[0]); got != 2 {
		t.Fatalf("main line (routineID 0) ran %d statements, want 2: %v", got, byRoutine[0])
	}
	for id, paths := range byRoutine {
		if id == 0 {
			continue
		}
		if len(paths) != 2 {
			t.Fatalf("routine %d ran %d statements, want 2 (let+return): %v",
				id, len(paths), paths)
		}
	}
}

// A routine launched from inside another routine's body must get its own
// distinct instance ID.
func TestStepInfoRoutineIDIsFreshForNestedRoutine(t *testing.T) {
	prog, err := compiler.ParseProgram([]byte(`[
		{"let":{"outer":{"routine":{"do":[
			{"let":{"inner":{"routine":{"do":[
				{"return":{"literal":{"type":"int64","value":"1"}}}
			]}}}},
			{"expr":{"await":{"var":"inner"}}}
		]}}}}
	]`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var mu sync.Mutex
	ids := map[uint64]bool{}

	exec := NewExecutor(context.Background(), NewScope(), nil, nil, Budget{})
	exec.OnStep = func(info StepInfo) error {
		mu.Lock()
		defer mu.Unlock()
		ids[info.RoutineID] = true
		return nil
	}
	if _, err := exec.RunProgram(prog); err != nil {
		t.Fatalf("run: %v", err)
	}
	awaitHandle(t, exec, "outer")

	mu.Lock()
	defer mu.Unlock()
	// main line (0) + outer instance + inner instance.
	if len(ids) != 3 {
		t.Fatalf("want 3 distinct routineIDs (main + outer + inner), got %d: %v", len(ids), ids)
	}
	if !ids[0] {
		t.Fatalf("main line should report routineID 0, got %v", ids)
	}
}

// Budget.MaxRoutines must count routines launched from inside another
// routine's body, not just those launched from the main line. Before the
// routineCount counter was shared by pointer, each child Executor got its own
// zero-valued counter and nested launches escaped the budget entirely.
func TestMaxRoutinesCountsNestedLaunches(t *testing.T) {
	// The outer routine holds the only slot (MaxRoutines: 1) while its body
	// tries to launch a second routine, which must be rejected with E_BUDGET.
	prog, err := compiler.ParseProgram([]byte(`[
		{"let":{"outer":{"routine":{"do":[
			{"let":{"inner":{"routine":{"do":[
				{"return":{"literal":{"type":"int64","value":"1"}}}
			]}}}},
			{"return":{"var":"inner"}}
		]}}}}
	]`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	exec := NewExecutor(context.Background(), NewScope(), nil, nil, Budget{MaxRoutines: 1})
	if _, err := exec.RunProgram(prog); err != nil {
		t.Fatalf("run: %v", err)
	}

	v, ok := exec.Scope().Lookup("outer")
	if !ok {
		t.Fatal("outer handle not found")
	}
	h := v.Go().(*RoutineHandle)
	if _, err = h.Wait(context.Background()); err == nil {
		t.Fatal("nested routine escaped MaxRoutines: want E_BUDGET, got nil")
	}
	assertLangCode(t, err, werr.CodeBudget)
}
