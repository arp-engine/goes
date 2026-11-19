package sema

import (
	goast "go/ast"
	gotoken "go/token"
	"fmt"
)

func CheckGoPackage(fset *gotoken.FileSet, files []*goast.File) (*Checked, error) {
	return CheckGoPackageWithOptions(fset, files, CheckOptions{})
}

func CheckGoPackageWithOptions(fset *gotoken.FileSet, files []*goast.File, opts CheckOptions) (*Checked, error) {
	if err := validateGoPackageStructure(fset, files); err != nil {
		return nil, err
	}
	goInfo, err := ResolveGoPackageWithOptions(fset, files, ResolveOptions{
		HostFunctions:    opts.HostFunctions,
		HostRegistry:     opts.HostRegistry,
		ImportedPackages: opts.ImportedPackages,
		PkgPath:          opts.PkgPath,
	})
	if err != nil {
		return nil, err
	}
	if err := checkGoPackageSemantics(fset, files, goInfo); err != nil {
		return nil, err
	}
	return &Checked{
		FileSet: fset,
		GoFiles: files,
		Facts:   goInfo,
	}, nil
}

func CheckGoPackageDep(fset *gotoken.FileSet, files []*goast.File, opts CheckOptions) (*Checked, error) {
	if err := validateGoPackageStructureDep(fset, files); err != nil {
		return nil, err
	}
	goInfo, err := ResolveGoPackageWithOptions(fset, files, ResolveOptions{
		HostFunctions:    opts.HostFunctions,
		HostRegistry:     opts.HostRegistry,
		ImportedPackages: opts.ImportedPackages,
		PkgPath:          opts.PkgPath,
	})
	if err != nil {
		return nil, err
	}
	if err := checkGoPackageSemantics(fset, files, goInfo); err != nil {
		return nil, err
	}
	return &Checked{
		FileSet: fset,
		GoFiles: files,
		Facts:   goInfo,
	}, nil
}

