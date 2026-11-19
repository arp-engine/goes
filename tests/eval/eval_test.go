package eval_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"goes/eval"
	"goes/internal/vm"
	"goes/runtime"
)

func TestRunPathExecutesSingleGoFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	src := `package main
func main() int {
	return 42
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := eval.RunPath(path)
	if err != nil {
		t.Fatalf("RunPath: %v", err)
	}
	if result.Kind != runtime.Int {
		t.Fatalf("result kind: want %v, got %v", runtime.Int, result.Kind)
	}
	if got := result.Int(); got != 42 {
		t.Fatalf("result: want %d, got %d", 42, got)
	}
}

func TestRunPathExecutesSingleGoesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.goes")
	src := `package main
func main() int {
	return 42
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := eval.RunPath(path)
	if err != nil {
		t.Fatalf("RunPath: %v", err)
	}
	if got := result.Int(); got != 42 {
		t.Fatalf("result: want %d, got %d", 42, got)
	}
}

func TestRunPathExecutesDirectoryThroughCheckedPackage(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"model.go": `package main
type pair struct {
	a int
	b int
}

const factor = 3

func base() pair {
	return pair{a: 4, b: 5}
}
`,
		"main.go": `package main
var seed = base()

func main() int {
	return seed.a*factor + seed.b
}
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}

	result, err := eval.RunPath(dir)
	if err != nil {
		t.Fatalf("RunPath: %v", err)
	}
	if result.Kind != runtime.Int {
		t.Fatalf("result kind: want %v, got %v", runtime.Int, result.Kind)
	}
	if got := result.Int(); got != 17 {
		t.Fatalf("result: want %d, got %d", 17, got)
	}
}

func TestCompileSourceFilePreservesDebugFilename(t *testing.T) {
	program, err := eval.CompileSourceFile("rule.goes", `package main
func main() int {
	return 1
}
`)
	if err != nil {
		t.Fatalf("CompileSourceFile: %v", err)
	}
	index, ok := program.LookupFunction("main")
	if !ok {
		t.Fatalf("main function not found")
	}
	for _, inst := range program.Functions[index].Code {
		if inst.Pos.File == "rule.goes" {
			return
		}
	}
	t.Fatalf("compiled instructions do not preserve rule.goes source filename")
}

func TestCompileSourceCompilesBenchStyleProgram(t *testing.T) {
	program, err := eval.CompileSource(compileBenchSource)
	if err != nil {
		t.Fatalf("CompileSource: %v", err)
	}
	result, err := vm.Run(program)
	if err != nil {
		t.Fatalf("vm.Run: %v", err)
	}
	if got := result.Int(); got != 13 {
		t.Fatalf("result: want %d, got %d", 13, got)
	}
}

func TestCompileSourceWithHostRegistryCompilesThroughCheckedPackage(t *testing.T) {
	host := runtime.NewHostRegistry()
	host.MustRegisterConst("HostFactor", 3)
	host.MustRegister("scale", func(ctx *runtime.HostContext, power int, factor int) (int, error) {
		return power * factor, nil
	})

	program, err := eval.CompileSourceWithHostRegistry(`package main
func OnCast(power int) int {
	_ = [HostFactor]int{1, 2, 3}
	return scale(power, HostFactor)
}

func main() int {
	return 0
}
`, host)
	if err != nil {
		t.Fatalf("CompileSourceWithHostRegistry: %v", err)
	}

	result, err := eval.RunFunctionWithHost(program, "OnCast", host, nil, runtime.IntValue(4))
	if err != nil {
		t.Fatalf("RunFunctionWithHost: %v", err)
	}
	if got := result.Int(); got != 12 {
		t.Fatalf("result: want %d, got %d", 12, got)
	}
}

func TestRunFunctionValuesSupportsMultiResultFunction(t *testing.T) {
	program, err := eval.CompileSource(`package main
func pair() (int, string) {
	return 7, "ok"
}

func main() int {
	return 0
}
`)
	if err != nil {
		t.Fatalf("CompileSource: %v", err)
	}

	results, err := eval.RunFunctionValues(program, "pair")
	if err != nil {
		t.Fatalf("RunFunctionValues: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("result count: want %d, got %d", 2, len(results))
	}
	if got := results[0].Int(); got != 7 {
		t.Fatalf("result[0]: want %d, got %d", 7, got)
	}
	if got := results[1].String(); got != "ok" {
		t.Fatalf("result[1]: want %q, got %q", "ok", got)
	}
}

func TestEvalNoLongerImportsFrontendAdapter(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	path := filepath.Join(root, "..", "..", "eval", "eval.go")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, disallowed := range []string{
		`"goes/internal/ast"`,
		`"goes/internal/source"`,
	} {
		if bytes.Contains(src, []byte(disallowed)) {
			t.Fatalf("eval.go still imports %s", disallowed)
		}
	}
}
