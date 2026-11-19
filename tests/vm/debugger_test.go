package vm_test

import (
	"strings"
	"testing"

	"goes/eval"
	"goes/runtime"
)

func TestGlobalDebuggerBreakpointsCallTrapAtGoesBreakpoint(t *testing.T) {
	program, err := eval.CompileSource(`package main
func main() int {
	x := 2
	y := x + 3
	return y
}
`)
	if err != nil {
		t.Fatalf("CompileSource: %v", err)
	}

	runtime.ClearBreakpoints()
	defer runtime.ClearBreakpoints()
	defer runtime.SetTrapHandler(nil)
	var traps []*runtime.Trap
	runtime.SetTrapHandler(func(trap *runtime.Trap) {
		traps = append(traps, trap)
	})
	if err := runtime.SetBreakpoint("main.go", 4); err != nil {
		t.Fatalf("SetBreakpoint: %v", err)
	}

	result, err := eval.RunFunctionSourceWithDebug(program, "main", true)
	if err != nil {
		t.Fatalf("RunFunctionSource: %v", err)
	}
	if got := result.Int(); got != 5 {
		t.Fatalf("result = %d, want 5", got)
	}
	if len(traps) != 1 {
		t.Fatalf("trap count = %d, want 1", len(traps))
	}
	trap := traps[0]
	if trap.File != "main.go" || trap.Line != 4 || trap.Function != "main" {
		t.Fatalf("trap location = %s:%d %s, want main.go:4 main", trap.File, trap.Line, trap.Function)
	}
	if !strings.Contains(trap.SourceLine, "y := x + 3") {
		t.Fatalf("trap source line = %q, want assignment", trap.SourceLine)
	}
	assertTrapVariable(t, trap.Locals, "x", "int", "int=2")
}

func TestGlobalDebuggerBreakpointsUseExactGoesFilename(t *testing.T) {
	program, err := eval.CompileSourceFile("main.goes", `package main
func main() int {
	x := 2
	y := x + 3
	return y
}
`)
	if err != nil {
		t.Fatalf("CompileSourceFile: %v", err)
	}

	runtime.ClearBreakpoints()
	defer runtime.ClearBreakpoints()
	defer runtime.SetTrapHandler(nil)
	var traps []*runtime.Trap
	runtime.SetTrapHandler(func(trap *runtime.Trap) {
		traps = append(traps, trap)
	})
	if err := runtime.SetBreakpoint("main.goes", 4); err != nil {
		t.Fatalf("SetBreakpoint: %v", err)
	}

	result, err := eval.RunFunctionSourceWithDebug(program, "main", true)
	if err != nil {
		t.Fatalf("RunFunctionSource: %v", err)
	}
	if got := result.Int(); got != 5 {
		t.Fatalf("result = %d, want 5", got)
	}
	if len(traps) != 1 {
		t.Fatalf("trap count = %d, want 1", len(traps))
	}
	trap := traps[0]
	if trap.File != "main.goes" || trap.Line != 4 {
		t.Fatalf("trap location = %s:%d, want main.goes:4", trap.File, trap.Line)
	}
}

func TestGlobalDebuggerBreakpointsCallTrapInsideHook(t *testing.T) {
	type SkillState struct {
		Counter int
	}
	type CastContext struct {
		Level int
	}

	module := eval.MustCompileHookModule(`package main
type SkillState struct { Counter int }
type CastContext struct { Level int }

func OnCast(state SkillState, ctx CastContext) SkillState {
	state.Counter = state.Counter + ctx.Level
	return state
}

func main() int {
	return 0
}
`, nil)

	runtime.ClearBreakpoints()
	defer runtime.ClearBreakpoints()
	defer runtime.SetTrapHandler(nil)
	var traps []*runtime.Trap
	runtime.SetTrapHandler(func(trap *runtime.Trap) {
		traps = append(traps, trap)
	})
	if err := runtime.SetBreakpoint("main.go", 6); err != nil {
		t.Fatalf("SetBreakpoint: %v", err)
	}

	stateAny, err := eval.InvokeHookWithDebug(module, "OnCast", SkillState{Counter: 1}, CastContext{Level: 4}, nil, true)
	if err != nil {
		t.Fatalf("InvokeHook: %v", err)
	}
	state := stateAny.(SkillState)
	if state.Counter != 5 {
		t.Fatalf("state.Counter = %d, want 5", state.Counter)
	}
	if len(traps) != 1 {
		t.Fatalf("trap count = %d, want 1", len(traps))
	}
	if traps[0].Function != "OnCast" {
		t.Fatalf("trap function = %q, want OnCast", traps[0].Function)
	}
	assertTrapVariable(t, traps[0].Locals, "state", "SkillState", "struct=SkillState")
	assertTrapVariable(t, traps[0].Locals, "ctx", "CastContext", "struct=CastContext")
}

func TestGlobalDebuggerBreakpointsBreakAgainWhenLoopReturnsToSameLine(t *testing.T) {
	program, err := eval.CompileSource(`package main
func main() int {
	total := 0
	for i := 0; i < 3; i++ {
		total = total + i
	}
	return total
}
`)
	if err != nil {
		t.Fatalf("CompileSource: %v", err)
	}

	runtime.ClearBreakpoints()
	defer runtime.ClearBreakpoints()
	defer runtime.SetTrapHandler(nil)
	var traps []*runtime.Trap
	runtime.SetTrapHandler(func(trap *runtime.Trap) {
		traps = append(traps, trap)
	})
	if err := runtime.SetBreakpoint("main.go", 5); err != nil {
		t.Fatalf("SetBreakpoint: %v", err)
	}

	result, err := eval.RunFunctionSourceWithDebug(program, "main", true)
	if err != nil {
		t.Fatalf("RunFunctionSource: %v", err)
	}
	if got := result.Int(); got != 3 {
		t.Fatalf("result = %d, want 3", got)
	}
	if len(traps) != 3 {
		t.Fatalf("trap count = %d, want 3", len(traps))
	}
}

