package eval

import (
	"fmt"
	goast "go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"goes/internal/sema"
)

type loadedProgram struct {
	FileSet  *token.FileSet
	Files    []loadedFile
	moduleRoot string
}

type loadedModule struct {
	PkgPath string
	Dir     string
	Files   []loadedFile
	Checked *sema.Checked
}

type loadedFile struct {
	Path string
	Text string
	AST  *goast.File
}

type checkLoadedProgramOptions struct {
	CheckOptions sema.CheckOptions
	ModuleRoot   string
}

func loadPath(path string) (*loadedProgram, error) {
	return loadPathWithModuleRoot(path, "")
}

func loadPathWithModuleRoot(path string, moduleRoot string) (*loadedProgram, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return loadDirWithModuleRoot(path, moduleRoot)
	}
	if !info.Mode().IsRegular() || !isGoesSourcePath(path) {
		return nil, os.ErrInvalid
	}
	return loadFiles([]string{path}, moduleRoot)
}

func loadSource(path string, text string) (*loadedProgram, error) {
	fset := token.NewFileSet()
	fileAST, err := parser.ParseFile(fset, path, text, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	return &loadedProgram{
		FileSet: fset,
		Files: []loadedFile{{
			Path: path,
			Text: normalizeFrontendText(text),
			AST:  fileAST,
		}},
	}, nil
}

func checkLoadedProgram(loaded *loadedProgram, opts checkLoadedProgramOptions) (*sema.Checked, error) {
	if loaded == nil {
		return nil, nil
	}
	goFiles := make([]*goast.File, 0, len(loaded.Files))
	for _, file := range loaded.Files {
		goFiles = append(goFiles, file.AST)
	}
	checked, err := sema.CheckGoPackageWithOptions(loaded.FileSet, goFiles, opts.CheckOptions)
	if err != nil {
		return nil, err
	}
	checked.SourceLines = sourceLinesForLoadedFiles(loaded.Files)
	return checked, nil
}

func loadDir(dir string) (*loadedProgram, error) {
	return loadDirWithModuleRoot(dir, "")
}

func loadDirWithModuleRoot(dir string, moduleRoot string) (*loadedProgram, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !isGoFile(entry) {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	slices.Sort(paths)
	return loadFiles(paths, moduleRoot)
}

func loadFiles(paths []string, moduleRoot string) (*loadedProgram, error) {
	fset := token.NewFileSet()
	loaded := &loadedProgram{
		FileSet:    fset,
		Files:      make([]loadedFile, 0, len(paths)),
		moduleRoot: moduleRoot,
	}
	for _, path := range paths {
		text, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		fileAST, err := parser.ParseFile(fset, path, text, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		loaded.Files = append(loaded.Files, loadedFile{
			Path: path,
			Text: normalizeFrontendText(string(text)),
			AST:  fileAST,
		})
	}
	return loaded, nil
}

func resolveImportPath(importerDir, importPath, moduleRoot string) (string, error) {
	if strings.HasPrefix(importPath, "./") || strings.HasPrefix(importPath, "../") {
		resolved := filepath.Join(importerDir, importPath)
		resolved, err := filepath.Abs(resolved)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("eval: cannot resolve import %q from %s: directory not found", importPath, importerDir)
		}
		return resolved, nil
	}
	if moduleRoot == "" {
		return "", fmt.Errorf("eval: cannot resolve import %q: no module root", importPath)
	}
	absRoot, err := filepath.Abs(moduleRoot)
	if err != nil {
		return "", err
	}
	resolved := filepath.Join(absRoot, importPath)
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("eval: cannot resolve import %q from module root %s: directory not found", importPath, moduleRoot)
	}
	return resolved, nil
}

func loadModule(dir string) (*loadedModule, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !isGoFile(entry) {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	slices.Sort(paths)
	if len(paths) == 0 {
		return nil, fmt.Errorf("eval: no Go source files in %s", dir)
	}
	fset := token.NewFileSet()
	files := make([]loadedFile, 0, len(paths))
	for _, path := range paths {
		text, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		fileAST, err := parser.ParseFile(fset, path, text, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		files = append(files, loadedFile{
			Path: path,
			Text: normalizeFrontendText(string(text)),
			AST:  fileAST,
		})
	}
	return &loadedModule{
		Dir:   dir,
		Files: files,
	}, nil
}

func loadDependencies(importerDir string, imports []*goast.ImportSpec, moduleRoot string, visited map[string]bool) ([]*loadedModule, error) {
	var deps []*loadedModule
	for _, imp := range imports {
		importPath := strings.Trim(imp.Path.Value, "\"")
		if importPath == "" {
			continue
		}
		dir, err := resolveImportPath(importerDir, importPath, moduleRoot)
		if err != nil {
			return nil, err
		}
		if visited[dir] {
			continue
		}
		visited[dir] = true
		mod, err := loadModule(dir)
		if err != nil {
			return nil, err
		}
		mod.PkgPath = importPath
		subDeps, err := loadDependencies(dir, collectImports(mod.Files), moduleRoot, visited)
		if err != nil {
			return nil, err
		}
		deps = append(deps, subDeps...)
		deps = append(deps, mod)
	}
	return deps, nil
}

func collectImports(files []loadedFile) []*goast.ImportSpec {
	var imports []*goast.ImportSpec
	for _, file := range files {
		if file.AST == nil {
			continue
		}
		for _, imp := range file.AST.Imports {
			imports = append(imports, imp)
		}
	}
	return imports
}

func isGoFile(entry fs.DirEntry) bool {
	if !entry.Type().IsRegular() {
		return false
	}
	return isGoesSourcePath(entry.Name())
}

func isGoesSourcePath(path string) bool {
	ext := filepath.Ext(path)
	return ext == ".go" || ext == ".goes"
}

func normalizeFrontendText(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}

func sourceLinesForLoadedFiles(files []loadedFile) map[string][]string {
	if len(files) == 0 {
		return nil
	}
	result := make(map[string][]string, len(files))
	for _, file := range files {
		result[file.Path] = strings.Split(normalizeFrontendText(file.Text), "\n")
	}
	return result
}
