package sema

import (
	"fmt"
	semaTypes "goes/types"
	goast "go/ast"
	gotoken "go/token"
	"strconv"
	"strings"
	"unicode"
)

func ResolveGoPackage(fset *gotoken.FileSet, files []*goast.File) (*Info, error) {
	return ResolveGoPackageWithOptions(fset, files, ResolveOptions{})
}

func ResolveGoPackageWithOptions(_ *gotoken.FileSet, files []*goast.File, opts ResolveOptions) (*Info, error) {
	r := &resolver{
		info: &Facts{
			Bindings:   make(map[any]*Symbol),
			Captures:   make(map[any]*Symbol),
			Types:      make(map[any]semaTypes.Type),
			ConstExprs: make(map[any]any),
			Selections: make(map[any]*Selection),
		},
		constValues:      make(map[*Symbol]any),
		pkgPath:          opts.PkgPath,
		scopeKinds:       make(map[*Scope]scopeKind),
		scopeFuncs:       make(map[*Scope]*funcFrame),
		hostFunctions:    opts.HostFunctions,
		hostTypes:        hostStructTypesFromRegistry(opts.HostRegistry),
		hostConsts:       hostConstsFromRegistry(opts.HostRegistry),
		importedPackages: opts.ImportedPackages,
	}

	pkgScope := NewScope(nil)
	r.scopeKinds[pkgScope] = scopePackage
	r.info.PackageScope = pkgScope
	for name, typ := range r.hostTypes {
		if err := r.declare(pkgScope, &Symbol{Name: name, Kind: SymType, Type: typ}); err != nil {
			return nil, err
		}
	}
	for name, binding := range r.hostConsts {
		sym := &Symbol{Name: name, Kind: SymConst, Type: binding.typ}
		if err := r.declare(pkgScope, sym); err != nil {
			return nil, err
		}
		r.constValues[sym] = goConstExprFromAny(binding.expr)
	}

	for _, file := range files {
		if file == nil {
			continue
		}
		if r.packageName == "" {
			r.packageName = file.Name.Name
		} else if file.Name != nil && file.Name.Name != r.packageName {
			return nil, fmt.Errorf("sema: mixed package names %q and %q", r.packageName, file.Name.Name)
		}
	}

	for _, file := range files {
		if file == nil {
			continue
		}
		if err := r.resolveGoImports(pkgScope, file); err != nil {
			return nil, err
		}
	}

	for _, file := range files {
		if file == nil {
			continue
		}
		if err := r.declareGoPackageNames(pkgScope, file); err != nil {
			return nil, err
		}
	}

	for _, file := range files {
		if file == nil {
			continue
		}
		if err := r.defineGoPackageTypes(pkgScope, file); err != nil {
			return nil, err
		}
	}
	if err := validateGoTypeCycles(pkgScope); err != nil {
		return nil, err
	}

	for _, file := range files {
		if file == nil {
			continue
		}
		if err := r.defineGoPackageMethods(pkgScope, file); err != nil {
			return nil, err
		}
	}

	for _, file := range files {
		if file == nil {
			continue
		}
		if err := r.defineGoPackageFuncTypes(pkgScope, file); err != nil {
			return nil, err
		}
	}

	for _, file := range files {
		if file == nil {
			continue
		}
		if err := r.defineGoPackageValueTypes(pkgScope, file); err != nil {
			return nil, err
		}
	}

	for _, file := range files {
		if file == nil {
			continue
		}
		if err := r.resolveGoFile(pkgScope, file); err != nil {
			return nil, err
		}
	}

	return r.info, nil
}

func goConstExprFromAny(expr any) any {
	switch expr := expr.(type) {
	case nil:
		return nil
	case goast.Expr:
		return expr
	default:
		return nil
	}
}

func isExported(name string) bool {
	if name == "" {
		return false
	}
	r := []rune(name)[0]
	return unicode.IsUpper(r)
}

