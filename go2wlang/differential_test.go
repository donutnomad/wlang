package go2wlang_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/donutnomad/wlang/go2wlang"
	"github.com/donutnomad/wlang/go2wlang/testdata/differential"
	"github.com/donutnomad/wlang/types"
	"github.com/donutnomad/wlang/wflang"
)

// differentialCase is one function of testdata/differential. The function runs
// natively and, translated by go2wlang, in the interpreter; both outcomes must
// agree. reject names a construct the translator must refuse at translation
// time: the diagnostic has to contain that text and a source position.
type differentialCase struct {
	fn     any
	reject string
	// unordered compares a slice result as a multiset: Go randomises map
	// iteration, so only the elements are comparable natively.
	unordered bool
}

var differentialCases = []differentialCase{
	{fn: differential.RangeIndexOnly},
	{fn: differential.RangeIndexReadsElement},
	{fn: differential.RangeKeyValue},
	{fn: differential.RangeValueOnly},
	{fn: differential.RangeNoVars},
	{fn: differential.RangeInt},
	{fn: differential.RangeIntNoVar},
	{fn: differential.RangeElementWriteVisible},
	{fn: differential.RangeLengthFixed},
	{fn: differential.RangeMapSum},
	{fn: differential.RangeMapKeys, unordered: true},
	{fn: differential.RangeMapDeleteUnvisited},
	{fn: differential.RangeString, reject: "range over string is not supported"},
	{fn: differential.RangeClosureCapture},
	{fn: differential.ForBoundReevaluated},
	{fn: differential.ForBodyWritesLoopVar},
	{fn: differential.ForLessEqual},
	{fn: differential.ForDescending},
	{fn: differential.ForStep},
	{fn: differential.ForMultiplyPost},
	{fn: differential.ForClosurePerIteration},
	{fn: differential.ForClosureSeesBodyWrite},
	{fn: differential.ForBreakContinue},
	{fn: differential.ForLabelledContinueOuter},
	{fn: differential.ForLabelledBreakFromSwitch},
	{fn: differential.ForBreakInsideSelectLikeSwitch},
	{fn: differential.ForClosureAfterBodyWrite},
	{fn: differential.ForLabelledBreak},
	{fn: differential.ForLabelledInnermost},
	{fn: differential.Goto, reject: "goto is not supported"},
	{fn: differential.SwitchExpr},
	{fn: differential.SwitchTagless},
	{fn: differential.SwitchDefaultFirst},
	{fn: differential.SwitchTagEvaluatedOnce},
	{fn: differential.SwitchCaseEvaluationOrder},
	{fn: differential.SwitchBreakInLoop},
	{fn: differential.SwitchContinueInLoop},
	{fn: differential.SwitchFallthrough, reject: "fallthrough is not supported"},
	{fn: differential.SwitchInit},
	{fn: differential.TypeSwitch},
	{fn: differential.DeferOrder},
	{fn: differential.DeferArgumentEvaluation},
	{fn: differential.DeferNamedResult},
	{fn: differential.Recover, reject: "builtin recover is not supported"},
	{fn: differential.IntDivisionNegative},
	{fn: differential.IntModuloNegative},
	{fn: differential.IntOverflowInt64},
	{fn: differential.IntOverflowInt32},
	{fn: differential.IntOverflowUint8},
	{fn: differential.IntShift, reject: "operator << is not supported"},
	{fn: differential.IntBitwise, reject: "operator & is not supported"},
	{fn: differential.IntToFloat, reject: "conversion from int to float64 is not supported"},
	{fn: differential.FloatToIntTruncates, reject: "conversion from float64 to int is not supported"},
	{fn: differential.NegativeFloat},
	{fn: differential.FloatDivision},
	{fn: differential.UntypedConstants},
	{fn: differential.IntConstantExpression},
	{fn: differential.DivideByZero},
	{fn: differential.StringIndexByte, reject: "conversion from byte to int is not supported"},
	{fn: differential.StringLen},
	{fn: differential.StringSlice, reject: "slicing a string is not supported"},
	{fn: differential.StringConcatCompare},
	{fn: differential.StringBytes, reject: "conversion from string to []byte is not supported"},
	{fn: differential.StringRunes, reject: "conversion from string to []rune is not supported"},
	{fn: differential.SliceNilJSON},
	{fn: differential.SliceEmptyJSON},
	{fn: differential.SliceAliasWrite},
	{fn: differential.SliceAppendIntoSpareCapacity},
	{fn: differential.SliceAppendExpression, reject: "append assignment requires ="},
	{fn: differential.SliceNilAppend},
	{fn: differential.SliceSubsliceShares},
	{fn: differential.SliceCopy},
	{fn: differential.SliceFullExpression, reject: "full slice expression xs[a:b:c] is not supported"},
	{fn: differential.SliceAppendMany},
	{fn: differential.SliceAppendSpread},
	{fn: differential.SliceOfStructValueCopy},
	{fn: differential.MapMissingKeyZero},
	{fn: differential.MapCommaOk},
	{fn: differential.MapCommaOkAssignOuter},
	{fn: differential.MapNilRead},
	{fn: differential.MapNilWrite},
	{fn: differential.MapLen},
	{fn: differential.MapAliasWrite},
	{fn: differential.MapNilJSON},
	{fn: differential.StructCopyOnAssign},
	{fn: differential.StructCopyOnCall},
	{fn: differential.StructNestedCopy},
	{fn: differential.StructCompare},
	{fn: differential.StructZeroValue},
	{fn: differential.StructPointerShares, reject: "address-of expression is not supported"},
	{fn: differential.StructNewPointer, reject: "new of a struct type declared in this package is not supported"},
	{fn: differential.EmbeddedPromotedField, reject: "embedded field Common is outside json/plain-v1"},
	{fn: differential.IndexedFieldRead, reject: "field access on a computed value is not supported"},
	{fn: differential.IndexedFieldWrite, reject: "assignment to a field of a computed value is not supported"},
	{fn: differential.CallResultField, reject: "field access on a computed value is not supported"},
	{fn: differential.Swap},
	{fn: differential.ParallelAssignEvaluatesFirst},
	{fn: differential.TupleAssignOuterVars},
	{fn: differential.TypeAssertOkAssignOuter},
	{fn: differential.ShadowingInBlock},
	{fn: differential.ShadowingTupleDefine},
	{fn: differential.IncDecStatements},
	{fn: differential.CompoundAssignOnField},
	{fn: differential.ShortCircuitAnd},
	{fn: differential.VariadicClosure, reject: "function parameter type is not supported"},
	{fn: differential.ClosureCounter},
	{fn: differential.NamedResultsBareReturn},
	{fn: differential.IotaConstants},
	{fn: differential.PackageConstant},
	{fn: differential.LocalConstant},
	{fn: differential.DurationArithmetic},
	{fn: differential.DurationConversionFromFloat, reject: "conversion from float64 to time.Duration is not supported"},
	{fn: differential.TypeAssertField, reject: "field access on a computed value is not supported"},
	{fn: differential.TypeAssertCommaOk},
	{fn: differential.TypeAssertPanics},
	{fn: differential.PanicString},
	{fn: differential.IndexOutOfRange},
	{fn: differential.StructRangeVarIsCopy},
	{fn: differential.StructAppendThenMutate},
	{fn: differential.StructMapValueCopy},
	{fn: differential.StructFieldStructCopy},
	{fn: differential.StructSliceElementCopy},
	{fn: differential.IfInitShadow},
	{fn: differential.TupleRedeclareReuse},
	{fn: differential.TupleAssignToField},
	{fn: differential.IntConstDivision},
	{fn: differential.SwitchNoMatch},
	{fn: differential.DeferInLoop},
	{fn: differential.NewScalarPointer, reject: "assignment target is not supported"},
	{fn: differential.GenericStruct, reject: "composite literal type is not supported"},
	{fn: differential.MapRangeValueWrite},
	{fn: differential.MinBuiltin, reject: "builtin min is not supported"},
	{fn: differential.MinConstantFolded},
	{fn: differential.FloatDivideByZero},
}

