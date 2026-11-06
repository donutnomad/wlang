package main

import (
	"errors"
	"fmt"
)

func multiExplicit() (out string, err error) {
	defer func() { out += "|D1" }()
	defer func() { out += "|D2" }()
	return "body", nil
}

func multiErr() (out string, err error) {
	defer func() { err = errors.New("deferred-err") }()
	return "body", nil
}

func evalOrder(log *[]string) (out string, err error) {
	f := func(v string) string { *log = append(*log, v); return v }
	defer func() { *log = append(*log, "defer") }()
	return f("expr"), nil
}

func shadowExplicit() (out string, err error) {
	out = "root"
	defer func() { out += "|D" }()
	{
		out := "shadow"
		return out, nil
	}
}

func blankExplicit() (_ string, err error) { return "blank", nil }

func blankBare() (_ string, err error) { return }

func closureFrames() (int64, int64) {
	count := int64(0)
	f := func() (out int64, err error) {
		count++
		out = count
		defer func() { out += 100 }()
		return
	}
	a, _ := f()
	b, _ := f()
	return a, b
}

func recursion(n int64) (out int64, err error) {
	defer func() { out += 100 }()
	if n <= 1 {
		return 1, nil
	}
	sub, err := recursion(n - 1)
	if err != nil {
		return 0, err
	}
	return sub * n, nil
}

func unnamedSnapshot() (string, string) {
	a, b := "a", "b"
	defer func() { b = "changed" }()
	return a, b
}

func main() {
	log := []string{}
	o, e := multiExplicit()
	fmt.Printf("multiExplicit=%q,%v\n", o, e)
	o, e = multiErr()
	fmt.Printf("multiErr=%q,%v\n", o, e)
	o, e = evalOrder(&log)
	fmt.Printf("evalOrder=%q,%v log=%v\n", o, e, log)
	o, e = shadowExplicit()
	fmt.Printf("shadowExplicit=%q,%v\n", o, e)
	o, e = blankExplicit()
	fmt.Printf("blankExplicit=%q,%v\n", o, e)
	o, e = blankBare()
	fmt.Printf("blankBare=%q,%v\n", o, e)
	a, b := closureFrames()
	fmt.Printf("closureFrames=%d,%d\n", a, b)
	r, _ := recursion(4)
	fmt.Printf("recursion=%d\n", r)
	x, y := unnamedSnapshot()
	fmt.Printf("unnamedSnapshot=%q,%q\n", x, y)
}