func (r *resolver) declareGoPackageNames(scope *Scope, file *goast.File) error {
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *goast.GenDecl:
			switch decl.Tok {
			case gotoken.VAR:
				for _, spec := range decl.Specs {
					valueSpec := spec.(*goast.ValueSpec)
					for _, name := range valueSpec.Names {
						sym := &Symbol{Name: name.Name, PkgPath: r.pkgPath, Exported: isExported(name.Name), Kind: SymVar, Type: semaTypes.InvalidType}
						if err := r.declare(scope, sym); err != nil {
							return err
						}
					}
				}
			case gotoken.CONST:
				for _, spec := range decl.Specs {
					valueSpec := spec.(*goast.ValueSpec)
					for i, name := range valueSpec.Names {
						sym := &Symbol{Name: name.Name, PkgPath: r.pkgPath, Exported: isExported(name.Name), Kind: SymConst, Type: semaTypes.InvalidType}
						if err := r.declare(scope, sym); err != nil {
							return err
						}
						if i < len(valueSpec.Values) {
							r.constValues[sym] = valueSpec.Values[i]
						} else if len(valueSpec.Values) == 1 {
							r.constValues[sym] = valueSpec.Values[0]
						}
					}
				}
			case gotoken.TYPE:
				for _, spec := range decl.Specs {
					typeSpec := spec.(*goast.TypeSpec)
					named := &semaTypes.NamedType{Name: typeSpec.Name.Name, PkgPath: r.pkgPath, Underlying: semaTypes.InvalidType}
					sym := &Symbol{Name: typeSpec.Name.Name, PkgPath: r.pkgPath, Exported: isExported(typeSpec.Name.Name), Kind: SymType, Type: named}
					if err := r.declare(scope, sym); err != nil {
						return err
					}
				}
			}
		case *goast.FuncDecl:
			if decl.Recv == nil {
				sym := &Symbol{Name: decl.Name.Name, PkgPath: r.pkgPath, Exported: isExported(decl.Name.Name), Kind: SymFunc, Type: semaTypes.InvalidType}
				if err := r.declare(scope, sym); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (r *resolver) resolveGoImports(scope *Scope, file *goast.File) error {
	if file == nil || len(file.Imports) == 0 {
		return nil
	}
	for _, imp := range file.Imports {
		importPath := strings.Trim(imp.Path.Value, "\"")
		if importPath == "" {
			continue
		}
		pkg, ok := r.importedPackages[importPath]
		if !ok || pkg == nil || pkg.Scope == nil {
			return fmt.Errorf("sema: imported package %q not found", importPath)
		}
		localName := pkgLocalName(importPath, imp)
		if localName == "_" {
			continue
		}
		if localName == "." {
			return fmt.Errorf("sema: dot imports are not supported")
		}
		if scope.LookupLocal(localName) != nil {
			return fmt.Errorf("sema: %q redeclared as imported package name", localName)
		}
		scope.Imported[localName] = pkg.Scope
		pkgSym := &Symbol{Name: localName, Kind: SymPackage, PkgPath: importPath}
		if err := r.declare(scope, pkgSym); err != nil {
			return err
		}
	}
	return nil
}

func pkgLocalName(importPath string, imp *goast.ImportSpec) string {
	if imp.Name != nil {
		return imp.Name.Name
	}
	idx := strings.LastIndex(importPath, "/")
	if idx >= 0 {
		return importPath[idx+1:]
	}
	return importPath
}

func (r *resolver) resolveGoPkgSelector(scope *Scope, pkgName, selName string) *Symbol {
	if scope == nil {
		return nil
	}
	for s := scope; s != nil; s = s.Parent {
		pkgScope, ok := s.Imported[pkgName]
		if !ok {
			continue
		}
		sym := pkgScope.LookupLocal(selName)
		if sym == nil {
			return nil
		}
		if !sym.Exported {
			return nil
		}
		return sym
	}
	return nil
}

func (r *resolver) defineGoPackageTypes(scope *Scope, file *goast.File) error {
	for _, decl := range file.Decls {
		gen, ok := decl.(*goast.GenDecl)
		if !ok || gen.Tok != gotoken.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec := spec.(*goast.TypeSpec)
			sym := scope.LookupLocal(typeSpec.Name.Name)
			named, _ := sym.Type.(*semaTypes.NamedType)
			underlying, err := r.resolveGoTypeExpr(scope, typeSpec.Type)
			if err != nil {
				return err
			}
			named.Underlying = underlying
		}
	}
	return nil
}

func (r *resolver) defineGoPackageValueTypes(scope *Scope, file *goast.File) error {
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *goast.GenDecl:
			switch decl.Tok {
			case gotoken.VAR, gotoken.CONST:
				for _, spec := range decl.Specs {
					valueSpec := spec.(*goast.ValueSpec)
					var explicitType semaTypes.Type
					if valueSpec.Type != nil {
						typ, err := r.resolveGoTypeExpr(scope, valueSpec.Type)
						if err != nil {
							return err
						}
						explicitType = typ
					}
					for i, name := range valueSpec.Names {
						typ := explicitType
						if typ == nil {
							switch {
							case i < len(valueSpec.Values):
								typ = r.inferGoExprType(scope, valueSpec.Values[i])
							case len(valueSpec.Values) == 1:
								typ = r.inferGoExprType(scope, valueSpec.Values[0])
							default:
								typ = semaTypes.InvalidType
							}
						}
						if sym := scope.LookupLocal(name.Name); sym != nil {
							sym.Type = typ
						}
					}
				}
			}
		case *goast.FuncDecl:
			if decl.Recv != nil {
				continue
			}
			continue
		}
	}
	return nil
}

func (r *resolver) defineGoPackageFuncTypes(scope *Scope, file *goast.File) error {
	for _, decl := range file.Decls {
		funcDecl, ok := decl.(*goast.FuncDecl)
		if !ok || funcDecl.Recv != nil {
			continue
		}
		typ, err := r.resolveGoTypeExpr(scope, funcDecl.Type)
		if err != nil {
			return err
		}
		scope.LookupLocal(funcDecl.Name.Name).Type = typ
	}
	return nil
}

func (r *resolver) defineGoPackageMethods(scope *Scope, file *goast.File) error {
	for _, decl := range file.Decls {
		funcDecl, ok := decl.(*goast.FuncDecl)
		if !ok || funcDecl.Recv == nil {
			continue
		}
		if len(funcDecl.Recv.List) != 1 {
			return fmt.Errorf("sema: methods must have exactly one receiver")
		}
		recvType, err := r.resolveGoTypeExpr(scope, funcDecl.Recv.List[0].Type)
		if err != nil {
			return err
		}
		named, ptrRecv, err := resolveGoReceiverNamedType(recvType)
		if err != nil {
			return err
		}
		sigType, err := r.resolveGoTypeExpr(scope, funcDecl.Type)
		if err != nil {
			return err
		}
		sig, ok := sigType.(*semaTypes.FuncType)
		if !ok || sig == nil {
			return fmt.Errorf("sema: method %q has unexpected signature type %T", funcDecl.Name.Name, sigType)
		}
		for _, method := range named.Methods {
			if method.Name == funcDecl.Name.Name {
				return fmt.Errorf("sema: duplicate method %q on %s", funcDecl.Name.Name, named.Name)
			}
		}
		named.Methods = append(named.Methods, semaTypes.Method{Name: funcDecl.Name.Name, Type: sig, PointerRecv: ptrRecv})
	}
	return nil
}

func (r *resolver) resolveGoFile(scope *Scope, file *goast.File) error {
	for _, decl := range file.Decls {
		if err := r.resolveGoPackageDecl(scope, decl); err != nil {
			return err
		}
	}
	return nil
}

