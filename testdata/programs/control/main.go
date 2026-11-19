package main

func main() int {
	sum := 0
	for i := 0; i < 5; i = i + 1 {
		if i%2 == 0 {
			sum = sum + i
		} else {
			sum = sum + 2
		}
	}
	switch sum {
	case 10:
		return 15
	default:
		return -1
	}
}
