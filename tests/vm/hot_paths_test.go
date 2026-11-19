package vm_test

import (
	"errors"
	"strings"
	"testing"

	"goes/eval"
	"goes/internal/bytecode"
	"goes/runtime"
)

func TestArrayAddFastPathUsesGoLiteralIndexAndKeepsBoundsCheck(t *testing.T) {
	program, err := eval.CompileSource(`package main
func read() int {
	a := [9]int{1, 2, 3, 4, 5, 6, 7, 8, 9}
	return a[0] + a[010] + a[0x1]
}
func invalidIndex() int {
	a := [1]int{2}
	return a[0] + a[1]
}
func main() int { return read() }
`)
	if err != nil {
		t.Fatal(err)
	}
	if dump := bytecode.FormatProgram(program); !strings.Contains(dump, "OpAddArrayElemLocalConst") {
		t.Fatalf("missing array add fast path:\n%s", dump)
	}
	value, err := eval.RunFunctionSource(program, "read")
	if err != nil || value.Int() != 12 {
		t.Fatalf("got %d, %v; want 12", value.Int(), err)
	}
	if _, err := eval.RunFunctionSource(program, "invalidIndex"); err == nil || !strings.Contains(err.Error(), "index 1 out of range") {
		t.Fatalf("got %v; want bounds error", err)
	}
}

func TestRangeInlineExitWithBreakContinueAndNilSources(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	var s []int = nil
	var m map[string]int = nil
	sum := 0
	for range s { sum += 100 }
	for range m { sum += 100 }
	for i, v := range [4]int{10, 20, 30, 40} {
		if i == 1 { continue }
		if i == 3 { break }
		sum += i + v
	}
	i := 99
	v := 99
	for i, v = range []int{1, 2} { sum += i + v }
	return sum + i + v
}
`)
	if err != nil || result.Int() != 49 {
		t.Fatalf("got %d, %v; want 49", result.Int(), err)
	}
}

func TestLocalStringIntMapFastPathInteroperatesWithDynamicAccess(t *testing.T) {
	program, err := eval.CompileSource(`package main
func main() int {
	m := make(map[string]int)
	alias := m
	key := "a"
	alias[key] = 5
	m["a"] += 2
	delta := 3
	m["b"] += delta
	m["b"] += alias["a"]
	v, ok := m["b"]
	if !ok { return -1 }
	delete(alias, key)
	return v + alias[key] + m["a"]
}
`)
	if err != nil {
		t.Fatal(err)
	}
	dump := bytecode.FormatProgram(program)
	for _, op := range []string{"OpMapGetLocalStringInt", "OpMapAddLocalStringInt", "OpMapGetOK"} {
		if !strings.Contains(dump, op) {
			t.Fatalf("missing %s:\n%s", op, dump)
		}
	}
	value, err := eval.RunFunctionSource(program, "main")
	if err != nil || value.Int() != 10 {
		t.Fatalf("got %d, %v; want 10", value.Int(), err)
	}
}

func TestLocalStringIntMapFastPathNilReadAndWrite(t *testing.T) {
	program, err := eval.CompileSource(`package main
func read() int {
	var m map[string]int = nil
	return m[""] + 1
}
func write() int {
	var m map[string]int = nil
	m[""] += 1
	return 0
}
func main() int { return read() }
`)
	if err != nil {
		t.Fatal(err)
	}
	value, err := eval.RunFunctionSource(program, "read")
	if err != nil || value.Int() != 1 {
		t.Fatalf("got %d, %v; want 1", value.Int(), err)
	}
	if _, err := eval.RunFunctionSource(program, "write"); err == nil || !strings.Contains(err.Error(), "expected map for assignment") {
		t.Fatalf("got %v; want nil map assignment error", err)
	}
}

func TestRestoreSnapshotInsideIndexedRange(t *testing.T) {
	program, host, err := eval.CompileSourceWithHostFuncs(`package main
func Run() int {
	sum := 0
	for i, v := range []int{3, 5, 7} {
		if i == 1 { sum += yield() }
		sum += i + v
	}
	return sum
}
func main() int { return 0 }
`, map[string]any{
		"yield": func(*runtime.HostContext) (int, error) { return 0, runtime.Yield(nil) },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = eval.RunFunctionWithHost(program, "Run", host, nil)
	var suspension *runtime.Suspension
	if !errors.As(err, &suspension) {
		t.Fatalf("got %v; want suspension", err)
	}
	snapshot, err := suspension.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := eval.RestoreSnapshot(program, snapshot, host, nil)
	if err != nil {
		t.Fatal(err)
	}
	values, err := restored.ResumeValues(runtime.IntValue(10))
	if err != nil || len(values) != 1 || values[0].Int() != 28 {
		t.Fatalf("got %v, %v; want 28", values, err)
	}
}
