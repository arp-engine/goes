package main

func main() int {
	x := 1
	f := func() int {
		return x
	}
	x = 5
	return f()
}
