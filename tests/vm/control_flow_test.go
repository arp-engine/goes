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

func TestControlFlowProgramMatchesRequiredShape(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "programs", "control", "main.go")

	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	text := string(src)
	for _, want := range []string{
		"for i := 0; i < 5; i = i + 1",
		"if i%2 == 0",
		"} else {",
		"switch sum",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("control sample must contain %q", want)
		}
	}
}

func TestRunProgramExecutesControlFlow(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "programs", "control")

	result, err := eval.RunProgram(dir)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if result.Kind != runtime.Int {
		t.Fatalf("result kind: want %v, got %v", runtime.Int, result.Kind)
	}

	if got := result.Int(); got != 15 {
		t.Fatalf("result: want %d, got %d", 15, got)
	}
}

func TestControlProgramMatchesGo(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "programs", "control")
	if err := eval.RunAndCompare(dir); err != nil {
		t.Fatal(err)
	}
}

func TestRunProgramSupportsIntegerRange(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	sum := 0
	for i := range 5 {
		sum = sum + i
	}
	for j := range int64(3) {
		sum = sum + int(j)
	}
	for range 0 {
		sum = sum + 100
	}
	return sum
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 13 {
		t.Fatalf("result: want %d, got %d", 13, got)
	}
}

func TestControlFlowSwitchUsesDefaultWhenNoCaseMatches(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	value := 3
	switch value {
	case 10:
		return 1
	default:
		return 9
	}
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if got := result.Int(); got != 9 {
		t.Fatalf("result: want %d, got %d", 9, got)
	}
}

func TestControlFlowSupportsSwitchWithoutTag(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	x := 3
	switch {
	case x < 0:
		return 1
	case x < 5:
		return 2
	default:
		return 3
	}
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 2 {
		t.Fatalf("result: want %d, got %d", 2, got)
	}
}

func TestControlFlowSwitchAllowsUntypedCaseForConvertedTag(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	x := 2
	switch int64(x) {
	case 2:
		return 7
	default:
		return 0
	}
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 7 {
		t.Fatalf("result: want %d, got %d", 7, got)
	}
}

func TestControlFlowSwitchStopsEvaluatingAfterFirstMatchingCase(t *testing.T) {
	result, err := runSource(t, `package main
func explode() int {
	return 1 / 0
}

func main() int {
	switch 1 {
	case 1:
		return 7
	case explode():
		return 9
	default:
		return 0
	}
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 7 {
		t.Fatalf("result: want %d, got %d", 7, got)
	}
}

func TestControlFlowExpressionSwitchCaseListShortCircuitsAfterMatch(t *testing.T) {
	result, err := runSource(t, `package main
func explode() int {
	return 1 / 0
}

func main() int {
	switch 1 {
	case 1, explode():
		return 7
	default:
		return 0
	}
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 7 {
		t.Fatalf("result: want %d, got %d", 7, got)
	}
}

func TestControlFlowSupportsRangeAssignmentForm(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	m := make(map[string]int)
	m["a"] = 2
	m["bb"] = 3
	var k string
	var v int
	sum := 0
	for k, v = range m {
		sum = sum + len(k) + v
	}
	return sum
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 8 {
		t.Fatalf("result: want %d, got %d", 8, got)
	}
}

func TestControlFlowBreakExitsLoop(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	sum := 0
	for i := 0; i < 5; i = i + 1 {
		if i == 3 {
			break
		}
		sum = sum + 1
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

func TestControlFlowContinueSkipsIterationRemainder(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	sum := 0
	for i := 0; i < 4; i = i + 1 {
		if i%2 == 0 {
			continue
		}
		sum = sum + i
		sum = sum + 10
	}
	return sum
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if got := result.Int(); got != 24 {
		t.Fatalf("result: want %d, got %d", 24, got)
	}
}

func TestControlFlowSupportsIncDecInForPost(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	sum := 0
	for i := 0; i < 4; i++ {
		sum += i
	}
	for j := 3; j > 0; j-- {
		sum += j
	}
	return sum
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 12 {
		t.Fatalf("result: want %d, got %d", 12, got)
	}
}

func TestControlFlowNestedSwitchBreakAndContinueTargets(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	sum := 0
	for i := 0; i < 3; i = i + 1 {
		switch i {
		case 0:
			continue
		case 1:
			break
		default:
			sum = sum + 100
		}
		sum = sum + i
	}
	return sum
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if got := result.Int(); got != 103 {
		t.Fatalf("result: want %d, got %d", 103, got)
	}
}

func TestControlFlowSupportsSimpleIfElseWithBoolLocal(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	var flag bool = false
	if flag {
		return 1
	} else {
		return 2
	}
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 2 {
		t.Fatalf("result: want %d, got %d", 2, got)
	}
}

func TestCompileRejectsShortVarDeclWithoutNewNames(t *testing.T) {
	_, err := compileSourceWithoutSema(`package main
func main() int {
	x := 1
	x := 2
	return x
}
`)
	if err == nil {
		t.Fatalf("Compile: expected error")
	}
	if !strings.Contains(err.Error(), "at least one new variable") {
		t.Fatalf("Compile error: got %q", err)
	}
}

func TestCompileControlFlowUsesLocalAddIntConst(t *testing.T) {
	program, err := compileSourceWithoutSema(`package main
func main() int {
	sum := 0
	sum += 1
	return sum
}
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	code := program.Functions[program.Entry].Code
	if !hasOpcode(code, bytecode.OpLocalAddIntConst) {
		t.Fatalf("expected OpLocalAddIntConst in %v", code)
	}
}

func TestCompileControlFlowUsesLocalAddLocal(t *testing.T) {
	program, err := compileSourceWithoutSema(`package main
func main() int {
	sum := 0
	i := 3
	sum += i
	return sum
}
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	code := program.Functions[program.Entry].Code
	if !hasOpcode(code, bytecode.OpLocalAddLocal) {
		t.Fatalf("expected OpLocalAddLocal in %v", code)
	}
}

func TestCompileControlFlowUsesJumpIfLocalIntLtConstFalse(t *testing.T) {
	program, err := compileSourceWithoutSema(`package main
func main() int {
	sum := 0
	for i := 0; i < 4; i++ {
		sum += i
	}
	return sum
}
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !hasOpcode(program.Functions[program.Entry].Code, bytecode.OpJumpIfLocalIntLtConstFalse) {
		t.Fatalf("expected OpJumpIfLocalIntLtConstFalse in %v", program.Functions[program.Entry].Code)
	}
}

func TestCompileControlFlowUsesJumpIfLocalBitAndIntEqConstFalse(t *testing.T) {
	program, err := compileSourceWithoutSema(`package main
func main() int {
	sum := 0
	for i := 0; i < 4; i++ {
		if i&1 == 0 {
			sum += 1
		}
	}
	return sum
}
`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !hasOpcode(program.Functions[program.Entry].Code, bytecode.OpJumpIfLocalBitAndIntEqConstFalse) {
		t.Fatalf("expected OpJumpIfLocalBitAndIntEqConstFalse in %v", program.Functions[program.Entry].Code)
	}
}

func hasOpcode(code []bytecode.Instruction, want bytecode.Opcode) bool {
	for _, inst := range code {
		if inst.Op == want {
			return true
		}
	}
	return false
}

func compileSourceWithoutSema(src string) (*bytecode.Program, error) {
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
