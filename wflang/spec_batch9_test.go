package wflang_test

import (
	"context"
	"math/big"
	"testing"

	"github.com/donutnomad/wlang/wflang"
)

// --- TC-020 bigInt 运算 -------------------------------------------------

func TestTC020_BigIntArith(t *testing.T) {
	src := []byte(`[{"return":{"+":[
		{"literal":{"type":"bigInt","value":"1000000000000000000"}},
		{"literal":{"type":"bigInt","value":"1"}}
	]}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want, _ := new(big.Int).SetString("1000000000000000001", 10)
	got, ok := v.Go().(*big.Int)
	if !ok {
		t.Fatalf("want *big.Int, got %T", v.Go())
	}
	if got.Cmp(want) != 0 {
		t.Fatalf("want %s, got %s", want, got)
	}
}

// --- TC-052 struct json tag 路径 ----------------------------------------

type JSONTaggedUser struct {
	Name string `json:"name"`
}

func TestTC052_StructJSONTagPath(t *testing.T) {
	u := &JSONTaggedUser{Name: "a"}
	v, err := runSrc(t, []byte(`[{"return":{"var":"u.name"}}]`), map[string]any{"u": u})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "a" {
		t.Fatalf("want a, got %v", v.Go())
	}
}

// --- TC-053 LookupPath 数组下标 ----------------------------------------

func TestTC053_ArrayIndexPath(t *testing.T) {
	src := []byte(`[{"return":{"var":"items.1"}}]`)
	v, err := runSrc(t, src, map[string]any{
		"items": []any{int64(10), int64(20), int64(30)},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 20 {
		t.Fatalf("want 20, got %v", v.Go())
	}
}

// --- TC-055 可写顶级 var -----------------------------------------------

func TestTC055_WritableTopLevelVar(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	sess, err := eng.NewSession(wflang.SessionOptions{
		Vars:       map[string]any{"counter": int64(0)},
		VarOptions: map[string]wflang.VarOptions{"counter": {Writable: true}},
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := sess.AppendRun(context.Background(),
		[]byte(`[{"set":{"counter":{"literal":{"type":"int64","value":"1"}}}}]`)); err != nil {
		t.Fatalf("set: %v", err)
	}
	v, err := sess.AppendRun(context.Background(),
		[]byte(`[{"return":{"var":"counter"}}]`))
	if err != nil {
		t.Fatalf("return: %v", err)
	}
	if v.Go().(int64) != 1 {
		t.Fatalf("want 1, got %v", v.Go())
	}
}

// --- TC-150 envelope 形态可执行 ----------------------------------------

func TestTC150_EnvelopeEquivalentToBare(t *testing.T) {
	env := []byte(`{"lang":"wflang/v1","imports":[],"program":[{"return":{"literal":{"type":"int64","value":"1"}}}]}`)
	bare := []byte(`[{"return":{"literal":{"type":"int64","value":"1"}}}]`)
	va, err := runSrc(t, env, nil)
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	vb, err := runSrc(t, bare, nil)
	if err != nil {
		t.Fatalf("bare: %v", err)
	}
	if va.Go().(int64) != vb.Go().(int64) {
		t.Fatalf("envelope %v != bare %v", va.Go(), vb.Go())
	}
}

// --- TC-152 渐进式三段 -------------------------------------------------

func TestTC152_ProgressiveThreeStage(t *testing.T) {
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: wflang.DefaultRegistry()})
	sess, err := eng.NewSession(wflang.SessionOptions{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := sess.AppendRun(context.Background(),
		[]byte(`[{"let":{"x":{"literal":{"type":"int64","value":"1"}}}}]`)); err != nil {
		t.Fatalf("s1: %v", err)
	}
	if _, err := sess.AppendRun(context.Background(),
		[]byte(`[{"set":{"x":{"+":[{"var":"x"},{"literal":{"type":"int64","value":"2"}}]}}}]`)); err != nil {
		t.Fatalf("s2: %v", err)
	}
	v, err := sess.AppendRun(context.Background(), []byte(`[{"return":{"var":"x"}}]`))
	if err != nil {
		t.Fatalf("s3: %v", err)
	}
	if v.Go().(int64) != 3 {
		t.Fatalf("want 3, got %v", v.Go())
	}
}

// --- TC-502 str.Replace / Split / Join --------------------------------

func TestTC502_StrReplaceSplitJoin(t *testing.T) {
	// Replace
	v, err := runSrc(t, []byte(`[{"return":{"Replace":[{"pkg":"str"},{"literal":{"type":"string","value":"hello"}},{"literal":{"type":"string","value":"l"}},{"literal":{"type":"string","value":"L"}}]}}]`), nil)
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if v.Go().(string) != "heLLo" {
		t.Fatalf("Replace: want heLLo, got %v", v.Go())
	}
}

// --- TC-540 to.Int64 数字转换 ------------------------------------------

func TestTC540_ToInt64(t *testing.T) {
	src := []byte(`[{"return":{"Int64":[{"pkg":"to"},{"literal":{"type":"string","value":"42"}}]}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if unwrap1(t, v).(int64) != 42 {
		t.Fatalf("want 42, got %v", v.Go())
	}
}

// --- TC-541 to.JSON ----------------------------------------------------

func TestTC541_ToJSON(t *testing.T) {
	src := []byte(`[{"return":{"JSON":[{"pkg":"to"},{"literal":{"type":"int64","value":"7"}}]}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if unwrap1(t, v).(string) != "7" {
		t.Fatalf("want \"7\", got %v", v.Go())
	}
}

// --- TC-550 json.Parse + Stringify round-trip -------------------------

func TestTC550_JSONRoundTrip(t *testing.T) {
	src := []byte(`[
		{"let":[["p","perr"], {"Parse":[{"pkg":"json"},{"literal":{"type":"string","value":"{\"a\":1}"}}]}]},
		{"return":{"Stringify":[{"pkg":"json"},{"var":"p"}]}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := unwrap1(t, v).(string)
	if got != `{"a":1}` {
		t.Fatalf("want {\"a\":1}, got %s", got)
	}
}

// --- TC-346 if.cond 必须为 boolean --------------------------------------

func TestTC346_IfCondMustBeBoolean(t *testing.T) {
	src := []byte(`[{"if":{"cond":{"literal":{"type":"int64","value":"1"}},"then":[],"else":[]}}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}
