package vm_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"goes/eval"
	"goes/runtime"
)

func TestCompileSourceWithHostAcceptsRegisteredBuiltin(t *testing.T) {
	_, _, err := eval.CompileSourceWithHost(`package main
func OnCast(power int) int {
	return scale(power, 3)
}

func main() int {
	return 0
}
`, map[string]eval.HostSignature{
		"scale": {ParamKinds: []runtime.Kind{runtime.Int, runtime.Int}, ResultKinds: []runtime.Kind{runtime.Int}},
	})
	if err != nil {
		t.Fatalf("CompileSourceWithHost: %v", err)
	}
}

func TestRunFunctionCallsHostBuiltin(t *testing.T) {
	program, host, err := eval.CompileSourceWithHostFuncs(`package main
func OnCast(power int) int {
	return scale(power, 3)
}

func main() int {
	return 0
}
`, map[string]any{
		"scale": func(ctx *runtime.HostContext, power int, factor int) (int, error) {
			res := power * factor
			t.Logf("scale(%d, %d) = %d", power, factor, res)
			return res, nil
		},
	})
	if err != nil {
		t.Fatalf("CompileSourceWithHostFuncs: %v", err)
	}

	result, err := eval.RunFunctionWithHost(program, "OnCast", host, nil, runtime.IntValue(4))
	if err != nil {
		t.Fatalf("RunFunctionWithHost: %v", err)
	}
	if got := result.Int(); got != 12 {
		t.Fatalf("result = %d, want 12", got)
	}
}

func TestCompileSourceWithHostFuncsRejectsInvalidSignature(t *testing.T) {
	_, _, err := eval.CompileSourceWithHostFuncs(`package main
func OnCast(power int) int {
	return scale(power, 3)
}

func main() int {
	return 0
}
`, map[string]any{
		"scale": func(power int, factor int) int {
			return power * factor
		},
	})
	if err == nil {
		t.Fatalf("CompileSourceWithHostFuncs: expected error")
	}
	if !strings.Contains(err.Error(), "HostContext") {
		t.Fatalf("error = %q, want HostContext detail", err)
	}
}

func TestRunFunctionSharesHostAcrossPrograms(t *testing.T) {
	host := runtime.NewHostRegistry()
	host.MustRegister("scale", func(ctx *runtime.HostContext, power int, factor int) (int, error) {
		return power * factor, nil
	})

	first, err := eval.CompileSourceWithHostRegistry(`package main
func OnCast(power int) int {
	return scale(power, 2)
}

func main() int {
	return 0
}
`, host)
	if err != nil {
		t.Fatalf("CompileSourceWithHostRegistry(first): %v", err)
	}

	second, err := eval.CompileSourceWithHostRegistry(`package main
func OnCast(power int) int {
	return scale(power, 5)
}

func main() int {
	return 0
}
`, host)
	if err != nil {
		t.Fatalf("CompileSourceWithHostRegistry(second): %v", err)
	}

	gotFirst, err := eval.RunFunctionWithHost(first, "OnCast", host, nil, runtime.IntValue(4))
	if err != nil {
		t.Fatalf("RunFunctionWithHost(first): %v", err)
	}
	if got := gotFirst.Int(); got != 8 {
		t.Fatalf("first result = %d, want 8", got)
	}

	gotSecond, err := eval.RunFunctionWithHost(second, "OnCast", host, nil, runtime.IntValue(4))
	if err != nil {
		t.Fatalf("RunFunctionWithHost(second): %v", err)
	}
	if got := gotSecond.Int(); got != 20 {
		t.Fatalf("second result = %d, want 20", got)
	}
}

