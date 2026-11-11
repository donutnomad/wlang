package differential

import (
	"strconv"
	up "strings"
)

// Helpers declared in another file of the package, with their own imports.

func shout(s string) string { return up.ToUpper(s) + "!" }

func sumAll(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}

func formatPoint(p Point) string {
	return "(" + strconv.Itoa(p.X) + "," + strconv.Itoa(p.Y) + ")"
}
