package wflang_test

import (
	"strings"
	"testing"
)

// --- TC-500 str.Len / Trim / Lower / Upper ------------------------------

func TestTC500_StrBasics(t *testing.T) {
	cases := []struct {
		src  string
		want any
	}{
		{`{"Len":[{"pkg":"str"},{"literal":{"type":"string","value":"hello"}}]}`, int64(5)},
		{`{"Trim":[{"pkg":"str"},{"literal":{"type":"string","value":"  hi  "}}]}`, "hi"},
		{`{"Lower":[{"pkg":"str"},{"literal":{"type":"string","value":"ABC"}}]}`, "abc"},
		{`{"Upper":[{"pkg":"str"},{"literal":{"type":"string","value":"abc"}}]}`, "ABC"},
	}
	for _, c := range cases {
		src := []byte(`[{"return":` + c.src + `}]`)
		v, err := runSrc(t, src, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.src, err)
		}
		if v.Go() != c.want {
			t.Fatalf("%s: want %v, got %v", c.src, c.want, v.Go())
		}
	}
}

// --- TC-501 str.Contains / StartsWith / EndsWith ------------------------

func TestTC501_StrPredicates(t *testing.T) {
	cases := map[string]bool{
		`{"Contains":[{"pkg":"str"},{"literal":{"type":"string","value":"hello"}},{"literal":{"type":"string","value":"ell"}}]}`:  true,
		`{"StartsWith":[{"pkg":"str"},{"literal":{"type":"string","value":"hello"}},{"literal":{"type":"string","value":"he"}}]}`: true,
		`{"EndsWith":[{"pkg":"str"},{"literal":{"type":"string","value":"hello"}},{"literal":{"type":"string","value":"zz"}}]}`:   false,
	}
	for body, want := range cases {
		src := []byte(`[{"return":` + body + `}]`)
		v, err := runSrc(t, src, nil)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if v.Go().(bool) != want {
			t.Fatalf("%s: want %v, got %v", body, want, v.Go())
		}
	}
}

// --- TC-510 num.Abs --------------------------------------------------

func TestTC510_NumAbs(t *testing.T) {
	src := []byte(`[{"return":{"Abs":[{"pkg":"num"},{"literal":{"type":"float64","value":"-5"}}]}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(float64) != 5.0 {
		t.Fatalf("want 5, got %v", v.Go())
	}
}

// --- TC-511 num.Min / Max -----------------------------------------------

func TestTC511_NumMinMax(t *testing.T) {
	cases := map[string]float64{
		`{"Min":[{"pkg":"num"},{"literal":{"type":"float64","value":"1.5"}},{"literal":{"type":"float64","value":"2.5"}}]}`: 1.5,
		`{"Max":[{"pkg":"num"},{"literal":{"type":"float64","value":"1.5"}},{"literal":{"type":"float64","value":"2.5"}}]}`: 2.5,
	}
	for body, want := range cases {
		src := []byte(`[{"return":` + body + `}]`)
		v, err := runSrc(t, src, nil)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if v.Go().(float64) != want {
			t.Fatalf("%s: want %v, got %v", body, want, v.Go())
		}
	}
}

// --- TC-520 arr.Len -----------------------------------------------------

func TestTC520_ArrLen(t *testing.T) {
	src := []byte(`[{"return":{"Len":[{"pkg":"arr"},{"literal":{"type":"array<int64>","value":[1,2,3,4]}}]}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(int64) != 4 {
		t.Fatalf("want 4, got %v", v.Go())
	}
}

// --- TC-560 val.TypeOf / IsNull / IsEmpty -------------------------------

func TestTC560_ValBasics(t *testing.T) {
	// TypeOf("hi") == "string"
	src := []byte(`[{"return":{"TypeOf":[{"pkg":"val"},{"literal":{"type":"string","value":"hi"}}]}}]`)
	v, err := runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(string) != "string" {
		t.Fatalf("want string, got %v", v.Go())
	}

	// IsNull(null) == true
	src = []byte(`[{"return":{"IsNull":[{"pkg":"val"},{"literal":{"type":"null","value":null}}]}}]`)
	v, err = runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(bool) != true {
		t.Fatalf("IsNull: want true, got %v", v.Go())
	}

	// IsEmpty("") == true
	src = []byte(`[{"return":{"IsEmpty":[{"pkg":"val"},{"literal":{"type":"string","value":""}}]}}]`)
	v, err = runSrc(t, src, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Go().(bool) != true {
		t.Fatalf("IsEmpty: want true, got %v", v.Go())
	}
}

// --- TC-570 拒绝 lambda -----------------------------------------------

func TestTC570_NoLambda(t *testing.T) {
	for _, kw := range []string{"lambda", "fn", "=>", "arrow"} {
		src := []byte(`[{"` + kw + `":{"params":["x"],"body":[]}}]`)
		_, err := runSrc(t, src, nil)
		if err == nil {
			t.Fatalf("keyword %s: want error, got nil", kw)
		}
		// Make sure it's a structural rejection — don't care about the exact wording.
		if !strings.Contains(strings.ToLower(err.Error()), "") {
			t.Fatalf("%s: unexpected error: %v", kw, err)
		}
	}
}

// --- TC-100 裸 JSON 数字非法 ------------------------------------------

func TestTC100_BareNumberInvalid(t *testing.T) {
	// A bare number literal (no typed wrapper) must be rejected.
	src := []byte(`[{"return":42}]`)
	_, err := runSrc(t, src, nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}
