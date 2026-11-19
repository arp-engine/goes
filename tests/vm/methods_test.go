package vm_test

import "testing"

func TestRunProgramSupportsDirectMethodCallOnValueReceiver(t *testing.T) {
	result, err := runSource(t, `package main
type adder struct {
	base int
}

func (a adder) Add(x int) int {
	return a.base + x
}

func main() int {
	a := adder{5}
	return a.Add(7)
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	if got := result.Int(); got != 12 {
		t.Fatalf("result: want %d, got %d", 12, got)
	}
}

func TestRunProgramSupportsMethodValuesOnValueReceivers(t *testing.T) {
	result, err := runSource(t, `package main
type adder struct {
	base int
}

func (a adder) Add(x int) int {
	return a.base + x
}

func main() int {
	a := adder{5}
	f := a.Add
	return f(7)
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 12 {
		t.Fatalf("result: want %d, got %d", 12, got)
	}
}

func TestRunProgramMethodValueCapturesValueReceiverSnapshot(t *testing.T) {
	result, err := runSource(t, `package main
type adder struct {
	base int
}

func (a adder) Add(x int) int {
	return a.base + x
}

func main() int {
	a := adder{5}
	f := a.Add
	a.base = 100
	return f(7)
}
`)
	if err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if got := result.Int(); got != 12 {
		t.Fatalf("result: want %d, got %d", 12, got)
	}
}
