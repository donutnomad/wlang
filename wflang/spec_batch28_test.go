// Batch 28 covers Session.ResumeYield (LANGUAGE.md §5.2 / §8):
// TC-160, TC-402, TC-403, TC-404, TC-405, TC-406, TC-407,
// TC-701, TC-702, TC-703, TC-704, TC-705, TC-706, TC-707, TC-708.
package wflang_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wflang/wflang/wflang"
)

// tc28Worker implements yielding host methods used across this batch.
// All Wait* return (T, NewYield(token, payload)); the goroutine inside
// the routine fires the yield handler synchronously, so tests can call
// ResumeYield as soon as the handler has been observed.
type tc28Worker struct{}

func (*tc28Worker) WaitS(token string) (string, error) {
	return "", wflang.NewYield(token, "payload-"+token)
}

func (*tc28Worker) WaitTwo(token string) (string, int64, error) {
	return "", int64(0), wflang.NewYield(token, nil)
}

func (*tc28Worker) WaitNoVal(token string) error {
	return wflang.NewYield(token, nil)
}

// tc28Hidden is unregistered on purpose — used by TC-708 to verify that
// ResumeYield accepts auto host type names.
type tc28Hidden struct{ N int64 }

func (*tc28Worker) WaitHidden(token string) (*tc28Hidden, error) {
	return nil, wflang.NewYield(token, nil)
}

