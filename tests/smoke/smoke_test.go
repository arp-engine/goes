package smoke_test

import (
	"path/filepath"
	"testing"

	"goes/eval"
)

func TestRunPathLoadsScalarProgramDir(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "programs", "scalar")

	result, err := eval.RunPath(dir)
	if err != nil {
		t.Fatalf("RunPath: %v", err)
	}
	if got := result.Int(); got != 7 {
		t.Fatalf("result: want %d, got %d", 7, got)
	}
}

func TestRunPathLoadsAllProgramSamples(t *testing.T) {
	programs := map[string]int64{
		"aggregate": 9,
		"closure":   5,
		"control":   15,
		"fibonacci": 0,
		"map":       4,
		"scalar":    7,
	}

	for name, want := range programs {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("..", "..", "testdata", "programs", name)
			result, err := eval.RunPath(dir)
			if err != nil {
				t.Fatalf("RunPath: %v", err)
			}
			if got := result.Int(); got != want {
				t.Fatalf("result: want %d, got %d", want, got)
			}
		})
	}
}