const differentialSource = "testdata/differential/cases.go"

func TestDifferentialGoMatchesWLang(t *testing.T) {
	src, err := os.ReadFile(differentialSource)
	if err != nil {
		t.Fatal(err)
	}
	filename, err := filepath.Abs(differentialSource)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range differentialCases {
		name := funcName(tc.fn)
		t.Run(name, func(t *testing.T) {
			want := runNative(tc.fn)
			result, err := go2wlang.TranslateFileDetailed(src, go2wlang.Options{FuncName: name, Filename: filename})
			if tc.reject != "" {
				if err == nil {
					t.Fatalf("translation accepted; want rejection mentioning %q\n%s", tc.reject, result.JSON)
				}
				if !strings.Contains(err.Error(), tc.reject) || !strings.Contains(err.Error(), "cases.go:") {
					t.Fatalf("rejection %q must mention %q and a source position", err, tc.reject)
				}
				return
			}
			if err != nil {
				t.Fatalf("translation rejected: %v (native outcome %s)", err, want)
			}
			got := runTranslated(result.JSON)
			if tc.unordered {
				want.sortElements()
				got.sortElements()
			}
			if want.String() != got.String() {
				t.Fatalf("outcome mismatch\nnative: %s %s\nwlang:  %s %s\n%s", want, want.detail, got, got.detail, result.JSON)
			}
		})
	}
}