// runRoutineAndWait spawns a session, runs a routine that immediately
// yields, and returns the captured YieldState plus the session.
func runRoutineAndWait(t *testing.T, body string) (*wflang.Session, wflang.YieldState) {
	t.Helper()
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*tc28Worker)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	yieldCh := make(chan wflang.YieldState, 4)
	sess, err := eng.NewSession(wflang.SessionOptions{
		Vars: map[string]any{"w": &tc28Worker{}},
		RoutineYieldHandler: func(ctx context.Context, y wflang.YieldState) {
			yieldCh <- y
		},
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := sess.AppendRun(t.Context(), []byte(body)); err != nil {
		t.Fatalf("append: %v", err)
	}
	select {
	case y := <-yieldCh:
		return sess, y
	case <-time.After(2 * time.Second):
		t.Fatalf("yield handler never fired")
	}
	return nil, wflang.YieldState{}
}

// --- TC-160 / TC-402 -- routine yielded → ResumeYield 通路 ---------------
func TestTC160_RoutineYieldedRecoverable(t *testing.T) {
	sess, y := runRoutineAndWait(t, `[
		{"routine":{"WaitS":[{"var":"w"},{"literal":{"type":"string","value":"t160"}}]}}
	]`)
	if y.Token != "t160" {
		t.Fatalf("want token t160, got %q", y.Token)
	}
	v, err := sess.ResumeYield(wflang.ResumeInput{
		Token:   "t160",
		Results: []wflang.Value{wflang.MustValue("string", "ok")},
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if v.Go().(string) != "ok" {
		t.Fatalf("resume returned %v", v.Go())
	}
}

// --- TC-403 token 一次性 ----------------------------------------------------
func TestTC403_ResumeYieldTokenIsOneShot(t *testing.T) {
	sess, _ := runRoutineAndWait(t, `[
		{"routine":{"WaitS":[{"var":"w"},{"literal":{"type":"string","value":"t403"}}]}}
	]`)
	if _, err := sess.ResumeYield(wflang.ResumeInput{
		Token:   "t403",
		Results: []wflang.Value{wflang.MustValue("string", "first")},
	}); err != nil {
		t.Fatalf("first resume: %v", err)
	}
	_, err := sess.ResumeYield(wflang.ResumeInput{
		Token:   "t403",
		Results: []wflang.Value{wflang.MustValue("string", "second")},
	})
	var le *wflang.LangError
	if !errors.As(err, &le) || le.Code != "E_YIELD_TOKEN_MISMATCH" {
		t.Fatalf("want E_YIELD_TOKEN_MISMATCH, got %v (%T)", err, err)
	}
}

// --- TC-404 Results 类型不匹配 ---------------------------------------------
func TestTC404_ResumeYieldTypeMismatch(t *testing.T) {
	sess, _ := runRoutineAndWait(t, `[
		{"routine":{"WaitS":[{"var":"w"},{"literal":{"type":"string","value":"t404"}}]}}
	]`)
	_, err := sess.ResumeYield(wflang.ResumeInput{
		Token:   "t404",
		Results: []wflang.Value{wflang.MustValue("int64", int64(7))},
	})
	var le *wflang.LangError
	if !errors.As(err, &le) || le.Code != "E_TYPE" {
		t.Fatalf("want E_TYPE, got %v (%T)", err, err)
	}
}

// --- TC-405 / TC-706 多业务返回值 → tuple ----------------------------------
func TestTC405_ResumeYieldTupleFromMultiReturn(t *testing.T) {
	sess, y := runRoutineAndWait(t, `[
		{"routine":{"WaitTwo":[{"var":"w"},{"literal":{"type":"string","value":"t405"}}]}}
	]`)
	if got := y.ReturnTypes; len(got) != 2 || got[0] != "string" || got[1] != "int64" {
		t.Fatalf("want ReturnTypes [string int64], got %v", got)
	}
	v, err := sess.ResumeYield(wflang.ResumeInput{
		Token: "t405",
		Results: []wflang.Value{
			wflang.MustValue("string", "abc"),
			wflang.MustValue("int64", int64(42)),
		},
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if v.TypeName() != "tuple<string,int64>" {
		t.Fatalf("want tuple<string,int64>, got %s", v.TypeName())
	}
	parts, ok := v.Go().([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("tuple shape: got %T = %v", v.Go(), v.Go())
	}
	if parts[0].(string) != "abc" || parts[1].(int64) != 42 {
		t.Fatalf("tuple parts: %v", parts)
	}
}

// --- TC-406 仅 error 返回的成功路径 → null ---------------------------------
func TestTC406_ResumeYieldErrorOnlyReturnsNull(t *testing.T) {
	sess, y := runRoutineAndWait(t, `[
		{"routine":{"WaitNoVal":[{"var":"w"},{"literal":{"type":"string","value":"t406"}}]}}
	]`)
	if len(y.ReturnTypes) != 0 {
		t.Fatalf("want empty ReturnTypes, got %v", y.ReturnTypes)
	}
	v, err := sess.ResumeYield(wflang.ResumeInput{Token: "t406"})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !v.IsNull() {
		t.Fatalf("want null, got %s", v.TypeName())
	}
}

// --- TC-407 / TC-707 ResumeInput.Err 注入 → 错误 ---------------------------
func TestTC407_ResumeYieldErrorInjection(t *testing.T) {
	sess, _ := runRoutineAndWait(t, `[
		{"routine":{"WaitS":[{"var":"w"},{"literal":{"type":"string","value":"t407"}}]}}
	]`)
	_, err := sess.ResumeYield(wflang.ResumeInput{
		Token: "t407",
		Err:   errors.New("upstream blew up"),
	})
	if err == nil {
		t.Fatalf("want error from Err injection, got nil")
	}
	if !strings.Contains(err.Error(), "upstream blew up") {
		t.Fatalf("want injected error message, got %v", err)
	}
}

// --- TC-701 token 唯一性 ---------------------------------------------------
func TestTC701_TokensUniqueAcrossSession(t *testing.T) {
	reg := wflang.DefaultRegistry()
	if err := reg.AutoBindType((*tc28Worker)(nil)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	yieldCh := make(chan wflang.YieldState, 4)
	sess, err := eng.NewSession(wflang.SessionOptions{
		Vars: map[string]any{"w": &tc28Worker{}},
		RoutineYieldHandler: func(ctx context.Context, y wflang.YieldState) {
			yieldCh <- y
		},
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	src := []byte(`[
		{"routine":{"WaitS":[{"var":"w"},{"literal":{"type":"string","value":"t701-a"}}]}},
		{"routine":{"WaitS":[{"var":"w"},{"literal":{"type":"string","value":"t701-b"}}]}}
	]`)
	if _, err := sess.AppendRun(t.Context(), src); err != nil {
		t.Fatalf("append: %v", err)
	}
	tokens := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case y := <-yieldCh:
			if tokens[y.Token] {
				t.Fatalf("token %q seen twice", y.Token)
			}
			tokens[y.Token] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("only saw %d yields", i)
		}
	}
	if !tokens["t701-a"] || !tokens["t701-b"] {
		t.Fatalf("missing tokens: %v", tokens)
	}
}

// --- TC-702 token 可使用外部任务 ID ---------------------------------------
func TestTC702_ExternalTaskIDAsToken(t *testing.T) {
	const externalID = "task-1234-abcd"
	sess, y := runRoutineAndWait(t, `[
		{"routine":{"WaitS":[{"var":"w"},
			{"literal":{"type":"string","value":"`+externalID+`"}}]}}
	]`)
	if y.Token != externalID {
		t.Fatalf("token want %q, got %q", externalID, y.Token)
	}
	if _, err := sess.ResumeYield(wflang.ResumeInput{
		Token:   externalID,
		Results: []wflang.Value{wflang.MustValue("string", "ok")},
	}); err != nil {
		t.Fatalf("resume by external id: %v", err)
	}
}

// --- TC-703 token mismatch -----------------------------------------------
func TestTC703_ResumeYieldUnknownToken(t *testing.T) {
	sess, _ := runRoutineAndWait(t, `[
		{"routine":{"WaitS":[{"var":"w"},{"literal":{"type":"string","value":"t703"}}]}}
	]`)
	_, err := sess.ResumeYield(wflang.ResumeInput{Token: "no-such-token"})
	var le *wflang.LangError
	if !errors.As(err, &le) || le.Code != "E_YIELD_TOKEN_MISMATCH" {
		t.Fatalf("want E_YIELD_TOKEN_MISMATCH, got %v (%T)", err, err)
	}
}

// --- TC-704 挂起点保存内容完整 -------------------------------------------
func TestTC704_YieldStateCapturesAllFields(t *testing.T) {
	_, y := runRoutineAndWait(t, `[
		{"routine":{"WaitTwo":[{"var":"w"},{"literal":{"type":"string","value":"t704"}}]}}
	]`)
	if y.Token != "t704" {
		t.Fatalf("Token: %q", y.Token)
	}
	if y.Path == "" {
		t.Fatalf("Path empty")
	}
	if len(y.ReturnTypes) != 2 || y.ReturnTypes[0] != "string" || y.ReturnTypes[1] != "int64" {
		t.Fatalf("ReturnTypes: %v", y.ReturnTypes)
	}
}

// --- TC-705 单业务返回值 ResumeYield ------------------------------------
func TestTC705_ResumeYieldSingleValue(t *testing.T) {
	sess, _ := runRoutineAndWait(t, `[
		{"routine":{"WaitS":[{"var":"w"},{"literal":{"type":"string","value":"t705"}}]}}
	]`)
	v, err := sess.ResumeYield(wflang.ResumeInput{
		Token:   "t705",
		Results: []wflang.Value{wflang.MustValue("string", "single")},
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if v.TypeName() != "string" || v.Go().(string) != "single" {
		t.Fatalf("got %s = %v", v.TypeName(), v.Go())
	}
}

// --- TC-708 自动宿主类型注入 ---------------------------------------------
func TestTC708_AutoHostTypeInjection(t *testing.T) {
	sess, y := runRoutineAndWait(t, `[
		{"routine":{"WaitHidden":[{"var":"w"},{"literal":{"type":"string","value":"t708"}}]}}
	]`)
	autoName := y.ReturnTypes[0]
	if !strings.HasPrefix(autoName, "*") || !strings.Contains(autoName, "tc28Hidden") {
		t.Fatalf("auto host type name: %q", autoName)
	}
	v, err := sess.ResumeYield(wflang.ResumeInput{
		Token:   "t708",
		Results: []wflang.Value{wflang.MustValue(autoName, &tc28Hidden{N: 9})},
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if v.TypeName() != autoName {
		t.Fatalf("want %s, got %s", autoName, v.TypeName())
	}
	if h, ok := v.Go().(*tc28Hidden); !ok || h.N != 9 {
		t.Fatalf("unwrap: %v", v.Go())
	}
}
