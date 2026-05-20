// Regression tests for runtime.ProgramContainsReturn — verifying that a
// `return` reachable through Foreach / Fori / Match / Select correctly marks
// the Session as finished (so a subsequent AppendRun yields
// E_INVALID_CONTROL_FLOW), and that a `return` inside a `routine.do` block
// stays scoped to the routine handle (the enclosing Session must remain
// open).
package wflang_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	werr "github.com/wflang/wflang/errors"
	"github.com/wflang/wflang/wflang"
)

func mustSession(t *testing.T) *wflang.Session {
	t.Helper()
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	sess, err := eng.NewSession(wflang.SessionOptions{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	return sess
}

// assertSecondAppendRejected runs `tail` on a Session that previously executed
// `head` (which is expected to have returned a value). The second AppendRun
// must fail with E_INVALID_CONTROL_FLOW.
func assertSecondAppendRejected(t *testing.T, head, tail []byte) {
	t.Helper()
	sess := mustSession(t)
	if _, err := sess.AppendRun(context.Background(), head); err != nil {
		t.Fatalf("first append: %v", err)
	}
	_, err := sess.AppendRun(context.Background(), tail)
	if err == nil {
		t.Fatal("second append: want E_INVALID_CONTROL_FLOW, got nil")
	}
	var le *werr.LangError
	if !errors.As(err, &le) {
		t.Fatalf("second append: want *werr.LangError, got %T: %v", err, err)
	}
	if le.Code != werr.CodeInvalidControlFlow {
		t.Fatalf("second append: code=%s, want %s; msg=%q",
			le.Code, werr.CodeInvalidControlFlow, le.Error())
	}
}

func TestSessionFinishedAfterReturnInForeach(t *testing.T) {
	head := []byte(`[
		{"foreach":{"target":{"literal":{"type":"array<int64>","value":[1,2,3]}},"as":"x","do":[
			{"return":{"var":"x"}}
		]}}
	]`)
	tail := []byte(`[{"return":{"literal":{"type":"int64","value":"42"}}}]`)
	assertSecondAppendRejected(t, head, tail)
}

func TestSessionFinishedAfterReturnInFori(t *testing.T) {
	head := []byte(`[
		{"fori":{"var":"i",
			"from":{"literal":{"type":"int64","value":"0"}},
			"to":{"literal":{"type":"int64","value":"3"}},
			"do":[{"return":{"var":"i"}}]
		}}
	]`)
	tail := []byte(`[{"return":{"literal":{"type":"int64","value":"42"}}}]`)
	assertSecondAppendRejected(t, head, tail)
}

func TestSessionFinishedAfterReturnInMatch(t *testing.T) {
	// match is an expression: a return inside its arm propagates out of
	// RunProgram via errReturnSig and ends the program.
	head := []byte(`[
		{"expr": {"match":{
			"value":{"literal":{"type":"int64","value":"2"}},
			"cases":[
				{"when":{"literal":{"type":"int64","value":"2"}},
				 "do":[{"return":{"literal":{"type":"string","value":"hit"}}}]}
			],
			"default":[{"return":{"literal":{"type":"string","value":"miss"}}}]
		}}}
	]`)
	tail := []byte(`[{"return":{"literal":{"type":"int64","value":"42"}}}]`)
	assertSecondAppendRejected(t, head, tail)
}

func TestSessionFinishedAfterReturnInSelect(t *testing.T) {
	// A buffered channel pre-loaded with one value so the recv case fires
	// immediately; that case body returns and must finish the Session.
	head := []byte(`[
		{"let":{"ch":{"chan":["int64",{"literal":{"type":"int64","value":"1"}}]}}},
		{"expr":{"ch.send":[{"var":"ch"},{"literal":{"type":"int64","value":"7"}}]}},
		{"select":[
			{"case":{"recv":[{"var":"ch"}],"bind":["v","_"],"do":[
				{"return":{"var":"v"}}
			]}},
			{"default":[{"expr":{"literal":{"type":"int64","value":"0"}}}]}
		]}
	]`)
	tail := []byte(`[{"return":{"literal":{"type":"int64","value":"42"}}}]`)
	assertSecondAppendRejected(t, head, tail)
}

func TestSessionStaysOpenAfterReturnInRoutineDo(t *testing.T) {
	// A `return` inside `routine.do` resolves the routine handle but must
	// not finish the enclosing Session. The second AppendRun must succeed.
	sess := mustSession(t)
	first := []byte(`[
		{"let":{"h":{"routine":{"do":[
			{"return":{"literal":{"type":"int64","value":"7"}}}
		]}}}},
		{"expr":{"await":{"var":"h"}}}
	]`)
	if _, err := sess.AppendRun(context.Background(), first); err != nil {
		t.Fatalf("first append: %v", err)
	}
	second := []byte(`[{"return":{"literal":{"type":"int64","value":"42"}}}]`)
	v, err := sess.AppendRun(context.Background(), second)
	if err != nil {
		t.Fatalf("second append: %v (session unexpectedly finished after routine.do return)", err)
	}
	if got, ok := v.Go().(int64); !ok || got != 42 {
		t.Fatalf("second append result = %v (%s), want 42", v.Go(), v.TypeName())
	}
	// And now the session itself is finished after the second AppendRun's
	// program-level return — a third call must be rejected.
	_, err = sess.AppendRun(context.Background(),
		[]byte(`[{"return":{"literal":{"type":"int64","value":"0"}}}]`))
	if err == nil {
		t.Fatal("third append: want E_INVALID_CONTROL_FLOW, got nil")
	}
	if !strings.Contains(err.Error(), "session already returned") {
		t.Fatalf("third append: unexpected error %v", err)
	}
}
