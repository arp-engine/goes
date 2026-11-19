package eval_test

import (
	"path/filepath"
	"testing"

	"goes/eval"
)

const compileBenchSource = `package main
type pair struct {
	a int
	b int
}

func sum(xs []int) int {
	total := 0
	for _, v := range xs {
		total += v
	}
	return total
}

func main() int {
	p := pair{a: 1, b: 2}
	xs := []int{1, 2, 3, 4}
	if p.a < p.b {
		return sum(xs) + p.a + p.b
	}
	return 0
}
`

func BenchmarkCompileSourceFrontend(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		program, err := eval.CompileSource(compileBenchSource)
		if err != nil {
			b.Fatalf("CompileSource: %v", err)
		}
		if program == nil {
			b.Fatalf("CompileSource: got nil program")
		}
	}
}

func BenchmarkRunPathScalarFrontend(b *testing.B) {
	path := filepath.Join("..", "..", "testdata", "programs", "scalar")

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		result, err := eval.RunPath(path)
		if err != nil {
			b.Fatalf("RunPath: %v", err)
		}
		if got := result.Int(); got != 7 {
			b.Fatalf("RunPath result: want %d, got %d", 7, got)
		}
	}
}
