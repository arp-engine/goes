package vm_test

import (
	"strings"
	"testing"

	"goes/eval"
	"goes/runtime"
)

func TestRunFunctionInvokesNamedHook(t *testing.T) {
	program, err := eval.CompileSource(`package main
func OnCast(power int) int {
	return power * 2
}

func main() int {
	return 0
}
`)
	if err != nil {
		t.Fatalf("CompileSource: %v", err)
	}

	result, err := eval.RunFunctionSource(program, "OnCast", runtime.IntValue(3))
	if err != nil {
		t.Fatalf("RunFunctionSource: %v", err)
	}

	if result.Kind != runtime.Int {
		t.Fatalf("result kind: want %v, got %v", runtime.Int, result.Kind)
	}
	if got := result.Int(); got != 6 {
		t.Fatalf("result: want %d, got %d", 6, got)
	}
}

func TestRunFunctionErrorIncludesVMTraceback(t *testing.T) {
	program, err := eval.CompileSource(`package main
func leaf(x int) int {
	return x / 0
}

func middle(x int) int {
	return leaf(x)
}

func OnCast(power int) int {
	return middle(power)
}

func main() int {
	return 0
}
`)
	if err != nil {
		t.Fatalf("CompileSource: %v", err)
	}

	_, err = eval.RunFunctionSource(program, "OnCast", runtime.IntValue(3))
	if err == nil {
		t.Fatalf("RunFunctionSource: expected error")
	}
	text := err.Error()
	t.Log(text)
	for _, want := range []string{
		"vm traceback:",
		"  leaf ip=",
		"main.go:3",
		"return x / 0",
		"  middle ip=",
		"main.go:7",
		"return leaf(x)",
		"  OnCast ip=",
		"main.go:11",
		"return middle(power)",
		"division by zero",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("error = %q, want %q", text, want)
		}
	}
}

func TestInvokeHookReturnsUpdatedState(t *testing.T) {
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

	stateAny, err := eval.InvokeHook(module, "OnCast", SkillState{Counter: 1}, CastContext{Level: 4}, nil)
	if err != nil {
		t.Fatalf("InvokeHook: %v", err)
	}
	state := stateAny.(SkillState)
	if state.Counter != 5 {
		t.Fatalf("state.Counter = %d, want 5", state.Counter)
	}
}

func TestInvokeHookReturnsUpdatedStateFromPointerReceiverMethod(t *testing.T) {
	type SkillState struct {
		Counter int
	}
	type CastContext struct {
		Level int
	}

	module := eval.MustCompileHookModule(`package main
type SkillState struct { Counter int }
type CastContext struct { Level int }

type SkillImpl struct {
}

func (s *SkillImpl) Invoke(state *SkillState, ctx *CastContext) SkillState {
	state.Counter = state.Counter + ctx.Level
	return *state
}

func OnCast(state SkillState, ctx CastContext) SkillState {
	var impl *SkillImpl
	state = impl.Invoke(&state, &ctx)
	return state
}

func main() int {
	return 0
}
`, nil)

	stateAny, err := eval.InvokeHook(module, "OnCast", SkillState{Counter: 1}, CastContext{Level: 4}, nil)
	if err != nil {
		t.Fatalf("InvokeHook: %v", err)
	}
	state := stateAny.(SkillState)
	if state.Counter != 5 {
		t.Fatalf("state.Counter = %d, want 5", state.Counter)
	}
}

func TestInvokeHookAllowsDiscardedCallStatement(t *testing.T) {
	type SkillState struct {
		Counter int
	}
	type CastContext struct {
		Level int
	}

	module := eval.MustCompileHookModule(`package main
type SkillState struct { Counter int }
type CastContext struct { Level int }

type SkillImpl struct {
	total int
}

func (s *SkillImpl) Invoke(state *SkillState, ctx *CastContext) int {
	state.Counter = state.Counter + ctx.Level
	s.total = s.total + state.Counter
	return s.total
}

func OnCast(state SkillState, ctx CastContext) SkillState {
	impl := SkillImpl{0}
	impl.Invoke(&state, &ctx)
	return state
}

func main() int {
	return 0
}
`, nil)

	stateAny, err := eval.InvokeHook(module, "OnCast", SkillState{Counter: 1}, CastContext{Level: 4}, nil)
	if err != nil {
		t.Fatalf("InvokeHook: %v", err)
	}
	state := stateAny.(SkillState)
	if state.Counter != 5 {
		t.Fatalf("state.Counter = %d, want 5", state.Counter)
	}
}

func TestInvokeMissingHookReturnsOriginalState(t *testing.T) {
	type SkillState struct {
		Counter int
	}

	module := eval.MustCompileHookModule(`package main
type SkillState struct { Counter int }

func main() int {
	return 0
}
`, nil)

	stateAny, err := eval.InvokeHook(module, "MissingHook", SkillState{Counter: 9}, nil, nil)
	if err != nil {
		t.Fatalf("InvokeHook: %v", err)
	}
	state := stateAny.(SkillState)
	if state.Counter != 9 {
		t.Fatalf("state.Counter = %d, want 9", state.Counter)
	}
}

func TestInvokeHookUsesHostRegisteredStructTypes(t *testing.T) {
	type SkillState struct {
		Counter int
	}
	type CastContext struct {
		Level int
	}

	host := runtime.NewHostRegistry()
	host.MustRegisterType(SkillState{})
	host.MustRegisterType(CastContext{})

	module := eval.MustCompileHookModuleWithHost(`package main
func (s *SkillState) Add(v int) int {
	s.Counter = s.Counter + v
	return s.Counter
}

func OnCast(state SkillState, ctx CastContext) SkillState {
	state.Add(ctx.Level)
	return state
}

func main() int {
	return 0
}
`, host)

	stateAny, err := eval.InvokeHook(module, "OnCast", SkillState{Counter: 1}, CastContext{Level: 4}, nil)
	if err != nil {
		t.Fatalf("InvokeHook: %v", err)
	}
	state := stateAny.(SkillState)
	if state.Counter != 5 {
		t.Fatalf("state.Counter = %d, want 5", state.Counter)
	}
}
