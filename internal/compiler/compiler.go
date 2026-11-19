package compiler

import (
	"fmt"

	"goes/internal/bytecode"
	"goes/internal/sema"
	"goes/runtime"
)

func CompileChecked(checked *sema.Checked) (*bytecode.Program, error) {
	return CompileCheckedWithOptions(checked, CompileOptions{})
}

type CompileOptions struct {
	HostFunctions   map[string]runtime.TypedHostSignature
	HostRegistry    *runtime.HostRegistry
	ImportedModules []*ImportedModule
	RequireMain     bool
}

type ImportedModule struct {
	PkgPath string
	Checked *sema.Checked
}

func CompileCheckedWithOptions(checked *sema.Checked, opts CompileOptions) (*bytecode.Program, error) {
	if checked == nil {
		return nil, fmt.Errorf("compiler: missing checked package")
	}
	if checked.Facts == nil {
		return nil, fmt.Errorf("compiler: missing facts")
	}
	if len(opts.ImportedModules) == 0 && len(checked.ImportedChecks) > 0 {
		for _, imp := range checked.ImportedChecks {
			opts.ImportedModules = append(opts.ImportedModules, &ImportedModule{
				PkgPath: imp.PkgPath,
				Checked: imp,
			})
		}
	}
	opts.RequireMain = true
	directProgram, handled, err := compileDirectGoFiles(checked, opts)
	if err != nil {
		return nil, err
	}
	if handled {
		return directProgram, nil
	}
	return nil, fmt.Errorf("compiler: direct go/ast lowering could not handle checked package")
}

func CompileCheckedWithImports(checked *sema.Checked, imports []*ImportedModule, opts CompileOptions) (*bytecode.Program, error) {
	if checked == nil {
		return nil, fmt.Errorf("compiler: missing checked package")
	}
	if checked.Facts == nil {
		return nil, fmt.Errorf("compiler: missing facts")
	}

	opts.ImportedModules = imports
	directProgram, handled, err := compileDirectGoFiles(checked, opts)
	if err != nil {
		return nil, err
	}
	if handled {
		return directProgram, nil
	}
	return nil, fmt.Errorf("compiler: direct go/ast lowering could not handle checked package with imports")
}
