package main

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/traefik/yaegi/interp"

	"goes/eval"
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
	sum := 0
	for i := 0; i < 100000; i++ {
		sum += fib(10)
	}
	return sum
}`

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
}`

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
}`

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
}`

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
}`

func fib(n int) int {
	if n <= 1 {
		return n
	}
	return fib(n-1) + fib(n-2)
}

func nativeFibonacciProgram() int {
	sum := 0
	for i := 0; i < 100000; i++ {
		sum += fib(10)
	}
	return sum
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

func nativeRangeProgram() int {
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

type timings struct {
	compile, run, total time.Duration
}

func runGoes(src string) (int, timings, error) {
	start := time.Now()
	program, err := eval.CompileSource(src)
	compiled := time.Now()
	if err != nil {
		return 0, timings{}, fmt.Errorf("goes compile: %w", err)
	}
	result, err := vm.Run(program)
	end := time.Now()
	if err != nil {
		return 0, timings{}, fmt.Errorf("goes run: %w", err)
	}
	return int(result.Int()), timings{compiled.Sub(start), end.Sub(compiled), end.Sub(start)}, nil
}

func runYaegi(src string) (int, timings, error) {
	engine := interp.New(interp.Options{})
	start := time.Now()
	program, err := engine.Compile(src)
	compiled := time.Now()
	if err != nil {
		return 0, timings{}, fmt.Errorf("yaegi compile: %w", err)
	}
	if _, err := engine.Execute(program); err != nil {
		return 0, timings{}, fmt.Errorf("yaegi load: %w", err)
	}
	entry, err := engine.Eval("bench.Run")
	if err != nil {
		return 0, timings{}, fmt.Errorf("yaegi resolve entry: %w", err)
	}
	if !entry.IsValid() {
		return 0, timings{}, fmt.Errorf("yaegi entry is missing")
	}
	run, ok := entry.Interface().(func() int)
	if !ok {
		return 0, timings{}, fmt.Errorf("yaegi entry has type %s, want func() int", entry.Type())
	}
	ready := time.Now()
	result := run()
	end := time.Now()
	return result, timings{compiled.Sub(start), end.Sub(ready), end.Sub(start)}, nil
}

func medianNS(samples []time.Duration) int64 {
	slices.Sort(samples)
	mid := len(samples) / 2
	if len(samples)%2 == 0 {
		return (samples[mid-1] + (samples[mid]-samples[mid-1])/2).Nanoseconds()
	}
	return samples[mid].Nanoseconds()
}

func runBenchmark(name, src string, nativeFn func() int, samples int) error {
	// A non-main package lets Yaegi load the program without running the workload.
	yaegiSrc := strings.NewReplacer("package main", "package bench", "func main()", "func Run()").Replace(src)
	nativeTimes := make([]time.Duration, 0, samples)
	var goesCompile, goesRun, goesTotal []time.Duration
	var yaegiCompile, yaegiRun, yaegiTotal []time.Duration
	var nativeResult, goesResult, yaegiResult int
	for i := 0; i < samples; i++ {
		start := time.Now()
		nativeResult = nativeFn()
		nativeTimes = append(nativeTimes, time.Since(start))

		g, gt, err := runGoes(src)
		if err != nil {
			return err
		}
		y, yt, err := runYaegi(yaegiSrc)
		if err != nil {
			return err
		}
		goesResult, yaegiResult = g, y
		if nativeResult != goesResult || nativeResult != yaegiResult {
			return fmt.Errorf("sample %d: native=%d goes_result=%d yaegi_result=%d", i+1, nativeResult, goesResult, yaegiResult)
		}
		goesCompile, goesRun, goesTotal = append(goesCompile, gt.compile), append(goesRun, gt.run), append(goesTotal, gt.total)
		yaegiCompile, yaegiRun, yaegiTotal = append(yaegiCompile, yt.compile), append(yaegiRun, yt.run), append(yaegiTotal, yt.total)
	}

	fmt.Printf("%s native=%d goes_compile=%d goes_run=%d goes_compile_run=%d yaegi_compile=%d yaegi_run=%d yaegi_compile_run=%d native_result=%d goes_result=%d yaegi_result=%d\n",
		name, medianNS(nativeTimes), medianNS(goesCompile), medianNS(goesRun), medianNS(goesTotal),
		medianNS(yaegiCompile), medianNS(yaegiRun), medianNS(yaegiTotal), nativeResult, goesResult, yaegiResult)
	return nil
}

func main() {
	samples := flag.Int("samples", 5, "number of samples per workload")
	flag.Parse()
	if *samples < 1 {
		fmt.Fprintln(os.Stderr, "-samples must be positive")
		os.Exit(2)
	}

	benchmarks := []struct {
		name   string
		source string
		native func() int
	}{
		{"fibonacci", fibonacciBenchSource, nativeFibonacciProgram},
		{"control", controlBenchSource, nativeControlProgram},
		{"aggregate", aggregateBenchSource, nativeAggregateProgram},
		{"map", mapBenchSource, nativeMapProgram},
		{"range", rangeBenchSource, nativeRangeProgram},
	}
	fmt.Printf("Median over %d samples (ns per workload)\n", *samples)
	for _, b := range benchmarks {
		if err := runBenchmark(b.name, b.source, b.native, *samples); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", b.name, err)
			os.Exit(1)
		}
	}
}
