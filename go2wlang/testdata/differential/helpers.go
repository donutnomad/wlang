package differential

import (
	"fmt"
	"strconv"
	"strings"
)

// ---- same-package helper functions ----

func double(n int) int { return n * 2 }

func label(prefix string, n int) string {
	return prefix + "-" + strconv.Itoa(double(n))
}

func HelperSameFile() []any {
	return []any{double(21), label("order", 4)}
}

// isEven and isOdd call each other.
func isEven(n int) bool {
	if n == 0 {
		return true
	}
	return isOdd(n - 1)
}

func isOdd(n int) bool {
	if n == 0 {
		return false
	}
	return isEven(n - 1)
}

func factorial(n int) int {
	if n <= 1 {
		return 1
	}
	return n * factorial(n-1)
}

func HelperRecursion() []any {
	return []any{factorial(10), isEven(10), isOdd(7), isEven(3)}
}

func divide(a, b int) (int, error) {
	if b == 0 {
		return 0, fmt.Errorf("divide %d by zero", a)
	}
	return a / b, nil
}

func splitPair(s string) (key, value string, ok bool) {
	parts := strings.Split(s, "=")
	if len(parts) != 2 {
		return
	}
	key, value, ok = parts[0], parts[1], true
	return
}

func HelperMultipleReturns() []any {
	q, err := divide(7, 2)
	_, zeroErr := divide(1, 0)
	k, v, ok := splitPair("a=1")
	_, _, bad := splitPair("nope")
	return []any{q, err == nil, zeroErr.Error(), k, v, ok, bad}
}

func HelperCrossFile() []any {
	return []any{shout("hi"), sumAll([]int{1, 2, 3}), formatPoint(Point{X: 1, Y: 2})}
}

const greeting = "hello"

func greet(name string) string { return greeting + ", " + name }

func HelperReadsConstant() string {
	return greet("go")
}

// Helpers get their own scope: a workflow local with a helper parameter's
// name stays untouched, and the struct argument is copied.
func movePoint(p Point, dx int) Point {
	p.X += dx
	return p
}

func HelperScopeAndCopy() []any {
	p := Point{X: 1, Y: 1}
	dx := 100
	moved := movePoint(p, 5)
	return []any{p.X, moved.X, dx}
}

func shadowedHelper(n int) int { return n + 1 }

func HelperShadowedByLocal() int {
	shadowedHelper := func(n int) int { return n * 10 }
	return shadowedHelper(2)
}

func applyTwice(f func(int) int, n int) int { return f(f(n)) }

func HelperTakesClosure() int {
	return applyTwice(func(n int) int { return double(n) + 1 }, 3)
}

func deferredLabel() (out string) {
	defer func() { out += "!" }()
	out = "done"
	return out
}

func HelperDeferNamedResult() string {
	return deferredLabel()
}

func panicky(n int) int {
	if n > 2 {
		panic("too big")
	}
	return n
}

func HelperPanics() int {
	return panicky(3)
}

func blankParams(_ int, _ string, n int) int { return n }

func HelperBlankParams() int {
	return blankParams(1, "x", 7)
}

type Money struct {
	Cents int
	Code  string
}

func (m Money) Add(cents int) Money {
	m.Cents += cents
	return m
}

func (m Money) String() string {
	return fmt.Sprintf("%d.%02d %s", m.Cents/100, m.Cents%100, m.Code)
}

func (m Money) Double() Money { return m.Add(m.Cents) }

func HelperValueMethods() []any {
	m := Money{Cents: 150, Code: "EUR"}
	more := m.Add(25)
	doubled := m.Double()
	return []any{m.Cents, more.Cents, more.String(), doubled.Cents}
}

type Counter struct{ N int }

func (c *Counter) Inc() { c.N++ }

func HelperPointerMethod() int {
	c := Counter{}
	c.Inc()
	return c.N
}

func Map[T any](xs []T, f func(T) T) []T {
	out := []T{}
	for _, x := range xs {
		out = append(out, f(x))
	}
	return out
}

func HelperGeneric() []int {
	return Map([]int{1, 2}, func(n int) int { return n + 1 })
}

func sum(xs ...int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}

func HelperVariadic() int {
	return sum(1, 2, 3)
}

var counter = 3

func readCounter() int { return counter }

func HelperPackageVariable() int {
	return readCounter()
}
