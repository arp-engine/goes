package compiler

import (
	"fmt"
	goast "go/ast"
	gotoken "go/token"
	"strconv"
	"strings"

	"goes/internal/bytecode"
	"goes/internal/sema"
	"goes/runtime"
	semaTypes "goes/types"
)

type directGoPackageVar struct {
	name string
	typ  semaTypes.Type
	expr goast.Expr
}

type directGoMethodDecl struct {
	recvName string
	decl     *goast.FuncDecl
}

func compileDirectGoFiles(checked *sema.Checked, opts CompileOptions) (*bytecode.Program, bool, error) {
	if checked == nil || len(checked.GoFiles) == 0 {
		return nil, false, nil
	}
	funcs := make([]*goast.FuncDecl, 0)
	methods := make([]directGoMethodDecl, 0)
	packageVars := make([]directGoPackageVar, 0)
	declaredFuncs := make(map[string]struct{})
	declaredTypes := make(map[string]semaTypes.Type)
	if opts.HostRegistry != nil {
		for name, schema := range opts.HostRegistry.Types() {
			fields := make([]semaTypes.Type, len(schema.FieldKinds))
			for i, kind := range schema.FieldKinds {
				switch kind {
				case runtime.Bool:
					fields[i] = semaTypes.BoolType
				case runtime.Int:
					fields[i] = semaTypes.IntType
				case runtime.Int64:
					fields[i] = semaTypes.Int64Type
				case runtime.Float64:
					fields[i] = semaTypes.Float64Type
				case runtime.String:
					fields[i] = semaTypes.StringType
				default:
					fields[i] = semaTypes.InvalidType
				}
			}
			declaredTypes[name] = &semaTypes.NamedType{
				Name: name,
				Underlying: &semaTypes.StructType{
					FieldNames: append([]string(nil), schema.FieldNames...),
					Fields:     fields,
				},
			}
		}
	}
	for _, file := range checked.GoFiles {
		if file == nil {
			continue
		}
		if opts.RequireMain && (file.Name == nil || file.Name.Name != "main") {
			return nil, false, nil
		}
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *goast.FuncDecl:
				if decl.Recv != nil {
					if len(decl.Recv.List) != 1 || len(decl.Recv.List[0].Names) != 1 {
						return nil, false, nil
					}
					recvName, ok := directGoReceiverName(decl.Recv.List[0].Type)
					if !ok {
						return nil, false, nil
					}
					methods = append(methods, directGoMethodDecl{recvName: recvName, decl: decl})
					continue
				}
				declaredFuncs[decl.Name.Name] = struct{}{}
				funcs = append(funcs, decl)
			case *goast.GenDecl:
				switch decl.Tok {
				case gotoken.IMPORT:
				case gotoken.VAR:
				case gotoken.CONST:
				case gotoken.TYPE:
					collected, ok := collectDirectGoSimpleTypes(decl)
					if decl.Tok == gotoken.TYPE && !ok {
						return nil, false, nil
					}
					if decl.Tok == gotoken.TYPE {
						for name, typ := range collected {
							if _, exists := declaredTypes[name]; exists {
								return nil, false, nil
							}
							declaredTypes[name] = typ
						}
					}
				default:
					return nil, false, nil
				}
			default:
				return nil, false, nil
			}
		}
	}
	if len(funcs) == 0 {
		return nil, false, nil
	}
	for _, file := range checked.GoFiles {
		for _, decl := range file.Decls {
			gen, ok := decl.(*goast.GenDecl)
			if !ok {
				continue
			}
			switch gen.Tok {
			case gotoken.IMPORT:
			case gotoken.VAR:
				vars, ok := supportsDirectGoPackageVars(checked.Facts, declaredFuncs, declaredTypes, gen)
				if !ok {
					return nil, false, nil
				}
				packageVars = append(packageVars, vars...)
			case gotoken.CONST:
				if !supportsDirectGoPackageConsts(checked.Facts, declaredFuncs, gen) {
					return nil, false, nil
				}
			case gotoken.TYPE:
			default:
				return nil, false, nil
			}
		}
	}
	for _, decl := range funcs {
		if decl.Name != nil && decl.Name.Name == "main" && decl.Type != nil && decl.Type.Params != nil && len(decl.Type.Params.List) > 0 {
			return nil, false, fmt.Errorf(`compiler: entry function "main" must have signature func() int`)
		}
		if !supportsDirectGoFunc(checked.Facts, declaredFuncs, declaredTypes, decl) {
			return nil, false, nil
		}
	}
	for _, method := range methods {
		if !supportsDirectGoMethod(checked.Facts, declaredFuncs, declaredTypes, method.decl) {
			return nil, false, nil
		}
	}

	c := newCompiler(opts, checked)
	c.collectHostFunctions()
	for name, typ := range declaredTypes {
		c.types[name] = typ
		if named, ok := typ.(*semaTypes.NamedType); ok {
			if strct, ok := semaTypes.Underlying(named).(*semaTypes.StructType); ok {
				schema, err := structSchemaForNamed(named.Name, strct)
				if err != nil {
					return nil, false, err
				}
				c.program.StructSchemas[name] = schema
			}
		}
	}

	if err := c.compileImportedModules(opts.ImportedModules); err != nil {
		return nil, false, err
	}

	for _, decl := range funcs {
		index := len(c.program.Functions)
		c.funcIndex[decl.Name.Name] = index
		c.program.FuncIndex[decl.Name.Name] = index
		c.program.Functions = append(c.program.Functions, bytecode.Function{Name: decl.Name.Name})
		if decl.Name.Name == "main" {
			c.program.Entry = index
		}
	}
	for _, method := range methods {
		if _, ok := c.program.MethodSets[method.recvName]; !ok {
			c.program.MethodSets[method.recvName] = make(map[string]int)
		}
		index := len(c.program.Functions)
		c.program.MethodSets[method.recvName][method.decl.Name.Name] = index
		c.program.Functions = append(c.program.Functions, bytecode.Function{Name: method.recvName + "." + method.decl.Name.Name})
	}
	for _, pkgVar := range packageVars {
		if _, exists := c.globalIndex[pkgVar.name]; exists {
			return nil, false, fmt.Errorf("compiler: duplicate global %q", pkgVar.name)
		}
		slot := len(c.globalIndex)
		c.globalIndex[pkgVar.name] = slot
		c.globalTypes[slot] = pkgVar.typ
		c.program.DebugGlobals = append(c.program.DebugGlobals, bytecode.DebugGlobal{
			Name: pkgVar.name,
			Slot: slot,
			Type: pkgVar.typ.String(),
		})
	}
	c.program.GlobalCount = len(c.globalIndex)
	for _, decl := range funcs {
		index := c.funcIndex[decl.Name.Name]
		if err := c.compileDirectGoFunc(index, decl); err != nil {
			return nil, false, err
		}
	}
	for _, method := range methods {
		index := c.program.MethodSets[method.recvName][method.decl.Name.Name]
		if err := c.compileDirectGoMethod(index, method.recvName, method.decl); err != nil {
			return nil, false, err
		}
	}
	if len(packageVars) > 0 {
		if err := c.compileDirectGoInit(packageVars); err != nil {
			return nil, false, err
		}
	}
	if opts.RequireMain && c.program.Entry < 0 {
		return nil, false, fmt.Errorf(`compiler: missing entry function "main"`)
	}
	return c.program, true, nil
}

func directGoReceiverName(expr goast.Expr) (string, bool) {
	switch expr := expr.(type) {
	case *goast.Ident:
		return expr.Name, true
	case *goast.StarExpr:
		return directGoReceiverName(expr.X)
	default:
		return "", false
	}
}

func supportsDirectGoFunc(facts *sema.Facts, declaredFuncs map[string]struct{}, declaredTypes map[string]semaTypes.Type, decl *goast.FuncDecl) bool {
	if decl == nil || decl.Name == nil || decl.Type == nil || decl.Body == nil {
		return false
	}
	if decl.Name.Name == "main" && decl.Type.Params != nil && len(decl.Type.Params.List) > 0 {
		return false
	}
	if decl.Type.Params != nil {
		for i, field := range decl.Type.Params.List {
			if _, ok := field.Type.(*goast.Ellipsis); ok && i != len(decl.Type.Params.List)-1 {
				return false
			}
			if field == nil || !isDirectGoLocalType(field.Type, declaredTypes) {
				return false
			}
			if len(field.Names) == 0 {
				return false
			}
		}
	}
	if decl.Type.Results != nil {
		for _, field := range decl.Type.Results.List {
			if field == nil || !isDirectGoLocalType(field.Type, declaredTypes) {
				return false
			}
		}
	}
	for _, stmt := range decl.Body.List {
		if !supportsDirectGoStmt(facts, declaredFuncs, declaredTypes, stmt) {
			return false
		}
	}
	return true
}

func supportsDirectGoMethod(facts *sema.Facts, declaredFuncs map[string]struct{}, declaredTypes map[string]semaTypes.Type, decl *goast.FuncDecl) bool {
	if decl == nil || decl.Recv == nil || len(decl.Recv.List) != 1 || len(decl.Recv.List[0].Names) != 1 {
		return false
	}
	if !isDirectGoLocalType(decl.Recv.List[0].Type, declaredTypes) {
		return false
	}
	return supportsDirectGoFunc(facts, declaredFuncs, declaredTypes, &goast.FuncDecl{
		Name: decl.Name,
		Type: decl.Type,
		Body: decl.Body,
	})
}

func isDirectGoLocalType(expr goast.Expr, declaredTypes map[string]semaTypes.Type) bool {
	_, err := directGoTypeForNamed(expr, declaredTypes)
	return err == nil
}

func isDirectGoScopedType(facts *sema.Facts, expr goast.Expr, declaredTypes map[string]semaTypes.Type) bool {
	if isDirectGoLocalType(expr, declaredTypes) {
		return true
	}
	ident, ok := expr.(*goast.Ident)
	if !ok || facts == nil || facts.Bindings == nil {
		return false
	}
	binding := facts.Bindings[ident]
	return binding != nil && binding.Kind == sema.SymType
}

