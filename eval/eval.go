package eval

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"goes/internal/bytecode"
	"goes/internal/compiler"
	"goes/internal/sema"
	"goes/internal/vm"
	"goes/runtime"
	semaTypes "goes/types"
)

func RunProgram(dir string) (runtime.Value, error) {
	checked, err := loadCheckedPath(dir)
	if err != nil {
		return runtime.Value{}, err
	}
	return runChecked(checked)
}

func RunProgramWithDebug(dir string, debug bool) (runtime.Value, error) {
	checked, err := loadCheckedPath(dir)
	if err != nil {
		return runtime.Value{}, err
	}
	return runCheckedWithDebug(checked, debug)
}

func RunPath(path string) (runtime.Value, error) {
	return RunPathWithModuleRoot(path, "")
}

func RunPathWithModuleRoot(path string, moduleRoot string) (runtime.Value, error) {
	checked, err := loadCheckedPathWithModuleRoot(path, moduleRoot)
	if err != nil {
		return runtime.Value{}, err
	}
	return runChecked(checked)
}

func RunPathWithDebug(path string, debug bool) (runtime.Value, error) {
	checked, err := loadCheckedPath(path)
	if err != nil {
		return runtime.Value{}, err
	}
	return runCheckedWithDebug(checked, debug)
}

func CompilePath(path string) (*bytecode.Program, error) {
	return CompilePathWithModuleRoot(path, "")
}

func CompilePathWithModuleRoot(path string, moduleRoot string) (*bytecode.Program, error) {
	checked, err := loadCheckedPathWithModuleRoot(path, moduleRoot)
	if err != nil {
		return nil, err
	}
	return compiler.CompileChecked(checked)
}

func runChecked(checked *sema.Checked) (runtime.Value, error) {
	return runCheckedWithDebug(checked, false)
}

func runCheckedWithDebug(checked *sema.Checked, debug bool) (runtime.Value, error) {
	program, err := compiler.CompileChecked(checked)
	if err != nil {
		return runtime.Value{}, err
	}

	return vm.RunWithDebug(program, debug)
}

func CompileSource(src string) (*bytecode.Program, error) {
	return compileSourceWithOptions(src, nil, nil)
}

func CompileSourceFile(path string, src string) (*bytecode.Program, error) {
	return compileSourceFileWithOptions(path, src, nil, nil)
}

type HostSignature struct {
	ParamKinds  []runtime.Kind
	ResultKinds []runtime.Kind
	Variadic    bool
}

func CompileSourceWithHost(src string, host map[string]HostSignature) (*bytecode.Program, *runtime.HostRegistry, error) {
	registry := runtime.NewHostRegistry()
	for name, sig := range host {
		if err := registry.Declare(name, runtime.HostSignature{
			ParamKinds:  append([]runtime.Kind(nil), sig.ParamKinds...),
			ResultKinds: append([]runtime.Kind(nil), sig.ResultKinds...),
			Variadic:    sig.Variadic,
		}); err != nil {
			return nil, nil, err
		}
	}
	program, err := CompileSourceWithHostRegistry(src, registry)
	if err != nil {
		return nil, nil, err
	}
	return program, registry, nil
}

func CompileSourceWithHostRegistry(src string, registry *runtime.HostRegistry) (*bytecode.Program, error) {
	return CompileSourceFileWithHostRegistry("main.go", src, registry)
}

func CompileSourceFileWithHostRegistry(path string, src string, registry *runtime.HostRegistry) (*bytecode.Program, error) {
	if registry == nil {
		return nil, fmt.Errorf("testutil: missing host registry")
	}
	hostTypes, err := runtimeHostSignaturesToTypes(registry.Signatures())
	if err != nil {
		return nil, err
	}
	program, err := compileSourceFileWithOptions(path, src, hostTypes, registry)
	if err != nil {
		return nil, err
	}
	return program, nil
}

func CompileSourceWithHostFuncs(src string, host map[string]any) (*bytecode.Program, *runtime.HostRegistry, error) {
	registry := runtime.NewHostRegistry()
	program, err := CompileSourceWithHostFuncsInto(src, registry, host)
	if err != nil {
		return nil, nil, err
	}
	return program, registry, nil
}