func (r *resolver) resolveGoPackageDecl(scope *Scope, decl goast.Decl) error {
	switch decl := decl.(type) {
	case *goast.FuncDecl:
		return r.resolveGoFunctionDecl(scope, decl)
	case *goast.GenDecl:
		switch decl.Tok {
		case gotoken.VAR, gotoken.CONST:
			for _, spec := range decl.Specs {
				valueSpec := spec.(*goast.ValueSpec)
				if valueSpec.Type != nil {
					if _, err := r.resolveGoTypeExpr(scope, valueSpec.Type); err != nil {
						return err
					}
				}
				for _, value := range valueSpec.Values {
					if err := r.resolveGoExpr(scope, value); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (r *resolver) resolveGoFunctionDecl(outer *Scope, decl *goast.FuncDecl) error {
	if decl == nil || decl.Body == nil {
		return nil
	}

	funcScope := NewScope(outer)
	frame := &funcFrame{parent: r.currentFunc}
	r.scopeKinds[funcScope] = scopeFunc
	r.scopeFuncs[funcScope] = frame

	prevFunc := r.currentFunc
	r.currentFunc = frame
	defer func() {
		r.currentFunc = prevFunc
	}()

	if decl.Recv != nil {
		for _, field := range decl.Recv.List {
			recvType, err := r.resolveGoTypeExpr(outer, field.Type)
			if err != nil {
				return err
			}
			for _, name := range field.Names {
				if err := r.declare(funcScope, &Symbol{Name: name.Name, Kind: SymVar, Type: recvType}); err != nil {
					return err
				}
			}
		}
	}
	if decl.Type != nil && decl.Type.Params != nil {
		for _, field := range decl.Type.Params.List {
			paramType, err := r.resolveGoTypeExpr(outer, field.Type)
			if err != nil {
				return err
			}
			for _, name := range field.Names {
				if err := r.declare(funcScope, &Symbol{Name: name.Name, Kind: SymVar, Type: paramType}); err != nil {
					return err
				}
			}
		}
	}
	if decl.Type != nil && decl.Type.Results != nil {
		for _, field := range decl.Type.Results.List {
			resultType, err := r.resolveGoTypeExpr(outer, field.Type)
			if err != nil {
				return err
			}
			for _, name := range field.Names {
				if err := r.declare(funcScope, &Symbol{Name: name.Name, Kind: SymVar, Type: resultType}); err != nil {
					return err
				}
			}
		}
	}
	return r.resolveGoBlockInScope(funcScope, decl.Body)
}

func (r *resolver) resolveGoBlock(parent *Scope, block *goast.BlockStmt) error {
	if block == nil {
		return nil
	}
	blockScope := NewScope(parent)
	r.scopeKinds[blockScope] = scopeBlock
	r.scopeFuncs[blockScope] = r.currentFunc
	return r.resolveGoBlockInScope(blockScope, block)
}

func (r *resolver) resolveGoBlockInScope(scope *Scope, block *goast.BlockStmt) error {
	if block == nil {
		return nil
	}
	for _, stmt := range block.List {
		if err := r.resolveGoStmt(scope, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (r *resolver) resolveGoStmt(scope *Scope, stmt goast.Stmt) error {
	switch stmt := stmt.(type) {
	case nil, *goast.EmptyStmt:
		return nil
	case *goast.BlockStmt:
		return r.resolveGoBlock(scope, stmt)
	case *goast.DeclStmt:
		return r.resolveGoLocalDecl(scope, stmt.Decl)
	case *goast.AssignStmt:
		if stmt.Tok == gotoken.DEFINE {
			introducesNew := false
			for _, lhs := range stmt.Lhs {
				ident, ok := lhs.(*goast.Ident)
				if !ok {
					return fmt.Errorf("sema: short variable declaration requires identifiers")
				}
				if ident.Name == "_" {
					continue
				}
				if scope.LookupLocal(ident.Name) == nil {
					introducesNew = true
					break
				}
			}
			if !introducesNew {
				return fmt.Errorf("sema: short variable declaration must introduce at least one new variable")
			}
			for _, expr := range stmt.Rhs {
				if err := r.resolveGoExpr(scope, expr); err != nil {
					return err
				}
			}
			rhsTypes := make([]semaTypes.Type, 0, len(stmt.Rhs))
			for _, expr := range stmt.Rhs {
				rhsTypes = append(rhsTypes, r.inferGoExprTypes(scope, expr)...)
			}
			for i, lhs := range stmt.Lhs {
				ident := lhs.(*goast.Ident)
				if ident.Name == "_" {
					continue
				}
				if scope.LookupLocal(ident.Name) != nil {
					continue
				}
				var typ semaTypes.Type = semaTypes.InvalidType
				if i < len(rhsTypes) {
					typ = rhsTypes[i]
				}
				if err := r.declare(scope, &Symbol{Name: ident.Name, Kind: SymVar, Type: typ}); err != nil {
					return err
				}
			}
			return nil
		}
		for _, expr := range stmt.Lhs {
			if err := r.resolveGoExpr(scope, expr); err != nil {
				return err
			}
		}
		for _, expr := range stmt.Rhs {
			if err := r.resolveGoExpr(scope, expr); err != nil {
				return err
			}
		}
		return nil
	case *goast.IncDecStmt:
		return r.resolveGoExpr(scope, stmt.X)
	case *goast.IfStmt:
		ifScope := NewScope(scope)
		r.scopeKinds[ifScope] = scopeBlock
		r.scopeFuncs[ifScope] = r.currentFunc
		if stmt.Init != nil {
			if err := r.resolveGoStmt(ifScope, stmt.Init); err != nil {
				return err
			}
		}
		if err := r.resolveGoExpr(ifScope, stmt.Cond); err != nil {
			return err
		}
		if err := r.resolveGoBlock(ifScope, stmt.Body); err != nil {
			return err
		}
		switch elseStmt := stmt.Else.(type) {
		case nil:
			return nil
		case *goast.BlockStmt:
			return r.resolveGoBlock(ifScope, elseStmt)
		default:
			return r.resolveGoStmt(ifScope, elseStmt)
		}
	case *goast.ForStmt:
		forScope := NewScope(scope)
		r.scopeKinds[forScope] = scopeBlock
		r.scopeFuncs[forScope] = r.currentFunc
		if stmt.Init != nil {
			if err := r.resolveGoStmt(forScope, stmt.Init); err != nil {
				return err
			}
		}
		if stmt.Cond != nil {
			if err := r.resolveGoExpr(forScope, stmt.Cond); err != nil {
				return err
			}
		}
		if stmt.Post != nil {
			if err := r.resolveGoStmt(forScope, stmt.Post); err != nil {
				return err
			}
		}
		return r.resolveGoBlock(forScope, stmt.Body)
	case *goast.RangeStmt:
		rangeScope := NewScope(scope)
		r.scopeKinds[rangeScope] = scopeBlock
		r.scopeFuncs[rangeScope] = r.currentFunc
		if err := r.resolveGoExpr(rangeScope, stmt.X); err != nil {
			return err
		}
		rangeType := r.inferGoExprType(rangeScope, stmt.X)
		var keyType semaTypes.Type = semaTypes.InvalidType
		var valueType semaTypes.Type = semaTypes.InvalidType
		switch t := semaTypes.Underlying(rangeType).(type) {
		case *semaTypes.MapType:
			keyType = t.Key
			valueType = t.Value
		case *semaTypes.ArrayType:
			keyType = semaTypes.IntType
			valueType = t.Elem
		case *semaTypes.SliceType:
			keyType = semaTypes.IntType
			valueType = t.Elem
		default:
			if semaTypes.IsInteger(rangeType) {
				keyType = rangeType
			}
		}
		if stmt.Tok == gotoken.DEFINE {
			if ident, ok := stmt.Key.(*goast.Ident); ok {
				if ident.Name != "_" {
					if err := r.declare(rangeScope, &Symbol{Name: ident.Name, Kind: SymVar, Type: keyType}); err != nil {
						return err
					}
				}
			} else if stmt.Key != nil {
				return fmt.Errorf("sema: range define key must be identifier")
			}
			if ident, ok := stmt.Value.(*goast.Ident); ok {
				if ident.Name != "_" {
					if err := r.declare(rangeScope, &Symbol{Name: ident.Name, Kind: SymVar, Type: valueType}); err != nil {
						return err
					}
				}
			} else if stmt.Value != nil {
				return fmt.Errorf("sema: range define value must be identifier")
			}
		} else {
			if err := r.resolveGoExpr(rangeScope, stmt.Key); err != nil {
				return err
			}
			if err := r.resolveGoExpr(rangeScope, stmt.Value); err != nil {
				return err
			}
		}
		return r.resolveGoBlock(rangeScope, stmt.Body)
	case *goast.SwitchStmt:
		switchScope := NewScope(scope)
		r.scopeKinds[switchScope] = scopeBlock
		r.scopeFuncs[switchScope] = r.currentFunc
		if stmt.Init != nil {
			if err := r.resolveGoStmt(switchScope, stmt.Init); err != nil {
				return err
			}
		}
		if stmt.Tag != nil {
			if err := r.resolveGoExpr(switchScope, stmt.Tag); err != nil {
				return err
			}
		}
		for _, clause := range stmt.Body.List {
			caseClause, ok := clause.(*goast.CaseClause)
			if !ok {
				continue
			}
			for _, expr := range caseClause.List {
				if err := r.resolveGoExpr(switchScope, expr); err != nil {
					return err
				}
			}
			clauseScope := NewScope(switchScope)
			r.scopeKinds[clauseScope] = scopeBlock
			r.scopeFuncs[clauseScope] = r.currentFunc
			for _, bodyStmt := range caseClause.Body {
				if err := r.resolveGoStmt(clauseScope, bodyStmt); err != nil {
					return err
				}
			}
		}
		return nil
	case *goast.ReturnStmt:
		for _, expr := range stmt.Results {
			if err := r.resolveGoExpr(scope, expr); err != nil {
				return err
			}
		}
		return nil
	case *goast.ExprStmt:
		return r.resolveGoExpr(scope, stmt.X)
	default:
		return nil
	}
}

func (r *resolver) resolveGoLocalDecl(scope *Scope, decl goast.Decl) error {
	gen, ok := decl.(*goast.GenDecl)
	if !ok {
		return nil
	}
	switch gen.Tok {
	case gotoken.VAR:
		for _, spec := range gen.Specs {
			valueSpec := spec.(*goast.ValueSpec)
			var explicitType semaTypes.Type
			var err error
			if valueSpec.Type != nil {
				explicitType, err = r.resolveGoTypeExpr(scope, valueSpec.Type)
				if err != nil {
					return err
				}
			}
			for _, expr := range valueSpec.Values {
				if err := r.resolveGoExpr(scope, expr); err != nil {
					return err
				}
			}
			for i, name := range valueSpec.Names {
				typ := explicitType
				if typ == nil && i < len(valueSpec.Values) {
					typ = r.inferGoExprType(scope, valueSpec.Values[i])
				}
				if err := r.declare(scope, &Symbol{Name: name.Name, Kind: SymVar, Type: typ}); err != nil {
					return err
				}
			}
		}
	case gotoken.CONST:
		for _, spec := range gen.Specs {
			valueSpec := spec.(*goast.ValueSpec)
			var explicitType semaTypes.Type
			var err error
			if valueSpec.Type != nil {
				explicitType, err = r.resolveGoTypeExpr(scope, valueSpec.Type)
				if err != nil {
					return err
				}
			}
			for _, expr := range valueSpec.Values {
				if err := r.resolveGoExpr(scope, expr); err != nil {
					return err
				}
			}
			for i, name := range valueSpec.Names {
				typ := explicitType
				if typ == nil && i < len(valueSpec.Values) {
					typ = r.inferGoExprType(scope, valueSpec.Values[i])
				}
				sym := &Symbol{Name: name.Name, Kind: SymConst, Type: typ}
				if err := r.declare(scope, sym); err != nil {
					return err
				}
				if i < len(valueSpec.Values) {
					r.constValues[sym] = valueSpec.Values[i]
				} else if len(valueSpec.Values) == 1 {
					r.constValues[sym] = valueSpec.Values[0]
				}
			}
		}
	case gotoken.TYPE:
		for _, spec := range gen.Specs {
			typeSpec := spec.(*goast.TypeSpec)
			named := &semaTypes.NamedType{Name: typeSpec.Name.Name, Underlying: semaTypes.InvalidType}
			if err := r.declare(scope, &Symbol{Name: typeSpec.Name.Name, Kind: SymType, Type: named}); err != nil {
				return err
			}
			underlying, err := r.resolveGoTypeExpr(scope, typeSpec.Type)
			if err != nil {
				return err
			}
			named.Underlying = underlying
		}
	}
	return nil
}

func (r *resolver) resolveGoTypeExpr(scope *Scope, expr goast.Expr) (semaTypes.Type, error) {
	switch expr := expr.(type) {
	case *goast.Ident:
		if builtin := semaTypes.LookupBuiltin(expr.Name); builtin != nil {
			return builtin, nil
		}
		sym, _ := scope.Lookup(expr.Name)
		if sym == nil {
			return nil, fmt.Errorf("sema: undefined type %q", expr.Name)
		}
		if sym.Kind != SymType {
			return nil, fmt.Errorf("sema: %q is not a type", expr.Name)
		}
		r.info.Bindings[expr] = sym
		return sym.Type, nil
	case *goast.ArrayType:
		if expr.Len != nil {
			if err := r.resolveGoExpr(scope, expr.Len); err != nil {
				return nil, err
			}
		}
		elem, err := r.resolveGoTypeExpr(scope, expr.Elt)
		if err != nil {
			return nil, err
		}
		if expr.Len == nil {
			return &semaTypes.SliceType{Elem: elem}, nil
		}
		length, err := r.goArrayLenValue(scope, expr.Len)
		if err != nil {
			return nil, err
		}
		return &semaTypes.ArrayType{Len: length, Elem: elem}, nil
	case *goast.StarExpr:
		base, err := r.resolveGoTypeExpr(scope, expr.X)
		if err != nil {
			return nil, err
		}
		return &semaTypes.PointerType{Base: base}, nil
	case *goast.MapType:
		key, err := r.resolveGoTypeExpr(scope, expr.Key)
		if err != nil {
			return nil, err
		}
		value, err := r.resolveGoTypeExpr(scope, expr.Value)
		if err != nil {
			return nil, err
		}
		return &semaTypes.MapType{Key: key, Value: value}, nil
	case *goast.Ellipsis:
		elem, err := r.resolveGoTypeExpr(scope, expr.Elt)
		if err != nil {
			return nil, err
		}
		return &semaTypes.SliceType{Elem: elem}, nil
	case *goast.StructType:
		fields := make([]semaTypes.Type, 0)
		names := make([]string, 0)
		for _, field := range expr.Fields.List {
			fieldType, err := r.resolveGoTypeExpr(scope, field.Type)
			if err != nil {
				return nil, err
			}
			if len(field.Names) == 0 {
				fields = append(fields, fieldType)
				names = append(names, "")
				continue
			}
			for _, name := range field.Names {
				fields = append(fields, fieldType)
				names = append(names, name.Name)
			}
		}
		return &semaTypes.StructType{FieldNames: names, Fields: fields}, nil
	case *goast.FuncType:
		params := make([]semaTypes.Type, 0)
		variadic := false
		if expr.Params != nil {
			for i, field := range expr.Params.List {
				fieldType, err := r.resolveGoTypeExpr(scope, field.Type)
				if err != nil {
					return nil, err
				}
				if _, ok := field.Type.(*goast.Ellipsis); ok && i == len(expr.Params.List)-1 {
					variadic = true
				}
				arity := 1
				if len(field.Names) > 0 {
					arity = len(field.Names)
				}
				for i := 0; i < arity; i++ {
					params = append(params, fieldType)
				}
			}
		}
		results := make([]semaTypes.Type, 0)
		if expr.Results != nil {
			for _, field := range expr.Results.List {
				fieldType, err := r.resolveGoTypeExpr(scope, field.Type)
				if err != nil {
					return nil, err
				}
				arity := 1
				if len(field.Names) > 0 {
					arity = len(field.Names)
				}
				for i := 0; i < arity; i++ {
					results = append(results, fieldType)
				}
			}
		}
		return &semaTypes.FuncType{Params: params, Results: results, Variadic: variadic}, nil
	case *goast.SelectorExpr:
		if ident, ok := expr.X.(*goast.Ident); ok {
			sym := r.resolveGoPkgSelector(scope, ident.Name, expr.Sel.Name)
			if sym != nil && sym.Kind == SymType {
				r.info.Bindings[expr] = sym
				return sym.Type, nil
			}
		}
		return nil, fmt.Errorf("sema: unsupported qualified type expression %T", expr)
	default:
		return nil, fmt.Errorf("sema: unsupported type expression %T", expr)
	}
}

func (r *resolver) goArrayLenValue(scope *Scope, expr goast.Expr) (int, error) {
	if expr == nil {
		return 0, nil
	}
	value, err := r.evalGoConstIntExpr(scope, expr, make(map[*Symbol]struct{}))
	if err != nil {
		return 0, fmt.Errorf("sema: array length must be a constant integer expression: %w", err)
	}
	if value < 0 {
		return 0, fmt.Errorf("sema: array length must be >= 0, got %d", value)
	}
	return value, nil
}

func (r *resolver) evalGoConstIntExpr(scope *Scope, expr goast.Expr, seen map[*Symbol]struct{}) (int, error) {
	switch expr := expr.(type) {
	case *goast.BasicLit:
		if expr.Kind != gotoken.INT {
			return 0, fmt.Errorf("got non-integer literal %q", expr.Value)
		}
		value, err := strconv.Atoi(expr.Value)
		if err != nil {
			return 0, fmt.Errorf("invalid integer literal %q", expr.Value)
		}
		return value, nil
	case *goast.ParenExpr:
		return r.evalGoConstIntExpr(scope, expr.X, seen)
	case *goast.UnaryExpr:
		value, err := r.evalGoConstIntExpr(scope, expr.X, seen)
		if err != nil {
			return 0, err
		}
		switch expr.Op {
		case gotoken.ADD:
			return value, nil
		case gotoken.SUB:
			return -value, nil
		default:
			return 0, fmt.Errorf("unsupported unary operator %v", expr.Op)
		}
	case *goast.BinaryExpr:
		left, err := r.evalGoConstIntExpr(scope, expr.X, seen)
		if err != nil {
			return 0, err
		}
		right, err := r.evalGoConstIntExpr(scope, expr.Y, seen)
		if err != nil {
			return 0, err
		}
		switch expr.Op {
		case gotoken.ADD:
			return left + right, nil
		case gotoken.SUB:
			return left - right, nil
		case gotoken.MUL:
			return left * right, nil
		case gotoken.QUO:
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return left / right, nil
		case gotoken.REM:
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return left % right, nil
		default:
			return 0, fmt.Errorf("unsupported binary operator %v", expr.Op)
		}
	case *goast.Ident:
		sym, _ := scope.Lookup(expr.Name)
		if sym == nil || sym.Kind != SymConst {
			return 0, fmt.Errorf("%q is not an integer constant", expr.Name)
		}
		if _, ok := seen[sym]; ok {
			return 0, fmt.Errorf("constant cycle involving %q", expr.Name)
		}
		constExpr, ok := r.constValues[sym].(goast.Expr)
		if !ok || constExpr == nil {
			return 0, fmt.Errorf("missing constant value for %q", expr.Name)
		}
		seen[sym] = struct{}{}
		defer delete(seen, sym)
		return r.evalGoConstIntExpr(scope, constExpr, seen)
	default:
		return 0, fmt.Errorf("unsupported constant expression %T", expr)
	}
}

func (r *resolver) resolveGoExpr(scope *Scope, expr goast.Expr) (err error) {
	orig := expr
	defer func() {
		if err != nil || orig == nil {
			return
		}
		if typ := r.inferGoExprType(scope, orig); typ != nil && !semaTypes.IsInvalid(typ) {
			r.info.Types[orig] = typ
		}
	}()

	switch expr := expr.(type) {
	case nil:
		return nil
	case *goast.Ident:
		if expr.Name == "_" || expr.Name == "true" || expr.Name == "false" || expr.Name == "nil" {
			return nil
		}
		sym, symScope := scope.Lookup(expr.Name)
		if sym == nil {
			return fmt.Errorf("sema: undefined name %q", expr.Name)
		}
		r.info.Bindings[expr] = sym
		if r.shouldCapture(symScope) {
			r.info.Captures[expr] = sym
		}
		if sym.Kind == SymConst {
			if constExpr, ok := r.constValues[sym].(goast.Expr); ok && constExpr != nil {
				r.info.ConstExprs[expr] = constExpr
			}
		}
		return nil
	case *goast.BasicLit:
		return nil
	case *goast.ParenExpr:
		return r.resolveGoExpr(scope, expr.X)
	case *goast.StarExpr:
		return r.resolveGoExpr(scope, expr.X)
	case *goast.UnaryExpr:
		return r.resolveGoExpr(scope, expr.X)
	case *goast.BinaryExpr:
		if err := r.resolveGoExpr(scope, expr.X); err != nil {
			return err
		}
		return r.resolveGoExpr(scope, expr.Y)
	case *goast.CallExpr:
		if ident, ok := expr.Fun.(*goast.Ident); ok && isGoTypeName(scope, ident.Name) {
			if _, err := r.resolveGoTypeExpr(scope, ident); err != nil {
				return err
			}
			for _, arg := range expr.Args {
				if err := r.resolveGoExpr(scope, arg); err != nil {
					return err
				}
			}
			return nil
		}
		if ident, ok := expr.Fun.(*goast.Ident); ok {
			if _, ok := r.hostFunctions[ident.Name]; ok {
				for _, arg := range expr.Args {
					if err := r.resolveGoExpr(scope, arg); err != nil {
						return err
					}
				}
				return nil
			}
		}
		if ident, ok := expr.Fun.(*goast.Ident); ok && isBuiltinValueName(ident.Name) {
			switch ident.Name {
			case "make":
				if len(expr.Args) >= 1 {
					if _, err := r.resolveGoTypeExpr(scope, expr.Args[0]); err != nil {
						return err
					}
					for _, arg := range expr.Args[1:] {
						if err := r.resolveGoExpr(scope, arg); err != nil {
							return err
						}
					}
					return nil
				}
				return nil
			default:
				for _, arg := range expr.Args {
					if err := r.resolveGoExpr(scope, arg); err != nil {
						return err
					}
				}
				return nil
			}
		}
		if err := r.resolveGoExpr(scope, expr.Fun); err != nil {
			return err
		}
		for _, arg := range expr.Args {
			if err := r.resolveGoExpr(scope, arg); err != nil {
				return err
			}
		}
		return nil
	case *goast.FuncLit:
		return r.resolveGoFuncLit(scope, expr)
	case *goast.SelectorExpr:
		if ident, ok := expr.X.(*goast.Ident); ok {
			if sym := r.resolveGoPkgSelector(scope, ident.Name, expr.Sel.Name); sym != nil {
				r.info.Bindings[expr] = sym
				return nil
			}
		}
		return r.resolveGoExpr(scope, expr.X)
	case *goast.IndexExpr:
		if err := r.resolveGoExpr(scope, expr.X); err != nil {
			return err
		}
		return r.resolveGoExpr(scope, expr.Index)
	case *goast.SliceExpr:
		if err := r.resolveGoExpr(scope, expr.X); err != nil {
			return err
		}
		if err := r.resolveGoExpr(scope, expr.Low); err != nil {
			return err
		}
		if err := r.resolveGoExpr(scope, expr.High); err != nil {
			return err
		}
		return r.resolveGoExpr(scope, expr.Max)
	case *goast.CompositeLit:
		if expr.Type != nil {
			if _, err := r.resolveGoTypeExpr(scope, expr.Type); err != nil {
				return err
			}
		}
		for _, elt := range expr.Elts {
			if kv, ok := elt.(*goast.KeyValueExpr); ok {
				if err := r.resolveGoExpr(scope, kv.Value); err != nil {
					return err
				}
				continue
			}
			if err := r.resolveGoExpr(scope, elt); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}

func (r *resolver) resolveGoFuncLit(outer *Scope, lit *goast.FuncLit) error {
	if lit == nil {
		return nil
	}
	funcScope := NewScope(outer)
	frame := &funcFrame{parent: r.currentFunc}
	r.scopeKinds[funcScope] = scopeFunc
	r.scopeFuncs[funcScope] = frame

	prevFunc := r.currentFunc
	r.currentFunc = frame
	defer func() {
		r.currentFunc = prevFunc
	}()

	if lit.Type != nil && lit.Type.Params != nil {
		for _, field := range lit.Type.Params.List {
			paramType, err := r.resolveGoTypeExpr(outer, field.Type)
			if err != nil {
				return err
			}
			for _, name := range field.Names {
				if err := r.declare(funcScope, &Symbol{Name: name.Name, Kind: SymVar, Type: paramType}); err != nil {
					return err
				}
			}
		}
	}
	if lit.Type != nil && lit.Type.Results != nil {
		for _, field := range lit.Type.Results.List {
			resultType, err := r.resolveGoTypeExpr(outer, field.Type)
			if err != nil {
				return err
			}
			for _, name := range field.Names {
				if err := r.declare(funcScope, &Symbol{Name: name.Name, Kind: SymVar, Type: resultType}); err != nil {
					return err
				}
			}
		}
	}
	return r.resolveGoBlockInScope(funcScope, lit.Body)
}

func (r *resolver) inferGoExprType(scope *Scope, expr goast.Expr) semaTypes.Type {
	switch expr := expr.(type) {
	case nil:
		return semaTypes.InvalidType
	case *goast.BasicLit:
		switch expr.Kind {
		case gotoken.STRING:
			return semaTypes.StringType
		case gotoken.FLOAT:
			return semaTypes.Float64Type
		default:
			return semaTypes.IntType
		}
	case *goast.Ident:
		if expr.Name == "nil" {
			return semaTypes.NilType
		}
		sym, _ := scope.Lookup(expr.Name)
		if sym == nil || sym.Type == nil {
			return semaTypes.InvalidType
		}
		return sym.Type
	case *goast.FuncLit:
		if expr.Type == nil {
			return semaTypes.InvalidType
		}
		typ, err := r.resolveGoTypeExpr(scope, expr.Type)
		if err != nil {
			return semaTypes.InvalidType
		}
		return typ
	case *goast.CompositeLit:
		if expr.Type == nil {
			return semaTypes.InvalidType
		}
		typ, err := r.resolveGoTypeExpr(scope, expr.Type)
		if err != nil {
			return semaTypes.InvalidType
		}
		return typ
	case *goast.SelectorExpr:
		if ident, ok := expr.X.(*goast.Ident); ok {
			if sym := r.resolveGoPkgSelector(scope, ident.Name, expr.Sel.Name); sym != nil {
				return sym.Type
			}
		}
		baseType := r.inferGoExprType(scope, expr.X)
		if methodType, ok := semaTypes.LookupMethod(baseType, expr.Sel.Name); ok {
			return methodType
		}
		strct, _, ok := structTypeForSelector(baseType)
		if !ok || strct == nil {
			return semaTypes.InvalidType
		}
		for i, name := range strct.FieldNames {
			if name == expr.Sel.Name {
				return strct.Fields[i]
			}
		}
		return semaTypes.InvalidType
	case *goast.IndexExpr:
		baseType := r.inferGoExprType(scope, expr.X)
		switch t := semaTypes.Underlying(baseType).(type) {
		case *semaTypes.ArrayType:
			return t.Elem
		case *semaTypes.SliceType:
			return t.Elem
		case *semaTypes.MapType:
			return t.Value
		default:
			return semaTypes.InvalidType
		}
	case *goast.SliceExpr:
		baseType := r.inferGoExprType(scope, expr.X)
		switch t := semaTypes.Underlying(baseType).(type) {
		case *semaTypes.ArrayType:
			return &semaTypes.SliceType{Elem: t.Elem}
		case *semaTypes.SliceType:
			return &semaTypes.SliceType{Elem: t.Elem}
		case *semaTypes.BasicType:
			if semaTypes.Identical(t, semaTypes.StringType) && expr.Max == nil {
				return semaTypes.StringType
			}
			return semaTypes.InvalidType
		default:
			return semaTypes.InvalidType
		}
	case *goast.CallExpr:
		if ident, ok := expr.Fun.(*goast.Ident); ok {
			if builtin := semaTypes.LookupBuiltin(ident.Name); builtin != nil {
				return builtin
			}
			switch ident.Name {
			case "make":
				if len(expr.Args) >= 1 {
					typ, err := r.resolveGoTypeExpr(scope, expr.Args[0])
					if err == nil {
						return typ
					}
				}
				return semaTypes.InvalidType
			case "append":
				if len(expr.Args) >= 1 {
					return r.inferGoExprType(scope, expr.Args[0])
				}
				return semaTypes.InvalidType
			case "len":
				return semaTypes.IntType
			case "clear":
				return semaTypes.IntType
			}
			if sig, ok := r.hostFunctions[ident.Name]; ok {
				if len(sig.Results) == 1 {
					return sig.Results[0]
				}
				return semaTypes.InvalidType
			}
			if sym, _ := scope.Lookup(ident.Name); sym != nil && sym.Kind == SymType {
				return sym.Type
			}
		}
		funType := r.inferGoExprType(scope, expr.Fun)
		if sig, ok := semaTypes.Underlying(funType).(*semaTypes.FuncType); ok && len(sig.Results) == 1 {
			return sig.Results[0]
		}
		return semaTypes.InvalidType
	case *goast.UnaryExpr:
		xType := r.inferGoExprType(scope, expr.X)
		switch expr.Op {
		case gotoken.NOT:
			return semaTypes.BoolType
		case gotoken.AND:
			return &semaTypes.PointerType{Base: xType}
		case gotoken.MUL:
			if ptr, ok := semaTypes.Underlying(xType).(*semaTypes.PointerType); ok && ptr != nil {
				return ptr.Base
			}
			return semaTypes.InvalidType
		default:
			return xType
		}
	case *goast.BinaryExpr:
		left := r.inferGoExprType(scope, expr.X)
		right := r.inferGoExprType(scope, expr.Y)
		switch expr.Op {
		case gotoken.LAND, gotoken.LOR, gotoken.EQL, gotoken.NEQ, gotoken.LSS, gotoken.LEQ, gotoken.GTR, gotoken.GEQ:
			return semaTypes.BoolType
		case gotoken.ADD:
			if semaTypes.IsString(left) && semaTypes.IsString(right) {
				return semaTypes.StringType
			}
			return commonGoNumericType(left, right)
		case gotoken.SUB, gotoken.MUL, gotoken.QUO:
			return commonGoNumericType(left, right)
		default:
			return left
		}
	default:
		return semaTypes.InvalidType
	}
}

func (r *resolver) inferGoExprTypes(scope *Scope, expr goast.Expr) []semaTypes.Type {
	if index, ok := expr.(*goast.IndexExpr); ok {
		baseType := r.inferGoExprType(scope, index.X)
		if mapType, ok := semaTypes.Underlying(baseType).(*semaTypes.MapType); ok && mapType != nil {
			return []semaTypes.Type{mapType.Value, semaTypes.BoolType}
		}
	}
	if call, ok := expr.(*goast.CallExpr); ok {
		if ident, ok := call.Fun.(*goast.Ident); ok {
			if sig, ok := r.hostFunctions[ident.Name]; ok && len(sig.Results) > 0 {
				return append([]semaTypes.Type(nil), sig.Results...)
			}
		}
		funType := r.inferGoExprType(scope, call.Fun)
		if sig, ok := semaTypes.Underlying(funType).(*semaTypes.FuncType); ok && len(sig.Results) > 0 {
			return append([]semaTypes.Type(nil), sig.Results...)
		}
	}
	typ := r.inferGoExprType(scope, expr)
	if typ == nil {
		return nil
	}
	return []semaTypes.Type{typ}
}

func validateGoTypeCycles(scope *Scope) error {
	if scope == nil {
		return nil
	}
	for _, sym := range scope.Symbols {
		if sym == nil || sym.Kind != SymType {
			continue
		}
		named, ok := sym.Type.(*semaTypes.NamedType)
		if !ok || named == nil {
			continue
		}
		if err := validateGoNamedType(named); err != nil {
			return err
		}
	}
	return nil
}

func validateGoNamedType(named *semaTypes.NamedType) error {
	if named == nil {
		return nil
	}
	switch t := named.Underlying.(type) {
	case *semaTypes.PointerType:
		if goTypeReferencesNamed(t.Base, named, make(map[semaTypes.Type]struct{})) {
			return fmt.Errorf("sema: type cycle detected involving %s", named.Name)
		}
	case *semaTypes.SliceType:
		if goTypeReferencesNamed(t.Elem, named, make(map[semaTypes.Type]struct{})) {
			return fmt.Errorf("sema: type cycle detected involving %s", named.Name)
		}
	}
	seen := make(map[*semaTypes.NamedType]struct{})
	if goNamedTypeHasCycle(named, seen) {
		return fmt.Errorf("sema: type cycle detected involving %s", named.Name)
	}
	return nil
}

func goNamedTypeHasCycle(named *semaTypes.NamedType, path map[*semaTypes.NamedType]struct{}) bool {
	if named == nil {
		return false
	}
	if _, ok := path[named]; ok {
		return true
	}
	path[named] = struct{}{}
	defer delete(path, named)
	return goTypeHasCycle(named.Underlying, path)
}

func goTypeHasCycle(t semaTypes.Type, path map[*semaTypes.NamedType]struct{}) bool {
	switch t := t.(type) {
	case nil:
		return false
	case *semaTypes.NamedType:
		return goNamedTypeHasCycle(t, path)
	case *semaTypes.ArrayType:
		return goTypeHasCycle(t.Elem, path)
	case *semaTypes.StructType:
		for _, field := range t.Fields {
			if goTypeHasCycle(field, path) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func goTypeReferencesNamed(t semaTypes.Type, want *semaTypes.NamedType, seen map[semaTypes.Type]struct{}) bool {
	if t == nil || want == nil {
		return false
	}
	if _, ok := seen[t]; ok {
		return false
	}
	seen[t] = struct{}{}
	switch t := t.(type) {
	case *semaTypes.NamedType:
		if t == want {
			return true
		}
		return goTypeReferencesNamed(t.Underlying, want, seen)
	case *semaTypes.ArrayType:
		return goTypeReferencesNamed(t.Elem, want, seen)
	case *semaTypes.SliceType:
		return goTypeReferencesNamed(t.Elem, want, seen)
	case *semaTypes.MapType:
		return goTypeReferencesNamed(t.Key, want, seen) || goTypeReferencesNamed(t.Value, want, seen)
	case *semaTypes.StructType:
		for _, field := range t.Fields {
			if goTypeReferencesNamed(field, want, seen) {
				return true
			}
		}
		return false
	case *semaTypes.PointerType:
		return goTypeReferencesNamed(t.Base, want, seen)
	case *semaTypes.FuncType:
		for _, param := range t.Params {
			if goTypeReferencesNamed(param, want, seen) {
				return true
			}
		}
		for _, result := range t.Results {
			if goTypeReferencesNamed(result, want, seen) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func resolveGoReceiverNamedType(t semaTypes.Type) (*semaTypes.NamedType, bool, error) {
	switch t := t.(type) {
	case *semaTypes.NamedType:
		return t, false, nil
	case *semaTypes.PointerType:
		if named, ok := t.Base.(*semaTypes.NamedType); ok {
			return named, true, nil
		}
	}
	return nil, false, fmt.Errorf("sema: method receiver must be a named type")
}

func isGoTypeName(scope *Scope, name string) bool {
	if semaTypes.LookupBuiltin(name) != nil {
		return true
	}
	sym, _ := scope.Lookup(name)
	return sym != nil && sym.Kind == SymType
}