func validateGoPackageStructure(fset *gotoken.FileSet, files []*goast.File) error {
	for _, file := range files {
		if file == nil {
			continue
		}
		if file.Name == nil || file.Name.Name != "main" {
			pos := gotoken.NoPos
			name := ""
			if file.Name != nil {
				pos = file.Name.Pos()
				name = file.Name.Name
			}
			return goastError(fset, pos, "only package main is supported, got %q", name)
		}
		for _, decl := range file.Decls {
			if err := validateGoDecl(fset, decl); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateGoPackageStructureDep(fset *gotoken.FileSet, files []*goast.File) error {
	for _, file := range files {
		if file == nil {
			continue
		}
		for _, decl := range file.Decls {
			if err := validateGoDecl(fset, decl); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateGoDecl(fset *gotoken.FileSet, decl goast.Decl) error {
	switch decl := decl.(type) {
	case *goast.GenDecl:
		for _, spec := range decl.Specs {
			switch spec := spec.(type) {
			case *goast.TypeSpec:
				if spec.TypeParams != nil {
					return goastUnsupported(fset, spec.TypeParams.Pos(), "type parameters are not supported")
				}
				if err := validateGoTypeExpr(fset, spec.Type); err != nil {
					return err
				}
			case *goast.ValueSpec:
				if spec.Type != nil {
					if err := validateGoTypeExpr(fset, spec.Type); err != nil {
						return err
					}
				}
			}
		}
	case *goast.FuncDecl:
		if decl.Type != nil && decl.Type.TypeParams != nil {
			return goastUnsupported(fset, decl.Type.TypeParams.Pos(), "type parameters are not supported")
		}
		if decl.Recv != nil {
			for _, field := range decl.Recv.List {
				if err := validateGoTypeExpr(fset, field.Type); err != nil {
					return err
				}
			}
		}
		if decl.Type != nil {
			if err := validateFieldListTypes(fset, decl.Type.Params); err != nil {
				return err
			}
			if err := validateFieldListTypes(fset, decl.Type.Results); err != nil {
				return err
			}
		}
		return validateGoBlock(fset, decl.Body)
	}
	return nil
}

func validateGoBlock(fset *gotoken.FileSet, block *goast.BlockStmt) error {
	if block == nil {
		return nil
	}
	for _, stmt := range block.List {
		if err := validateGoStmt(fset, stmt); err != nil {
			return err
		}
	}
	return nil
}

func validateGoStmt(fset *gotoken.FileSet, stmt goast.Stmt) error {
	switch stmt := stmt.(type) {
	case nil, *goast.EmptyStmt:
		return nil
	case *goast.BlockStmt:
		return validateGoBlock(fset, stmt)
	case *goast.IfStmt:
		if err := validateGoStmt(fset, stmt.Init); err != nil {
			return err
		}
		if err := validateGoBlock(fset, stmt.Body); err != nil {
			return err
		}
		switch elseStmt := stmt.Else.(type) {
		case nil:
			return nil
		case *goast.BlockStmt:
			return validateGoBlock(fset, elseStmt)
		default:
			return validateGoStmt(fset, elseStmt)
		}
	case *goast.ForStmt:
		if err := validateGoStmt(fset, stmt.Init); err != nil {
			return err
		}
		if err := validateGoStmt(fset, stmt.Post); err != nil {
			return err
		}
		return validateGoBlock(fset, stmt.Body)
	case *goast.RangeStmt:
		return validateGoBlock(fset, stmt.Body)
	case *goast.SwitchStmt:
		if err := validateGoStmt(fset, stmt.Init); err != nil {
			return err
		}
		if stmt.Body == nil {
			return nil
		}
		for _, clause := range stmt.Body.List {
			caseClause, ok := clause.(*goast.CaseClause)
			if !ok {
				return goastUnsupported(fset, clause.Pos(), "switch clause %T is not supported", clause)
			}
			for _, bodyStmt := range caseClause.Body {
				if err := validateGoStmt(fset, bodyStmt); err != nil {
					return err
				}
			}
		}
		return nil
	case *goast.DeferStmt:
		return goastUnsupported(fset, stmt.Pos(), "defer statements are not supported")
	case *goast.GoStmt:
		return goastUnsupported(fset, stmt.Pos(), "go statements are not supported")
	case *goast.SelectStmt:
		return goastUnsupported(fset, stmt.Pos(), "select statements are not supported")
	case *goast.LabeledStmt:
		return goastUnsupported(fset, stmt.Pos(), "labeled statements are not supported")
	case *goast.BranchStmt:
		switch stmt.Tok {
		case gotoken.GOTO:
			return goastUnsupported(fset, stmt.Pos(), "goto statements are not supported")
		case gotoken.FALLTHROUGH:
			return goastUnsupported(fset, stmt.Pos(), "fallthrough statements are not supported")
		default:
			return nil
		}
	default:
		return nil
	}
}

func validateFieldListTypes(fset *gotoken.FileSet, fields *goast.FieldList) error {
	if fields == nil {
		return nil
	}
	for i, field := range fields.List {
		if _, ok := field.Type.(*goast.Ellipsis); ok && i != len(fields.List)-1 {
			return goastUnsupported(fset, field.Pos(), "variadic parameter must be the final parameter")
		}
		if err := validateGoTypeExpr(fset, field.Type); err != nil {
			return err
		}
	}
	return nil
}

func validateGoTypeExpr(fset *gotoken.FileSet, expr goast.Expr) error {
	switch expr := expr.(type) {
	case nil, *goast.Ident:
		return nil
	case *goast.ArrayType:
		return validateGoTypeExpr(fset, expr.Elt)
	case *goast.MapType:
		if err := validateGoTypeExpr(fset, expr.Key); err != nil {
			return err
		}
		return validateGoTypeExpr(fset, expr.Value)
	case *goast.StructType:
		if expr.Fields == nil {
			return nil
		}
		for _, field := range expr.Fields.List {
			if len(field.Names) == 0 {
				return goastUnsupported(fset, field.Pos(), "embedded fields are not supported")
			}
			if err := validateGoTypeExpr(fset, field.Type); err != nil {
				return err
			}
		}
		return nil
	case *goast.StarExpr:
		return validateGoTypeExpr(fset, expr.X)
	case *goast.FuncType:
		if expr.TypeParams != nil {
			return goastUnsupported(fset, expr.TypeParams.Pos(), "type parameters are not supported")
		}
		if err := validateFieldListTypes(fset, expr.Params); err != nil {
			return err
		}
		return validateFieldListTypes(fset, expr.Results)
	case *goast.Ellipsis:
		return validateGoTypeExpr(fset, expr.Elt)
	case *goast.InterfaceType:
		return goastUnsupported(fset, expr.Pos(), "interface types are not supported")
	case *goast.ChanType:
		return goastUnsupported(fset, expr.Pos(), "chan types are not supported")
	case *goast.SelectorExpr:
		return nil
	default:
		return goastUnsupported(fset, expr.Pos(), "type expression %T is not supported", expr)
	}
}

func goastUnsupported(fset *gotoken.FileSet, pos gotoken.Pos, format string, args ...any) error {
	return formatGoASTError(fset, pos, "unsupported: "+fmt.Sprintf(format, args...))
}

func goastError(fset *gotoken.FileSet, pos gotoken.Pos, format string, args ...any) error {
	return formatGoASTError(fset, pos, fmt.Sprintf(format, args...))
}

func formatGoASTError(fset *gotoken.FileSet, pos gotoken.Pos, msg string) error {
	if fset == nil {
		return fmt.Errorf("%s", msg)
	}
	position := fset.Position(pos)
	if position.IsValid() {
		return fmt.Errorf("%s:%d:%d: %s", position.Filename, position.Line, position.Column, msg)
	}
	return fmt.Errorf("%s", msg)
}