func CompileSourceWithHostFuncsInto(src string, registry *runtime.HostRegistry, host map[string]any) (*bytecode.Program, error) {
	hostSigns, err := hostFuncsToSignatures(host)
	if err != nil {
		return nil, err
	}
	for name, sig := range hostSigns {
		if err := registry.Declare(name, runtime.HostSignature{
			ParamKinds:  append([]runtime.Kind(nil), sig.ParamKinds...),
			ResultKinds: append([]runtime.Kind(nil), sig.ResultKinds...),
			Variadic:    sig.Variadic,
		}); err != nil {
			return nil, err
		}
	}
	program, err := CompileSourceWithHostRegistry(src, registry)
	if err != nil {
		return nil, err
	}
	for name, fn := range host {
		if _, exists := registry.Lookup(name); exists {
			continue
		}
		if err := registry.Register(name, fn); err != nil {
			return nil, err
		}
	}
	return program, nil
}

func compileSourceWithOptions(src string, host map[string]semaTypes.HostSignature, registry *runtime.HostRegistry) (*bytecode.Program, error) {
	return compileSourceFileWithOptions("main.go", src, host, registry)
}

func compileSourceFileWithOptions(path string, src string, host map[string]semaTypes.HostSignature, registry *runtime.HostRegistry) (*bytecode.Program, error) {
	checked, err := checkSource(path, src, sema.CheckOptions{HostFunctions: host, HostRegistry: registry})
	if err != nil {
		return nil, err
	}
	return compiler.CompileCheckedWithOptions(checked, compiler.CompileOptions{HostFunctions: host, HostRegistry: registry})
}

func loadCheckedPath(path string) (*sema.Checked, error) {
	return loadCheckedPathWithModuleRoot(path, "")
}

func loadCheckedPathWithModuleRoot(path string, moduleRoot string) (*sema.Checked, error) {
	loaded, err := loadPathWithModuleRoot(path, moduleRoot)
	if err != nil {
		return nil, err
	}
	importerDir := path
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		importerDir = path
	} else {
		importerDir = filepath.Dir(path)
	}

	allImports := collectImports(loaded.Files)
	var deps []*loadedModule
	if len(allImports) > 0 {
		visited := make(map[string]bool)
		deps, err = loadDependencies(importerDir, allImports, moduleRoot, visited)
		if err != nil {
			return nil, err
		}
	}

	importedPackages := make(map[string]*sema.ImportedPackage)
	var importedChecks []*sema.Checked
	for _, dep := range deps {
		goFiles := make([]*ast.File, 0, len(dep.Files))
		for _, f := range dep.Files {
			goFiles = append(goFiles, f.AST)
		}
		fset := token.NewFileSet()
		for _, f := range dep.Files {
			parser.ParseFile(fset, f.Path, f.Text, parser.SkipObjectResolution)
		}
		depChecked, err := sema.CheckGoPackageDep(fset, goFiles, sema.CheckOptions{PkgPath: dep.PkgPath})
		if err != nil {
			return nil, fmt.Errorf("eval: failed to check imported package %q: %w", dep.PkgPath, err)
		}
		depChecked.PkgPath = dep.PkgPath
		dep.Checked = depChecked
		importedChecks = append(importedChecks, depChecked)
		importedPackages[dep.PkgPath] = &sema.ImportedPackage{
			PkgPath: dep.PkgPath,
			Scope:   depChecked.Facts.PackageScope,
		}
	}

	opts := sema.CheckOptions{
		ImportedPackages: importedPackages,
	}
	checked, err := checkLoadedProgram(loaded, checkLoadedProgramOptions{
		CheckOptions: opts,
		ModuleRoot:   moduleRoot,
	})
	if err != nil {
		return nil, err
	}
	checked.ImportedChecks = importedChecks
	return checked, nil
}

func checkSource(path, src string, opts sema.CheckOptions) (*sema.Checked, error) {
	return checkSourceFiles([]loadedFile{{Path: path, Text: src}}, opts)
}

func checkSourceFiles(files []loadedFile, opts sema.CheckOptions) (*sema.Checked, error) {
	if len(files) == 0 {
		return nil, nil
	}
	fset := token.NewFileSet()
	parsed := make([]*ast.File, 0, len(files))
	for _, file := range files {
		fileAST, err := parser.ParseFile(fset, file.Path, file.Text, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, fileAST)
	}
	checked, err := sema.CheckGoPackageWithOptions(fset, parsed, opts)
	if err != nil {
		return nil, err
	}
	checked.SourceLines = sourceLinesForLoadedFiles(files)
	return checked, nil
}

