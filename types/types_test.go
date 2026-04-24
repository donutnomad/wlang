package types

import (
	"math/big"
	"testing"
)

// TC-010 内置整数类型完整覆盖
func TestIntTypes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want any
	}{
		{"uint8", "1", uint8(1)},
		{"uint16", "1", uint16(1)},
		{"uint32", "1", uint32(1)},
		{"uint64", "1", uint64(1)},
		{"int8", "1", int8(1)},
		{"int16", "1", int16(1)},
		{"int32", "1", int32(1)},
		{"int64", "1", int64(1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := NewLiteral(c.name, c.in)
			if err != nil {
				t.Fatalf("NewLiteral(%s,%s): %v", c.name, c.in, err)
			}
			if v.TypeName() != c.name {
				t.Fatalf("TypeName=%s want %s", v.TypeName(), c.name)
			}
			if v.Go() != c.want {
				t.Fatalf("Go()=%#v want %#v", v.Go(), c.want)
			}
		})
	}
}

// TC-011 浮点类型映射
func TestFloatTypes(t *testing.T) {
	v32, _ := NewLiteral("float32", "1.5")
	if g, ok := v32.Go().(float32); !ok || g != 1.5 {
		t.Fatalf("float32 wrong: %#v", v32.Go())
	}
	v64, _ := NewLiteral("float64", "1.5")
	if g, ok := v64.Go().(float64); !ok || g != 1.5 {
		t.Fatalf("float64 wrong: %#v", v64.Go())
	}
}

// TC-012 boolean/string/null/error mapping
func TestBasicTypes(t *testing.T) {
	vb, _ := NewLiteral("boolean", "true")
	if g, ok := vb.Go().(bool); !ok || !g {
		t.Fatalf("boolean wrong: %#v", vb.Go())
	}
	vs, _ := NewLiteral("string", "hello")
	if g, ok := vs.Go().(string); !ok || g != "hello" {
		t.Fatalf("string wrong: %#v", vs.Go())
	}
	// null literal uses JSON null value – we encode with sentinel:
	vn, err := NewNull()
	if err != nil {
		t.Fatal(err)
	}
	if vn.TypeName() != "null" {
		t.Fatalf("null type: %s", vn.TypeName())
	}
	if vn.Go() != nil {
		t.Fatalf("null Go() must be nil, got %#v", vn.Go())
	}
}

// TC-013 bigInt / bigDecimal
func TestBig(t *testing.T) {
	vi, err := NewLiteral("bigInt", "100000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	bi, ok := vi.Go().(*big.Int)
	if !ok {
		t.Fatalf("bigInt Go() wrong: %T", vi.Go())
	}
	want, _ := new(big.Int).SetString("100000000000000000000", 10)
	if bi.Cmp(want) != 0 {
		t.Fatalf("bigInt value: %s", bi.String())
	}
	vd, err := NewLiteral("bigDecimal", "1.000")
	if err != nil {
		t.Fatal(err)
	}
	br, ok := vd.Go().(*big.Rat)
	if !ok {
		t.Fatalf("bigDecimal Go() wrong: %T", vd.Go())
	}
	if br.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("bigDecimal must equal 1")
	}
}

// TC-015 禁止 int / uint 平台依赖宽度
func TestRejectPlatformInt(t *testing.T) {
	if _, err := NewLiteral("int", "1"); err == nil {
		t.Fatalf("int must be rejected")
	}
	if _, err := NewLiteral("uint", "1"); err == nil {
		t.Fatalf("uint must be rejected")
	}
}

// TC-105 typed literal value 类型/格式错误立即报错
func TestLiteralParseError(t *testing.T) {
	if _, err := NewLiteral("int64", "abc"); err == nil {
		t.Fatalf("int64=abc must fail")
	}
	if _, err := NewLiteral("boolean", "notbool"); err == nil {
		t.Fatalf("bad bool must fail")
	}
	if _, err := NewLiteral("bigInt", "1.2"); err == nil {
		t.Fatalf("bigInt=1.2 must fail")
	}
}

// TC-125 E1007 unsupported typed literal
func TestUnsupportedLiteralType(t *testing.T) {
	if _, err := NewLiteral("unknown_t", "x"); err == nil {
		t.Fatalf("unknown_t must fail")
	}
}
