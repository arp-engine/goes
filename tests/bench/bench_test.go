package bench_test

import (
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"testing"

	"goes/eval"
	"goes/internal/bytecode"
	"goes/internal/compiler"
	"goes/internal/sema"
	"goes/internal/vm"
)

const fibonacciBenchSource = `package main
func fib(n int) int {
	if n <= 1 {
		return n
	}
	return fib(n-1) + fib(n-2)
}

func main() int {
	for i := 0; i < 100000; i++ {
		fib(10)
	}
	return 0
}
`

const controlBenchSource = `package main
func main() int {
	sum := 0
	for i := 0; i < 2000000; i++ {
		if i & 1 == 0 {
			sum += i
		} else {
			sum -= 1
		}
	}
	return sum
}
`

const aggregateBenchSource = `package main
type pair struct {
	a int
	b int
}

func main() int {
	p := pair{1, 2}
	xs := [4]int{1, 2, 3, 4}
	sum := 0
	for i := 0; i < 1000000; i++ {
		p.a += 1
		p.b += p.a
		xs[i & 3] += p.a
		sum += xs[0] + xs[1] + xs[2] + xs[3]
	}
	return sum + p.a + p.b
}
`

const mapBenchSource = `package main
func main() int {
	m := make(map[string]int)
	m["a"] = 1
	m["b"] = 2
	m["c"] = 3
	sum := 0
	for i := 0; i < 500000; i++ {
		m["a"] += 1
		m["b"] += m["a"]
		sum += m["a"] + m["b"] + m["c"]
	}
	return sum
}
`

const rangeBenchSource = `package main
func main() int {
	xs := []int{1, 2, 3, 4, 5, 6, 7, 8}
	arr := [4]int{3, 5, 7, 11}
	m := make(map[string]int)
	m["a"] = 1
	m["bb"] = 2
	m["ccc"] = 3
	sum := 0
	for n := 0; n < 200000; n++ {
		for i, v := range xs {
			sum += i + v
		}
		for i, v := range arr {
			sum += i + v
		}
		for k, v := range m {
			sum += len(k) + v
		}
	}
	return sum
}
`

func fib(n int) int {
	if n <= 1 {
		return n
	}
	return fib(n-1) + fib(n-2)
}

func nativeFibonacciProgram() int {
	for i := 0; i < 100000; i++ {
		fib(10)
	}
	return 0
}

func nativeControlProgram() int {
	sum := 0
	for i := 0; i < 2000000; i++ {
		if i&1 == 0 {
			sum += i
		} else {
			sum -= 1
		}
	}
	return sum
}

func nativeAggregateProgram() int {
	type pair struct {
		a int
		b int
	}
	p := pair{1, 2}
	xs := [4]int{1, 2, 3, 4}
	sum := 0
	for i := 0; i < 1000000; i++ {
		p.a += 1
		p.b += p.a
		xs[i&3] += p.a
		sum += xs[0] + xs[1] + xs[2] + xs[3]
	}
	return sum + p.a + p.b
}

func nativeMapProgram() int {
	m := map[string]int{
		"a": 1,
		"b": 2,
		"c": 3,
	}
	sum := 0
	for i := 0; i < 500000; i++ {
		m["a"] += 1
		m["b"] += m["a"]
		sum += m["a"] + m["b"] + m["c"]
	}
	return sum
}

func nativeRangeProgram() int {
	xs := []int{1, 2, 3, 4, 5, 6, 7, 8}
	arr := [4]int{3, 5, 7, 11}
	m := map[string]int{
		"a":   1,
		"bb":  2,
		"ccc": 3,
	}
	sum := 0
	for n := 0; n < 200000; n++ {
		for i, v := range xs {
			sum += i + v
		}
		for i, v := range arr {
			sum += i + v
		}
		for k, v := range m {
			sum += len(k) + v
		}
	}
	return sum
}

func compileProgramSource(b testing.TB, src string) *bytecode.Program {
	b.Helper()
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "bench.go", src, goparser.SkipObjectResolution)
	if err != nil {
		b.Fatalf("ParseFile: %v", err)
	}
	checked, err := sema.CheckGoPackage(fset, []*goast.File{file})
	if err != nil {
		b.Fatalf("Check: %v", err)
	}

	program, err := compiler.CompileChecked(checked)
	if err != nil {
		b.Fatalf("Compile: %v", err)
	}
	return program
}

func benchmarkNative(b *testing.B, fn func() int) {
	b.Helper()
	for i := 0; i < b.N; i++ {
		_ = fn()
	}
}

func benchmarkVMOnly(b *testing.B, src string) {
	b.Helper()
	program := compileProgramSource(b, src)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.Run(program); err != nil {
			b.Fatalf("vm.Run: %v", err)
		}
	}
}

func benchmarkCompileAndRun(b *testing.B, src string) {
	b.Helper()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		program, err := eval.CompileSource(src)
		if err != nil {
			b.Fatalf("CompileSource: %v", err)
		}
		if _, err := vm.Run(program); err != nil {
			b.Fatalf("vm.Run: %v", err)
		}
	}
}

func BenchmarkNativeFibonacci(b *testing.B)    { benchmarkNative(b, nativeFibonacciProgram) }
func BenchmarkGoesVMRunFibonacci(b *testing.B) { benchmarkVMOnly(b, fibonacciBenchSource) }
func BenchmarkGoesRunProgramFibonacci(b *testing.B) {
	benchmarkCompileAndRun(b, fibonacciBenchSource)
}

func BenchmarkNativeControl(b *testing.B)    { benchmarkNative(b, nativeControlProgram) }
func BenchmarkGoesVMRunControl(b *testing.B) { benchmarkVMOnly(b, controlBenchSource) }
func BenchmarkGoesRunProgramControl(b *testing.B) {
	benchmarkCompileAndRun(b, controlBenchSource)
}

func BenchmarkNativeAggregate(b *testing.B)    { benchmarkNative(b, nativeAggregateProgram) }
func BenchmarkGoesVMRunAggregate(b *testing.B) { benchmarkVMOnly(b, aggregateBenchSource) }
func BenchmarkGoesRunProgramAggregate(b *testing.B) {
	benchmarkCompileAndRun(b, aggregateBenchSource)
}

func BenchmarkNativeMap(b *testing.B)    { benchmarkNative(b, nativeMapProgram) }
func BenchmarkGoesVMRunMap(b *testing.B) { benchmarkVMOnly(b, mapBenchSource) }
func BenchmarkGoesRunProgramMap(b *testing.B) {
	benchmarkCompileAndRun(b, mapBenchSource)
}

func BenchmarkNativeRange(b *testing.B)    { benchmarkNative(b, nativeRangeProgram) }
func BenchmarkGoesVMRunRange(b *testing.B) { benchmarkVMOnly(b, rangeBenchSource) }
func BenchmarkGoesRunProgramRange(b *testing.B) {
	benchmarkCompileAndRun(b, rangeBenchSource)
}
