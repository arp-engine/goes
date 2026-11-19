package vm_test

import (
	"path/filepath"
	"testing"

	"goes/eval"
	"goes/runtime"
)

func TestRunClosureProgram(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "programs", "closure")

	result, err := eval.RunProgram(dir)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if result.Kind != runtime.Int {
		t.Fatalf("result kind: want %v, got %v", runtime.Int, result.Kind)
	}

	if got := result.Int(); got != 5 {
		t.Fatalf("result: want %d, got %d", 5, got)
	}
}

func TestClosureProgramMatchesGo(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "programs", "closure")
	if err := eval.RunAndCompare(dir); err != nil {
		t.Fatal(err)
	}
}

func TestRunClosureProgramMultipleClosuresShareSameCapturedCell(t *testing.T) {
	result, err := runSource(t, `package main
func main() int {
	x := 1
	f := func() int { return x }
	g := func() int { return x }
	x = 4
	return f() + g()
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if got := result.Int(); got != 8 {
		t.Fatalf("result: want %d, got %d", 8, got)
	}
}