func TestCompileSourceWithHostFuncsIntoUsesProvidedRegistry(t *testing.T) {
	host := runtime.NewHostRegistry()

	program, err := eval.CompileSourceWithHostFuncsInto(`package main
func OnCast(power int) int {
	return scale(power, 3)
}

func main() int {
	return 0
}
`, host, map[string]any{
		"scale": func(ctx *runtime.HostContext, power int, factor int) (int, error) {
			return power * factor, nil
		},
	})
	if err != nil {
		t.Fatalf("CompileSourceWithHostFuncsInto: %v", err)
	}

	got, err := eval.RunFunctionWithHost(program, "OnCast", host, nil, runtime.IntValue(4))
	if err != nil {
		t.Fatalf("RunFunctionWithHost: %v", err)
	}
	if got.Int() != 12 {
		t.Fatalf("result = %d, want 12", got.Int())
	}
}

func TestRunFunctionReportsMissingHostRegistration(t *testing.T) {
	program, host, err := eval.CompileSourceWithHost(`package main
func OnCast(power int) int {
	return scale(power, 3)
}

func main() int {
	return 0
}
`, map[string]eval.HostSignature{
		"scale": {ParamKinds: []runtime.Kind{runtime.Int, runtime.Int}, ResultKinds: []runtime.Kind{runtime.Int}},
	})
	if err != nil {
		t.Fatalf("CompileSourceWithHost: %v", err)
	}

	_, err = eval.RunFunctionWithHost(program, "OnCast", host, nil, runtime.IntValue(4))
	if err == nil {
		t.Fatalf("RunFunctionWithHost: expected error")
	}
	if !strings.Contains(err.Error(), "scale") {
		t.Fatalf("error = %q, want host function name", err)
	}
}

func TestRunFunctionHostErrorIncludesVMTraceback(t *testing.T) {
	program, host, err := eval.CompileSourceWithHostFuncs(`package main
func leaf(power int) int {
	return scale(power, 3)
}

func OnCast(power int) int {
	return leaf(power)
}

func main() int {
	return 0
}
`, map[string]any{
		"scale": func(ctx *runtime.HostContext, power int, factor int) (int, error) {
			return 0, errors.New("boom")
		},
	})
	if err != nil {
		t.Fatalf("CompileSourceWithHostFuncs: %v", err)
	}

	_, err = eval.RunFunctionWithHost(program, "OnCast", host, nil, runtime.IntValue(4))
	if err == nil {
		t.Fatalf("RunFunctionWithHost: expected error")
	}
	text := err.Error()
	for _, want := range []string{"vm traceback:", "  OnCast", "  leaf", "scale", "boom"} {
		if !strings.Contains(text, want) {
			t.Fatalf("error = %q, want %q", text, want)
		}
	}
}

func TestRunFunctionHostErrorIncludesCurrentFrameStack(t *testing.T) {
	program, host, err := eval.CompileSourceWithHostFuncs(`package main
func OnCast(power int) int {
	return 7 + fail(power)
}

func main() int {
	return 0
}
`, map[string]any{
		"fail": func(ctx *runtime.HostContext, power int) (int, error) {
			return 0, errors.New("boom")
		},
	})
	if err != nil {
		t.Fatalf("CompileSourceWithHostFuncs: %v", err)
	}

	_, err = eval.RunFunctionWithHost(program, "OnCast", host, nil, runtime.IntValue(4))
	if err == nil {
		t.Fatalf("RunFunctionWithHost: expected error")
	}
	text := err.Error()
	for _, want := range []string{"vm traceback:", "stack:", "int=7"} {
		if !strings.Contains(text, want) {
			t.Fatalf("error = %q, want %q", text, want)
		}
	}
}

func TestRunFunctionRejectsWrongHookArgCount(t *testing.T) {
	program, err := eval.CompileSource(`package main
func OnCast(power int) int {
	return power
}

func main() int {
	return 0
}
`)
	if err != nil {
		t.Fatalf("CompileSource: %v", err)
	}

	_, err = eval.RunFunctionSource(program, "OnCast")
	if err == nil {
		t.Fatalf("RunFunctionSource: expected error")
	}
	if !strings.Contains(err.Error(), "argument count") {
		t.Fatalf("error = %q, want argument count", err)
	}
}

