//go:build go2wlangexample

// command: go run ./cmd/go2wl -func FeatureShowcase -pseudo go2wlang/examples/feature_showcase.go
package examples

type ShowcaseItem struct {
	Name  string
	Count int64
}

type ShowcaseCarrier struct {
	Item  ShowcaseItem
	Total int64
	Label string
}

func Identity[T any](v T) T { return v }

func FeatureShowcase(input any, value *int64, scores []int64, labels map[string]int64, ch chan string) string {
	if n := len(scores); n > 0 {
		scores[0] = +n
	}

	pair := ShowcaseItem{"start", 1}
	pair.Name = Identity[string](pair.Name)

	carrier := ShowcaseCarrier{
		Item:  pair,
		Total: *value,
		Label: "init",
	}
	carrier.Item.Count = -carrier.Item.Count

	keyed := []int64{2: 9}
	part := scores[0:1]
	copied := copy(scores, keyed)
	_ = part
	_ = copied

	val, ok := labels["primary"]
	if ok {
		labels["copy"] = val
		delete(labels, "primary")
	} else {
		labels["fallback"] = 1
	}

	switch {
	case ok:
		carrier.Label = "map"
	default:
		carrier.Label = "none"
	}

	switch val {
	case 1, 2:
		carrier.Total = carrier.Total + val
	default:
		carrier.Total = carrier.Total + int64(cap(scores))
	}

	s, typeOK := input.(string)
	if typeOK {
		carrier.Label = s
	}

	switch x := input.(type) {
	case string:
		carrier.Label = x
	case int64:
		carrier.Total = x
	default:
		carrier.Label = "unknown"
	}

	p := new(int64)
	deref := *value
	mask := ^val
	z := complex(1.0, 2.0)
	realPart := real(z)
	imagPart := imag(z)
	_ = p
	_ = deref
	_ = mask
	_ = realPart
	_ = imagPart

	go func(msg string) {
		ch <- msg
	}("ready")

	select {
	case got := <-ch:
		return got
	default:
		return carrier.Label
	}
}