func hostSignaturesToTypes(host map[string]HostSignature) (map[string]semaTypes.HostSignature, error) {
	if len(host) == 0 {
		return nil, nil
	}
	result := make(map[string]semaTypes.HostSignature, len(host))
	for name, sig := range host {
		params := make([]semaTypes.Type, len(sig.ParamKinds))
		for i, kind := range sig.ParamKinds {
			typ, err := typeForRuntimeKind(kind)
			if err != nil {
				return nil, fmt.Errorf("testutil: host function %q param %d: %w", name, i, err)
			}
			if sig.Variadic && i == len(sig.ParamKinds)-1 {
				typ = &semaTypes.SliceType{Elem: typ}
			}
			params[i] = typ
		}
		results := make([]semaTypes.Type, len(sig.ResultKinds))
		for i, kind := range sig.ResultKinds {
			resultType, err := typeForRuntimeKind(kind)
			if err != nil {
				return nil, fmt.Errorf("testutil: host function %q result %d: %w", name, i, err)
			}
			results[i] = resultType
		}
		result[name] = semaTypes.HostSignature{Params: params, Results: results, Variadic: sig.Variadic}
	}
	return result, nil
}

func runtimeHostSignaturesToTypes(host map[string]runtime.HostSignature) (map[string]semaTypes.HostSignature, error) {
	if len(host) == 0 {
		return nil, nil
	}
	result := make(map[string]HostSignature, len(host))
	for name, sig := range host {
		result[name] = HostSignature{
			ParamKinds:  append([]runtime.Kind(nil), sig.ParamKinds...),
			ResultKinds: append([]runtime.Kind(nil), sig.ResultKinds...),
			Variadic:    sig.Variadic,
		}
	}
	return hostSignaturesToTypes(result)
}

func hostFuncsToSignatures(host map[string]any) (map[string]HostSignature, error) {
	if len(host) == 0 {
		return nil, nil
	}
	result := make(map[string]HostSignature, len(host))
	for name, fn := range host {
		paramKinds, resultKinds, variadic, err := runtime.InferHostFunctionSignature(fn)
		if err != nil {
			return nil, fmt.Errorf("testutil: host function %q: %w", name, err)
		}
		result[name] = HostSignature{ParamKinds: paramKinds, ResultKinds: resultKinds, Variadic: variadic}
	}
	return result, nil
}

func typeForRuntimeKind(kind runtime.Kind) (semaTypes.Type, error) {
	switch kind {
	case runtime.Bool:
		return semaTypes.BoolType, nil
	case runtime.Int:
		return semaTypes.IntType, nil
	case runtime.Int64:
		return semaTypes.Int64Type, nil
	case runtime.Float64:
		return semaTypes.Float64Type, nil
	case runtime.String:
		return semaTypes.StringType, nil
	default:
		return nil, fmt.Errorf("unsupported host kind %d", kind)
	}
}

func RunFunctionSource(program *bytecode.Program, funcName string, args ...runtime.Value) (runtime.Value, error) {
	return RunFunctionSourceWithDebug(program, funcName, false, args...)
}

func RunFunctionSourceWithDebug(program *bytecode.Program, funcName string, debug bool, args ...runtime.Value) (runtime.Value, error) {
	if program == nil {
		return runtime.Value{}, fmt.Errorf("testutil: missing program")
	}
	index, ok := program.LookupFunction(funcName)
	if !ok {
		return runtime.Value{}, fmt.Errorf("testutil: unknown function %q", funcName)
	}
	return vm.RunFunctionWithDebug(program, index, args, debug)
}

func RunFunctionValues(program *bytecode.Program, funcName string, args ...runtime.Value) ([]runtime.Value, error) {
	return RunFunctionValuesWithDebug(program, funcName, false, args...)
}

func RunFunctionValuesWithDebug(program *bytecode.Program, funcName string, debug bool, args ...runtime.Value) ([]runtime.Value, error) {
	if program == nil {
		return nil, fmt.Errorf("testutil: missing program")
	}
	index, ok := program.LookupFunction(funcName)
	if !ok {
		return nil, fmt.Errorf("testutil: unknown function %q", funcName)
	}
	return vm.RunFunctionValuesWithDebug(program, index, args, debug)
}

