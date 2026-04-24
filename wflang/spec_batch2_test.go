package wflang_test

import (
	"testing"
)

// --- TC-082 函数名严格大小写 ---------------------------------------------

func TestTC082_FuncNameCaseSensitive(t *testing.T) {
	src := []byte(`[{"return":{"Contains":[
		{"literal":{"type":"string","value":"hello"}},
		{"literal":{"type":"string","value":"ell"}}
	]}}]`)
	_, err := runSrc(t, src, nil)
	// `Contains` is not a builtin string operator; builtin is `contains`.
	// No str method named `Contains` on a string receiver; expect E_SYMBOL /
	// E_OPERATOR_NOT_FOUND.
	if err == nil {
		t.Fatalf("want error, got nil")
	}
}

// --- TC-191 if.then return ----------------------------------------------

func TestTC191_IfThenReturn(t *testing.T) {
	src := []byte(`[
		{"if":{"cond":{"literal":{"type":"boolean","value":"true"}},"then":[
			{"return":{"literal":{"type":"int64","value":"7"}}}
		],"else":[]}},
		{"return":{"literal":{"type":"int64","value":"99"}}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 7 {
		t.Fatalf("want 7, got %v", v.Go())
	}
}

// --- TC-193 foreach index ----------------------------------------------

func TestTC193_ForeachIndex(t *testing.T) {
	src := []byte(`[
		{"let":{"s":{"literal":{"type":"int64","value":"0"}}}},
		{"foreach":{"target":{"literal":{"type":"array<int64>","value":[10,20,30]}},"as":"item","index":"i","do":[
			{"set":{"s":{"+":[{"var":"s"},{"var":"i"}]}}}
		]}},
		{"return":{"var":"s"}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 3 {
		t.Fatalf("want 3, got %v", v.Go())
	}
}

// --- TC-194 foreach as == index 非法 ------------------------------------

func TestTC194_ForeachDuplicateVar(t *testing.T) {
	src := []byte(`[
		{"foreach":{"target":{"literal":{"type":"array<int64>","value":[1]}},"as":"x","index":"x","do":[]}}
	]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatalf("want error, got nil")
	}
}

// --- TC-195 foreach target 非数组 ---------------------------------------

func TestTC195_ForeachTargetNonArray(t *testing.T) {
	src := []byte(`[
		{"foreach":{"target":{"literal":{"type":"int64","value":"1"}},"as":"x","do":[]}}
	]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatalf("want error, got nil")
	}
}

// --- TC-197 嵌套 foreach inner break ------------------------------------

func TestTC197_NestedForeachInnerBreak(t *testing.T) {
	src := []byte(`[
		{"let":{"hits":{"literal":{"type":"int64","value":"0"}}}},
		{"foreach":{"target":{"literal":{"type":"array<int64>","value":[1,2]}},"as":"a","do":[
			{"foreach":{"target":{"literal":{"type":"array<int64>","value":[10,20,30]}},"as":"b","do":[
				{"set":{"hits":{"+":[{"var":"hits"},{"literal":{"type":"int64","value":"1"}}]}}},
				{"break":{}}
			]}}
		]}},
		{"return":{"var":"hits"}}
	]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Inner break stops after first iteration each time → 2 outer iters × 1 inner = 2.
	if v.Go().(int64) != 2 {
		t.Fatalf("want 2, got %v", v.Go())
	}
}
