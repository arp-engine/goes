package main

func fib(n int) int {
	if n <= 1 {
		return n
	}
	return fib(n-1) + fib(n-2)
}

func main() int {
	for range 100000 {
		fib(10)
	}
	return 0
}
