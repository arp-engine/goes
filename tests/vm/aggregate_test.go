package vm_test

import (
	"path/filepath"
	"strings"
	"testing"

	"goes/eval"
	"goes/internal/bytecode"
	"goes/runtime"
)

func TestRunProgramSupportsAggregateObjects(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "programs", "aggregate")

	result, err := eval.RunProgram(dir)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if result.Kind != runtime.Int {
		t.Fatalf("result kind: want %v, got %v", runtime.Int, result.Kind)
	}

	if got := result.Int(); got != 9 {
		t.Fatalf("result: want %d, got %d", 9, got)
	}
}

func TestAggregateProgramMatchesGo(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "programs", "aggregate")
	if err := eval.RunAndCompare(dir); err != nil {
		t.Fatal(err)
	}
}

func TestRunProgramAggregateStructCopyDoesNotAliasOriginal(t *testing.T) {
	result, err := runSource(t, `package main
type pair struct {
	a int
	b int
}

func main() int {
	p := pair{1, 3}
	q := p
	q.a = 2
	return p.a + q.a + p.b
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if got := result.Int(); got != 6 {
		t.Fatalf("result: want %d, got %d", 6, got)
	}
}

func TestRunProgramSupportsNilPointerComparison(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	var p *int = nil
	if p == nil {
		return 1
	}
	return 0
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if got := result.Int(); got != 1 {
		t.Fatalf("result: want %d, got %d", 1, got)
	}
}

func TestRunProgramAggregateArrayCopyDoesNotAliasOriginal(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	a := [2]int{4, 5}
	b := a
	b[0] = 1
	return a[0] + b[0] + a[1]
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if got := result.Int(); got != 10 {
		t.Fatalf("result: want %d, got %d", 10, got)
	}
}

func TestRunProgramSupportsInt64ArrayAndSliceIndexes(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	a := [2]int{4, 5}
	xs := []int{7, 8}
	a[int64(0)] = a[int64(1)]
	xs[int64(0)] = xs[int64(1)]
	return a[int64(0)] + xs[int64(0)]
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if got := result.Int(); got != 13 {
		t.Fatalf("result: want %d, got %d", 13, got)
	}
}

func TestRunProgramAggregateAddressWritebackStillWorks(t *testing.T) {
	result, err := runSource(t, `package main
type pair struct {
	a int
}

func bump(x *int) int {
	*x = *x + 3
	return 0
}

func main() int {
	p := pair{1}
	_ = bump(&p.a)
	a := [1]int{2}
	_ = bump(&a[0])
	return p.a + a[0]
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if got := result.Int(); got != 9 {
		t.Fatalf("result: want %d, got %d", 9, got)
	}
}

func TestRunProgramAggregateIndirectScalarWritebackKeepsValueCopies(t *testing.T) {
	result, err := runSource(t, `package main
type pair struct {
	a int
}

func bump(x *int) int {
	*x = *x + 1
	return 0
}

func main() int {
	p := pair{1}
	q := p
	_ = bump(&q.a)
	return p.a + q.a
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 3 {
		t.Fatalf("result: want %d, got %d", 3, got)
	}
}

func TestRunProgramSupportsSliceExpressions(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	xs := [4]int{2, 4, 6, 8}
	ys := xs[1:3]
	return ys[0] + ys[1]
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 10 {
		t.Fatalf("result: want %d, got %d", 10, got)
	}
}

func TestRunProgramSupportsFullSliceExpressionSemantics(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	xs := [4]int{2, 4, 6, 8}
	ys := xs[:3]
	ys[1] = 40
	zs := ys[1:]
	ws := xs[0:2:3]
	ws = append(ws, 60)
	var nilSlice []int = nil
	empty := nilSlice[:]
	return xs[1] + zs[0] + ws[2] + len(empty)
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 140 {
		t.Fatalf("result: want %d, got %d", 140, got)
	}
}

func TestRunProgramSupportsKeyedStructLiterals(t *testing.T) {
	result, err := runSource(t, `package main
type pair struct {
	a int
	b int
}
func main() int {
	p := pair{b: 2, a: 1}
	return p.a + p.b
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 3 {
		t.Fatalf("result: want %d, got %d", 3, got)
	}
}

func TestRunProgramSupportsNestedArrayOfStructLiterals(t *testing.T) {
	result, err := runSource(t, `package main
type pair struct {
	a int
	b int
}
func main() int {
	xs := [2]pair{{1, 2}, {3, 4}}
	return xs[0].a + xs[0].b + xs[1].a + xs[1].b
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 10 {
		t.Fatalf("result: want %d, got %d", 10, got)
	}
}

func TestRunProgramSupportsNestedSliceOfStructLiterals(t *testing.T) {
	result, err := runSource(t, `package main
type pair struct {
	a int
	b int
}
func main() int {
	xs := []pair{{1, 2}, {3, 4}}
	return xs[0].a + xs[0].b + xs[1].a + xs[1].b
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 10 {
		t.Fatalf("result: want %d, got %d", 10, got)
	}
}

func TestCompileAggregateUsesStructFieldAddIntConst(t *testing.T) {
	program, err := compileSource(`package main
type pair struct {
	a int
}
func main() int {
	p := pair{1}
	p.a += 1
	return p.a
}
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !hasOpcodeAggregate(program.Functions[program.Entry].Code, bytecode.OpStructFieldAddIntConst) {
		t.Fatalf("expected OpStructFieldAddIntConst in %v", program.Functions[program.Entry].Code)
	}
}

func TestCompileAggregateUsesStructFieldAddLocal(t *testing.T) {
	program, err := compileSource(`package main
type pair struct {
	a int
	b int
}
func main() int {
	p := pair{1, 2}
	p.b += p.a
	return p.b
}
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	code := program.Functions[program.Entry].Code
	if !hasOpcodeAggregate(code, bytecode.OpStructFieldAddLocal) && !hasOpcodeAggregate(code, bytecode.OpStructFieldAddField) {
		t.Fatalf("expected OpStructFieldAddLocal or OpStructFieldAddField in %v", code)
	}
}

func TestCompileAggregateUsesArrayElemAddIntConst(t *testing.T) {
	program, err := compileSource(`package main
func main() int {
	xs := [2]int{1, 2}
	xs[0] += 3
	return xs[0]
}
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !hasOpcodeAggregate(program.Functions[program.Entry].Code, bytecode.OpArrayElemAddIntConst) {
		t.Fatalf("expected OpArrayElemAddIntConst in %v", program.Functions[program.Entry].Code)
	}
}

func TestCompileAggregateUsesArrayElemAddStructField(t *testing.T) {
	program, err := compileSource(`package main
type pair struct {
	a int
}
func main() int {
	p := pair{2}
	xs := [4]int{1, 2, 3, 4}
	i := 1
	xs[i&3] += p.a
	return xs[1]
}
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !hasOpcodeAggregate(program.Functions[program.Entry].Code, bytecode.OpArrayElemAddStructField) {
		t.Fatalf("expected OpArrayElemAddStructField in %v", program.Functions[program.Entry].Code)
	}
}

func TestCompileAggregateUsesLoadStructFieldLocal(t *testing.T) {
	program, err := compileSource(`package main
type pair struct {
	a int
}
func main() int {
	p := pair{3}
	return p.a
}
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !hasOpcodeAggregate(program.Functions[program.Entry].Code, bytecode.OpLoadStructFieldLocal) {
		t.Fatalf("expected OpLoadStructFieldLocal in %v", program.Functions[program.Entry].Code)
	}
}

func TestCompileAggregateUsesLoadArrayElemLocalConst(t *testing.T) {
	program, err := compileSource(`package main
func main() int {
	xs := [2]int{3, 4}
	return xs[1]
}
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !hasOpcodeAggregate(program.Functions[program.Entry].Code, bytecode.OpLoadArrayElemLocalConst) {
		t.Fatalf("expected OpLoadArrayElemLocalConst in %v", program.Functions[program.Entry].Code)
	}
}

func TestCompileAggregateUsesLocalAddStructField(t *testing.T) {
	program, err := compileSource(`package main
type pair struct {
	a int
}
func main() int {
	sum := 0
	p := pair{3}
	sum += p.a
	return sum
}
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !hasOpcodeAggregate(program.Functions[program.Entry].Code, bytecode.OpLocalAddStructField) {
		t.Fatalf("expected OpLocalAddStructField in %v", program.Functions[program.Entry].Code)
	}
}

func TestCompileAggregateUsesLocalAddArrayElemConst(t *testing.T) {
	program, err := compileSource(`package main
func main() int {
	sum := 0
	xs := [2]int{3, 4}
	sum += xs[1]
	return sum
}
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !hasOpcodeAggregate(program.Functions[program.Entry].Code, bytecode.OpLocalAddArrayElemConst) {
		t.Fatalf("expected OpLocalAddArrayElemConst in %v", program.Functions[program.Entry].Code)
	}
}

func hasOpcodeAggregate(code []bytecode.Instruction, want bytecode.Opcode) bool {
	for _, inst := range code {
		if inst.Op == want {
			return true
		}
	}
	return false
}

func TestRunProgramAggregateRejectsWrongArrayLiteralArity(t *testing.T) {
	_, err := compileSource(`package main
func main() int {
	a := [2]int{1}
	return a[0]
}
`)
	if err == nil {
		t.Fatalf("Compile: expected error")
	}
	if !strings.Contains(err.Error(), "composite literal has 1 elements for [2]int") {
		t.Fatalf("Compile error: got %q", err)
	}
}

func TestRunProgramAggregateRejectsAssignmentToSelectorOnNonAddressableBase(t *testing.T) {
	_, err := compileSource(`package main
type pair struct {
	a int
}
func main() int {
	pair{1}.a = 2
	return 0
}
`)
	if err == nil {
		t.Fatalf("Compile: expected error")
	}
	if !strings.Contains(err.Error(), "left side") && !strings.Contains(err.Error(), "addressable") {
		t.Fatalf("Compile error: got %q", err)
	}
}

func TestRunProgramAggregateRejectsAddressOfSelectorOnNonAddressableBase(t *testing.T) {
	_, err := compileSource(`package main
type pair struct {
	a int
}
func main() int {
	_ = &pair{1}.a
	return 0
}
`)
	if err == nil {
		t.Fatalf("Compile: expected error")
	}
	if !strings.Contains(err.Error(), "left side") && !strings.Contains(err.Error(), "addressable") {
		t.Fatalf("Compile error: got %q", err)
	}
}

func TestRunProgramAggregateRejectsAssignmentToIndexOnNonAddressableBase(t *testing.T) {
	_, err := compileSource(`package main
func main() int {
	[1]int{1}[0] = 2
	return 0
}
`)
	if err == nil {
		t.Fatalf("Compile: expected error")
	}
	if !strings.Contains(err.Error(), "left side") && !strings.Contains(err.Error(), "addressable") {
		t.Fatalf("Compile error: got %q", err)
	}
}

func TestRunProgramAggregateCompilerAcceptsConstArrayLengths(t *testing.T) {
	result, err := runSource(t, `package main
const N = 2
func main() int {
	a := [1+1]int{3, 4}
	b := [N]int{1, 2}
	return a[0] + a[1] + b[0] + b[1]
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 10 {
		t.Fatalf("result: want %d, got %d", 10, got)
	}
}

func TestRunProgramSupportsRangeOverSlice(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	xs := []int{2, 4, 6}
	sum := 0
	for i, v := range xs {
		sum = sum + i + v
	}
	return sum
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 15 {
		t.Fatalf("result: want %d, got %d", 15, got)
	}
}

func TestRunProgramSupportsRangeOverArrayKeyOnly(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	xs := [3]int{2, 4, 6}
	sum := 0
	for i := range xs {
		sum = sum + i
	}
	return sum
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 3 {
		t.Fatalf("result: want %d, got %d", 3, got)
	}
}

func TestRunProgramSupportsRangeOverSliceAssignment(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	xs := []int{3, 5}
	i := 0
	v := 0
	sum := 0
	for i, v = range xs {
		sum = sum + i + v
	}
	return sum
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 9 {
		t.Fatalf("result: want %d, got %d", 9, got)
	}
}

func TestRunProgramRangeOverSliceReadsCurrentElementValues(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	xs := []int{1, 2, 3}
	sum := 0
	for i, v := range xs {
		sum = sum + v
		if i == 0 {
			xs[1] = 20
			xs[2] = 30
		}
	}
	return sum
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 51 {
		t.Fatalf("result: want %d, got %d", 51, got)
	}
}

func TestRunProgramSupportsMakeSliceAndAppend(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	xs := make([]int, 2)
	xs[0] = 5
	xs[1] = 7
	xs = append(xs, 11, 13)
	ys := make([]int, 0, 8)
	ys = append(ys, xs...)
	return len(xs) + len(ys) + ys[0] + ys[3]
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 26 {
		t.Fatalf("result: want %d, got %d", 26, got)
	}
}

func TestRunProgramSupportsAppendToNilSlice(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	var xs []int = nil
	xs = append(xs, 3)
	xs = append(xs, 4, 5)
	return len(xs) + xs[0] + xs[2]
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 11 {
		t.Fatalf("result: want %d, got %d", 11, got)
	}
}

func TestRunFunctionConvertsUntypedConstantsForInt64AppendElements(t *testing.T) {
	program, err := eval.CompileSource(`package main
func one() int64 {
	var xs []int64
	xs = append(xs, 1)
	return xs[0]
}

func main() int {
	return 0
}
`)
	if err != nil {
		t.Fatalf("CompileSource: %v", err)
	}

	results, err := eval.RunFunctionValues(program, "one")
	if err != nil {
		t.Fatalf("RunFunctionValues: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("result count: want %d, got %d", 1, len(results))
	}
	if results[0].Kind != runtime.Int64 {
		t.Fatalf("result kind: want %v, got %v", runtime.Int64, results[0].Kind)
	}
	if got := results[0].Int(); got != 1 {
		t.Fatalf("result: want %d, got %d", 1, got)
	}
}

func TestRunProgramSupportsMakeSliceOfStruct(t *testing.T) {
	result, err := runSource(t, `package main
type pair struct {
	a int
	b int
}

func main() int {
	xs := make([]pair, 1, 3)
	xs = append(xs, pair{3, 4})
	return len(xs) + xs[0].a + xs[1].a + xs[1].b
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 9 {
		t.Fatalf("result: want %d, got %d", 9, got)
	}
}

func TestRunProgramSupportsClearSlice(t *testing.T) {
	result, err := runSource(t, `package main
type pair struct {
	a int
	b int
}

func main() int {
	xs := []int{3, 5, 7}
	clear(xs)
	ps := []pair{pair{2, 3}}
	clear(ps)
	var nilSlice []int = nil
	clear(nilSlice)
	return len(xs) + xs[0] + xs[1] + xs[2] + ps[0].a + ps[0].b + len(nilSlice)
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 3 {
		t.Fatalf("result: want %d, got %d", 3, got)
	}
}

func TestRunProgramSupportsCompoundAssignmentOnFieldAndIndex(t *testing.T) {
	result, err := runSource(t, `package main
type pair struct {
	a int
}

func main() int {
	p := pair{1}
	xs := [2]int{3, 4}
	p.a += 4
	xs[1] *= 2
	xs[0]++
	return p.a + xs[0] + xs[1]
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 17 {
		t.Fatalf("result: want %d, got %d", 17, got)
	}
}
