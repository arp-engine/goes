package vm_test

import (
	"path/filepath"
	"testing"

	"goes/eval"
	"goes/runtime"
)

func TestRunMapProgram(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "programs", "map")

	result, err := eval.RunProgram(dir)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if result.Kind != runtime.Int {
		t.Fatalf("result kind: want %v, got %v", runtime.Int, result.Kind)
	}

	if got := result.Int(); got != 4 {
		t.Fatalf("result: want %d, got %d", 4, got)
	}
}

func TestMapProgramMatchesGo(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "programs", "map")
	if err := eval.RunAndCompare(dir); err != nil {
		t.Fatal(err)
	}
}

func TestRunMapProgramMissingKeyReturnsZeroValue(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	m := make(map[string]int)
	m["a"] = 4
	return m["missing"] + m["a"]
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if got := result.Int(); got != 4 {
		t.Fatalf("result: want %d, got %d", 4, got)
	}
}

func TestRunMapProgramSupportsCommaOkRead(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	m := make(map[string]int)
	m["present"] = 7
	present, okPresent := m["present"]
	missing, okMissing := m["missing"]
	var nilMap map[string]int = nil
	nilValue, okNil := nilMap["anything"]
	score := present + missing + nilValue
	if okPresent {
		score = score + 10
	}
	if okMissing {
		score = score + 100
	}
	if okNil {
		score = score + 1000
	}
	return score
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if got := result.Int(); got != 17 {
		t.Fatalf("result: want %d, got %d", 17, got)
	}
}

func TestRunMapProgramUsesDeclaredKeyTypeForUntypedIntegerKeys(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	m := make(map[int64]int)
	m[int64(1)] = 7
	read := m[1]
	m[2] = 5
	delete(m, 1)
	return read + m[int64(2)] + m[int64(1)]
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if got := result.Int(); got != 12 {
		t.Fatalf("result: want %d, got %d", 12, got)
	}
}

func TestRunMapProgramRangeSupportsKeyAndValue(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	m := make(map[string]int)
	m["x"] = 2
	m["yy"] = 3
	sum := 0
	for k, v := range m {
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

func TestRunMapProgramSupportsNilMap(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	var m map[string]int = nil
	if m == nil {
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

func TestRunMapProgramSupportsCompoundAssignmentAndIncDecOnIndex(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	m := make(map[string]int)
	m["a"] = 2
	m["a"] += 3
	m["a"]++
	return m["a"]
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 6 {
		t.Fatalf("result: want %d, got %d", 6, got)
	}
}

func TestRunMapProgramSupportsClear(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	m := make(map[string]int)
	m["a"] = 2
	m["b"] = 3
	clear(m)
	var nilMap map[string]int = nil
	clear(nilMap)
	return len(m) + nilMap["missing"]
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 0 {
		t.Fatalf("result: want %d, got %d", 0, got)
	}
}