func supportsDirectGoStmt(facts *sema.Facts, declaredFuncs map[string]struct{}, declaredTypes map[string]semaTypes.Type, stmt goast.Stmt) bool {
	switch stmt := stmt.(type) {
	case *goast.AssignStmt:
		if len(stmt.Lhs) > 1 {
			if len(stmt.Rhs) != 1 {
				return false
			}
			if !supportsDirectGoExpr(facts, declaredFuncs, stmt.Rhs[0]) {
				return false
			}
			for _, lhs := range stmt.Lhs {
				ident, ok := lhs.(*goast.Ident)
				if !ok || ident.Name == "" {
					return false
				}
			}
			return true
		}
		if len(stmt.Lhs) != 1 || len(stmt.Rhs) != 1 {
			return false
		}
		if !supportsDirectGoExpr(facts, declaredFuncs, stmt.Rhs[0]) {
			return false
		}
		switch lhs := stmt.Lhs[0].(type) {
		case *goast.Ident:
			if lhs.Name == "_" && stmt.Tok != gotoken.ASSIGN {
				return false
			}
		case *goast.StarExpr:
			if stmt.Tok != gotoken.ASSIGN {
				return false
			}
			if !supportsDirectGoExpr(facts, declaredFuncs, lhs.X) {
				return false
			}
		case *goast.SelectorExpr:
			selection := directGoSelection(facts, lhs)
			if selection == nil || selection.Kind != sema.SelectionField {
				return false
			}
			if stmt.Tok == gotoken.ADD_ASSIGN {
				if !semaTypes.Identical(selection.Type, semaTypes.IntType) {
					return false
				}
				if _, ok := lhs.X.(*goast.Ident); !ok {
					return false
				}
			} else if stmt.Tok != gotoken.ASSIGN &&
				stmt.Tok != gotoken.SUB_ASSIGN &&
				stmt.Tok != gotoken.MUL_ASSIGN &&
				stmt.Tok != gotoken.QUO_ASSIGN &&
				stmt.Tok != gotoken.REM_ASSIGN &&
				stmt.Tok != gotoken.AND_ASSIGN &&
				stmt.Tok != gotoken.OR_ASSIGN &&
				stmt.Tok != gotoken.XOR_ASSIGN &&
				stmt.Tok != gotoken.AND_NOT_ASSIGN &&
				stmt.Tok != gotoken.SHL_ASSIGN &&
				stmt.Tok != gotoken.SHR_ASSIGN {
				return false
			}
		case *goast.IndexExpr:
			baseType := directGoFactOrExprType(facts, lhs.X)
			if _, ok := semaTypes.Underlying(baseType).(*semaTypes.MapType); ok {
				return stmt.Tok == gotoken.ASSIGN || stmt.Tok == gotoken.ADD_ASSIGN
			}
			if stmt.Tok != gotoken.ADD_ASSIGN &&
				stmt.Tok != gotoken.ASSIGN &&
				stmt.Tok != gotoken.SUB_ASSIGN &&
				stmt.Tok != gotoken.MUL_ASSIGN &&
				stmt.Tok != gotoken.QUO_ASSIGN &&
				stmt.Tok != gotoken.REM_ASSIGN &&
				stmt.Tok != gotoken.AND_ASSIGN &&
				stmt.Tok != gotoken.OR_ASSIGN &&
				stmt.Tok != gotoken.XOR_ASSIGN &&
				stmt.Tok != gotoken.AND_NOT_ASSIGN &&
				stmt.Tok != gotoken.SHL_ASSIGN &&
				stmt.Tok != gotoken.SHR_ASSIGN {
				return false
			}
			var elemType semaTypes.Type
			switch t := semaTypes.Underlying(baseType).(type) {
			case *semaTypes.ArrayType:
				elemType = t.Elem
			case *semaTypes.SliceType:
				elemType = t.Elem
			default:
				return false
			}
			if stmt.Tok == gotoken.ADD_ASSIGN && !semaTypes.Identical(elemType, semaTypes.IntType) {
				return false
			}
			if stmt.Tok == gotoken.ADD_ASSIGN && func() bool { _, ok := lhs.X.(*goast.Ident); return ok }() == false {
				return false
			}
		default:
			return false
		}
		switch stmt.Tok {
		case gotoken.DEFINE, gotoken.ASSIGN,
			gotoken.ADD_ASSIGN, gotoken.SUB_ASSIGN, gotoken.MUL_ASSIGN, gotoken.QUO_ASSIGN, gotoken.REM_ASSIGN,
			gotoken.AND_ASSIGN, gotoken.OR_ASSIGN, gotoken.XOR_ASSIGN, gotoken.AND_NOT_ASSIGN,
			gotoken.SHL_ASSIGN, gotoken.SHR_ASSIGN:
			return true
		default:
			return false
		}
	case *goast.IncDecStmt:
		switch target := stmt.X.(type) {
		case *goast.Ident:
			return target.Name != "_" && (stmt.Tok == gotoken.INC || stmt.Tok == gotoken.DEC)
		case *goast.IndexExpr:
			baseType := directGoFactOrExprType(facts, target.X)
			switch semaTypes.Underlying(baseType).(type) {
			case *semaTypes.MapType, *semaTypes.ArrayType:
				return stmt.Tok == gotoken.INC || stmt.Tok == gotoken.DEC
			default:
				return false
			}
		case *goast.SelectorExpr:
			return stmt.Tok == gotoken.INC || stmt.Tok == gotoken.DEC
		default:
			return false
		}
	case *goast.BranchStmt:
		return stmt.Label == nil && (stmt.Tok == gotoken.BREAK || stmt.Tok == gotoken.CONTINUE)
	case *goast.DeclStmt:
		return supportsDirectGoDecl(facts, declaredFuncs, declaredTypes, stmt.Decl)
	case *goast.BlockStmt:
		for _, bodyStmt := range stmt.List {
			if !supportsDirectGoStmt(facts, declaredFuncs, declaredTypes, bodyStmt) {
				return false
			}
		}
		return true
	case *goast.ExprStmt:
		call, ok := stmt.X.(*goast.CallExpr)
		if !ok {
			return false
		}
		if ident, ok := call.Fun.(*goast.Ident); ok && ident.Name == "delete" {
			return len(call.Args) == 2 &&
				supportsDirectGoExpr(facts, declaredFuncs, call.Args[0]) &&
				supportsDirectGoExpr(facts, declaredFuncs, call.Args[1])
		}
		return supportsDirectGoExpr(facts, declaredFuncs, call)
	case *goast.IfStmt:
		if stmt.Init != nil || stmt.Cond == nil || !supportsDirectGoExpr(facts, declaredFuncs, stmt.Cond) {
			return false
		}
		for _, bodyStmt := range stmt.Body.List {
			if !supportsDirectGoStmt(facts, declaredFuncs, declaredTypes, bodyStmt) {
				return false
			}
		}
		if stmt.Else == nil {
			return true
		}
		elseBlock, ok := stmt.Else.(*goast.BlockStmt)
		if !ok {
			return false
		}
		for _, bodyStmt := range elseBlock.List {
			if !supportsDirectGoStmt(facts, declaredFuncs, declaredTypes, bodyStmt) {
				return false
			}
		}
		return true
	case *goast.ForStmt:
		if stmt.Init != nil && !supportsDirectGoStmt(facts, declaredFuncs, declaredTypes, stmt.Init) {
			return false
		}
		if stmt.Cond != nil && !supportsDirectGoExpr(facts, declaredFuncs, stmt.Cond) {
			return false
		}
		if stmt.Post != nil && !supportsDirectGoStmt(facts, declaredFuncs, declaredTypes, stmt.Post) {
			return false
		}
		for _, bodyStmt := range stmt.Body.List {
			if !supportsDirectGoStmt(facts, declaredFuncs, declaredTypes, bodyStmt) {
				return false
			}
		}
		return true
	case *goast.SwitchStmt:
		if stmt.Init != nil {
			return false
		}
		if stmt.Tag != nil && !supportsDirectGoExpr(facts, declaredFuncs, stmt.Tag) {
			return false
		}
		for i, clauseStmt := range stmt.Body.List {
			clause, ok := clauseStmt.(*goast.CaseClause)
			if !ok {
				return false
			}
			if len(clause.List) == 0 && i != len(stmt.Body.List)-1 {
				return false
			}
			for _, expr := range clause.List {
				if !supportsDirectGoExpr(facts, declaredFuncs, expr) {
					return false
				}
			}
			for _, bodyStmt := range clause.Body {
				if !supportsDirectGoStmt(facts, declaredFuncs, declaredTypes, bodyStmt) {
					return false
				}
			}
		}
		return true
	case *goast.RangeStmt:
		if !supportsDirectGoExpr(facts, declaredFuncs, stmt.X) {
			return false
		}
		if stmt.Key != nil {
			if _, ok := stmt.Key.(*goast.Ident); !ok {
				return false
			}
		}
		if stmt.Value != nil {
			if _, ok := stmt.Value.(*goast.Ident); !ok {
				return false
			}
		}
		for _, bodyStmt := range stmt.Body.List {
			if !supportsDirectGoStmt(facts, declaredFuncs, declaredTypes, bodyStmt) {
				return false
			}
		}
		return true
	case *goast.ReturnStmt:
		if len(stmt.Results) == 0 {
			return true
		}
		for _, expr := range stmt.Results {
			if !supportsDirectGoExpr(facts, declaredFuncs, expr) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func supportsDirectGoDecl(facts *sema.Facts, declaredFuncs map[string]struct{}, declaredTypes map[string]semaTypes.Type, decl goast.Decl) bool {
	gen, ok := decl.(*goast.GenDecl)
	if !ok {
		return false
	}
	if gen.Tok == gotoken.CONST {
		return supportsDirectGoPackageConsts(facts, declaredFuncs, gen)
	}
	if gen.Tok == gotoken.TYPE {
		_, ok := collectDirectGoSimpleTypes(gen)
		return ok
	}
	if gen.Tok != gotoken.VAR {
		return false
	}
	for _, spec := range gen.Specs {
		valueSpec, ok := spec.(*goast.ValueSpec)
		if !ok {
			return false
		}
		for _, name := range valueSpec.Names {
			if name == nil || name.Name == "_" {
				return false
			}
		}
		switch {
		case len(valueSpec.Values) == 0:
			if valueSpec.Type == nil || !isDirectGoScopedType(facts, valueSpec.Type, declaredTypes) {
				return false
			}
		case len(valueSpec.Values) != len(valueSpec.Names):
			return false
		}
		if valueSpec.Type != nil && !isDirectGoScopedType(facts, valueSpec.Type, declaredTypes) {
			return false
		}
		for _, value := range valueSpec.Values {
			if !supportsDirectGoExpr(facts, declaredFuncs, value) {
				return false
			}
		}
	}
	return true
}

func collectDirectGoSimpleTypes(decl *goast.GenDecl) (map[string]semaTypes.Type, bool) {
	if decl == nil || decl.Tok != gotoken.TYPE {
		return nil, false
	}
	types := make(map[string]semaTypes.Type, len(decl.Specs))
	for _, spec := range decl.Specs {
		typeSpec, ok := spec.(*goast.TypeSpec)
		if !ok || typeSpec.Name == nil || typeSpec.Name.Name == "_" {
			return nil, false
		}
		underlying, err := directGoUnderlyingType(typeSpec.Type)
		if err != nil {
			return nil, false
		}
		types[typeSpec.Name.Name] = &semaTypes.NamedType{Name: typeSpec.Name.Name, Underlying: underlying}
	}
	return types, true
}

func directGoUnderlyingType(expr goast.Expr) (semaTypes.Type, error) {
	if typ, err := directGoType(expr); err == nil {
		return typ, nil
	}
	strct, ok := expr.(*goast.StructType)
	if !ok || strct.Fields == nil {
		return nil, fmt.Errorf("compiler: direct go/ast type %T is not supported", expr)
	}
	fieldNames := make([]string, 0)
	fields := make([]semaTypes.Type, 0)
	for _, field := range strct.Fields.List {
		if field == nil || field.Type == nil || len(field.Names) == 0 {
			return nil, fmt.Errorf("compiler: direct go/ast struct field is not supported")
		}
		fieldType, err := directGoType(field.Type)
		if err != nil {
			return nil, err
		}
		for _, name := range field.Names {
			fieldNames = append(fieldNames, name.Name)
			fields = append(fields, fieldType)
		}
	}
	return &semaTypes.StructType{FieldNames: fieldNames, Fields: fields}, nil
}

func supportsDirectGoPackageVars(facts *sema.Facts, declaredFuncs map[string]struct{}, declaredTypes map[string]semaTypes.Type, decl *goast.GenDecl) ([]directGoPackageVar, bool) {
	if decl == nil || decl.Tok != gotoken.VAR {
		return nil, false
	}
	vars := make([]directGoPackageVar, 0)
	for _, spec := range decl.Specs {
		valueSpec, ok := spec.(*goast.ValueSpec)
		if !ok {
			return nil, false
		}
		for _, name := range valueSpec.Names {
			if name == nil || name.Name == "_" {
				return nil, false
			}
		}
		switch {
		case len(valueSpec.Values) == 0:
			if valueSpec.Type == nil || !isDirectGoPackageType(valueSpec.Type, declaredTypes) {
				return nil, false
			}
		case len(valueSpec.Values) != len(valueSpec.Names):
			return nil, false
		}
		typ, ok := directGoValueSpecTypeFromFacts(facts, valueSpec, declaredTypes)
		if !ok {
			return nil, false
		}
		for i, name := range valueSpec.Names {
			var expr goast.Expr
			if i < len(valueSpec.Values) {
				expr = valueSpec.Values[i]
				if !supportsDirectGoExpr(facts, declaredFuncs, expr) {
					return nil, false
				}
			}
			vars = append(vars, directGoPackageVar{name: name.Name, typ: typ, expr: expr})
		}
	}
	return vars, true
}

func supportsDirectGoPackageConsts(facts *sema.Facts, declaredFuncs map[string]struct{}, decl *goast.GenDecl) bool {
	if decl == nil || decl.Tok != gotoken.CONST {
		return false
	}
	for _, spec := range decl.Specs {
		valueSpec, ok := spec.(*goast.ValueSpec)
		if !ok || len(valueSpec.Names) == 0 || len(valueSpec.Values) == 0 {
			return false
		}
		if len(valueSpec.Values) != len(valueSpec.Names) && len(valueSpec.Values) != 1 {
			return false
		}
		for _, value := range valueSpec.Values {
			if !supportsDirectGoExpr(facts, declaredFuncs, value) {
				return false
			}
		}
	}
	return true
}

func isDirectGoPackageType(expr goast.Expr, declaredTypes map[string]semaTypes.Type) bool {
	_, err := directGoTypeForNamed(expr, declaredTypes)
	return err == nil
}

func supportsDirectGoExpr(facts *sema.Facts, declaredFuncs map[string]struct{}, expr goast.Expr) bool {
	switch expr := expr.(type) {
	case *goast.BasicLit:
		return true
	case *goast.Ident:
		if expr.Name == "true" || expr.Name == "false" || expr.Name == "nil" {
			return true
		}
		if _, exists := declaredFuncs[expr.Name]; exists {
			return true
		}
		if directGoConstExpr(facts, expr) != nil {
			return true
		}
		if facts != nil && facts.Captures != nil && facts.Captures[expr] != nil {
			return true
		}
		if facts == nil || facts.Bindings == nil {
			return false
		}
		binding := facts.Bindings[expr]
		if binding == nil {
			return true
		}
		return binding.Kind == sema.SymVar || binding.Kind == sema.SymFunc || binding.Kind == sema.SymPackage
	case *goast.ParenExpr:
		return supportsDirectGoExpr(facts, declaredFuncs, expr.X)
	case *goast.UnaryExpr:
		switch expr.Op {
		case gotoken.AND:
			switch expr.X.(type) {
			case *goast.Ident, *goast.CompositeLit, *goast.SelectorExpr, *goast.IndexExpr:
				return true
			default:
				return false
			}
		case gotoken.ADD, gotoken.SUB, gotoken.NOT:
			return supportsDirectGoExpr(facts, declaredFuncs, expr.X)
		default:
			return false
		}
	case *goast.StarExpr:
		return supportsDirectGoExpr(facts, declaredFuncs, expr.X)
	case *goast.BinaryExpr:
		if !supportsDirectGoExpr(facts, declaredFuncs, expr.X) || !supportsDirectGoExpr(facts, declaredFuncs, expr.Y) {
			return false
		}
		switch expr.Op {
		case gotoken.ADD, gotoken.SUB, gotoken.MUL, gotoken.QUO, gotoken.REM,
			gotoken.AND, gotoken.OR, gotoken.XOR, gotoken.AND_NOT, gotoken.SHL, gotoken.SHR,
			gotoken.LAND, gotoken.LOR, gotoken.EQL, gotoken.NEQ,
			gotoken.LSS, gotoken.LEQ, gotoken.GTR, gotoken.GEQ:
			return true
		default:
			return false
		}
	case *goast.CallExpr:
		if ident, ok := expr.Fun.(*goast.Ident); ok && ident.Name == "make" {
			return len(expr.Args) >= 1
		}
		if ident, ok := expr.Fun.(*goast.Ident); ok && ident.Name == "append" {
			if len(expr.Args) < 2 {
				return false
			}
			for _, arg := range expr.Args {
				if !supportsDirectGoExpr(facts, declaredFuncs, arg) {
					return false
				}
			}
			return true
		}
		if ident, ok := expr.Fun.(*goast.Ident); ok && ident.Name == "len" {
			return len(expr.Args) == 1 && supportsDirectGoExpr(facts, declaredFuncs, expr.Args[0])
		}
		if ident, ok := expr.Fun.(*goast.Ident); ok {
			if _, err := directGoTypeForNamed(ident, nil); err == nil {
				return len(expr.Args) == 1 && supportsDirectGoExpr(facts, declaredFuncs, expr.Args[0])
			}
			if facts != nil && facts.Bindings != nil {
				if binding := facts.Bindings[ident]; binding != nil && binding.Kind == sema.SymType {
					return len(expr.Args) == 1 && supportsDirectGoExpr(facts, declaredFuncs, expr.Args[0])
				}
			}
		}
		if sel, ok := expr.Fun.(*goast.SelectorExpr); ok {
			if facts != nil && facts.Bindings != nil {
				if sym := facts.Bindings[sel]; sym != nil && sym.Kind == sema.SymFunc && sym.PkgPath != "" {
					for _, arg := range expr.Args {
						if !supportsDirectGoExpr(facts, declaredFuncs, arg) {
							return false
						}
					}
					return supportsDirectGoExpr(facts, declaredFuncs, sel.X)
				}
			}
		}
		if !supportsDirectGoExpr(facts, declaredFuncs, expr.Fun) {
			return false
		}
		for _, arg := range expr.Args {
			if !supportsDirectGoExpr(facts, declaredFuncs, arg) {
				return false
			}
		}
		if ident, ok := expr.Fun.(*goast.Ident); ok && ident.Name != "" {
			if _, exists := declaredFuncs[ident.Name]; exists {
				return true
			}
		}
		return true
	case *goast.FuncLit:
		return expr.Type != nil && expr.Body != nil
	case *goast.CompositeLit:
		var typ semaTypes.Type
		if facts != nil && facts.Types != nil {
			typ = facts.Types[expr]
		}
		_, isStruct := semaTypes.Underlying(typ).(*semaTypes.StructType)
		for _, elt := range expr.Elts {
			if kv, ok := elt.(*goast.KeyValueExpr); ok {
				if !isStruct {
					return false
				}
				if _, ok := kv.Key.(*goast.Ident); !ok {
					return false
				}
				if !supportsDirectGoExpr(facts, declaredFuncs, kv.Value) {
					return false
				}
				continue
			}
			if !supportsDirectGoExpr(facts, declaredFuncs, elt) {
				return false
			}
		}
		return true
	case *goast.SelectorExpr:
		selection := directGoSelection(facts, expr)
		if selection == nil {
			if facts != nil && facts.Bindings != nil {
				if sym := facts.Bindings[expr]; sym != nil && sym.Kind == sema.SymFunc && sym.PkgPath != "" {
					return false
				}
			}
		}
		return (selection == nil || selection.Kind == sema.SelectionField || selection.Kind == sema.SelectionMethod) &&
			supportsDirectGoExpr(facts, declaredFuncs, expr.X)
	case *goast.IndexExpr:
		return supportsDirectGoExpr(facts, declaredFuncs, expr.X) && supportsDirectGoExpr(facts, declaredFuncs, expr.Index)
	case *goast.SliceExpr:
		if !supportsDirectGoExpr(facts, declaredFuncs, expr.X) {
			return false
		}
		if expr.Low != nil && !supportsDirectGoExpr(facts, declaredFuncs, expr.Low) {
			return false
		}
		if expr.High != nil && !supportsDirectGoExpr(facts, declaredFuncs, expr.High) {
			return false
		}
		if expr.Max != nil && !supportsDirectGoExpr(facts, declaredFuncs, expr.Max) {
			return false
		}
		return true
	default:
		return false
	}
}

func (c *compiler) compileDirectGoFunc(index int, decl *goast.FuncDecl) error {
	resultTypes, err := directGoFieldTypesForNamed(decl.Type.Results, c.types)
	if err != nil {
		return err
	}
	fc := &funcCompiler{
		parent:        c,
		functionIndex: index,
		function:      &c.program.Functions[index],
		scope:         newScope(nil),
		slotTypes:     make(map[int]semaTypes.Type),
		resultTypes:   resultTypes,
		captureSlots:  make(map[string]int),
		captureTypes:  make(map[int]semaTypes.Type),
	}
	params, err := fc.declareDirectGoParams(decl.Type.Params)
	if err != nil {
		return err
	}
	c.program.Functions[index].Params = params
	c.program.Functions[index].Results = len(resultTypes)
	c.program.Functions[index].Variadic = directGoFieldListVariadic(decl.Type.Params)
	if err := fc.declareDirectGoNamedResults(decl.Type.Results); err != nil {
		return err
	}
	if err := fc.emitDirectGoBlock(decl.Body); err != nil {
		return err
	}
	c.program.Functions[index].Locals = fc.nextSlot
	c.program.Functions[index].MaxStack = computeMaxStack(c.program.Functions[index].Code)
	c.program.Functions[index].DebugLocals = fc.finishDebugLocals()
	if len(c.program.Functions[index].Code) == 0 || c.program.Functions[index].Code[len(c.program.Functions[index].Code)-1].Op != bytecode.OpReturn {
		return fmt.Errorf("compiler: function %q must end with return", decl.Name.Name)
	}
	return nil
}

func (c *compiler) compileDirectGoMethod(index int, recvName string, decl *goast.FuncDecl) error {
	resultTypes, err := directGoFieldTypesForNamed(decl.Type.Results, c.types)
	if err != nil {
		return err
	}
	fc := &funcCompiler{
		parent:        c,
		functionIndex: index,
		function:      &c.program.Functions[index],
		scope:         newScope(nil),
		slotTypes:     make(map[int]semaTypes.Type),
		resultTypes:   resultTypes,
		captureSlots:  make(map[string]int),
		captureTypes:  make(map[int]semaTypes.Type),
	}
	recvField := decl.Recv.List[0]
	recvType, err := directGoTypeForNamed(recvField.Type, c.types)
	if err != nil {
		return err
	}
	if _, err := fc.declareSlotKind(recvField.Names[0].Name, recvType, bytecode.DebugLocalParam); err != nil {
		return err
	}
	params, err := fc.declareDirectGoParams(decl.Type.Params)
	if err != nil {
		return err
	}
	c.program.Functions[index].Params = params + 1
	c.program.Functions[index].Results = len(resultTypes)
	c.program.Functions[index].Variadic = directGoFieldListVariadic(decl.Type.Params)
	if err := fc.declareDirectGoNamedResults(decl.Type.Results); err != nil {
		return err
	}
	if err := fc.emitDirectGoBlock(decl.Body); err != nil {
		return err
	}
	c.program.Functions[index].Locals = fc.nextSlot
	c.program.Functions[index].MaxStack = computeMaxStack(c.program.Functions[index].Code)
	c.program.Functions[index].DebugLocals = fc.finishDebugLocals()
	if len(c.program.Functions[index].Code) == 0 || c.program.Functions[index].Code[len(c.program.Functions[index].Code)-1].Op != bytecode.OpReturn {
		return fmt.Errorf("compiler: function %q must end with return", recvName+"."+decl.Name.Name)
	}
	return nil
}

func (c *compiler) compileDirectGoFuncLit(parent *funcCompiler, lit *goast.FuncLit) (int, []captureRef, error) {
	if lit == nil {
		return 0, nil, fmt.Errorf("compiler: missing function literal")
	}
	index := len(c.program.Functions)
	name := parent.function.Name + "$lit" + strconv.Itoa(index)
	c.program.Functions = append(c.program.Functions, bytecode.Function{Name: name})
	c.rebindFunctionChain(parent)
	resultTypes, err := directGoFieldTypesForNamed(lit.Type.Results, c.types)
	if err != nil {
		return 0, nil, err
	}
	fc := &funcCompiler{
		parent:        c,
		parentFunc:    parent,
		functionIndex: index,
		function:      &c.program.Functions[index],
		scope:         newScope(nil),
		slotTypes:     make(map[int]semaTypes.Type),
		resultTypes:   resultTypes,
		captureSlots:  make(map[string]int),
		captureTypes:  make(map[int]semaTypes.Type),
	}
	params, err := fc.declareDirectGoParams(lit.Type.Params)
	if err != nil {
		return 0, nil, err
	}
	fc.function.Params = params
	fc.function.Results = len(resultTypes)
	fc.function.Variadic = directGoFieldListVariadic(lit.Type.Params)
	if err := fc.declareDirectGoNamedResults(lit.Type.Results); err != nil {
		return 0, nil, err
	}
	if err := fc.emitDirectGoBlock(lit.Body); err != nil {
		return 0, nil, err
	}
	fc.function.Locals = fc.nextSlot
	fc.function.MaxStack = computeMaxStack(fc.function.Code)
	fc.function.DebugLocals = fc.finishDebugLocals()
	if len(fc.function.Code) == 0 || fc.function.Code[len(fc.function.Code)-1].Op != bytecode.OpReturn {
		return 0, nil, fmt.Errorf("compiler: function %q must end with return", name)
	}
	return index, fc.captures, nil
}

func (fc *funcCompiler) declareDirectGoParams(params *goast.FieldList) (int, error) {
	if params == nil {
		return 0, nil
	}
	count := 0
	for _, field := range params.List {
		typ, err := directGoTypeForNamed(field.Type, fc.parent.types)
		if err != nil {
			return 0, err
		}
		for _, name := range field.Names {
			if _, err := fc.declareSlotKind(name.Name, typ, bytecode.DebugLocalParam); err != nil {
				return 0, err
			}
			count++
		}
	}
	return count, nil
}

func (fc *funcCompiler) declareDirectGoNamedResults(results *goast.FieldList) error {
	if results == nil {
		return nil
	}
	for _, field := range results.List {
		if len(field.Names) == 0 {
			continue
		}
		typ, err := directGoTypeForNamed(field.Type, fc.parent.types)
		if err != nil {
			return err
		}
		for _, name := range field.Names {
			slot, err := fc.declareSlotKind(name.Name, typ, bytecode.DebugLocalResult)
			if err != nil {
				return err
			}
			fc.namedResults = append(fc.namedResults, slot)
			if err := fc.emitZeroValue(typ); err != nil {
				return err
			}
			fc.emit(bytecode.OpStoreLocal, slot, 0, 0)
		}
	}
	return nil
}

func directGoFieldCount(fields *goast.FieldList) int {
	if fields == nil {
		return 0
	}
	count := 0
	for _, field := range fields.List {
		if len(field.Names) > 0 {
			count += len(field.Names)
		} else {
			count++
		}
	}
	return count
}

func directGoFieldTypesForNamed(fields *goast.FieldList, namedTypes map[string]semaTypes.Type) ([]semaTypes.Type, error) {
	if fields == nil {
		return nil, nil
	}
	types := make([]semaTypes.Type, 0, directGoFieldCount(fields))
	for _, field := range fields.List {
		typ, err := directGoTypeForNamed(field.Type, namedTypes)
		if err != nil {
			return nil, err
		}
		count := 1
		if len(field.Names) > 0 {
			count = len(field.Names)
		}
		for i := 0; i < count; i++ {
			types = append(types, typ)
		}
	}
	return types, nil
}

func directGoFieldListVariadic(fields *goast.FieldList) bool {
	if fields == nil || len(fields.List) == 0 {
		return false
	}
	_, ok := fields.List[len(fields.List)-1].Type.(*goast.Ellipsis)
	return ok
}

func directGoType(expr goast.Expr) (semaTypes.Type, error) {
	if ellipsis, ok := expr.(*goast.Ellipsis); ok {
		elem, err := directGoType(ellipsis.Elt)
		if err != nil {
			return nil, err
		}
		return &semaTypes.SliceType{Elem: elem}, nil
	}
	if star, ok := expr.(*goast.StarExpr); ok {
		base, err := directGoType(star.X)
		if err != nil {
			return nil, err
		}
		return &semaTypes.PointerType{Base: base}, nil
	}
	if array, ok := expr.(*goast.ArrayType); ok {
		elem, err := directGoType(array.Elt)
		if err != nil {
			return nil, err
		}
		if array.Len == nil {
			return &semaTypes.SliceType{Elem: elem}, nil
		}
		lit, ok := array.Len.(*goast.BasicLit)
		if !ok {
			return nil, fmt.Errorf("compiler: direct go/ast array length %T is not supported", array.Len)
		}
		n, err := strconv.Atoi(lit.Value)
		if err != nil {
			return nil, fmt.Errorf("compiler: invalid array length %q: %w", lit.Value, err)
		}
		return &semaTypes.ArrayType{Len: n, Elem: elem}, nil
	}
	if mp, ok := expr.(*goast.MapType); ok {
		key, err := directGoType(mp.Key)
		if err != nil {
			return nil, err
		}
		value, err := directGoType(mp.Value)
		if err != nil {
			return nil, err
		}
		return &semaTypes.MapType{Key: key, Value: value}, nil
	}
	ident, ok := expr.(*goast.Ident)
	if !ok {
		return nil, fmt.Errorf("compiler: direct go/ast type %T is not supported", expr)
	}
	switch ident.Name {
	case "int":
		return semaTypes.IntType, nil
	case "bool":
		return semaTypes.BoolType, nil
	case "string":
		return semaTypes.StringType, nil
	case "float64":
		return semaTypes.Float64Type, nil
	case "int64":
		return semaTypes.Int64Type, nil
	default:
		return nil, fmt.Errorf("compiler: direct go/ast type %q is not supported", ident.Name)
	}
}

func directGoTypeForNamed(expr goast.Expr, namedTypes map[string]semaTypes.Type) (semaTypes.Type, error) {
	switch expr := expr.(type) {
	case *goast.Ellipsis:
		elem, err := directGoTypeForNamed(expr.Elt, namedTypes)
		if err != nil {
			return nil, err
		}
		return &semaTypes.SliceType{Elem: elem}, nil
	case *goast.Ident:
		if typ, err := directGoType(expr); err == nil {
			return typ, nil
		}
		typ, ok := namedTypes[expr.Name]
		if !ok {
			return nil, fmt.Errorf("compiler: direct go/ast type %q is not supported", expr.Name)
		}
		return typ, nil
	case *goast.StarExpr:
		base, err := directGoTypeForNamed(expr.X, namedTypes)
		if err != nil {
			return nil, err
		}
		return &semaTypes.PointerType{Base: base}, nil
	case *goast.ArrayType:
		elem, err := directGoTypeForNamed(expr.Elt, namedTypes)
		if err != nil {
			return nil, err
		}
		if expr.Len == nil {
			return &semaTypes.SliceType{Elem: elem}, nil
		}
		lit, ok := expr.Len.(*goast.BasicLit)
		if !ok {
			return nil, fmt.Errorf("compiler: direct go/ast array length %T is not supported", expr.Len)
		}
		n, err := strconv.Atoi(lit.Value)
		if err != nil {
			return nil, fmt.Errorf("compiler: invalid array length %q: %w", lit.Value, err)
		}
		return &semaTypes.ArrayType{Len: n, Elem: elem}, nil
	case *goast.MapType:
		key, err := directGoTypeForNamed(expr.Key, namedTypes)
		if err != nil {
			return nil, err
		}
		value, err := directGoTypeForNamed(expr.Value, namedTypes)
		if err != nil {
			return nil, err
		}
		return &semaTypes.MapType{Key: key, Value: value}, nil
	default:
		return nil, fmt.Errorf("compiler: direct go/ast type %T is not supported", expr)
	}
}

func (fc *funcCompiler) emitDirectGoBlock(block *goast.BlockStmt) error {
	restore := fc.pushScope()
	defer restore()
	for _, stmt := range block.List {
		if err := fc.emitDirectGoStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (fc *funcCompiler) emitDirectGoStmt(stmt goast.Stmt) error {
	if stmt == nil {
		return fmt.Errorf("compiler: missing statement")
	}
	restorePos := fc.withPos(stmt.Pos())
	defer restorePos()

	switch stmt := stmt.(type) {
	case *goast.AssignStmt:
		if len(stmt.Lhs) > 1 {
			return fc.emitDirectGoMultiAssign(stmt)
		}
		switch lhs := stmt.Lhs[0].(type) {
		case *goast.Ident:
			return fc.emitDirectGoIdentAssign(stmt, lhs)
		case *goast.StarExpr:
			if stmt.Tok != gotoken.ASSIGN {
				return fmt.Errorf("compiler: direct go/ast assignment target %T is not supported", stmt.Lhs[0])
			}
			targetType := directGoFactOrExprType(fc.parent.facts, lhs)
			if err := fc.emitDirectGoExpr(lhs.X); err != nil {
				return err
			}
			if err := fc.emitDirectGoExprForExpectedType(stmt.Rhs[0], targetType); err != nil {
				return err
			}
			fc.emit(bytecode.OpStoreIndirect, 0, 0, 0)
			return nil
		case *goast.SelectorExpr:
			if stmt.Tok == gotoken.ADD_ASSIGN && fc.emitDirectGoStructFieldAddFastPath(lhs, stmt.Rhs[0]) {
				return nil
			}
			if stmt.Tok == gotoken.ASSIGN {
				targetType := directGoFactOrExprType(fc.parent.facts, lhs)
				if err := fc.emitDirectGoAddress(lhs); err != nil {
					return err
				}
				if err := fc.emitDirectGoExprForExpectedType(stmt.Rhs[0], targetType); err != nil {
					return err
				}
				fc.emit(bytecode.OpStoreIndirect, 0, 0, 0)
				return nil
			}
			if binary := assignTokenToBinaryToken(stmt.Tok); binary != stmt.Tok {
				return fc.emitDirectGoAddressableUpdate(lhs, stmt.Rhs[0], binary)
			}
			return fmt.Errorf("compiler: direct go/ast assignment target %T is not supported", stmt.Lhs[0])
		case *goast.IndexExpr:
			baseType := directGoFactOrExprType(fc.parent.facts, lhs.X)
			switch semaTypes.Underlying(baseType).(type) {
			case *semaTypes.ArrayType, *semaTypes.SliceType:
				if stmt.Tok != gotoken.ASSIGN {
					break
				}
				targetType := directGoFactOrExprType(fc.parent.facts, lhs)
				if err := fc.emitDirectGoAddress(lhs); err != nil {
					return err
				}
				if err := fc.emitDirectGoExprForExpectedType(stmt.Rhs[0], targetType); err != nil {
					return err
				}
				fc.emit(bytecode.OpStoreIndirect, 0, 0, 0)
				return nil
			}
			if mapType, ok := semaTypes.Underlying(baseType).(*semaTypes.MapType); ok {
				if stmt.Tok == gotoken.ASSIGN {
					if err := fc.emitDirectGoExpr(lhs.X); err != nil {
						return err
					}
					if err := fc.emitDirectGoExprForExpectedType(lhs.Index, mapType.Key); err != nil {
						return err
					}
					if err := fc.emitDirectGoExprForExpectedType(stmt.Rhs[0], mapType.Value); err != nil {
						return err
					}
					fc.emit(bytecode.OpMapSet, 0, 0, 0)
					return nil
				}
				if stmt.Tok == gotoken.ADD_ASSIGN {
					return fc.emitDirectGoMapIndexAddAssign(lhs, stmt.Rhs[0])
				}
			}
			if stmt.Tok == gotoken.ADD_ASSIGN && fc.emitDirectGoArrayElemAddFastPath(lhs, stmt.Rhs[0]) {
				return nil
			}
			if binary := assignTokenToBinaryToken(stmt.Tok); binary != stmt.Tok {
				return fc.emitDirectGoAddressableUpdate(lhs, stmt.Rhs[0], binary)
			}
			return fmt.Errorf("compiler: direct go/ast assignment target %T is not supported", stmt.Lhs[0])
		default:
			return fmt.Errorf("compiler: direct go/ast assignment target %T is not supported", stmt.Lhs[0])
		}
	case *goast.IncDecStmt:
		return fc.emitDirectGoIncDec(stmt)
	case *goast.BranchStmt:
		switch stmt.Tok {
		case gotoken.BREAK:
			if stmt.Label != nil {
				return fmt.Errorf("compiler: labeled break is not supported")
			}
			ctx := fc.currentBreakControl()
			if ctx == nil {
				return fmt.Errorf("compiler: break used outside of for or switch")
			}
			ctx.breakPatches = append(ctx.breakPatches, fc.emitJump(bytecode.OpJump))
			return nil
		case gotoken.CONTINUE:
			if stmt.Label != nil {
				return fmt.Errorf("compiler: labeled continue is not supported")
			}
			ctx := fc.currentContinueControl()
			if ctx == nil {
				return fmt.Errorf("compiler: continue used outside of for")
			}
			ctx.continuePatches = append(ctx.continuePatches, fc.emitJump(bytecode.OpJump))
			return nil
		default:
			return fmt.Errorf("compiler: direct go/ast branch statement %s is not supported", stmt.Tok.String())
		}
	case *goast.DeclStmt:
		return fc.emitDirectGoDecl(stmt.Decl)
	case *goast.BlockStmt:
		return fc.emitDirectGoBlock(stmt)
	case *goast.ExprStmt:
		call, ok := stmt.X.(*goast.CallExpr)
		if !ok {
			return fmt.Errorf("compiler: expression statement must be a call")
		}
		if ident, ok := call.Fun.(*goast.Ident); ok && ident.Name == "delete" {
			mapType, ok := semaTypes.Underlying(directGoFactOrExprType(fc.parent.facts, call.Args[0])).(*semaTypes.MapType)
			if !ok || mapType == nil {
				return fmt.Errorf("compiler: delete expects a map")
			}
			if err := fc.emitDirectGoExpr(call.Args[0]); err != nil {
				return err
			}
			if err := fc.emitDirectGoExprForExpectedType(call.Args[1], mapType.Key); err != nil {
				return err
			}
			fc.emit(bytecode.OpMapDelete, 0, 0, 0)
			return nil
		}
		if ident, ok := call.Fun.(*goast.Ident); ok && ident.Name == "clear" {
			if len(call.Args) != 1 {
				return fmt.Errorf("compiler: clear expects 1 argument, got %d", len(call.Args))
			}
			if err := fc.emitDirectGoExpr(call.Args[0]); err != nil {
				return err
			}
			fc.emit(bytecode.OpClear, 0, 0, 0)
			return nil
		}
		if err := fc.emitDirectGoExpr(call); err != nil {
			return err
		}
		fc.emit(bytecode.OpPop, 0, 0, 0)
		return nil
	case *goast.IfStmt:
		return fc.emitDirectGoIf(stmt)
	case *goast.ForStmt:
		return fc.emitDirectGoFor(stmt)
	case *goast.RangeStmt:
		if stmt.Tok == gotoken.DEFINE {
			return fc.emitDirectGoRange(stmt)
		}
		return fc.emitDirectGoRangeAssign(stmt)
	case *goast.SwitchStmt:
		return fc.emitDirectGoSwitch(stmt)
	case *goast.ReturnStmt:
		if len(stmt.Results) == 0 {
			for _, slot := range fc.namedResults {
				fc.emit(bytecode.OpLoadLocal, slot, 0, 0)
			}
			fc.emit(bytecode.OpReturn, 0, 0, 0)
			return nil
		}
		for i, result := range stmt.Results {
			var expected semaTypes.Type
			if len(stmt.Results) == len(fc.resultTypes) {
				expected = fc.resultTypes[i]
			}
			if err := fc.emitDirectGoExprForExpectedType(result, expected); err != nil {
				return err
			}
		}
		fc.emit(bytecode.OpReturn, 0, 0, 0)
		return nil
	default:
		return fmt.Errorf("compiler: direct go/ast statement %T is not supported", stmt)
	}
}

func (fc *funcCompiler) emitDirectGoIdentAssign(stmt *goast.AssignStmt, ident *goast.Ident) error {
	if ident.Name == "_" {
		if stmt.Tok != gotoken.ASSIGN {
			return fmt.Errorf("compiler: direct go/ast blank identifier only supports plain assignment")
		}
		if err := fc.emitDirectGoExpr(stmt.Rhs[0]); err != nil {
			return err
		}
		fc.emit(bytecode.OpPop, 0, 0, 0)
		return nil
	}
	slot, ok := fc.scope.lookup(ident.Name)
	switch stmt.Tok {
	case gotoken.DEFINE:
		if err := fc.emitDirectGoExpr(stmt.Rhs[0]); err != nil {
			return err
		}
		if _, exists := fc.scope.lookupLocal(ident.Name); !exists {
			slot = fc.nextSlot
			fc.scope.slots[ident.Name] = slot
			fc.nextSlot++
			if typ := directGoFactOrExprType(fc.parent.facts, stmt.Rhs[0]); typ != nil {
				fc.slotTypes[slot] = typ
				fc.addDebugLocal(ident.Name, slot, bytecode.DebugLocalLocal, typ)
			}
		} else if !ok {
			return fmt.Errorf("compiler: undefined local %q", ident.Name)
		}
	case gotoken.ASSIGN:
		if !ok {
			return fmt.Errorf("compiler: undefined local %q", ident.Name)
		}
		if err := fc.emitDirectGoExprForExpectedType(stmt.Rhs[0], fc.slotTypes[slot]); err != nil {
			return err
		}
	case gotoken.ADD_ASSIGN, gotoken.SUB_ASSIGN, gotoken.MUL_ASSIGN, gotoken.QUO_ASSIGN, gotoken.REM_ASSIGN,
		gotoken.AND_ASSIGN, gotoken.OR_ASSIGN, gotoken.XOR_ASSIGN, gotoken.AND_NOT_ASSIGN,
		gotoken.SHL_ASSIGN, gotoken.SHR_ASSIGN:
		if !ok {
			return fmt.Errorf("compiler: undefined local %q", ident.Name)
		}
		if fc.emitDirectGoLocalAssignFastPath(slot, stmt.Rhs[0], stmt.Tok) {
			return nil
		}
		fc.emit(bytecode.OpLoadLocal, slot, 0, 0)
		if err := fc.emitDirectGoExpr(stmt.Rhs[0]); err != nil {
			return err
		}
		if err := fc.emitDirectGoBinaryOp(assignTokenToBinaryToken(stmt.Tok)); err != nil {
			return err
		}
	default:
		return fmt.Errorf("compiler: direct go/ast assignment operator %s is not supported", stmt.Tok.String())
	}
	fc.emit(bytecode.OpStoreLocal, slot, 0, 0)
	return nil
}

func (fc *funcCompiler) emitDirectGoMultiAssign(stmt *goast.AssignStmt) error {
	if len(stmt.Rhs) != 1 {
		return fmt.Errorf("compiler: multi assignment currently requires a single RHS expression")
	}
	resultTypes, err := fc.directGoMultiAssignResultTypes(stmt.Rhs[0])
	if err != nil {
		return err
	}
	if len(resultTypes) != len(stmt.Lhs) {
		return fmt.Errorf("compiler: multi assignment count mismatch: %d = %d", len(stmt.Lhs), len(resultTypes))
	}
	if index, ok := stmt.Rhs[0].(*goast.IndexExpr); ok && len(resultTypes) == 2 {
		if err := fc.emitDirectGoMapGet(index, true); err != nil {
			return err
		}
	} else if err := fc.emitDirectGoExpr(stmt.Rhs[0]); err != nil {
		return err
	}
	for i := len(stmt.Lhs) - 1; i >= 0; i-- {
		ident, ok := stmt.Lhs[i].(*goast.Ident)
		if !ok {
			return fmt.Errorf("compiler: multi assignment target %T is not supported", stmt.Lhs[i])
		}
		if ident.Name == "_" {
			fc.emit(bytecode.OpPop, 0, 0, 0)
			continue
		}
		slot, exists := fc.scope.lookup(ident.Name)
		if stmt.Tok == gotoken.DEFINE {
			if !exists {
				slot = fc.nextSlot
				fc.scope.slots[ident.Name] = slot
				fc.nextSlot++
				fc.slotTypes[slot] = resultTypes[i]
				fc.addDebugLocal(ident.Name, slot, bytecode.DebugLocalLocal, resultTypes[i])
			}
		} else if !exists {
			return fmt.Errorf("compiler: undefined local %q", ident.Name)
		}
		fc.emit(bytecode.OpStoreLocal, slot, 0, 0)
	}
	return nil
}

func (fc *funcCompiler) directGoMultiAssignResultTypes(expr goast.Expr) ([]semaTypes.Type, error) {
	if index, ok := expr.(*goast.IndexExpr); ok {
		baseType := directGoFactOrExprType(fc.parent.facts, index.X)
		if mapType, ok := semaTypes.Underlying(baseType).(*semaTypes.MapType); ok && mapType != nil {
			return []semaTypes.Type{mapType.Value, semaTypes.BoolType}, nil
		}
	}
	call, ok := expr.(*goast.CallExpr)
	if !ok {
		return nil, fmt.Errorf("compiler: multi assignment currently requires a multi-value expression")
	}
	return fc.directGoCallResultTypes(call)
}

func (fc *funcCompiler) directGoCallResultTypes(call *goast.CallExpr) ([]semaTypes.Type, error) {
	if ident, ok := call.Fun.(*goast.Ident); ok && fc.parent != nil {
		if sig, ok := fc.parent.hostFunctions[ident.Name]; ok {
			return append([]semaTypes.Type(nil), sig.Results...), nil
		}
	}
	funType := directGoFactOrExprType(fc.parent.facts, call.Fun)
	if funType == nil {
		return nil, fmt.Errorf("compiler: missing function type for call")
	}
	fn, ok := semaTypes.Underlying(funType).(*semaTypes.FuncType)
	if !ok {
		return nil, fmt.Errorf("compiler: call target is not a function")
	}
	return append([]semaTypes.Type(nil), fn.Results...), nil
}

func (fc *funcCompiler) directGoFuncSignature(expr *goast.CallExpr) (*semaTypes.FuncType, error) {
	funType := directGoFactOrExprType(fc.parent.facts, expr.Fun)
	if funType == nil {
		return nil, fmt.Errorf("compiler: missing function type for call")
	}
	sig, ok := semaTypes.Underlying(funType).(*semaTypes.FuncType)
	if !ok || sig == nil {
		return nil, fmt.Errorf("compiler: call target is not a function")
	}
	return sig, nil
}

func (fc *funcCompiler) directGoHostSignature(name string) (*semaTypes.HostSignature, error) {
	if fc == nil || fc.parent == nil {
		return nil, fmt.Errorf("compiler: missing compiler state")
	}
	sig, ok := fc.parent.hostFunctions[name]
	if !ok {
		return nil, fmt.Errorf("compiler: unknown host function %q", name)
	}
	return &sig, nil
}

func (fc *funcCompiler) emitDirectGoCallArgsForSignature(expr *goast.CallExpr, sig *semaTypes.FuncType) (int, error) {
	if sig == nil {
		return 0, fmt.Errorf("compiler: missing function signature")
	}
	if !sig.Variadic {
		for i, arg := range expr.Args {
			if err := fc.emitDirectGoExprForExpectedType(arg, sig.Params[i]); err != nil {
				return 0, err
			}
		}
		return len(expr.Args), nil
	}
	fixedCount := len(sig.Params) - 1
	for i := 0; i < fixedCount; i++ {
		if err := fc.emitDirectGoExprForExpectedType(expr.Args[i], sig.Params[i]); err != nil {
			return 0, err
		}
	}
	lastParam := sig.Params[len(sig.Params)-1]
	sliceType, ok := semaTypes.Underlying(lastParam).(*semaTypes.SliceType)
	if !ok || sliceType == nil {
		return 0, fmt.Errorf("compiler: variadic parameter must be a slice type")
	}
	if expr.Ellipsis.IsValid() {
		if err := fc.emitDirectGoExprForExpectedType(expr.Args[len(expr.Args)-1], lastParam); err != nil {
			return 0, err
		}
		return fixedCount + 1, nil
	}
	for i := fixedCount; i < len(expr.Args); i++ {
		if err := fc.emitDirectGoExprForExpectedType(expr.Args[i], sliceType.Elem); err != nil {
			return 0, err
		}
	}
	fc.emit(bytecode.OpMakeSlice, len(expr.Args)-fixedCount, 0, 0)
	return fixedCount + 1, nil
}

func (fc *funcCompiler) emitDirectGoCallArgsForHostSignature(expr *goast.CallExpr, sig *semaTypes.HostSignature) (int, error) {
	if sig == nil {
		return 0, fmt.Errorf("compiler: missing host function signature")
	}
	if !sig.Variadic {
		for i, arg := range expr.Args {
			if err := fc.emitDirectGoExprForExpectedType(arg, sig.Params[i]); err != nil {
				return 0, err
			}
		}
		return len(expr.Args), nil
	}
	fixedCount := len(sig.Params) - 1
	for i := 0; i < fixedCount; i++ {
		if err := fc.emitDirectGoExprForExpectedType(expr.Args[i], sig.Params[i]); err != nil {
			return 0, err
		}
	}
	lastParam := sig.Params[len(sig.Params)-1]
	sliceType, ok := semaTypes.Underlying(lastParam).(*semaTypes.SliceType)
	if !ok || sliceType == nil {
		return 0, fmt.Errorf("compiler: variadic host parameter must be a slice type")
	}
	if expr.Ellipsis.IsValid() {
		if err := fc.emitDirectGoExprForExpectedType(expr.Args[len(expr.Args)-1], lastParam); err != nil {
			return 0, err
		}
		return fixedCount + 1, nil
	}
	for i := fixedCount; i < len(expr.Args); i++ {
		if err := fc.emitDirectGoExprForExpectedType(expr.Args[i], sliceType.Elem); err != nil {
			return 0, err
		}
	}
	fc.emit(bytecode.OpMakeSlice, len(expr.Args)-fixedCount, 0, 0)
	_ = sliceType
	return fixedCount + 1, nil
}

func (fc *funcCompiler) emitDirectGoAddress(expr goast.Expr) error {
	switch expr := expr.(type) {
	case *goast.Ident:
		if slot, ok := fc.lookupSlot(expr.Name); ok {
			fc.emit(bytecode.OpAddrLocal, slot, 0, 0)
			return nil
		}
		if slot, ok := fc.parent.lookupGlobal(expr.Name); ok {
			fc.emit(bytecode.OpAddrGlobal, slot, 0, 0)
			return nil
		}
		return fmt.Errorf("compiler: undefined local %q", expr.Name)
	case *goast.SelectorExpr:
		selection := directGoSelection(fc.parent.facts, expr)
		if selection == nil || selection.Kind != sema.SelectionField {
			return fmt.Errorf("compiler: missing selection fact for direct go/ast selector")
		}
		if err := fc.emitDirectGoExpr(expr.X); err != nil {
			return err
		}
		if selection.NeedsDeref {
			fc.emit(bytecode.OpDeref, 0, 0, 0)
		}
		fc.emit(bytecode.OpAddrField, selection.FieldIndex, 0, 0)
		return nil
	case *goast.IndexExpr:
		if err := fc.emitDirectGoExpr(expr.X); err != nil {
			return err
		}
		if err := fc.emitDirectGoExpr(expr.Index); err != nil {
			return err
		}
		fc.emit(bytecode.OpAddrIndex, 0, 0, 0)
		return nil
	default:
		return fmt.Errorf("compiler: direct go/ast address target %T is not supported", expr)
	}
}

func (fc *funcCompiler) emitDirectGoAddressableUpdate(target goast.Expr, value goast.Expr, op gotoken.Token) error {
	targetType := directGoFactOrExprType(fc.parent.facts, target)
	if targetType == nil {
		return fmt.Errorf("compiler: missing type fact for expression %T", target)
	}
	ptrSlot, err := fc.declareSlot("_", &semaTypes.PointerType{Base: targetType})
	if err != nil {
		return err
	}
	valueSlot, err := fc.declareSlot("_", targetType)
	if err != nil {
		return err
	}
	if err := fc.emitDirectGoAddress(target); err != nil {
		return err
	}
	fc.emit(bytecode.OpStoreLocal, ptrSlot, 0, 0)
	fc.emit(bytecode.OpLoadLocal, ptrSlot, 0, 0)
	fc.emit(bytecode.OpDeref, 0, 0, 0)
	if err := fc.emitDirectGoExpr(value); err != nil {
		return err
	}
	if err := fc.emitDirectGoBinaryOp(op); err != nil {
		return err
	}
	fc.emit(bytecode.OpStoreLocal, valueSlot, 0, 0)
	fc.emit(bytecode.OpLoadLocal, ptrSlot, 0, 0)
	fc.emit(bytecode.OpLoadLocal, valueSlot, 0, 0)
	fc.emit(bytecode.OpStoreIndirect, 0, 0, 0)
	return nil
}

func (fc *funcCompiler) emitDirectGoIf(stmt *goast.IfStmt) error {
	if stmt.Init != nil {
		return fmt.Errorf("compiler: direct go/ast if init statements are not supported")
	}
	if stmt.Cond == nil {
		return fmt.Errorf("compiler: direct go/ast if statements require a condition")
	}
	elseJump, handled, err := fc.emitDirectGoCondJumpFalse(stmt.Cond)
	if err != nil {
		return err
	}
	if !handled {
		if err := fc.emitDirectGoExpr(stmt.Cond); err != nil {
			return err
		}
		elseJump = fc.emitJump(bytecode.OpJumpIfFalse)
	}
	if err := fc.emitDirectGoBlock(stmt.Body); err != nil {
		return err
	}
	if stmt.Else == nil {
		fc.patchJump(elseJump, len(fc.function.Code))
		return nil
	}
	elseBlock, ok := stmt.Else.(*goast.BlockStmt)
	if !ok {
		return fmt.Errorf("compiler: direct go/ast else statement %T is not supported", stmt.Else)
	}
	endJump := fc.emitJump(bytecode.OpJump)
	fc.patchJump(elseJump, len(fc.function.Code))
	if err := fc.emitDirectGoBlock(elseBlock); err != nil {
		return err
	}
	fc.patchJump(endJump, len(fc.function.Code))
	return nil
}

func (fc *funcCompiler) emitDirectGoFor(stmt *goast.ForStmt) error {
	restore := fc.pushScope()
	defer restore()

	if stmt.Init != nil {
		if err := fc.emitDirectGoStmt(stmt.Init); err != nil {
			return err
		}
	}

	loopStart := len(fc.function.Code)
	exitJump := -1
	if stmt.Cond != nil {
		var handled bool
		var err error
		exitJump, handled, err = fc.emitDirectGoCondJumpFalse(stmt.Cond)
		if err != nil {
			return err
		}
		if !handled {
			if err := fc.emitDirectGoExpr(stmt.Cond); err != nil {
				return err
			}
			exitJump = fc.emitJump(bytecode.OpJumpIfFalse)
		}
	}

	ctx := fc.pushControl(true)
	if err := fc.emitDirectGoBlock(stmt.Body); err != nil {
		fc.popControl()
		return err
	}
	continueTarget := loopStart
	if stmt.Post != nil {
		continueTarget = len(fc.function.Code)
	}
	for _, patch := range ctx.continuePatches {
		fc.patchJump(patch, continueTarget)
	}
	if stmt.Post != nil {
		if err := fc.emitDirectGoStmt(stmt.Post); err != nil {
			fc.popControl()
			return err
		}
	}
	fc.emit(bytecode.OpJump, loopStart, 0, 0)

	end := len(fc.function.Code)
	if exitJump >= 0 {
		fc.patchJump(exitJump, end)
	}
	for _, patch := range ctx.breakPatches {
		fc.patchJump(patch, end)
	}
	fc.popControl()
	return nil
}

func (fc *funcCompiler) emitDirectGoRange(stmt *goast.RangeStmt) error {
	rangeType := directGoFactOrExprType(fc.parent.facts, stmt.X)
	if rangeType == nil {
		return fmt.Errorf("compiler: missing type fact for expression %T", stmt.X)
	}
	keyType, valueType, err := rangeKeyValueTypes(rangeType)
	if err != nil {
		return err
	}

	restore := fc.pushScope()
	defer restore()

	if err := fc.emitDirectGoExpr(stmt.X); err != nil {
		return err
	}
	rangeInit, sourceSlot, indexSlot, err := fc.emitDirectRangeInit(rangeType)
	if err != nil {
		return err
	}

	keySlot, err := fc.rangeSlotForDirect(stmt.Key, keyType)
	if err != nil {
		return err
	}
	valueSlot, err := fc.rangeSlotForDirect(stmt.Value, valueType)
	if err != nil {
		return err
	}

	loopStart := len(fc.function.Code)
	fc.emitDirectRangeNext(rangeInit, sourceSlot, indexSlot, keySlot, valueSlot)

	ctx := fc.pushControl(true)
	if err := fc.emitDirectGoBlock(stmt.Body); err != nil {
		fc.popControl()
		return err
	}
	fc.emit(bytecode.OpJump, loopStart, 0, 0)

	end := len(fc.function.Code)
	fc.function.Code[loopStart].C = int32(end)
	for _, patch := range ctx.breakPatches {
		fc.patchJump(patch, end)
	}
	for _, patch := range ctx.continuePatches {
		fc.patchJump(patch, loopStart)
	}
	fc.popControl()
	return nil
}

func (fc *funcCompiler) rangeSlotForDirect(expr goast.Expr, typ semaTypes.Type) (int, error) {
	if ident, ok := expr.(*goast.Ident); ok && ident.Name != "_" {
		return fc.declareSlot(ident.Name, typ)
	}
	return fc.declareSlot("_", typ)
}

func (fc *funcCompiler) emitDirectRangeInit(rangeType semaTypes.Type) (bytecode.Opcode, int, int, error) {
	if _, ok := semaTypes.Underlying(rangeType).(*semaTypes.MapType); ok {
		sourceSlot, err := fc.declareSlot("_", rangeType)
		if err != nil {
			return 0, 0, 0, err
		}
		indexSlot, err := fc.declareSlot("_", semaTypes.IntType)
		if err != nil {
			return 0, 0, 0, err
		}
		fc.emit(bytecode.OpRangeInitMap, sourceSlot, indexSlot, 0)
		return bytecode.OpRangeInitMap, sourceSlot, indexSlot, nil
	}
	sourceSlot, err := fc.declareSlot("_", rangeType)
	if err != nil {
		return 0, 0, 0, err
	}
	indexSlot, err := fc.declareSlot("_", semaTypes.IntType)
	if err != nil {
		return 0, 0, 0, err
	}
	fc.emit(bytecode.OpRangeInitIndexed, sourceSlot, indexSlot, 0)
	return bytecode.OpRangeInitIndexed, sourceSlot, indexSlot, nil
}

func (fc *funcCompiler) emitDirectRangeNext(initOp bytecode.Opcode, sourceSlot, indexSlot, keySlot, valueSlot int) {
	switch initOp {
	case bytecode.OpRangeInitMap:
		fc.emit(bytecode.OpRangeNextMap, sourceSlot, indexSlot, bytecode.PackRangeTargets(keySlot, valueSlot))
	default:
		fc.emit(bytecode.OpRangeNextIndexed, sourceSlot, indexSlot, bytecode.PackRangeTargets(keySlot, valueSlot))
	}
}

func (fc *funcCompiler) emitDirectGoRangeAssign(stmt *goast.RangeStmt) error {
	rangeType := directGoFactOrExprType(fc.parent.facts, stmt.X)
	if rangeType == nil {
		return fmt.Errorf("compiler: missing type fact for expression %T", stmt.X)
	}
	keyType, valueType, err := rangeKeyValueTypes(rangeType)
	if err != nil {
		return err
	}
	restore := fc.pushScope()
	defer restore()
	if err := fc.emitDirectGoExpr(stmt.X); err != nil {
		return err
	}
	keyTmp, err := fc.declareSlot("_range_key", keyType)
	if err != nil {
		return err
	}
	valueTmp, err := fc.declareSlot("_range_value", valueType)
	if err != nil {
		return err
	}
	rangeInit, sourceSlot, indexSlot, err := fc.emitDirectRangeInit(rangeType)
	if err != nil {
		return err
	}
	loopStart := len(fc.function.Code)
	fc.emitDirectRangeNext(rangeInit, sourceSlot, indexSlot, keyTmp, valueTmp)
	if stmt.Key != nil {
		if err := fc.emitStoreLocalToDirectTarget(stmt.Key, keyTmp); err != nil {
			return err
		}
	}
	if stmt.Value != nil {
		if err := fc.emitStoreLocalToDirectTarget(stmt.Value, valueTmp); err != nil {
			return err
		}
	}
	ctx := fc.pushControl(true)
	if err := fc.emitDirectGoBlock(stmt.Body); err != nil {
		fc.popControl()
		return err
	}
	fc.emit(bytecode.OpJump, loopStart, 0, 0)
	end := len(fc.function.Code)
	fc.function.Code[loopStart].C = int32(end)
	for _, patch := range ctx.breakPatches {
		fc.patchJump(patch, end)
	}
	for _, patch := range ctx.continuePatches {
		fc.patchJump(patch, loopStart)
	}
	fc.popControl()
	return nil
}

func (fc *funcCompiler) emitStoreLocalToDirectTarget(target goast.Expr, sourceSlot int) error {
	ident, ok := target.(*goast.Ident)
	if !ok {
		return fmt.Errorf("compiler: direct go/ast range assignment target %T is not supported", target)
	}
	if ident.Name == "_" {
		return nil
	}
	slot, ok := fc.lookupSlot(ident.Name)
	if !ok {
		return fmt.Errorf("compiler: undefined local %q", ident.Name)
	}
	fc.emit(bytecode.OpLoadLocal, sourceSlot, 0, 0)
	fc.emit(bytecode.OpStoreLocal, slot, 0, 0)
	return nil
}

func (fc *funcCompiler) emitDirectGoSwitch(stmt *goast.SwitchStmt) error {
	if stmt.Init != nil {
		return fmt.Errorf("compiler: direct go/ast switch init statements are not supported")
	}

	tagSlot := -1
	if stmt.Tag != nil {
		if err := fc.emitDirectGoExpr(stmt.Tag); err != nil {
			return err
		}
		tagSlot = fc.nextSlot
		fc.nextSlot++
		fc.emit(bytecode.OpStoreLocal, tagSlot, 0, 0)
	}

	ctx := fc.pushControl(false)
	nextClausePatch := -1
	for i, clauseStmt := range stmt.Body.List {
		if nextClausePatch >= 0 {
			fc.patchJump(nextClausePatch, len(fc.function.Code))
			nextClausePatch = -1
		}

		clause, ok := clauseStmt.(*goast.CaseClause)
		if !ok {
			fc.popControl()
			return fmt.Errorf("compiler: direct go/ast switch clause %T is not supported", clauseStmt)
		}
		isFinalClause := i == len(stmt.Body.List)-1
		if len(clause.List) == 0 {
			if !isFinalClause {
				fc.popControl()
				return fmt.Errorf("compiler: direct go/ast default case must be last")
			}
			if err := fc.emitDirectGoCaseBody(clause.Body); err != nil {
				fc.popControl()
				return err
			}
			continue
		}

		matchedJumps := make([]int, 0, len(clause.List))
		for _, expr := range clause.List {
			if tagSlot >= 0 {
				fc.emit(bytecode.OpLoadLocal, tagSlot, 0, 0)
			}
			if err := fc.emitDirectGoExpr(expr); err != nil {
				fc.popControl()
				return err
			}
			if tagSlot >= 0 {
				fc.emit(bytecode.OpCmpEq, 0, 0, 0)
			}
			nextExprJump := fc.emitJump(bytecode.OpJumpIfFalse)
			matchedJumps = append(matchedJumps, fc.emitJump(bytecode.OpJump))
			fc.patchJump(nextExprJump, len(fc.function.Code))
		}

		nextClausePatch = fc.emitJump(bytecode.OpJump)
		bodyStart := len(fc.function.Code)
		for _, patch := range matchedJumps {
			fc.patchJump(patch, bodyStart)
		}
		if err := fc.emitDirectGoCaseBody(clause.Body); err != nil {
			fc.popControl()
			return err
		}
		if !isFinalClause {
			ctx.breakPatches = append(ctx.breakPatches, fc.emitJump(bytecode.OpJump))
		}
	}

	if nextClausePatch >= 0 {
		fc.patchJump(nextClausePatch, len(fc.function.Code))
	}
	end := len(fc.function.Code)
	for _, patch := range ctx.breakPatches {
		fc.patchJump(patch, end)
	}
	fc.popControl()
	return nil
}

func (fc *funcCompiler) emitDirectGoIncDec(stmt *goast.IncDecStmt) error {
	if stmt == nil {
		return fmt.Errorf("compiler: missing inc/dec statement")
	}
	switch target := stmt.X.(type) {
	case *goast.Ident:
		slot, ok := fc.lookupSlot(target.Name)
		if !ok {
			return fmt.Errorf("compiler: undefined local %q", target.Name)
		}
		one := &goast.BasicLit{Kind: gotoken.INT, Value: "1"}
		tok := gotoken.ADD_ASSIGN
		if stmt.Tok == gotoken.DEC {
			tok = gotoken.SUB_ASSIGN
		}
		if fc.emitDirectGoLocalAssignFastPath(slot, one, tok) {
			return nil
		}
		fc.emit(bytecode.OpLoadLocal, slot, 0, 0)
		fc.emit(bytecode.OpLoadInt, 0, 0, 1)
		if stmt.Tok == gotoken.INC {
			fc.emit(bytecode.OpAdd, 0, 0, 0)
		} else {
			fc.emit(bytecode.OpSub, 0, 0, 0)
		}
		fc.emit(bytecode.OpStoreLocal, slot, 0, 0)
		return nil
	case *goast.IndexExpr:
		baseType := directGoFactOrExprType(fc.parent.facts, target.X)
		if _, ok := semaTypes.Underlying(baseType).(*semaTypes.MapType); ok {
			one := &goast.BasicLit{Kind: gotoken.INT, Value: "1"}
			if stmt.Tok == gotoken.INC {
				return fc.emitDirectGoMapIndexAddAssign(target, one)
			}
			return fmt.Errorf("compiler: direct go/ast inc/dec target %T is not supported", stmt.X)
		}
		one := &goast.BasicLit{Kind: gotoken.INT, Value: "1"}
		op := gotoken.ADD
		if stmt.Tok == gotoken.DEC {
			op = gotoken.SUB
		}
		return fc.emitDirectGoAddressableUpdate(target, one, op)
	case *goast.SelectorExpr:
		one := &goast.BasicLit{Kind: gotoken.INT, Value: "1"}
		op := gotoken.ADD
		if stmt.Tok == gotoken.DEC {
			op = gotoken.SUB
		}
		return fc.emitDirectGoAddressableUpdate(target, one, op)
	default:
		return fmt.Errorf("compiler: direct go/ast inc/dec target %T is not supported", stmt.X)
	}
}

func (fc *funcCompiler) emitDirectGoCaseBody(stmts []goast.Stmt) error {
	restore := fc.pushScope()
	defer restore()
	for _, stmt := range stmts {
		if err := fc.emitDirectGoStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

func directGoFactOrExprType(facts *sema.Facts, expr goast.Expr) semaTypes.Type {
	if facts != nil && facts.Types != nil {
		if typ := facts.Types[expr]; typ != nil {
			return typ
		}
	}
	typ, err := directGoExprType(expr)
	if err != nil {
		return nil
	}
	return typ
}

func directGoFactBinding(facts *sema.Facts, expr goast.Expr) *sema.Symbol {
	if facts == nil || facts.Bindings == nil {
		return nil
	}
	return facts.Bindings[expr]
}

func directGoSelection(facts *sema.Facts, expr *goast.SelectorExpr) *sema.Selection {
	if facts == nil || facts.Selections == nil || expr == nil {
		return nil
	}
	return facts.Selections[expr]
}

func (fc *funcCompiler) directGoMethodIndex(expr *goast.SelectorExpr) (int, bool) {
	if fc == nil || fc.parent == nil || expr == nil {
		return 0, false
	}
	baseType := directGoFactOrExprType(fc.parent.facts, expr.X)
	if baseType == nil {
		return 0, false
	}
	switch t := baseType.(type) {
	case *semaTypes.NamedType:
		methods := fc.parent.program.MethodSets[t.Name]
		index, ok := methods[expr.Sel.Name]
		return index, ok
	case *semaTypes.PointerType:
		if named, ok := t.Base.(*semaTypes.NamedType); ok {
			methods := fc.parent.program.MethodSets[named.Name]
			index, ok := methods[expr.Sel.Name]
			return index, ok
		}
	}
	return 0, false
}

func (fc *funcCompiler) emitDirectGoLocalAssignFastPath(targetSlot int, value goast.Expr, tok gotoken.Token) bool {
	targetType, ok := fc.slotTypes[targetSlot]
	if !ok || !semaTypes.Identical(targetType, semaTypes.IntType) {
		return false
	}

	switch v := value.(type) {
	case *goast.BinaryExpr:
		if tok != gotoken.ADD_ASSIGN || v.Op != gotoken.ADD {
			return false
		}
		leftSlot, leftOK := fc.directGoIntLocal(v.X)
		rightSlot, rightOK := fc.directGoIntLocal(v.Y)
		if !leftOK || !rightOK {
			return false
		}
		fc.emit(bytecode.OpLocalAddTwoLocals, targetSlot, leftSlot, int64(rightSlot))
		return true
	case *goast.BasicLit:
		literal, err := strconv.ParseInt(v.Value, 10, 64)
		if err != nil {
			return false
		}
		switch tok {
		case gotoken.ADD_ASSIGN:
			fc.emit(bytecode.OpLocalAddIntConst, targetSlot, 0, literal)
			return true
		case gotoken.SUB_ASSIGN:
			fc.emit(bytecode.OpLocalSubIntConst, targetSlot, 0, literal)
			return true
		default:
			return false
		}
	case *goast.Ident:
		sourceSlot, ok := fc.lookupSlot(v.Name)
		if !ok {
			return false
		}
		sourceType, ok := fc.slotTypes[sourceSlot]
		if !ok || !semaTypes.Identical(sourceType, semaTypes.IntType) {
			return false
		}
		switch tok {
		case gotoken.ADD_ASSIGN:
			fc.emit(bytecode.OpLocalAddLocal, targetSlot, sourceSlot, 0)
			return true
		case gotoken.SUB_ASSIGN:
			fc.emit(bytecode.OpLocalSubLocal, targetSlot, sourceSlot, 0)
			return true
		default:
			return false
		}
	case *goast.SelectorExpr:
		baseIdent, ok := v.X.(*goast.Ident)
		if !ok {
			return false
		}
		baseSlot, ok := fc.lookupSlot(baseIdent.Name)
		if !ok {
			return false
		}
		baseType, ok := fc.slotTypes[baseSlot]
		if !ok {
			return false
		}
		if _, ok := semaTypes.Underlying(baseType).(*semaTypes.PointerType); ok {
			return false
		}
		selection := directGoSelection(fc.parent.facts, v)
		if selection == nil || selection.Kind != sema.SelectionField || !semaTypes.Identical(selection.Type, semaTypes.IntType) {
			return false
		}
		if tok != gotoken.ADD_ASSIGN {
			return false
		}
		fc.emit(bytecode.OpLocalAddStructField, targetSlot, baseSlot, int64(selection.FieldIndex))
		return true
	case *goast.IndexExpr:
		baseIdent, ok := v.X.(*goast.Ident)
		if !ok {
			return false
		}
		baseSlot, ok := fc.lookupSlot(baseIdent.Name)
		if !ok {
			return false
		}
		baseType, ok := fc.slotTypes[baseSlot]
		if !ok {
			return false
		}
		arrayType, ok := semaTypes.Underlying(baseType).(*semaTypes.ArrayType)
		if !ok || !semaTypes.Identical(arrayType.Elem, semaTypes.IntType) || tok != gotoken.ADD_ASSIGN {
			return false
		}
		indexLit, ok := v.Index.(*goast.BasicLit)
		if !ok {
			return false
		}
		index, err := strconv.ParseInt(indexLit.Value, 10, 64)
		if err != nil {
			return false
		}
		fc.emit(bytecode.OpLocalAddArrayElemConst, targetSlot, baseSlot, index)
		return true
	default:
		return false
	}
}

func (fc *funcCompiler) emitDirectGoStructFieldAddFastPath(target *goast.SelectorExpr, value goast.Expr) bool {
	baseIdent, ok := target.X.(*goast.Ident)
	if !ok {
		return false
	}
	baseSlot, ok := fc.lookupSlot(baseIdent.Name)
	if !ok {
		return false
	}
	baseType, ok := fc.slotTypes[baseSlot]
	if !ok {
		return false
	}
	if _, isPtr := semaTypes.Underlying(baseType).(*semaTypes.PointerType); isPtr {
		return false
	}
	selection := directGoSelection(fc.parent.facts, target)
	if selection == nil || selection.Kind != sema.SelectionField || !semaTypes.Identical(selection.Type, semaTypes.IntType) {
		return false
	}
	switch v := value.(type) {
	case *goast.BasicLit:
		literal, err := strconv.ParseInt(v.Value, 10, 64)
		if err != nil {
			return false
		}
		fc.emit(bytecode.OpStructFieldAddIntConst, baseSlot, selection.FieldIndex, literal)
		return true
	case *goast.Ident:
		sourceSlot, ok := fc.lookupSlot(v.Name)
		if !ok {
			return false
		}
		sourceType, ok := fc.slotTypes[sourceSlot]
		if !ok || !semaTypes.Identical(sourceType, semaTypes.IntType) {
			return false
		}
		fc.emit(bytecode.OpStructFieldAddLocal, baseSlot, selection.FieldIndex, int64(sourceSlot))
		return true
	case *goast.SelectorExpr:
		sourceBase, ok := v.X.(*goast.Ident)
		if !ok || sourceBase.Name != baseIdent.Name {
			return false
		}
		sourceSelection := directGoSelection(fc.parent.facts, v)
		if sourceSelection == nil || sourceSelection.Kind != sema.SelectionField || !semaTypes.Identical(sourceSelection.Type, semaTypes.IntType) {
			return false
		}
		fc.emit(bytecode.OpStructFieldAddField, baseSlot, selection.FieldIndex, int64(sourceSelection.FieldIndex))
		return true
	default:
		return false
	}
}

func (fc *funcCompiler) directGoIntLocal(expr goast.Expr) (int, bool) {
	ident, ok := expr.(*goast.Ident)
	if !ok {
		return 0, false
	}
	slot, ok := fc.lookupSlot(ident.Name)
	return slot, ok && semaTypes.Identical(fc.slotTypes[slot], semaTypes.IntType)
}

func (fc *funcCompiler) directGoLocalIntArrayConstIndex(expr *goast.IndexExpr) (int, int64, bool) {
	base, ok := expr.X.(*goast.Ident)
	if !ok {
		return 0, 0, false
	}
	slot, ok := fc.lookupSlot(base.Name)
	if !ok {
		return 0, 0, false
	}
	arrayType, ok := semaTypes.Underlying(fc.slotTypes[slot]).(*semaTypes.ArrayType)
	if !ok || !semaTypes.Identical(arrayType.Elem, semaTypes.IntType) {
		return 0, 0, false
	}
	lit, ok := expr.Index.(*goast.BasicLit)
	if !ok || lit.Kind != gotoken.INT {
		return 0, 0, false
	}
	index, err := strconv.ParseInt(lit.Value, 0, 64)
	return slot, index, err == nil
}

func (fc *funcCompiler) emitDirectGoArrayElemAddFastPath(target *goast.IndexExpr, value goast.Expr) bool {
	baseIdent, ok := target.X.(*goast.Ident)
	if !ok {
		return false
	}
	baseSlot, ok := fc.lookupSlot(baseIdent.Name)
	if !ok {
		return false
	}
	baseType, ok := fc.slotTypes[baseSlot]
	if !ok {
		return false
	}
	arrayType, ok := semaTypes.Underlying(baseType).(*semaTypes.ArrayType)
	if !ok || !semaTypes.Identical(arrayType.Elem, semaTypes.IntType) {
		return false
	}
	indexSlot, err := fc.declareSlot("_", semaTypes.IntType)
	if err != nil {
		return false
	}
	if err := fc.emitDirectGoExpr(target.Index); err != nil {
		return false
	}
	fc.emit(bytecode.OpStoreLocal, indexSlot, 0, 0)
	switch v := value.(type) {
	case *goast.BasicLit:
		literal, err := strconv.ParseInt(v.Value, 10, 64)
		if err != nil {
			return false
		}
		fc.emit(bytecode.OpArrayElemAddIntConst, baseSlot, indexSlot, literal)
		return true
	case *goast.Ident:
		sourceSlot, ok := fc.lookupSlot(v.Name)
		if !ok {
			return false
		}
		sourceType, ok := fc.slotTypes[sourceSlot]
		if !ok || !semaTypes.Identical(sourceType, semaTypes.IntType) {
			return false
		}
		fc.emit(bytecode.OpArrayElemAddLocal, baseSlot, indexSlot, int64(sourceSlot))
		return true
	case *goast.SelectorExpr:
		sourceBase, ok := v.X.(*goast.Ident)
		if !ok {
			return false
		}
		sourceSlot, ok := fc.lookupSlot(sourceBase.Name)
		if !ok {
			return false
		}
		sourceBaseType, ok := fc.slotTypes[sourceSlot]
		if !ok {
			return false
		}
		if _, isPtr := semaTypes.Underlying(sourceBaseType).(*semaTypes.PointerType); isPtr {
			return false
		}
		selection := directGoSelection(fc.parent.facts, v)
		if selection == nil || selection.Kind != sema.SelectionField || !semaTypes.Identical(selection.Type, semaTypes.IntType) {
			return false
		}
		fc.emit(bytecode.OpArrayElemAddStructField, baseSlot, indexSlot, int64(sourceSlot))
		fc.function.Code[len(fc.function.Code)-1].C = int32(selection.FieldIndex)
		return true
	default:
		return false
	}
}

func (fc *funcCompiler) emitDirectGoMapIndexAddAssign(indexExpr *goast.IndexExpr, value goast.Expr) error {
	baseType := directGoFactOrExprType(fc.parent.facts, indexExpr.X)
	mapType, ok := semaTypes.Underlying(baseType).(*semaTypes.MapType)
	if !ok || mapType == nil {
		return fmt.Errorf("compiler: expected map index update target")
	}
	if slot, key, ok := fc.directGoLocalStringIntMapAccess(indexExpr); ok {
		// A call or other effectful expression may change the map or its entry.
		simple := false
		switch rhs := value.(type) {
		case *goast.BasicLit:
			simple = rhs.Kind == gotoken.INT
		case *goast.Ident:
			_, simple = fc.lookupSlot(rhs.Name)
		case *goast.IndexExpr:
			_, _, simple = fc.directGoLocalStringIntMapAccess(rhs)
		}
		if simple {
			if err := fc.emitDirectGoExprForExpectedType(value, mapType.Value); err != nil {
				return err
			}
			fc.emit(bytecode.OpMapAddLocalStringInt, slot, 0, 0)
			fc.function.Code[len(fc.function.Code)-1].S = key
			return nil
		}
	}
	if emitBase, emitKey, ok := fc.directGoSimpleMapAccess(indexExpr, mapType); ok {
		valueSlot, err := fc.declareSlot("_", mapType.Value)
		if err != nil {
			return err
		}
		if err := emitBase(); err != nil {
			return err
		}
		if err := emitKey(); err != nil {
			return err
		}
		fc.emit(bytecode.OpMapGet, 0, 0, 0)
		if err := fc.emitDirectGoExpr(value); err != nil {
			return err
		}
		fc.emit(bytecode.OpAdd, 0, 0, 0)
		fc.emit(bytecode.OpStoreLocal, valueSlot, 0, 0)
		if err := emitBase(); err != nil {
			return err
		}
		if err := emitKey(); err != nil {
			return err
		}
		fc.emit(bytecode.OpLoadLocal, valueSlot, 0, 0)
		fc.emit(bytecode.OpMapSet, 0, 0, 0)
		return nil
	}
	mapSlot, err := fc.declareSlot("_", baseType)
	if err != nil {
		return err
	}
	keySlot, err := fc.declareSlot("_", mapType.Key)
	if err != nil {
		return err
	}
	valueSlot, err := fc.declareSlot("_", mapType.Value)
	if err != nil {
		return err
	}
	if err := fc.emitDirectGoExpr(indexExpr.X); err != nil {
		return err
	}
	fc.emit(bytecode.OpStoreLocal, mapSlot, 0, 0)
	if err := fc.emitDirectGoExprForExpectedType(indexExpr.Index, mapType.Key); err != nil {
		return err
	}
	fc.emit(bytecode.OpStoreLocal, keySlot, 0, 0)
	fc.emit(bytecode.OpLoadLocal, mapSlot, 0, 0)
	fc.emit(bytecode.OpLoadLocal, keySlot, 0, 0)
	fc.emit(bytecode.OpMapGet, 0, 0, 0)
	if err := fc.emitDirectGoExpr(value); err != nil {
		return err
	}
	fc.emit(bytecode.OpAdd, 0, 0, 0)
	fc.emit(bytecode.OpStoreLocal, valueSlot, 0, 0)
	fc.emit(bytecode.OpLoadLocal, mapSlot, 0, 0)
	fc.emit(bytecode.OpLoadLocal, keySlot, 0, 0)
	fc.emit(bytecode.OpLoadLocal, valueSlot, 0, 0)
	fc.emit(bytecode.OpMapSet, 0, 0, 0)
	return nil
}

func (fc *funcCompiler) directGoSimpleMapAccess(indexExpr *goast.IndexExpr, mapType *semaTypes.MapType) (func() error, func() error, bool) {
	if fc == nil || indexExpr == nil || mapType == nil {
		return nil, nil, false
	}
	if !semaTypes.Identical(mapType.Key, semaTypes.StringType) {
		return nil, nil, false
	}
	keyLit, ok := indexExpr.Index.(*goast.BasicLit)
	if !ok || keyLit.Kind != gotoken.STRING {
		return nil, nil, false
	}
	key, err := strconv.Unquote(keyLit.Value)
	if err != nil {
		return nil, nil, false
	}
	ident, ok := indexExpr.X.(*goast.Ident)
	if !ok {
		return nil, nil, false
	}
	if slot, ok := fc.lookupSlot(ident.Name); ok {
		return func() error {
				fc.emit(bytecode.OpLoadLocal, slot, 0, 0)
				return nil
			}, func() error {
				fc.emit(bytecode.OpLoadString, 0, 0, 0)
				fc.function.Code[len(fc.function.Code)-1].S = key
				return nil
			}, true
	}
	if slot, ok := fc.parent.lookupGlobal(ident.Name); ok {
		return func() error {
				fc.emit(bytecode.OpLoadGlobal, slot, 0, 0)
				return nil
			}, func() error {
				fc.emit(bytecode.OpLoadString, 0, 0, 0)
				fc.function.Code[len(fc.function.Code)-1].S = key
				return nil
			}, true
	}
	return nil, nil, false
}

func (fc *funcCompiler) emitDirectGoCondJumpFalse(cond goast.Expr) (int, bool, error) {
	bin, ok := cond.(*goast.BinaryExpr)
	if !ok {
		return -1, false, nil
	}

	if bin.Op == gotoken.LSS {
		left, ok := bin.X.(*goast.Ident)
		if !ok || left.Name == "_" {
			return -1, false, nil
		}
		slot, ok := fc.lookupSlot(left.Name)
		if !ok {
			return -1, false, nil
		}
		typ, ok := fc.slotTypes[slot]
		if !ok || !semaTypes.Identical(typ, semaTypes.IntType) {
			return -1, false, nil
		}
		right, ok := bin.Y.(*goast.BasicLit)
		if !ok {
			return -1, false, nil
		}
		value, err := strconv.ParseInt(right.Value, 10, 64)
		if err != nil {
			return -1, false, nil
		}
		index := len(fc.function.Code)
		fc.emit(bytecode.OpJumpIfLocalIntLtConstFalse, -1, slot, value)
		return index, true, nil
	}

	if bin.Op == gotoken.EQL {
		and, ok := bin.X.(*goast.BinaryExpr)
		if !ok || and.Op != gotoken.AND {
			return -1, false, nil
		}
		left, ok := and.X.(*goast.Ident)
		if !ok || left.Name == "_" {
			return -1, false, nil
		}
		slot, ok := fc.lookupSlot(left.Name)
		if !ok {
			return -1, false, nil
		}
		typ, ok := fc.slotTypes[slot]
		if !ok || !semaTypes.Identical(typ, semaTypes.IntType) {
			return -1, false, nil
		}
		maskLit, ok := and.Y.(*goast.BasicLit)
		if !ok {
			return -1, false, nil
		}
		mask, err := strconv.ParseInt(maskLit.Value, 10, 64)
		if err != nil {
			return -1, false, nil
		}
		expectedLit, ok := bin.Y.(*goast.BasicLit)
		if !ok {
			return -1, false, nil
		}
		expected, err := strconv.ParseInt(expectedLit.Value, 10, 64)
		if err != nil {
			return -1, false, nil
		}
		index := len(fc.function.Code)
		fc.emit(bytecode.OpJumpIfLocalBitAndIntEqConstFalse, -1, slot, mask)
		if int64(int32(expected)) == expected {
			fc.function.Code[index].C = int32(expected)
		} else {
			fc.function.Code[index].S = strconv.FormatInt(expected, 10)
		}
		return index, true, nil
	}

	return -1, false, nil
}

func assignTokenToBinaryToken(tok gotoken.Token) gotoken.Token {
	switch tok {
	case gotoken.ADD_ASSIGN:
		return gotoken.ADD
	case gotoken.SUB_ASSIGN:
		return gotoken.SUB
	case gotoken.MUL_ASSIGN:
		return gotoken.MUL
	case gotoken.QUO_ASSIGN:
		return gotoken.QUO
	case gotoken.REM_ASSIGN:
		return gotoken.REM
	case gotoken.AND_ASSIGN:
		return gotoken.AND
	case gotoken.OR_ASSIGN:
		return gotoken.OR
	case gotoken.XOR_ASSIGN:
		return gotoken.XOR
	case gotoken.AND_NOT_ASSIGN:
		return gotoken.AND_NOT
	case gotoken.SHL_ASSIGN:
		return gotoken.SHL
	case gotoken.SHR_ASSIGN:
		return gotoken.SHR
	default:
		return tok
	}
}

func (fc *funcCompiler) emitDirectGoDecl(decl goast.Decl) error {
	gen := decl.(*goast.GenDecl)
	if gen.Tok == gotoken.CONST {
		return nil
	}
	if gen.Tok == gotoken.TYPE {
		collected, ok := collectDirectGoSimpleTypes(gen)
		if !ok {
			return fmt.Errorf("compiler: direct go/ast local type declaration is not supported")
		}
		for name, typ := range collected {
			fc.scope.types[name] = typ
		}
		return nil
	}
	for _, spec := range gen.Specs {
		valueSpec := spec.(*goast.ValueSpec)
		valueType, err := fc.directGoTypeForScopeOrValueSpec(valueSpec)
		if err != nil {
			return err
		}
		for i, name := range valueSpec.Names {
			slot, err := fc.declareSlot(name.Name, valueType)
			if err != nil {
				return err
			}
			if i < len(valueSpec.Values) {
				if err := fc.emitDirectGoExprForExpectedType(valueSpec.Values[i], valueType); err != nil {
					return err
				}
			} else {
				if err := fc.emitZeroValue(valueType); err != nil {
					return err
				}
			}
			fc.emit(bytecode.OpStoreLocal, slot, 0, 0)
		}
	}
	return nil
}

func (fc *funcCompiler) directGoTypeForScope(expr goast.Expr) (semaTypes.Type, error) {
	switch expr := expr.(type) {
	case *goast.Ident:
		if typ, err := directGoType(expr); err == nil {
			return typ, nil
		}
		if typ, ok := fc.scope.lookupType(expr.Name); ok {
			return typ, nil
		}
		typ, ok := fc.parent.types[expr.Name]
		if !ok {
			return nil, fmt.Errorf("compiler: direct go/ast type %q is not supported", expr.Name)
		}
		return typ, nil
	case *goast.StarExpr:
		base, err := fc.directGoTypeForScope(expr.X)
		if err != nil {
			return nil, err
		}
		return &semaTypes.PointerType{Base: base}, nil
	case *goast.ArrayType:
		elem, err := fc.directGoTypeForScope(expr.Elt)
		if err != nil {
			return nil, err
		}
		if expr.Len == nil {
			return &semaTypes.SliceType{Elem: elem}, nil
		}
		lit, ok := expr.Len.(*goast.BasicLit)
		if !ok {
			return nil, fmt.Errorf("compiler: direct go/ast array length %T is not supported", expr.Len)
		}
		n, err := strconv.Atoi(lit.Value)
		if err != nil {
			return nil, fmt.Errorf("compiler: invalid array length %q: %w", lit.Value, err)
		}
		return &semaTypes.ArrayType{Len: n, Elem: elem}, nil
	case *goast.MapType:
		key, err := fc.directGoTypeForScope(expr.Key)
		if err != nil {
			return nil, err
		}
		value, err := fc.directGoTypeForScope(expr.Value)
		if err != nil {
			return nil, err
		}
		return &semaTypes.MapType{Key: key, Value: value}, nil
	default:
		return nil, fmt.Errorf("compiler: direct go/ast type %T is not supported", expr)
	}
}

func (fc *funcCompiler) directGoTypeForScopeOrValueSpec(spec *goast.ValueSpec) (semaTypes.Type, error) {
	if spec == nil {
		return nil, fmt.Errorf("compiler: missing direct go/ast value spec")
	}
	if spec.Type != nil {
		return fc.directGoTypeForScope(spec.Type)
	}
	if len(spec.Values) == 0 {
		return nil, fmt.Errorf("compiler: direct go/ast var declaration requires a type or value")
	}
	return directGoExprType(spec.Values[0])
}

func directGoValueSpecTypeFromFacts(facts *sema.Facts, spec *goast.ValueSpec, declaredTypes map[string]semaTypes.Type) (semaTypes.Type, bool) {
	if spec == nil {
		return nil, false
	}
	if spec.Type != nil {
		if typ, err := directGoType(spec.Type); err == nil {
			return typ, true
		}
		ident, ok := spec.Type.(*goast.Ident)
		if !ok {
			return nil, false
		}
		typ, ok := declaredTypes[ident.Name]
		return typ, ok
	}
	if len(spec.Values) == 0 {
		return nil, false
	}
	if facts != nil && facts.Types != nil {
		if typ := facts.Types[spec.Values[0]]; typ != nil {
			return typ, true
		}
	}
	typ, err := directGoExprType(spec.Values[0])
	if err == nil {
		return typ, true
	}
	return semaTypes.InvalidType, true
}

func directGoConstExpr(facts *sema.Facts, expr *goast.Ident) goast.Expr {
	if facts == nil || facts.ConstExprs == nil || expr == nil {
		return nil
	}
	constExpr, ok := facts.ConstExprs[expr].(goast.Expr)
	if !ok {
		return nil
	}
	return constExpr
}

func directGoExprType(expr goast.Expr) (semaTypes.Type, error) {
	switch expr := expr.(type) {
	case *goast.BasicLit:
		switch expr.Kind {
		case gotoken.STRING:
			return semaTypes.StringType, nil
		case gotoken.FLOAT:
			return semaTypes.Float64Type, nil
		default:
			return semaTypes.IntType, nil
		}
	case *goast.Ident:
		switch expr.Name {
		case "true", "false":
			return semaTypes.BoolType, nil
		default:
			return nil, fmt.Errorf("compiler: direct go/ast cannot infer type for identifier %q", expr.Name)
		}
	case *goast.UnaryExpr:
		switch expr.Op {
		case gotoken.ADD, gotoken.SUB:
			return directGoExprType(expr.X)
		case gotoken.NOT:
			return semaTypes.BoolType, nil
		default:
			return nil, fmt.Errorf("compiler: direct go/ast cannot infer type for unary operator %s", expr.Op.String())
		}
	default:
		return nil, fmt.Errorf("compiler: direct go/ast cannot infer type for %T", expr)
	}
}

func (c *compiler) compileDirectGoInit(packageVars []directGoPackageVar) error {
	index := len(c.program.Functions)
	c.program.Functions = append(c.program.Functions, bytecode.Function{Name: "__init"})
	c.program.Init = index
	fc := &funcCompiler{
		parent:        c,
		functionIndex: index,
		function:      &c.program.Functions[index],
		scope:         newScope(nil),
		slotTypes:     make(map[int]semaTypes.Type),
		captureSlots:  make(map[string]int),
		captureTypes:  make(map[int]semaTypes.Type),
	}
	for _, pkgVar := range packageVars {
		slot := c.globalIndex[pkgVar.name]
		if pkgVar.expr != nil {
			if err := fc.emitDirectGoExprForExpectedType(pkgVar.expr, pkgVar.typ); err != nil {
				if strings.Contains(err.Error(), "missing type fact for expression") {
					return fmt.Errorf("compiler: missing type fact for package expression %T", pkgVar.expr)
				}
				return err
			}
		} else {
			if err := fc.emitZeroValue(pkgVar.typ); err != nil {
				return err
			}
		}
		fc.emit(bytecode.OpStoreGlobal, slot, 0, 0)
	}
	fc.emit(bytecode.OpLoadInt, 0, 0, 0)
	fc.emit(bytecode.OpReturn, 0, 0, 0)
	c.program.Functions[index].Locals = fc.nextSlot
	c.program.Functions[index].MaxStack = computeMaxStack(c.program.Functions[index].Code)
	c.program.Functions[index].DebugLocals = fc.finishDebugLocals()
	return nil
}

func (fc *funcCompiler) emitDirectGoExpr(expr goast.Expr) error {
	if expr == nil {
		return fmt.Errorf("compiler: missing expression")
	}
	restorePos := fc.withPos(expr.Pos())
	defer restorePos()

	switch expr := expr.(type) {
	case *goast.BasicLit:
		if expr.Kind == gotoken.STRING {
			value, err := strconv.Unquote(expr.Value)
			if err != nil {
				return fmt.Errorf("compiler: invalid string literal %q: %w", expr.Value, err)
			}
			fc.emit(bytecode.OpLoadString, 0, 0, 0)
			fc.function.Code[len(fc.function.Code)-1].S = value
			return nil
		}
		if strings.Contains(expr.Value, ".") {
			fc.emit(bytecode.OpLoadFloat64, 0, 0, 0)
			fc.function.Code[len(fc.function.Code)-1].S = expr.Value
			return nil
		}
		value, err := strconv.ParseInt(expr.Value, 10, 64)
		if err != nil {
			return fmt.Errorf("compiler: invalid int literal %q: %w", expr.Value, err)
		}
		fc.emit(bytecode.OpLoadInt, 0, 0, value)
		return nil
	case *goast.Ident:
		switch expr.Name {
		case "true", "false":
			fc.emit(bytecode.OpLoadBool, 0, 0, 0)
			if expr.Name == "true" {
				fc.function.Code[len(fc.function.Code)-1].A = 1
			}
			return nil
		case "nil":
			fc.emit(bytecode.OpLoadNil, 0, 0, 0)
			return nil
		default:
			if constExpr := directGoConstExpr(fc.parent.facts, expr); constExpr != nil {
				return fc.emitDirectGoExpr(constExpr)
			}
			if fc.parent != nil && fc.parent.facts != nil && fc.parent.facts.Captures != nil {
				if sym := fc.parent.facts.Captures[expr]; sym != nil {
					capture, _, err := fc.ensureCapture(sym.Name)
					if err != nil {
						return err
					}
					fc.emit(bytecode.OpLoadCapture, capture, 0, 0)
					return nil
				}
			}
			if fc.parent != nil && fc.parent.facts != nil {
				if fc.parent.facts.Bindings == nil || fc.parent.facts.Bindings[expr] == nil {
					return fmt.Errorf("compiler: missing binding fact for ident %q", expr.Name)
				}
			}
			slot, ok := fc.scope.lookup(expr.Name)
			if !ok {
				if slot, ok := fc.parent.lookupGlobal(expr.Name); ok {
					fc.emit(bytecode.OpLoadGlobal, slot, 0, 0)
					return nil
				}
				if index, isFunc := fc.parent.funcIndex[expr.Name]; isFunc {
					fc.emit(bytecode.OpMakeFuncValue, index, 0, 0)
					return nil
				}
				return fmt.Errorf("compiler: undefined local %q", expr.Name)
			}
			fc.emit(bytecode.OpLoadLocal, slot, 0, 0)
			return nil
		}
	case *goast.ParenExpr:
		return fc.emitDirectGoExpr(expr.X)
	case *goast.UnaryExpr:
		if expr.Op == gotoken.AND {
			if lit, ok := expr.X.(*goast.CompositeLit); ok {
				typ := directGoFactOrExprType(fc.parent.facts, lit)
				if typ == nil {
					return fmt.Errorf("compiler: missing type fact for expression %T", lit)
				}
				slot, err := fc.declareSlot("_", typ)
				if err != nil {
					return err
				}
				if err := fc.emitDirectGoExpr(lit); err != nil {
					return err
				}
				fc.emit(bytecode.OpStoreLocal, slot, 0, 0)
				fc.emit(bytecode.OpAddrLocal, slot, 0, 0)
				return nil
			}
			return fc.emitDirectGoAddress(expr.X)
		}
		if err := fc.emitDirectGoExpr(expr.X); err != nil {
			return err
		}
		switch expr.Op {
		case gotoken.ADD:
			return nil
		case gotoken.SUB:
			fc.emit(bytecode.OpNeg, 0, 0, 0)
			return nil
		case gotoken.NOT:
			fc.emit(bytecode.OpNot, 0, 0, 0)
			return nil
		default:
			return fmt.Errorf("compiler: direct go/ast unary operator %s is not supported", expr.Op.String())
		}
	case *goast.StarExpr:
		if err := fc.emitDirectGoExpr(expr.X); err != nil {
			return err
		}
		fc.emit(bytecode.OpDeref, 0, 0, 0)
		return nil
	case *goast.BinaryExpr:
		if expr.Op == gotoken.LAND {
			if err := fc.emitDirectGoExpr(expr.X); err != nil {
				return err
			}
			falseJump := fc.emitJump(bytecode.OpJumpIfFalse)
			if err := fc.emitDirectGoExpr(expr.Y); err != nil {
				return err
			}
			endJump := fc.emitJump(bytecode.OpJump)
			fc.patchJump(falseJump, len(fc.function.Code))
			fc.emit(bytecode.OpLoadBool, 0, 0, 0)
			fc.patchJump(endJump, len(fc.function.Code))
			return nil
		}
		if expr.Op == gotoken.LOR {
			if err := fc.emitDirectGoExpr(expr.X); err != nil {
				return err
			}
			evalRightJump := fc.emitJump(bytecode.OpJumpIfFalse)
			fc.emit(bytecode.OpLoadBool, 1, 0, 0)
			endJump := fc.emitJump(bytecode.OpJump)
			fc.patchJump(evalRightJump, len(fc.function.Code))
			if err := fc.emitDirectGoExpr(expr.Y); err != nil {
				return err
			}
			fc.patchJump(endJump, len(fc.function.Code))
			return nil
		}
		if directGoFactOrExprType(fc.parent.facts, expr) == nil {
			return fmt.Errorf("compiler: missing type fact for expression %T", expr)
		}
		if err := fc.emitDirectGoExpr(expr.X); err != nil {
			return err
		}
		if expr.Op == gotoken.ADD && semaTypes.Identical(directGoFactOrExprType(fc.parent.facts, expr.X), semaTypes.IntType) {
			if right, ok := expr.Y.(*goast.IndexExpr); ok {
				if slot, index, ok := fc.directGoLocalIntArrayConstIndex(right); ok {
					fc.emit(bytecode.OpAddArrayElemLocalConst, slot, 0, index)
					return nil
				}
			}
		}
		if err := fc.emitDirectGoExpr(expr.Y); err != nil {
			return err
		}
		return fc.emitDirectGoBinaryOp(expr.Op)
	case *goast.CallExpr:
		if directGoFactOrExprType(fc.parent.facts, expr) == nil {
			if _, err := fc.directGoCallResultTypes(expr); err != nil {
				return fmt.Errorf("compiler: missing type fact for expression %T", expr)
			}
		}
		if ident, ok := expr.Fun.(*goast.Ident); ok && ident.Name == "make" {
			if len(expr.Args) < 1 {
				return fmt.Errorf("compiler: make expects at least 1 argument, got %d", len(expr.Args))
			}
			typ, err := directGoTypeForNamed(expr.Args[0], fc.parent.types)
			if err != nil {
				return err
			}
			if mapType, ok := semaTypes.Underlying(typ).(*semaTypes.MapType); ok {
				if len(expr.Args) != 1 {
					return fmt.Errorf("compiler: make map expects 1 argument, got %d", len(expr.Args))
				}
				kind, err := runtimeKindForType(mapType.Value)
				if err != nil {
					return err
				}
				fc.emit(bytecode.OpMakeMap, int(kind), 0, 0)
				return nil
			}
			if sliceType, ok := semaTypes.Underlying(typ).(*semaTypes.SliceType); ok {
				if len(expr.Args) != 2 && len(expr.Args) != 3 {
					return fmt.Errorf("compiler: make slice expects 2 or 3 arguments, got %d", len(expr.Args))
				}
				if err := fc.emitDirectGoExpr(expr.Args[1]); err != nil {
					return err
				}
				hasCap := 0
				if len(expr.Args) == 3 {
					if err := fc.emitDirectGoExpr(expr.Args[2]); err != nil {
						return err
					}
					hasCap = 1
				}
				kind, typeName, err := runtimeZeroKindForType(sliceType.Elem)
				if err != nil {
					return err
				}
				fc.emit(bytecode.OpMakeSliceLenCap, int(kind), hasCap, 0)
				fc.function.Code[len(fc.function.Code)-1].S = typeName
				return nil
			}
			return fmt.Errorf("compiler: direct go/ast make only supports map and slice types")
		}
		if ident, ok := expr.Fun.(*goast.Ident); ok && ident.Name == "len" {
			if len(expr.Args) != 1 {
				return fmt.Errorf("compiler: len expects 1 argument, got %d", len(expr.Args))
			}
			if err := fc.emitDirectGoExpr(expr.Args[0]); err != nil {
				return err
			}
			fc.emit(bytecode.OpBuiltinLen, 0, 0, 0)
			return nil
		}
		if ident, ok := expr.Fun.(*goast.Ident); ok && ident.Name == "append" {
			if len(expr.Args) < 2 {
				return fmt.Errorf("compiler: append expects at least 2 arguments, got %d", len(expr.Args))
			}
			targetType := directGoFactOrExprType(fc.parent.facts, expr.Args[0])
			sliceType, _ := semaTypes.Underlying(targetType).(*semaTypes.SliceType)
			if sliceType == nil {
				return fmt.Errorf("compiler: append first argument must be slice")
			}
			if err := fc.emitDirectGoExpr(expr.Args[0]); err != nil {
				return err
			}
			if expr.Ellipsis.IsValid() {
				if len(expr.Args) != 2 {
					return fmt.Errorf("compiler: append slice expansion expects exactly 2 arguments, got %d", len(expr.Args))
				}
				if err := fc.emitDirectGoExprForExpectedType(expr.Args[1], targetType); err != nil {
					return err
				}
				fc.emit(bytecode.OpAppendSlice, 0, 1, 0)
				return nil
			}
			for _, arg := range expr.Args[1:] {
				if err := fc.emitDirectGoExprForExpectedType(arg, sliceType.Elem); err != nil {
					return err
				}
			}
			fc.emit(bytecode.OpAppendSlice, len(expr.Args)-1, 0, 0)
			return nil
		}
		if ident, ok := expr.Fun.(*goast.Ident); ok && ident.Name == "clear" {
			return fmt.Errorf("compiler: clear has no value")
		}
		if ident, ok := expr.Fun.(*goast.Ident); ok {
			if targetType, err := fc.directGoTypeForScope(ident); err == nil && len(expr.Args) == 1 {
				if err := fc.emitDirectGoExpr(expr.Args[0]); err != nil {
					return err
				}
				sourceType := directGoFactOrExprType(fc.parent.facts, expr.Args[0])
				return fc.emitDirectGoConversion(sourceType, targetType)
			}
			if hostIndex, ok := fc.parent.program.LookupHostFunction(ident.Name); ok {
				sig, err := fc.directGoHostSignature(ident.Name)
				if err != nil {
					return err
				}
				argCount, err := fc.emitDirectGoCallArgsForHostSignature(expr, sig)
				if err != nil {
					return err
				}
				fc.emit(bytecode.OpCallHost, hostIndex, argCount, 0)
				return nil
			}
			if index, ok := fc.parent.funcIndex[ident.Name]; ok {
				sig, err := fc.directGoFuncSignature(expr)
				if err != nil {
					return err
				}
				argCount, err := fc.emitDirectGoCallArgsForSignature(expr, sig)
				if err != nil {
					return err
				}
				fc.emit(bytecode.OpCall, index, argCount, 0)
				return nil
			}
		}
		if sel, ok := expr.Fun.(*goast.SelectorExpr); ok {
			if sym := directGoFactBinding(fc.parent.facts, sel); sym != nil && sym.Kind == sema.SymFunc && sym.PkgPath != "" {
				qualifiedName := sym.PkgPath + "." + sym.Name
				if index, ok := fc.parent.pkgFuncIndex[qualifiedName]; ok {
					sig, err := fc.directGoFuncSignature(expr)
					if err != nil {
						return err
					}
					argCount, err := fc.emitDirectGoCallArgsForSignature(expr, sig)
					if err != nil {
						return err
					}
					fc.emit(bytecode.OpCall, index, argCount, 0)
					return nil
				}
				return fmt.Errorf("compiler: undefined function %q in package %q", sym.Name, sym.PkgPath)
			}
			if selection := directGoSelection(fc.parent.facts, sel); selection != nil && selection.Kind == sema.SelectionMethod {
				methodIndex, ok := fc.directGoMethodIndex(sel)
				if ok {
					var err error
					if selection.NeedsAddress {
						err = fc.emitDirectGoAddress(sel.X)
					} else {
						err = fc.emitDirectGoExpr(sel.X)
					}
					if err != nil {
						return err
					}
					sig, ok := semaTypes.Underlying(selection.Type).(*semaTypes.FuncType)
					if !ok || sig == nil {
						return fmt.Errorf("compiler: method selection is not a function")
					}
					argCount, err := fc.emitDirectGoCallArgsForSignature(expr, sig)
					if err != nil {
						return err
					}
					fc.emit(bytecode.OpCall, methodIndex, argCount+1, 0)
					return nil
				}
			}
		}
		if err := fc.emitDirectGoExpr(expr.Fun); err != nil {
			return err
		}
		sig, err := fc.directGoFuncSignature(expr)
		if err != nil {
			return err
		}
		argCount, err := fc.emitDirectGoCallArgsForSignature(expr, sig)
		if err != nil {
			return err
		}
		fc.emit(bytecode.OpCallValue, argCount, 0, 0)
		return nil
	case *goast.FuncLit:
		if fc.parent == nil || fc.parent.facts == nil || fc.parent.facts.Types == nil || fc.parent.facts.Types[expr] == nil {
			return fmt.Errorf("compiler: missing type fact for expression %T", expr)
		}
		index, captures, err := fc.parent.compileDirectGoFuncLit(fc, expr)
		if err != nil {
			return err
		}
		for _, capture := range captures {
			if capture.fromCapture {
				fc.emit(bytecode.OpAddrCapture, capture.index, 0, 0)
			} else {
				fc.emit(bytecode.OpAddrLocal, capture.index, 0, 0)
			}
		}
		fc.emit(bytecode.OpMakeClosure, index, len(captures), 0)
		return nil
	case *goast.CompositeLit:
		typ := directGoFactOrExprType(fc.parent.facts, expr)
		if typ == nil {
			return fmt.Errorf("compiler: missing type fact for expression %T", expr)
		}
		return fc.emitDirectGoCompositeLitForType(expr, typ)
	case *goast.SelectorExpr:
		selection := directGoSelection(fc.parent.facts, expr)
		if selection == nil {
			return fmt.Errorf("compiler: missing selection fact for direct go/ast selector")
		}
		if selection.Kind == sema.SelectionMethod {
			methodIndex, ok := fc.directGoMethodIndex(expr)
			if !ok {
				return fmt.Errorf("compiler: missing method selection for direct go/ast selector")
			}
			var err error
			if selection.NeedsAddress {
				err = fc.emitDirectGoAddress(expr.X)
			} else {
				err = fc.emitDirectGoExpr(expr.X)
			}
			if err != nil {
				return err
			}
			fc.emit(bytecode.OpMakeMethodValue, methodIndex, 0, 0)
			return nil
		}
		if selection.Kind != sema.SelectionField {
			return fmt.Errorf("compiler: missing selection fact for direct go/ast selector")
		}
		if baseIdent, ok := expr.X.(*goast.Ident); ok && !selection.NeedsDeref && semaTypes.Identical(selection.Type, semaTypes.IntType) {
			if baseSlot, ok := fc.lookupSlot(baseIdent.Name); ok {
				baseType, ok := fc.slotTypes[baseSlot]
				if ok {
					if _, isPtr := semaTypes.Underlying(baseType).(*semaTypes.PointerType); !isPtr {
						fc.emit(bytecode.OpLoadStructFieldLocal, baseSlot, selection.FieldIndex, 0)
						return nil
					}
				}
			}
		}
		if err := fc.emitDirectGoExpr(expr.X); err != nil {
			return err
		}
		if selection.NeedsDeref {
			fc.emit(bytecode.OpDeref, 0, 0, 0)
		}
		fc.emit(bytecode.OpLoadField, selection.FieldIndex, 0, 0)
		return nil
	case *goast.IndexExpr:
		if fc.parent.facts == nil || fc.parent.facts.Types == nil || fc.parent.facts.Types[expr] == nil {
			return fmt.Errorf("compiler: missing type fact for expression %T", expr)
		}
		baseType := directGoFactOrExprType(fc.parent.facts, expr.X)
		if _, ok := semaTypes.Underlying(baseType).(*semaTypes.MapType); ok {
			return fc.emitDirectGoMapGet(expr, false)
		}
		if slot, index, ok := fc.directGoLocalIntArrayConstIndex(expr); ok {
			fc.emit(bytecode.OpLoadArrayElemLocalConst, slot, 0, index)
			return nil
		}
		if err := fc.emitDirectGoExpr(expr.X); err != nil {
			return err
		}
		if err := fc.emitDirectGoExpr(expr.Index); err != nil {
			return err
		}
		fc.emit(bytecode.OpLoadIndex, 0, 0, 0)
		return nil
	case *goast.SliceExpr:
		if fc.parent.facts == nil || fc.parent.facts.Types == nil || fc.parent.facts.Types[expr] == nil {
			return fmt.Errorf("compiler: missing type fact for expression %T", expr)
		}
		if err := fc.emitDirectGoExpr(expr.X); err != nil {
			return err
		}
		lowPresent := 0
		if expr.Low != nil {
			if err := fc.emitDirectGoExpr(expr.Low); err != nil {
				return err
			}
			lowPresent = 1
		}
		highPresent := 0
		if expr.High != nil {
			if err := fc.emitDirectGoExpr(expr.High); err != nil {
				return err
			}
			highPresent = 1
		}
		maxPresent := int64(0)
		if expr.Max != nil {
			if err := fc.emitDirectGoExpr(expr.Max); err != nil {
				return err
			}
			maxPresent = 1
		}
		fc.emit(bytecode.OpSlice, lowPresent, highPresent, maxPresent)
		return nil
	default:
		return fmt.Errorf("compiler: direct go/ast expression %T is not supported", expr)
	}
}

func (fc *funcCompiler) emitDirectGoMapGet(expr *goast.IndexExpr, commaOK bool) error {
	if !commaOK {
		if slot, key, ok := fc.directGoLocalStringIntMapAccess(expr); ok {
			fc.emit(bytecode.OpMapGetLocalStringInt, slot, 0, 0)
			fc.function.Code[len(fc.function.Code)-1].S = key
			return nil
		}
	}
	baseType := directGoFactOrExprType(fc.parent.facts, expr.X)
	mapType, ok := semaTypes.Underlying(baseType).(*semaTypes.MapType)
	if !ok || mapType == nil {
		return fmt.Errorf("compiler: expected map index expression")
	}
	if err := fc.emitDirectGoExpr(expr.X); err != nil {
		return err
	}
	if err := fc.emitDirectGoExprForExpectedType(expr.Index, mapType.Key); err != nil {
		return err
	}
	kind, typeName, err := runtimeZeroKindForType(mapType.Value)
	if err != nil {
		return err
	}
	op := bytecode.OpMapGet
	if commaOK {
		op = bytecode.OpMapGetOK
	}
	fc.emit(op, int(kind), 0, 0)
	fc.function.Code[len(fc.function.Code)-1].S = typeName
	return nil
}

func (fc *funcCompiler) directGoLocalStringIntMapAccess(expr *goast.IndexExpr) (int, string, bool) {
	base, ok := expr.X.(*goast.Ident)
	if !ok {
		return 0, "", false
	}
	literal, ok := expr.Index.(*goast.BasicLit)
	if !ok || literal.Kind != gotoken.STRING {
		return 0, "", false
	}
	slot, ok := fc.lookupSlot(base.Name)
	if !ok {
		return 0, "", false
	}
	mapType, ok := semaTypes.Underlying(fc.slotTypes[slot]).(*semaTypes.MapType)
	if !ok || !semaTypes.Identical(mapType.Key, semaTypes.StringType) || !semaTypes.Identical(mapType.Value, semaTypes.IntType) {
		return 0, "", false
	}
	key, err := strconv.Unquote(literal.Value)
	return slot, key, err == nil
}

func (fc *funcCompiler) emitDirectGoConversion(src, dst semaTypes.Type) error {
	if src == nil || dst == nil {
		return fmt.Errorf("compiler: missing type fact for conversion")
	}
	srcKind := semaTypes.Underlying(src).Kind()
	dstKind := semaTypes.Underlying(dst).Kind()
	if srcKind == dstKind {
		return nil
	}
	switch dstKind {
	case semaTypes.Int:
		fc.emit(bytecode.OpConvertInt, 0, 0, 0)
		return nil
	case semaTypes.Int64:
		fc.emit(bytecode.OpConvertInt64, 0, 0, 0)
		return nil
	case semaTypes.Float64:
		fc.emit(bytecode.OpConvertFloat64, 0, 0, 0)
		return nil
	case semaTypes.String:
		fc.emit(bytecode.OpConvertString, 0, 0, 0)
		return nil
	default:
		if semaTypes.Underlying(src).Kind() == semaTypes.Underlying(dst).Kind() {
			return nil
		}
		return fmt.Errorf("compiler: unsupported conversion from %s to %s", src.String(), dst.String())
	}
}

func (fc *funcCompiler) emitDirectGoCompositeLitForType(expr *goast.CompositeLit, typ semaTypes.Type) error {
	switch t := semaTypes.Underlying(typ).(type) {
	case *semaTypes.StructType:
		keyed := false
		for _, elt := range expr.Elts {
			if _, ok := elt.(*goast.KeyValueExpr); ok {
				keyed = true
				break
			}
		}
		if keyed {
			values := make([]goast.Expr, len(t.Fields))
			for _, elt := range expr.Elts {
				kv, ok := elt.(*goast.KeyValueExpr)
				if !ok {
					return fmt.Errorf("compiler: direct go/ast struct literal mixes keyed and positional elements")
				}
				key, ok := kv.Key.(*goast.Ident)
				if !ok {
					return fmt.Errorf("compiler: direct go/ast struct literal key %T is not supported", kv.Key)
				}
				fieldIndex := -1
				for i, name := range t.FieldNames {
					if name == key.Name {
						fieldIndex = i
						break
					}
				}
				if fieldIndex < 0 {
					return fmt.Errorf("compiler: direct go/ast struct literal field %q not found", key.Name)
				}
				values[fieldIndex] = kv.Value
			}
			for i, fieldType := range t.Fields {
				if values[i] == nil {
					if err := fc.emitZeroValue(fieldType); err != nil {
						return err
					}
					continue
				}
				if err := fc.emitDirectGoExprForExpectedType(values[i], fieldType); err != nil {
					return err
				}
			}
			fc.emit(bytecode.OpMakeStruct, len(t.Fields), 0, 0)
			if named, ok := typ.(*semaTypes.NamedType); ok {
				fc.function.Code[len(fc.function.Code)-1].S = named.Name
			}
			return nil
		}
		if len(expr.Elts) != len(t.Fields) {
			return fmt.Errorf("compiler: direct go/ast struct literal expects %d elements, got %d", len(t.Fields), len(expr.Elts))
		}
		for i, elt := range expr.Elts {
			if err := fc.emitDirectGoExprForExpectedType(elt, t.Fields[i]); err != nil {
				return err
			}
		}
		fc.emit(bytecode.OpMakeStruct, len(expr.Elts), 0, 0)
		if named, ok := typ.(*semaTypes.NamedType); ok {
			fc.function.Code[len(fc.function.Code)-1].S = named.Name
		}
		return nil
	case *semaTypes.ArrayType:
		if len(expr.Elts) != t.Len {
			return fmt.Errorf("compiler: composite literal has %d elements for %s", len(expr.Elts), typ.String())
		}
		for _, elt := range expr.Elts {
			if err := fc.emitDirectGoExprForExpectedType(elt, t.Elem); err != nil {
				return err
			}
		}
		fc.emit(bytecode.OpMakeArray, len(expr.Elts), 0, 0)
		return nil
	case *semaTypes.SliceType:
		for _, elt := range expr.Elts {
			if err := fc.emitDirectGoExprForExpectedType(elt, t.Elem); err != nil {
				return err
			}
		}
		fc.emit(bytecode.OpMakeSlice, len(expr.Elts), 0, 0)
		return nil
	default:
		return fmt.Errorf("compiler: unsupported direct go/ast composite literal type %s", typ.String())
	}
}

func (fc *funcCompiler) emitDirectGoExprForExpectedType(expr goast.Expr, expected semaTypes.Type) error {
	if lit, ok := expr.(*goast.CompositeLit); ok && lit.Type == nil && expected != nil {
		return fc.emitDirectGoCompositeLitForType(lit, expected)
	}
	if err := fc.emitDirectGoExpr(expr); err != nil {
		return err
	}
	if expected == nil {
		return nil
	}
	src := directGoFactOrExprType(fc.parent.facts, expr)
	if src == nil || semaTypes.AssignableTo(src, expected) {
		return nil
	}
	return fc.emitDirectGoConversion(src, expected)
}

func (fc *funcCompiler) emitDirectGoBinaryOp(op gotoken.Token) error {
	switch op {
	case gotoken.ADD:
		fc.emit(bytecode.OpAdd, 0, 0, 0)
	case gotoken.SUB:
		fc.emit(bytecode.OpSub, 0, 0, 0)
	case gotoken.MUL:
		fc.emit(bytecode.OpMul, 0, 0, 0)
	case gotoken.QUO:
		fc.emit(bytecode.OpDiv, 0, 0, 0)
	case gotoken.REM:
		fc.emit(bytecode.OpModInt, 0, 0, 0)
	case gotoken.AND:
		fc.emit(bytecode.OpBitAnd, 0, 0, 0)
	case gotoken.OR:
		fc.emit(bytecode.OpBitOr, 0, 0, 0)
	case gotoken.XOR:
		fc.emit(bytecode.OpBitXor, 0, 0, 0)
	case gotoken.AND_NOT:
		fc.emit(bytecode.OpBitClear, 0, 0, 0)
	case gotoken.SHL:
		fc.emit(bytecode.OpShiftLeft, 0, 0, 0)
	case gotoken.SHR:
		fc.emit(bytecode.OpShiftRight, 0, 0, 0)
	case gotoken.LAND:
		fc.emit(bytecode.OpAndAnd, 0, 0, 0)
	case gotoken.LOR:
		fc.emit(bytecode.OpOrOr, 0, 0, 0)
	case gotoken.EQL:
		fc.emit(bytecode.OpCmpEq, 0, 0, 0)
	case gotoken.NEQ:
		fc.emit(bytecode.OpCmpEq, 0, 0, 0)
		fc.emit(bytecode.OpNot, 0, 0, 0)
	case gotoken.LSS:
		fc.emit(bytecode.OpCmpLt, 0, 0, 0)
	case gotoken.LEQ:
		fc.emit(bytecode.OpCmpLe, 0, 0, 0)
	case gotoken.GTR:
		fc.emit(bytecode.OpCmpGt, 0, 0, 0)
	case gotoken.GEQ:
		fc.emit(bytecode.OpCmpGe, 0, 0, 0)
	default:
		return fmt.Errorf("compiler: direct go/ast binary operator %s is not supported", op.String())
	}
	return nil
}
