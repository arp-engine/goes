package vm_test

import (
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goes/eval"
	"goes/internal/bytecode"
	"goes/internal/compiler"
	"goes/internal/sema"
	"goes/runtime"
)

func TestRunProgramReturnsScalarResult(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "programs", "scalar")

	result, err := eval.RunProgram(dir)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if result.Kind != runtime.Int {
		t.Fatalf("result kind: want %v, got %v", runtime.Int, result.Kind)
	}

	if got := result.Int(); got != 7 {
		t.Fatalf("result: want %d, got %d", 7, got)
	}
}

func TestScalarProgramMatchesGo(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "programs", "scalar")
	if err := eval.RunAndCompare(dir); err != nil {
		t.Fatal(err)
	}
}

func TestRunProgramNestedBlockShadowingKeepsOuterValue(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	x := 1
	{
		x := 2
		_ = x
	}
	return x
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if got := result.Int(); got != 1 {
		t.Fatalf("result: want %d, got %d", 1, got)
	}
}

func TestRunProgramInnerVarDoesNotConflictWithOuterName(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	x := 1
	{
		var x int = 2
		_ = x
	}
	return x
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if got := result.Int(); got != 1 {
		t.Fatalf("result: want %d, got %d", 1, got)
	}
}

func TestRunProgramSupportsBoolLogicAndUntypedBoolConditions(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	if !false && (true || false) {
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

func TestRunProgramSupportsStringConcatAndCompare(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	s := "go" + "es"
	if s == "goes" && "a" < "b" {
		return len(s)
	}
	return 0
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 4 {
		t.Fatalf("result: want %d, got %d", 4, got)
	}
}

func TestRunProgramSupportsFloat64ArithmeticAndComparison(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	x := -1.5
	y := 2.0
	if x+y < 1.0 {
		return 7
	}
	return 0
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 7 {
		t.Fatalf("result: want %d, got %d", 7, got)
	}
}

func DisabledDuplicateRunProgramSupportsConstArrayLengthExpressions(t *testing.T) {
	result, err := runSource(t, `package main
const N = 1 + 2

func main() int {
	xs := [N]int{1, 2, 3}
	return xs[0] + xs[1] + xs[2]
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 6 {
		t.Fatalf("result: want %d, got %d", 6, got)
	}
}

func DisabledDuplicateRunProgramSupportsPackageVarTypeInferenceFromCallAndSelector(t *testing.T) {
	result, err := runSource(t, `package main
type pair struct { a int }

func makePair() pair {
	return pair{a: 4}
}

var p = makePair()
var x = p.a

func main() int {
	return x
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 4 {
		t.Fatalf("result: want %d, got %d", 4, got)
	}
}

func TestRunProgramSupportsConstArrayLengthExpressions(t *testing.T) {
	result, err := runSource(t, `package main
const N = 1 + 2

func main() int {
	xs := [N]int{1, 2, 3}
	return xs[0] + xs[1] + xs[2]
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 6 {
		t.Fatalf("result: want %d, got %d", 6, got)
	}
}

func TestRunProgramSupportsPackageVarTypeInferenceFromCallAndSelector(t *testing.T) {
	result, err := runSource(t, `package main
type pair struct { a int }

func makePair() pair {
	return pair{a: 4}
}

var p = makePair()
var x = p.a

func main() int {
	return x
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 4 {
		t.Fatalf("result: want %d, got %d", 4, got)
	}
}

func TestRunProgramSupportsInt64Arithmetic(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	var x int64 = 5
	var y int64 = 2
	if x%y == 1 {
		return 9
	}
	return 0
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 9 {
		t.Fatalf("result: want %d, got %d", 9, got)
	}
}

func TestRunProgramSupportsCompoundAssignmentsAndIncDec(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	i := 3
	var j int64 = 10
	var f float64 = 1.5
	s := "go"
	i += 2
	i *= 3
	i /= 3
	i %= 4
	i |= 8
	i &= 11
	i ^= 3
	i &^= 2
	i <<= 1
	i >>= 2
	j -= 4
	f += 0.5
	s += "es"
	i++
	j--
	if s == "goes" && f == 2.0 && j == 5 {
		return i
	}
	return 0
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 5 {
		t.Fatalf("result: want %d, got %d", 5, got)
	}
}

func TestRunProgramSupportsMultiReturnAssignmentAndForwarding(t *testing.T) {
	result, err := runSource(t, `package main
func pair() (int, int) { return 1, 2 }
func wrap() (int, int) { return pair() }
func main() int {
	a, b := wrap()
	return a + b
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 3 {
		t.Fatalf("result: want %d, got %d", 3, got)
	}
}

func TestRunProgramSupportsVariadicCalls(t *testing.T) {
	result, err := runSource(t, `package main
func sum(xs ...int) int {
	total := 0
	for _, v := range xs {
		total += v
	}
	return total
}
func main() int {
	return sum(1, 2, 3)
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 6 {
		t.Fatalf("result: want %d, got %d", 6, got)
	}
}

func TestRunProgramSupportsVariadicSliceExpansion(t *testing.T) {
	result, err := runSource(t, `package main
func sum(xs ...int) int {
	total := 0
	for _, v := range xs {
		total += v
	}
	return total
}
func main() int {
	xs := []int{1, 2, 3}
	return sum(xs...)
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 6 {
		t.Fatalf("result: want %d, got %d", 6, got)
	}
}

func TestRunProgramSupportsBitwiseAndShiftExpressions(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	a := 6 & 3
	b := 1 | 4
	c := b ^ 3
	d := c &^ 1
	e := d << 2
	f := e >> 1
	return a + b + c + d + f
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 31 {
		t.Fatalf("result: want %d, got %d", 31, got)
	}
}

func TestRunProgramSupportsExplicitNumericConversions(t *testing.T) {
	result, err := runSource(t, `package main
type local int64

func main() int {
	var x int = 7
	var y int64 = int64(x)
	var z float64 = float64(y) + 0.9
	var named local = local(z)
	ch := string(int64(65))
	if ch == "65" {
		return int(named) + len(ch)
	}
	return 0
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 9 {
		t.Fatalf("result: want %d, got %d", 9, got)
	}
}

func TestRunProgramSupportsExplicitStringNumericConversions(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	i := int("41")
	i64 := int64("42")
	f := float64("7.75")
	si := string(i)
	s64 := string(i64)
	sf := string(f)
	if si == "41" && s64 == "42" && sf == "7.75" {
		return i + int(i64) + int(f) + len(si) + len(s64) + len(sf)
	}
	return 0
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 98 {
		t.Fatalf("result: want %d, got %d", 98, got)
	}
}

func TestRunProgramSupportsNestedScalarConversions(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	a := string(int64(float64(int("12"))))
	b := string(float64(int64("34")))
	c := int(string(int64(float64("56.9"))))
	d := int64(string(int(float64("78.9"))))
	e := float64(string(int64(float64(int("2")))))
	if a == "12" && b == "34" && c == 56 && d == 78 && e == 2.0 {
		return len(a) + len(b) + c + int(d) + int(e)
	}
	return 0
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 140 {
		t.Fatalf("result: want %d, got %d", 140, got)
	}
}

func TestRunProgramAllowsUntypedConstantsInInt64AssignmentAndReturn(t *testing.T) {
	result, err := runSource(t, `package main
func one() int64 {
	return 1
}

func main() int {
	var x int64
	x = 2
	return int(x + one())
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 3 {
		t.Fatalf("result: want %d, got %d", 3, got)
	}
}

func TestRunFunctionPreservesDeclaredInt64ForUntypedReturn(t *testing.T) {
	program, err := eval.CompileSource(`package main
func one() int64 {
	return 1
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

func TestRunFunctionConvertsUntypedConstantsForInt64Parameters(t *testing.T) {
	program, err := eval.CompileSource(`package main
func id(x int64) int64 {
	return x
}

func one() int64 {
	return id(1)
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

func TestRunFunctionConvertsUntypedConstantsForInt64MethodParameters(t *testing.T) {
	program, err := eval.CompileSource(`package main
type counter int

func (c counter) id(x int64) int64 {
	return x
}

func one() int64 {
	var c counter
	return c.id(1)
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

func TestRunFunctionConvertsUntypedConstantsForInt64FunctionValueParameters(t *testing.T) {
	program, err := eval.CompileSource(`package main
func id(x int64) int64 {
	return x
}

func one() int64 {
	f := id
	return f(1)
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

func TestRunProgramSupportsPackageVarAndLocalConstTypeFunc(t *testing.T) {
	result, err := runSource(t, `package main
var seed int = 3

func main() int {
	const inc = 2
	type local int
	funcAdd := func(x int) int {
		type another int
		_ = another(0)
		return x + inc
	}
	var x local = local(seed)
	return funcAdd(int(x))
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 5 {
		t.Fatalf("result: want %d, got %d", 5, got)
	}
}

func DisabledDuplicateRunProgramSupportsLocalVarDeclAndAssignment(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	var x int = 2
	x = x + 3
	return x
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 5 {
		t.Fatalf("result: want %d, got %d", 5, got)
	}
}

func TestRunProgramSupportsLocalVarDeclAndAssignment(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	var x int = 2
	x = x + 3
	return x
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 5 {
		t.Fatalf("result: want %d, got %d", 5, got)
	}
}

func TestCompileRejectsMainWithParameters(t *testing.T) {
	_, err := compileSource(`package main
func main(x int) int {
	return x
}
`)
	if err == nil {
		t.Fatalf("Compile: expected error")
	}
	if !strings.Contains(err.Error(), `entry function "main" must have signature func() int`) {
		t.Fatalf("Compile error: got %q", err)
	}
}

func TestCompileAcceptsBareFunctionValuesInV2(t *testing.T) {
	program, err := compileSource(`package main
func helper() int {
	return 1
}
func main() int {
	_ = helper
	return 0
}
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !hasOpcodeScalar(program.Functions[program.Entry].Code, bytecode.OpMakeFuncValue) {
		t.Fatalf("expected OpMakeFuncValue in %v", program.Functions[program.Entry].Code)
	}
}

func TestCompileAcceptsBareMethodValuesInV2(t *testing.T) {
	program, err := compileSource(`package main
type pair struct {
	a int
}
func (p pair) value() int {
	return p.a
}
func main() int {
	p := pair{1}
	_ = p.value
	return 0
}
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !hasOpcodeScalar(program.Functions[program.Entry].Code, bytecode.OpMakeMethodValue) {
		t.Fatalf("expected OpMakeMethodValue in %v", program.Functions[program.Entry].Code)
	}
}

func TestCompileRejectsNonCallExpressionStatementsUnsupportedByV1(t *testing.T) {
	_, err := compileSource(`package main
func main() int {
	1 + 2
	return 0
}
`)
	if err == nil {
		t.Fatalf("Compile: expected error")
	}
	if !strings.Contains(err.Error(), "expression statement must be a call") &&
		!strings.Contains(err.Error(), "expression statements are not supported yet") {
		t.Fatalf("Compile error: got %q", err)
	}
}

func TestRunProgramSupportsNamedFunctionValues(t *testing.T) {
	result, err := runSource(t, `package main
func add1(x int) int {
	return x + 1
}

func main() int {
	f := add1
	return f(3)
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 4 {
		t.Fatalf("result: want %d, got %d", 4, got)
	}
}

func TestCompileSupportsNamedFunctionValues(t *testing.T) {
	program, err := compileSource(`package main
func add1(x int) int {
	return x + 1
}

func main() int {
	f := add1
	return f(3)
}
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !hasOpcodeScalar(program.Functions[program.Entry].Code, bytecode.OpMakeFuncValue) {
		t.Fatalf("expected OpMakeFuncValue in %v", program.Functions[program.Entry].Code)
	}
}

func hasOpcodeScalar(code []bytecode.Instruction, want bytecode.Opcode) bool {
	for _, inst := range code {
		if inst.Op == want {
			return true
		}
	}
	return false
}

func runSource(t *testing.T, src string) (runtime.Value, error) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	return eval.RunProgram(dir)
}

func compileSource(src string) (*bytecode.Program, error) {
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	checked, err := sema.CheckGoPackage(fset, []*goast.File{file})
	if err != nil {
		return nil, err
	}

	return compiler.CompileChecked(checked)
}