func TestGlobalDebuggerClearBreakpointsStopsTrap(t *testing.T) {
	program, err := eval.CompileSource(`package main
func main() int {
	x := 1
	return x
}
`)
	if err != nil {
		t.Fatalf("CompileSource: %v", err)
	}

	runtime.ClearBreakpoints()
	defer runtime.ClearBreakpoints()
	defer runtime.SetTrapHandler(nil)
	var traps []*runtime.Trap
	runtime.SetTrapHandler(func(trap *runtime.Trap) {
		traps = append(traps, trap)
	})
	if err := runtime.SetBreakpoint("main.go", 3); err != nil {
		t.Fatalf("SetBreakpoint: %v", err)
	}
	runtime.ClearBreakpoints()

	result, err := eval.RunFunctionSourceWithDebug(program, "main", true)
	if err != nil {
		t.Fatalf("RunFunctionSource: %v", err)
	}
	if got := result.Int(); got != 1 {
		t.Fatalf("result = %d, want 1", got)
	}
	if len(traps) != 0 {
		t.Fatalf("trap count = %d, want 0", len(traps))
	}
}

func TestGlobalDebuggerSetBreakpointsJSONReplacesBreakpoints(t *testing.T) {
	program, err := eval.CompileSource(`package main
func main() int {
	x := 1
	y := x + 1
	return y
}
`)
	if err != nil {
		t.Fatalf("CompileSource: %v", err)
	}

	runtime.ClearBreakpoints()
	defer runtime.ClearBreakpoints()
	defer runtime.SetTrapHandler(nil)
	var traps []*runtime.Trap
	runtime.SetTrapHandler(func(trap *runtime.Trap) {
		traps = append(traps, trap)
	})
	if err := runtime.SetBreakpoint("main.go", 3); err != nil {
		t.Fatalf("SetBreakpoint: %v", err)
	}
	if err := runtime.SetBreakpointsJSON(`[{"file":"main.go","line":4,"enabled":true}]`); err != nil {
		t.Fatalf("SetBreakpointsJSON: %v", err)
	}

	result, err := eval.RunFunctionSourceWithDebug(program, "main", true)
	if err != nil {
		t.Fatalf("RunFunctionSource: %v", err)
	}
	if got := result.Int(); got != 2 {
		t.Fatalf("result = %d, want 2", got)
	}
	if len(traps) != 1 {
		t.Fatalf("trap count = %d, want 1", len(traps))
	}
	if traps[0].Line != 4 {
		t.Fatalf("trap line = %d, want 4", traps[0].Line)
	}
}

func TestGlobalDebuggerBreakpointsOnlyTrapWhenRunWithDebug(t *testing.T) {
	program, err := eval.CompileSource(`package main
func main() int {
	x := 1
	return x
}
`)
	if err != nil {
		t.Fatalf("CompileSource: %v", err)
	}

	runtime.ClearBreakpoints()
	defer runtime.ClearBreakpoints()
	defer runtime.SetTrapHandler(nil)
	var traps []*runtime.Trap
	runtime.SetTrapHandler(func(trap *runtime.Trap) {
		traps = append(traps, trap)
	})
	if err := runtime.SetBreakpoint("main.go", 3); err != nil {
		t.Fatalf("SetBreakpoint: %v", err)
	}

	result, err := eval.RunFunctionSource(program, "main")
	if err != nil {
		t.Fatalf("RunFunctionSource disabled: %v", err)
	}
	if got := result.Int(); got != 1 {
		t.Fatalf("disabled result = %d, want 1", got)
	}
	if len(traps) != 0 {
		t.Fatalf("disabled trap count = %d, want 0", len(traps))
	}

	result, err = eval.RunFunctionSource(program, "main")
	if err != nil {
		t.Fatalf("RunFunctionSource after setting breakpoint: %v", err)
	}
	if got := result.Int(); got != 1 {
		t.Fatalf("plain run result = %d, want 1", got)
	}
	if len(traps) != 0 {
		t.Fatalf("plain run trap count = %d, want 0", len(traps))
	}

	result, err = eval.RunFunctionSourceWithDebug(program, "main", true)
	if err != nil {
		t.Fatalf("RunFunctionSourceWithDebug: %v", err)
	}
	if got := result.Int(); got != 1 {
		t.Fatalf("with debug result = %d, want 1", got)
	}
	if len(traps) != 1 {
		t.Fatalf("with debug trap count = %d, want 1", len(traps))
	}
}

func assertTrapVariable(t *testing.T, variables []runtime.Variable, name string, typ string, value string) {
	t.Helper()
	for _, variable := range variables {
		if variable.Name == name {
			if variable.Type != typ || variable.Value != value {
				t.Fatalf("variable %q = %#v, want type=%s value=%s", name, variable, typ, value)
			}
			if variable.RuntimeValue.Kind == runtime.Invalid {
				t.Fatalf("variable %q has invalid RuntimeValue", name)
			}
			return
		}
	}
	t.Fatalf("missing variable %q in %#v", name, variables)
}
