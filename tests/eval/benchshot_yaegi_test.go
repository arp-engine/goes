package eval_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestBenchshotReportsYaegiColumns(t *testing.T) {
	cmd := exec.Command("go", "run", "./cmd/benchshot", "-samples", "1")
	cmd.Dir = "../.."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run ./cmd/benchshot: %v\n%s", err, out)
	}

	text := string(out)
	for _, want := range []string{
		"Median over 1 samples",
		"fibonacci native=",
		"control native=",
		"aggregate native=",
		"map native=",
		"range native=",
		"yaegi_compile=",
		"yaegi_run=",
		"yaegi_compile_run=",
		"native_result=5500000 goes_result=5500000 yaegi_result=5500000",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("benchshot output missing %q:\n%s", want, text)
		}
	}
}
