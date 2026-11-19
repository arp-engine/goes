package main

func main() int {
	m := make(map[string]int)
	m["a"] = 1
	m["b"] = 2
	m["c"] = 3
	delete(m, "b")

	sum := 0
	for _, v := range m {
		sum = sum + v
	}
	return sum
}