func RunFunctionWithHost(program *bytecode.Program, funcName string, host *runtime.HostRegistry, ctx *runtime.HostContext, args ...runtime.Value) (runtime.Value, error) {
	return RunFunctionWithHostDebug(program, funcName, host, ctx, false, args...)
}

func RunFunctionWithHostDebug(program *bytecode.Program, funcName string, host *runtime.HostRegistry, ctx *runtime.HostContext, debug bool, args ...runtime.Value) (runtime.Value, error) {
	if program == nil {
		return runtime.Value{}, fmt.Errorf("testutil: missing program")
	}
	index, ok := program.LookupFunction(funcName)
	if !ok {
		return runtime.Value{}, fmt.Errorf("testutil: unknown function %q", funcName)
	}
	return vm.RunFunctionWithHostDebug(program, index, args, host, ctx, debug)
}

func RunFunctionWithHostValues(program *bytecode.Program, funcName string, host *runtime.HostRegistry, ctx *runtime.HostContext, args ...runtime.Value) ([]runtime.Value, error) {
	return RunFunctionWithHostValuesDebug(program, funcName, host, ctx, false, args...)
}

func RunFunctionWithHostValuesDebug(program *bytecode.Program, funcName string, host *runtime.HostRegistry, ctx *runtime.HostContext, debug bool, args ...runtime.Value) ([]runtime.Value, error) {
	if program == nil {
		return nil, fmt.Errorf("testutil: missing program")
	}
	index, ok := program.LookupFunction(funcName)
	if !ok {
		return nil, fmt.Errorf("testutil: unknown function %q", funcName)
	}
	return vm.RunFunctionWithHostValuesDebug(program, index, args, host, ctx, debug)
}

func RestoreSnapshot(program *bytecode.Program, snapshot *runtime.Snapshot, host *runtime.HostRegistry, ctx *runtime.HostContext) (*runtime.Suspension, error) {
	return vm.RestoreSnapshot(program, snapshot, host, ctx)
}

type HookModule struct {
	Program *bytecode.Program
	Host    *runtime.HostRegistry
	Schemas map[string]runtime.StructSchema
}

func MustCompileHookModule(src string, hostSign map[string]HostSignature) *HookModule {
	host := runtime.NewHostRegistry()
	for name, sig := range hostSign {
		if err := host.Declare(name, runtime.HostSignature{
			ParamKinds:  append([]runtime.Kind(nil), sig.ParamKinds...),
			ResultKinds: append([]runtime.Kind(nil), sig.ResultKinds...),
			Variadic:    sig.Variadic,
		}); err != nil {
			fmt.Printf("CompileSource: %v\n", err)
			return nil
		}
	}
	program, err := CompileSourceWithHostRegistry(src, host)
	if err != nil {
		fmt.Printf("CompileSource: %v\n", err)
		return nil
	}
	return &HookModule{
		Program: program,
		Host:    host,
		Schemas: runtimeSchemasFromProgram(program, host),
	}
}

func MustCompileHookModuleWithHost(src string, host *runtime.HostRegistry) *HookModule {
	program, err := CompileSourceWithHostRegistry(src, host)
	if err != nil {
		fmt.Printf("CompileSourceWithHostRegistry: %v\n", err)
		return nil
	}
	return &HookModule{
		Program: program,
		Host:    host,
		Schemas: runtimeSchemasFromProgram(program, host),
	}
}

func MustCompileHookModuleFileWithHost(path string, src string, host *runtime.HostRegistry) *HookModule {
	program, err := CompileSourceFileWithHostRegistry(path, src, host)
	if err != nil {
		fmt.Printf("CompileSourceFileWithHostRegistry: %v\n", err)
		return nil
	}
	return &HookModule{
		Program: program,
		Host:    host,
		Schemas: runtimeSchemasFromProgram(program, host),
	}
}