func TestRunFunctionUsesHostRegisteredConsts(t *testing.T) {
	host := runtime.NewHostRegistry()
	host.MustRegisterConst("HostFactor", 3)
	host.MustRegisterConst("HostEnabled", true)

	program, err := eval.CompileSourceWithHostRegistry(`package main
func OnCast(power int) int {
	_ = [HostFactor]int{1, 2, 3}
	if HostEnabled {
		return power * HostFactor
	}
	return power
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
		t.Fatalf("result = %d, want 12", got)
	}
}

func TestRunProgramSupportsHostMultiResultForwarding(t *testing.T) {
	program, registry, err := eval.CompileSourceWithHostFuncs(`package main
func pair() (int, string) {
	return hostPair()
}
func main() int {
	a, b := pair()
	if b == "ok" {
		return a
	}
	return 0
}
`, map[string]any{
		"hostPair": func(ctx *runtime.HostContext) (int, string, error) {
			return 7, "ok", nil
		},
	})
	if err != nil {
		t.Fatalf("CompileSourceWithHostFuncs: %v", err)
	}
	result, err := eval.RunFunctionWithHost(program, "main", registry, nil)
	if err != nil {
		t.Fatalf("RunFunctionWithHost: %v", err)
	}
	if got := result.Int(); got != 7 {
		t.Fatalf("result: want %d, got %d", 7, got)
	}
}

func TestCompileSourceWithHostMultiResultShortDeclPassesResultsToScriptHelper(t *testing.T) {
	_, _, err := eval.CompileSourceWithHostFuncs(`package main
func waitNearAtLeast(connID int64, entityID int64, srvID int64, want int64) bool {
	return RobotNearCount(connID, entityID, srvID) >= want
}

func OnStart(connID int64) int {
	entityID, srvID := RobotRoleLogin(connID)
	if waitNearAtLeast(connID, entityID, srvID, int64(1)) {
		return 1
	}
	return 0
}

func main() int {
	return 0
}
`, map[string]any{
		"RobotRoleLogin": func(ctx *runtime.HostContext, connID int64) (int64, int64, error) {
			return connID + 10, connID + 20, nil
		},
		"RobotNearCount": func(ctx *runtime.HostContext, connID int64, entityID int64, srvID int64) (int64, error) {
			return connID + entityID + srvID, nil
		},
	})
	if err != nil {
		t.Fatalf("CompileSourceWithHostFuncs: %v", err)
	}
}

func TestRunProgramSupportsVariadicHostFunction(t *testing.T) {
	program, registry, err := eval.CompileSourceWithHostFuncs(`package main
func main() int {
	return hostSum(1, 2, 3)
}
`, map[string]any{
		"hostSum": func(ctx *runtime.HostContext, xs ...int) (int, error) {
			total := 0
			for _, v := range xs {
				total += v
			}
			return total, nil
		},
	})
	if err != nil {
		t.Fatalf("CompileSourceWithHostFuncs: %v", err)
	}
	result, err := eval.RunFunctionWithHost(program, "main", registry, nil)
	if err != nil {
		t.Fatalf("RunFunctionWithHost: %v", err)
	}
	if got := result.Int(); got != 6 {
		t.Fatalf("result: want %d, got %d", 6, got)
	}
}

func TestRunFunctionHostYieldCanResumeEntireCallStack(t *testing.T) {
	program, registry, err := eval.CompileSourceWithHostFuncs(`package main
func leaf(power int) int {
	return asyncScale(power) + 1
}

func middle(power int) int {
	return leaf(power) + 2
}

func OnCast(power int) int {
	return middle(power) + 3
}

func main() int {
	return 0
}
`, map[string]any{
		"asyncScale": func(ctx *runtime.HostContext, power int) (int, error) {
			return 0, runtime.Yield(fmt.Sprintf("scale:%d", power))
		},
	})
	if err != nil {
		t.Fatalf("CompileSourceWithHostFuncs: %v", err)
	}

	_, err = eval.RunFunctionWithHost(program, "OnCast", registry, nil, runtime.IntValue(4))
	if err == nil {
		t.Fatalf("RunFunctionWithHost: expected suspension")
	}
	var suspension *runtime.Suspension
	if !errors.As(err, &suspension) {
		t.Fatalf("RunFunctionWithHost error = %T %[1]v, want *runtime.Suspension", err)
	}
	if got, want := suspension.Payload, "scale:4"; got != want {
		t.Fatalf("suspension payload = %v, want %v", got, want)
	}

	results, err := suspension.ResumeValues(runtime.IntValue(40))
	if err != nil {
		t.Fatalf("ResumeValues: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("resume results: got %d values, want 1", len(results))
	}
	if got, want := results[0].Int(), int64(46); got != want {
		t.Fatalf("resume result = %d, want %d", got, want)
	}
}

func TestRunFunctionHostYieldSnapshotCanResumeInNewVM(t *testing.T) {
	program, registry, err := eval.CompileSourceWithHostFuncs(`package main
func leaf(power int) int {
	local := power + 3
	return asyncScale(local) + local
}

func OnCast(power int) int {
	outer := 2
	return leaf(power) + outer
}

func main() int {
	return 0
}
`, map[string]any{
		"asyncScale": func(ctx *runtime.HostContext, power int) (int, error) {
			return 0, runtime.Yield(fmt.Sprintf("scale:%d", power))
		},
	})
	if err != nil {
		t.Fatalf("CompileSourceWithHostFuncs: %v", err)
	}

	_, err = eval.RunFunctionWithHost(program, "OnCast", registry, nil, runtime.IntValue(4))
	if err == nil {
		t.Fatalf("RunFunctionWithHost: expected suspension")
	}
	var suspension *runtime.Suspension
	if !errors.As(err, &suspension) {
		t.Fatalf("RunFunctionWithHost error = %T %[1]v, want *runtime.Suspension", err)
	}

	snapshot, err := suspension.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("Marshal snapshot: %v", err)
	}
	var decoded runtime.Snapshot
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal snapshot: %v", err)
	}

	resumeHost := runtime.NewHostRegistry()
	if err := resumeHost.Declare("asyncScale", runtime.HostSignature{
		ParamKinds:  []runtime.Kind{runtime.Int},
		ResultKinds: []runtime.Kind{runtime.Int},
	}); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	restored, err := eval.RestoreSnapshot(program, &decoded, resumeHost, nil)
	if err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}
	if got, want := restored.Payload, "scale:7"; got != want {
		t.Fatalf("restored payload = %v, want %v", got, want)
	}
	results, err := restored.ResumeValues(runtime.IntValue(40))
	if err != nil {
		t.Fatalf("restored ResumeValues: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("resume results: got %d values, want 1", len(results))
	}
	if got, want := results[0].Int(), int64(49); got != want {
		t.Fatalf("resume result = %d, want %d", got, want)
	}
}

func TestRunFunctionHostYieldResumeRejectsWrongResultCount(t *testing.T) {
	program, registry, err := eval.CompileSourceWithHostFuncs(`package main
func OnCast(power int) int {
	return asyncScale(power) + 1
}

func main() int {
	return 0
}
`, map[string]any{
		"asyncScale": func(ctx *runtime.HostContext, power int) (int, error) {
			return 0, runtime.Yield(nil)
		},
	})
	if err != nil {
		t.Fatalf("CompileSourceWithHostFuncs: %v", err)
	}

	_, err = eval.RunFunctionWithHost(program, "OnCast", registry, nil, runtime.IntValue(4))
	if err == nil {
		t.Fatalf("RunFunctionWithHost: expected suspension")
	}
	var suspension *runtime.Suspension
	if !errors.As(err, &suspension) {
		t.Fatalf("RunFunctionWithHost error = %T %[1]v, want *runtime.Suspension", err)
	}

	_, err = suspension.ResumeValues()
	if err == nil {
		t.Fatalf("ResumeValues: expected error")
	}
	if !strings.Contains(err.Error(), "host resume expected 1 results") {
		t.Fatalf("ResumeValues error = %q, want result count detail", err)
	}
}
