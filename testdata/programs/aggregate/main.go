package main

type pair struct {
	a int
	b int
}

func bump(x *int) int {
	*x = *x + 2
	return 0
}

func main() int {
	p := pair{3, 4}
	x := p.a
	_ = bump(&x)
	s := []int{p.b, x}
	return s[0] + s[1]
}