func MustCompileHookModuleWithFuncs(src string, hostFuncs map[string]any) *HookModule {
	host := runtime.NewHostRegistry()
	program, err := CompileSourceWithHostFuncsInto(src, host, hostFuncs)
	if err != nil {
		fmt.Printf("CompileSourceWithHostFuncs: %v\n", err)
		return nil
	}
	return &HookModule{
		Program: program,
		Host:    host,
		Schemas: runtimeSchemasFromProgram(program, host),
	}
}

func MustCompileHookModuleWithFuncsInto(src string, host *runtime.HostRegistry, hostFuncs map[string]any) *HookModule {
	program, err := CompileSourceWithHostFuncsInto(src, host, hostFuncs)
	if err != nil {
		fmt.Printf("CompileSourceWithHostFuncsInto: %v\n", err)
		return nil
	}
	return &HookModule{
		Program: program,
		Host:    host,
		Schemas: runtimeSchemasFromProgram(program, host),
	}
}

func InvokeHook(module *HookModule, hook string, state any, ctx any, env any) (any, error) {
	return InvokeHookWithDebug(module, hook, state, ctx, env, false)
}

func InvokeHookWithDebug(module *HookModule, hook string, state any, ctx any, env any, debug bool) (any, error) {
	if module == nil || module.Program == nil {
		return nil, fmt.Errorf("testutil: missing hook module")
	}
	index, ok := module.Program.LookupFunction(hook)
	if !ok {
		return state, nil
	}
	args := make([]runtime.Value, 0, 2)
	stateValue, err := runtime.FromGoValue(state, module.Schemas)
	if err != nil {
		return nil, err
	}
	args = append(args, stateValue)
	if ctx != nil {
		ctxValue, err := runtime.FromGoValue(ctx, module.Schemas)
		if err != nil {
			return nil, err
		}
		args = append(args, ctxValue)
	}
	result, err := vm.RunFunctionWithHostDebug(module.Program, index, args, module.Host, &runtime.HostContext{Env: env}, debug)
	if err != nil {
		return nil, err
	}
	return runtime.ToGoValue(result, reflect.TypeOf(state), module.Schemas)
}

func runtimeSchemasFromProgram(program *bytecode.Program, host *runtime.HostRegistry) map[string]runtime.StructSchema {
	size := 0
	if program != nil {
		size += len(program.StructSchemas)
	}
	if host != nil {
		size += len(host.Types())
	}
	if size == 0 {
		return nil
	}
	result := make(map[string]runtime.StructSchema, size)
	if host != nil {
		for name, schema := range host.Types() {
			result[name] = schema
		}
	}
	if program != nil {
		for name, schema := range program.StructSchemas {
			result[name] = schema
		}
	}
	return result
}

func RunAndCompare(dir string) error {
	vmResult, err := RunProgram(dir)
	if err != nil {
		return err
	}
	if vmResult.Kind != runtime.Int {
		return fmt.Errorf("testutil: only int comparison is supported, got kind %d", vmResult.Kind)
	}

	nativeResult, err := RunNative(dir)
	if err != nil {
		return err
	}
	if vmResult.Int() != nativeResult {
		return fmt.Errorf("vm result = %d, native result = %d", vmResult.Int(), nativeResult)
	}
	return nil
}

func RunNative(dir string) (int64, error) {
	loaded, err := loadPath(dir)
	if err != nil {
		return 0, err
	}

	workDir, err := os.MkdirTemp("", "goes-native-*")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(workDir)

	for _, src := range loaded.Files {
		text := strings.Replace(src.Text, "func main() int", "func goesMain() int", 1)
		if text == src.Text {
			return 0, fmt.Errorf("testutil: could not rewrite main signature in %s", src.Path)
		}
		path := filepath.Join(workDir, filepath.Base(src.Path))
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return 0, err
		}
	}

	runner := `package main
import "fmt"
func main() {
	fmt.Print(goesMain())
}
`
	if err := os.WriteFile(filepath.Join(workDir, "zz_goes_runner.go"), []byte(runner), 0o644); err != nil {
		return 0, err
	}

	cacheDir := filepath.Join(workDir, ".gocache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return 0, err
	}

	cmd := exec.Command("go", "run", ".")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "GO111MODULE=off", "GOCACHE="+cacheDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("native go run failed: %w: %s", err, strings.TrimSpace(string(output)))
	}

	value, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("native output %q is not an int: %w", strings.TrimSpace(string(output)), err)
	}
	return value, nil
}