// outcome is a JSON-normalised result, or the fact that execution failed.
type outcome struct {
	value  string
	failed bool
	detail string
}

func (o outcome) String() string {
	if o.failed {
		return "failure"
	}
	return o.value
}

func (o *outcome) sortElements() {
	var items []json.RawMessage
	if o.failed || json.Unmarshal([]byte(o.value), &items) != nil {
		return
	}
	sort.Slice(items, func(i, j int) bool { return string(items[i]) < string(items[j]) })
	raw, _ := json.Marshal(items)
	o.value = string(raw)
}

func funcName(fn any) string {
	full := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	return full[strings.LastIndex(full, ".")+1:]
}

func runNative(fn any) (out outcome) {
	defer func() {
		if r := recover(); r != nil {
			out = outcome{failed: true, detail: fmt.Sprint(r)}
		}
	}()
	results := reflect.ValueOf(fn).Call(nil)
	var value any
	if len(results) == 1 {
		value = results[0].Interface()
	} else {
		values := make([]any, len(results))
		for i, result := range results {
			values[i] = result.Interface()
		}
		value = values
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return outcome{failed: true, detail: err.Error()}
	}
	return outcome{value: normalJSON(raw)}
}

func runTranslated(program []byte) (out outcome) {
	defer func() {
		if r := recover(); r != nil {
			// A Go panic inside the interpreter is a crash, never a match.
			out = outcome{value: fmt.Sprintf("interpreter crash: %v", r)}
		}
	}()
	reg := wflang.DefaultRegistry()
	eng := wflang.NewEngine(wflang.EngineOptions{Registry: reg})
	prog, err := eng.CompileJSON(program)
	if err != nil {
		return outcome{failed: true, detail: "compile: " + err.Error()}
	}
	value, err := prog.Run(context.Background(), wflang.RunOptions{})
	if err != nil {
		return outcome{failed: true, detail: err.Error()}
	}
	encode := func(v types.Value) (json.RawMessage, error) {
		if v.IsNull() {
			return json.RawMessage("null"), nil
		}
		return reg.EncodeJSONValue(v)
	}
	if items, ok := types.UnpackTuple(value); ok {
		parts := make([]json.RawMessage, len(items))
		for i, item := range items {
			if parts[i], err = encode(item); err != nil {
				return outcome{failed: true, detail: "encode: " + err.Error()}
			}
		}
		raw, _ := json.Marshal(parts)
		return outcome{value: normalJSON(raw)}
	}
	raw, err := encode(value)
	if err != nil {
		return outcome{failed: true, detail: "encode: " + err.Error()}
	}
	return outcome{value: normalJSON(raw)}
}

// normalJSON re-encodes raw with sorted object keys and exact numbers.
func normalJSON(raw []byte) string {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "invalid JSON: " + string(raw)
	}
	out, _ := json.Marshal(value)
	return string(out)
}

// Go leaves map iteration order unspecified; the translation fixes it to
// ascending keys so a replayed workflow visits the keys in the same order.
func TestRangeOverMapVisitsKeysInAscendingOrder(t *testing.T) {
	src, err := os.ReadFile(differentialSource)
	if err != nil {
		t.Fatal(err)
	}
	result, err := go2wlang.TranslateFileDetailed(src, go2wlang.Options{FuncName: "RangeMapKeys", Filename: differentialSource})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if got := runTranslated(result.JSON); got.String() != `["a","b","c"]` {
			t.Fatalf("run %d visited %s, want [\"a\",\"b\",\"c\"]", i, got)
		}
	}
}
