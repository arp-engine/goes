package sema

import (
	"fmt"
	goast "go/ast"
	gotoken "go/token"
	"strconv"

	semaTypes "goes/types"
)

func checkGoPackageSemantics(fset *gotoken.FileSet, files []*goast.File, facts *Facts) error {
	for _, file := range files {
		if file == nil {
			continue
		}
		for _, decl := range file.Decls {
			if err := checkGoDeclSemantics(fset, facts, decl); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkGoDeclSemantics(fset *gotoken.FileSet, facts *Facts, decl goast.Decl) error {
	switch decl := decl.(type) {
	case *goast.FuncDecl:
		return checkGoFuncDeclSemantics(fset, facts, decl)
	case *goast.GenDecl:
		for _, spec := range decl.Specs {
			valueSpec, ok := spec.(*goast.ValueSpec)
			if !ok {
				continue
			}
			var declaredType semaTypes.Type
			if valueSpec.Type != nil {
				typ, err := goTypeFromFacts(facts, valueSpec.Type)
				if err != nil {
					return err
				}
				declaredType = typ
			}
			for _, value := range valueSpec.Values {
				valueType, ok, err := tryGoExprType(facts, value)
				if err != nil {
					return err
				}
				if ok && declaredType != nil && !semaTypes.AssignableTo(valueType, declaredType) && !goExprAssignableTo(value, declaredType, facts) {
					return goastError(fset, value.Pos(), "cannot initialize value of type %s as %s", valueType.String(), declaredType.String())
				}
				if err := checkNestedGoExprSemantics(fset, facts, value); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func checkGoFuncDeclSemantics(fset *gotoken.FileSet, facts *Facts, decl *goast.FuncDecl) error {
	if decl == nil || decl.Body == nil {
		return nil
	}
	results, err := goResultsFromFuncDecl(facts, decl)
	if err != nil {
		return err
	}
	return checkGoBlockSemantics(fset, facts, decl.Body, results)
}

func checkGoBlockSemantics(fset *gotoken.FileSet, facts *Facts, block *goast.BlockStmt, results []semaTypes.Type) error {
	if block == nil {
		return nil
	}
	for _, stmt := range block.List {
		if err := checkGoStmtSemantics(fset, facts, stmt, results); err != nil {
			return err
		}
	}
	return nil
}

func checkGoStmtSemantics(fset *gotoken.FileSet, facts *Facts, stmt goast.Stmt, results []semaTypes.Type) error {
	switch stmt := stmt.(type) {
	case nil, *goast.EmptyStmt:
		return nil
	case *goast.BlockStmt:
		return checkGoBlockSemantics(fset, facts, stmt, results)
	case *goast.IfStmt:
		if stmt.Init != nil {
			if err := checkGoStmtSemantics(fset, facts, stmt.Init, results); err != nil {
				return err
			}
		}
		condType, ok, err := tryGoExprType(facts, stmt.Cond)
		if err != nil {
			return err
		}
		if ok && !semaTypes.IsBoolean(condType) {
			return goastError(fset, stmt.Cond.Pos(), "condition must be bool, got %s", condType.String())
		}
		if err := checkGoBlockSemantics(fset, facts, stmt.Body, results); err != nil {
			return err
		}
		switch elseStmt := stmt.Else.(type) {
		case nil:
			return nil
		case *goast.BlockStmt:
			return checkGoBlockSemantics(fset, facts, elseStmt, results)
		default:
			return checkGoStmtSemantics(fset, facts, elseStmt, results)
		}
	case *goast.ForStmt:
		if stmt.Init != nil {
			if err := checkGoStmtSemantics(fset, facts, stmt.Init, results); err != nil {
				return err
			}
		}
		if stmt.Cond != nil {
			if _, _, err := tryGoExprType(facts, stmt.Cond); err != nil {
				return err
			}
			if err := checkNestedGoExprSemantics(fset, facts, stmt.Cond); err != nil {
				return err
			}
		}
		if stmt.Post != nil {
			if err := checkGoStmtSemantics(fset, facts, stmt.Post, results); err != nil {
				return err
			}
		}
		return checkGoBlockSemantics(fset, facts, stmt.Body, results)
	case *goast.RangeStmt:
		rangeType, ok, err := tryGoExprType(facts, stmt.X)
		if err != nil {
			return err
		}
		if ok {
			if err := checkGoRangeSemantics(rangeType, stmt); err != nil {
				return err
			}
			if err := checkGoRangeAssignSemantics(fset, facts, rangeType, stmt); err != nil {
				return err
			}
		}
		if err := checkNestedGoExprSemantics(fset, facts, stmt.X); err != nil {
			return err
		}
		if stmt.Tok == gotoken.ASSIGN {
			if stmt.Key != nil {
				if _, _, err := tryGoExprType(facts, stmt.Key); err != nil {
					return err
				}
				if err := checkNestedGoExprSemantics(fset, facts, stmt.Key); err != nil {
					return err
				}
			}
			if stmt.Value != nil {
				if _, _, err := tryGoExprType(facts, stmt.Value); err != nil {
					return err
				}
				if err := checkNestedGoExprSemantics(fset, facts, stmt.Value); err != nil {
					return err
				}
			}
		}
		return checkGoBlockSemantics(fset, facts, stmt.Body, results)
	case *goast.SwitchStmt:
		if stmt.Init != nil {
			if err := checkGoStmtSemantics(fset, facts, stmt.Init, results); err != nil {
				return err
			}
		}
		if stmt.Tag != nil {
			if _, _, err := tryGoExprType(facts, stmt.Tag); err != nil {
				return err
			}
			if err := checkNestedGoExprSemantics(fset, facts, stmt.Tag); err != nil {
				return err
			}
		}
		if stmt.Body == nil {
			return nil
		}
		for _, clause := range stmt.Body.List {
			caseClause, ok := clause.(*goast.CaseClause)
			if !ok {
				continue
			}
			for _, expr := range caseClause.List {
				caseType, ok, err := tryGoExprType(facts, expr)
				if err != nil {
					return err
				}
				if ok && stmt.Tag != nil {
					tagType, ok, err := tryGoExprType(facts, stmt.Tag)
					if err != nil {
						return err
					}
					if ok && !goSwitchCaseCompatible(expr, caseType, tagType, facts) {
						return goastError(fset, expr.Pos(), "switch case type %s is not compatible with %s", caseType.String(), tagType.String())
					}
				}
				if err := checkNestedGoExprSemantics(fset, facts, expr); err != nil {
					return err
				}
			}
			for _, bodyStmt := range caseClause.Body {
				if err := checkGoStmtSemantics(fset, facts, bodyStmt, results); err != nil {
					return err
				}
			}
		}
		return nil
	case *goast.DeclStmt:
		return checkGoDeclSemantics(fset, facts, stmt.Decl)
	case *goast.ExprStmt:
		return checkGoExprStmtSemantics(fset, facts, stmt)
	case *goast.IncDecStmt:
		return checkGoIncDecSemantics(fset, facts, stmt)
	case *goast.AssignStmt:
		for _, expr := range stmt.Rhs {
			if _, _, err := tryGoExprType(facts, expr); err != nil {
				return err
			}
			if err := checkNestedGoExprSemantics(fset, facts, expr); err != nil {
				return err
			}
		}
		if stmt.Tok == gotoken.DEFINE {
			return checkGoDefineSemantics(fset, facts, stmt)
		}
		for _, expr := range stmt.Lhs {
			if _, _, err := tryGoExprType(facts, expr); err != nil {
				return err
			}
			if err := checkNestedGoExprSemantics(fset, facts, expr); err != nil {
				return err
			}
		}
		if stmt.Tok != gotoken.ASSIGN {
			return checkGoCompoundAssignSemantics(fset, facts, stmt)
		}
		return checkGoAssignSemantics(fset, facts, stmt)
	case *goast.ReturnStmt:
		return checkGoReturnSemantics(fset, facts, stmt, results)
	default:
		return nil
	}
}

func checkGoRangeSemantics(rangeType semaTypes.Type, stmt *goast.RangeStmt) error {
	if semaTypes.IsInteger(rangeType) {
		if stmt.Value != nil {
			return fmt.Errorf("sema: integer range permits at most one iteration variable")
		}
		return nil
	}
	switch semaTypes.Underlying(rangeType).(type) {
	case *semaTypes.MapType, *semaTypes.ArrayType, *semaTypes.SliceType:
		return nil
	default:
		return fmt.Errorf("sema: cannot range over %s", rangeType.String())
	}
}

func checkGoRangeAssignSemantics(fset *gotoken.FileSet, facts *Facts, rangeType semaTypes.Type, stmt *goast.RangeStmt) error {
	if stmt.Tok != gotoken.ASSIGN {
		return nil
	}
	keyType, valueType, err := goRangeKeyValueTypes(rangeType)
	if err != nil {
		return err
	}
	if err := checkGoRangeAssignTarget(fset, facts, stmt.Key, keyType); err != nil {
		return err
	}
	return checkGoRangeAssignTarget(fset, facts, stmt.Value, valueType)
}

func checkGoRangeAssignTarget(fset *gotoken.FileSet, facts *Facts, target goast.Expr, sourceType semaTypes.Type) error {
	if target == nil {
		return nil
	}
	if ident, ok := target.(*goast.Ident); ok && ident.Name == "_" {
		return nil
	}
	targetType, ok, err := tryGoExprType(facts, target)
	if err != nil || !ok {
		return err
	}
	if semaTypes.IsInvalid(sourceType) || semaTypes.IsInvalid(targetType) {
		return nil
	}
	if !goAssignmentCompatible(nil, sourceType, targetType, facts) {
		return goastError(fset, target.Pos(), "cannot assign %s to %s", sourceType.String(), targetType.String())
	}
	return nil
}

func goRangeKeyValueTypes(rangeType semaTypes.Type) (semaTypes.Type, semaTypes.Type, error) {
	if semaTypes.IsInteger(rangeType) {
		return rangeType, semaTypes.InvalidType, nil
	}
	switch t := semaTypes.Underlying(rangeType).(type) {
	case *semaTypes.MapType:
		return t.Key, t.Value, nil
	case *semaTypes.ArrayType:
		return semaTypes.IntType, t.Elem, nil
	case *semaTypes.SliceType:
		return semaTypes.IntType, t.Elem, nil
	default:
		return nil, nil, fmt.Errorf("sema: cannot range over %s", rangeType.String())
	}
}

func checkGoAssignSemantics(fset *gotoken.FileSet, facts *Facts, stmt *goast.AssignStmt) error {
	rightTypes, ok, err := goAssignRightTypes(facts, stmt)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if len(stmt.Lhs) == 1 && len(rightTypes) > 1 {
		return goastError(fset, stmt.Rhs[0].Pos(), "multiple values in single-value context")
	}
	if len(stmt.Lhs) != len(rightTypes) {
		return goastError(fset, stmt.Pos(), "assignment count mismatch: %d = %d", len(stmt.Lhs), len(rightTypes))
	}
	for i, lhs := range stmt.Lhs {
		if ident, ok := lhs.(*goast.Ident); ok && ident.Name == "_" {
			continue
		}
		if !isGoAddressableExpr(facts, lhs) {
			return goastError(fset, lhs.Pos(), "left side of assignment is not addressable: %T", lhs)
		}
		leftType, ok, err := tryGoExprType(facts, lhs)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if semaTypes.IsInvalid(rightTypes[i]) || semaTypes.IsInvalid(leftType) {
			return nil
		}
		var rightExpr goast.Expr
		if len(stmt.Rhs) == len(stmt.Lhs) {
			rightExpr = stmt.Rhs[i]
		}
		if !goAssignmentCompatible(rightExpr, rightTypes[i], leftType, facts) {
			return goastError(fset, lhs.Pos(), "cannot assign %s to %s", rightTypes[i].String(), leftType.String())
		}
	}
	return nil
}

func checkGoDefineSemantics(fset *gotoken.FileSet, facts *Facts, stmt *goast.AssignStmt) error {
	rightTypes, ok, err := goAssignRightTypes(facts, stmt)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if len(stmt.Lhs) == 1 && len(rightTypes) > 1 {
		return goastError(fset, stmt.Rhs[0].Pos(), "multiple values in single-value context")
	}
	if len(stmt.Lhs) != len(rightTypes) {
		return goastError(fset, stmt.Pos(), "assignment count mismatch: %d = %d", len(stmt.Lhs), len(rightTypes))
	}
	return nil
}

func goAssignRightTypes(facts *Facts, stmt *goast.AssignStmt) ([]semaTypes.Type, bool, error) {
	if stmt != nil && len(stmt.Rhs) == 1 && len(stmt.Lhs) == 2 {
		if valueType, ok, err := goMapIndexCommaOkTypes(facts, stmt.Rhs[0]); err != nil || ok {
			return valueType, ok, err
		}
	}
	rightTypes := make([]semaTypes.Type, 0, len(stmt.Rhs))
	for _, expr := range stmt.Rhs {
		types, ok, err := goExprResultTypes(facts, expr)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		rightTypes = append(rightTypes, types...)
	}
	return rightTypes, true, nil
}

func goMapIndexCommaOkTypes(facts *Facts, expr goast.Expr) ([]semaTypes.Type, bool, error) {
	index, ok := expr.(*goast.IndexExpr)
	if !ok {
		return nil, false, nil
	}
	baseType, ok, err := tryGoExprType(facts, index.X)
	if err != nil || !ok {
		return nil, ok, err
	}
	mapType, ok := semaTypes.Underlying(baseType).(*semaTypes.MapType)
	if !ok || mapType == nil {
		return nil, false, nil
	}
	indexType, ok, err := tryGoExprType(facts, index.Index)
	if err != nil || !ok {
		return nil, ok, err
	}
	if !goAssignmentCompatible(index.Index, indexType, mapType.Key, facts) {
		return nil, false, fmt.Errorf("sema: cannot index %s with %s", baseType.String(), indexType.String())
	}
	recordGoExprType(facts, index, mapType.Value)
	return []semaTypes.Type{mapType.Value, semaTypes.BoolType}, true, nil
}

func checkGoCompoundAssignSemantics(fset *gotoken.FileSet, facts *Facts, stmt *goast.AssignStmt) error {
	if len(stmt.Lhs) != 1 || len(stmt.Rhs) != 1 {
		return goastError(fset, stmt.Pos(), "compound assignment count mismatch: %d %s= %d", len(stmt.Lhs), stmt.Tok.String(), len(stmt.Rhs))
	}
	if !isGoAddressableExpr(facts, stmt.Lhs[0]) {
		return goastError(fset, stmt.Lhs[0].Pos(), "left side of assignment is not addressable: %T", stmt.Lhs[0])
	}
	leftType, ok, err := tryGoExprType(facts, stmt.Lhs[0])
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	rightType, ok, err := tryGoExprType(facts, stmt.Rhs[0])
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	switch stmt.Tok {
	case gotoken.ADD_ASSIGN:
		if semaTypes.IsString(leftType) && semaTypes.IsString(rightType) {
			return nil
		}
		if !semaTypes.IsNumeric(leftType) || !semaTypes.IsNumeric(rightType) {
			return goastError(fset, stmt.Pos(), "operator + expects numeric or string operands, got %s and %s", leftType.String(), rightType.String())
		}
	case gotoken.SUB_ASSIGN, gotoken.MUL_ASSIGN, gotoken.QUO_ASSIGN:
		if !semaTypes.IsNumeric(leftType) || !semaTypes.IsNumeric(rightType) {
			return goastError(fset, stmt.Pos(), "arithmetic operands must be numeric, got %s and %s", leftType.String(), rightType.String())
		}
	case gotoken.REM_ASSIGN, gotoken.AND_ASSIGN, gotoken.OR_ASSIGN, gotoken.XOR_ASSIGN, gotoken.AND_NOT_ASSIGN:
		if !semaTypes.IsInteger(leftType) || !semaTypes.IsInteger(rightType) {
			return goastError(fset, stmt.Pos(), "operator requires integer operands, got %s and %s", leftType.String(), rightType.String())
		}
	case gotoken.SHL_ASSIGN, gotoken.SHR_ASSIGN:
		if !semaTypes.IsInteger(leftType) || !semaTypes.IsInteger(rightType) {
			return goastError(fset, stmt.Pos(), "shift operands must be integers, got %s and %s", leftType.String(), rightType.String())
		}
	default:
		return nil
	}
	return nil
}

func checkGoReturnSemantics(fset *gotoken.FileSet, facts *Facts, stmt *goast.ReturnStmt, results []semaTypes.Type) error {
	if len(stmt.Results) == 0 {
		return nil
	}
	gotTypes := make([]semaTypes.Type, 0, len(stmt.Results))
	gotExprs := make([]goast.Expr, 0, len(stmt.Results))
	for _, expr := range stmt.Results {
		types, ok, err := goExprResultTypes(facts, expr)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		gotTypes = append(gotTypes, types...)
		if len(types) == 1 {
			gotExprs = append(gotExprs, expr)
		} else {
			for range types {
				gotExprs = append(gotExprs, nil)
			}
		}
	}
	if len(gotTypes) != len(results) {
		return goastError(fset, stmt.Pos(), "return value count mismatch: got %d, want %d", len(gotTypes), len(results))
	}
	for i, got := range gotTypes {
		if semaTypes.IsInvalid(got) || semaTypes.IsInvalid(results[i]) {
			return nil
		}
		if !goAssignmentCompatible(gotExprs[i], got, results[i], facts) {
			return goastError(fset, stmt.Pos(), "cannot return %s as %s", got.String(), results[i].String())
		}
	}
	for _, expr := range stmt.Results {
		if err := checkNestedGoExprSemantics(fset, facts, expr); err != nil {
			return err
		}
	}
	return nil
}

func goExprResultTypes(facts *Facts, expr goast.Expr) ([]semaTypes.Type, bool, error) {
	if call, ok := expr.(*goast.CallExpr); ok {
		if ident, ok := call.Fun.(*goast.Ident); ok {
			if builtin := semaTypes.LookupBuiltin(ident.Name); builtin != nil {
				return []semaTypes.Type{builtin}, true, nil
			}
			switch ident.Name {
			case "make", "len", "delete", "append":
				typ, ok, err := tryGoExprType(facts, expr)
				if err != nil || !ok {
					return nil, ok, err
				}
				return []semaTypes.Type{typ}, true, nil
			case "clear":
				if _, ok, err := tryGoExprType(facts, expr); err != nil || !ok {
					return nil, ok, err
				}
				return nil, true, nil
			}
		}
		funType, ok, err := tryGoExprType(facts, call.Fun)
		if err != nil || !ok {
			return nil, ok, err
		}
		if fn, ok := semaTypes.Underlying(funType).(*semaTypes.FuncType); ok {
			return append([]semaTypes.Type(nil), fn.Results...), true, nil
		}
		return nil, false, nil
	}
	typ, ok, err := tryGoExprType(facts, expr)
	if err != nil || !ok {
		return nil, ok, err
	}
	return []semaTypes.Type{typ}, true, nil
}

func checkGoExprStmtSemantics(fset *gotoken.FileSet, facts *Facts, stmt *goast.ExprStmt) error {
	call, ok := stmt.X.(*goast.CallExpr)
	if !ok {
		return goastError(fset, stmt.X.Pos(), "expression statement must be a call")
	}
	if ident, ok := call.Fun.(*goast.Ident); ok {
		if semaTypes.LookupBuiltin(ident.Name) != nil {
			return goastError(fset, stmt.X.Pos(), "expression statement must be a call")
		}
		if facts != nil && facts.Bindings != nil {
			if sym := facts.Bindings[ident]; sym != nil && sym.Kind == SymType {
				return goastError(fset, stmt.X.Pos(), "expression statement must be a call")
			}
		}
	}
	if _, _, err := tryGoExprType(facts, stmt.X); err != nil {
		return err
	}
	return checkNestedGoExprSemantics(fset, facts, stmt.X)
}

func checkGoIncDecSemantics(fset *gotoken.FileSet, facts *Facts, stmt *goast.IncDecStmt) error {
	if stmt == nil {
		return nil
	}
	if !isGoAddressableExpr(facts, stmt.X) {
		return goastError(fset, stmt.X.Pos(), "left side of assignment is not addressable: %T", stmt.X)
	}
	typ, ok, err := tryGoExprType(facts, stmt.X)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if !semaTypes.IsNumeric(typ) {
		return goastError(fset, stmt.X.Pos(), "operator %q requires numeric operand, got %s", stmt.Tok.String(), typ.String())
	}
	return checkNestedGoExprSemantics(fset, facts, stmt.X)
}

func checkNestedGoExprSemantics(fset *gotoken.FileSet, facts *Facts, expr goast.Expr) error {
	if expr == nil {
		return nil
	}
	var err error
	goast.Inspect(expr, func(node goast.Node) bool {
		if err != nil {
			return false
		}
		child, ok := node.(goast.Expr)
		if !ok {
			return true
		}
		_, _, err = tryGoExprType(facts, child)
		if err != nil {
			return false
		}
		if funcLit, ok := child.(*goast.FuncLit); ok {
			results, err2 := goResultsFromFuncType(facts, funcLit.Type)
			if err2 != nil {
				err = err2
				return false
			}
			err = checkGoBlockSemantics(fset, facts, funcLit.Body, results)
			return false
		}
		return true
	})
	return err
}

func goResultsFromFuncDecl(facts *Facts, decl *goast.FuncDecl) ([]semaTypes.Type, error) {
	if decl == nil || decl.Type == nil || decl.Type.Results == nil {
		return nil, nil
	}
	return goResultsFromFuncType(facts, decl.Type)
}

func goResultsFromFuncType(facts *Facts, funcType *goast.FuncType) ([]semaTypes.Type, error) {
	if funcType == nil || funcType.Results == nil {
		return nil, nil
	}
	results := make([]semaTypes.Type, 0)
	for _, field := range funcType.Results.List {
		fieldType, err := goTypeFromFacts(facts, field.Type)
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
	return results, nil
}

func goTypeFromFacts(facts *Facts, expr goast.Expr) (semaTypes.Type, error) {
	switch expr := expr.(type) {
	case *goast.Ident:
		if builtin := semaTypes.LookupBuiltin(expr.Name); builtin != nil {
			return builtin, nil
		}
		sym := facts.Bindings[expr]
		if sym == nil || sym.Kind != SymType {
			return nil, fmt.Errorf("sema: missing type binding for %q", expr.Name)
		}
		return sym.Type, nil
	case *goast.ArrayType:
		elem, err := goTypeFromFacts(facts, expr.Elt)
		if err != nil {
			return nil, err
		}
		if expr.Len == nil {
			return &semaTypes.SliceType{Elem: elem}, nil
		}
		length, err := goConstArrayLenFromFacts(facts, expr.Len)
		if err != nil {
			return nil, err
		}
		return &semaTypes.ArrayType{Len: length, Elem: elem}, nil
	case *goast.StarExpr:
		base, err := goTypeFromFacts(facts, expr.X)
		if err != nil {
			return nil, err
		}
		return &semaTypes.PointerType{Base: base}, nil
	case *goast.MapType:
		key, err := goTypeFromFacts(facts, expr.Key)
		if err != nil {
			return nil, err
		}
		value, err := goTypeFromFacts(facts, expr.Value)
		if err != nil {
			return nil, err
		}
		return &semaTypes.MapType{Key: key, Value: value}, nil
	case *goast.Ellipsis:
		elem, err := goTypeFromFacts(facts, expr.Elt)
		if err != nil {
			return nil, err
		}
		return &semaTypes.SliceType{Elem: elem}, nil
	case *goast.StructType:
		fields := make([]semaTypes.Type, 0)
		names := make([]string, 0)
		if expr.Fields == nil {
			return &semaTypes.StructType{}, nil
		}
		for _, field := range expr.Fields.List {
			fieldType, err := goTypeFromFacts(facts, field.Type)
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
				fieldType, err := goTypeFromFacts(facts, field.Type)
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
				fieldType, err := goTypeFromFacts(facts, field.Type)
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
	default:
		return nil, fmt.Errorf("sema: unsupported go/ast type expression %T in direct checker", expr)
	}
}

func goConstArrayLenFromFacts(facts *Facts, expr goast.Expr) (int, error) {
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
		return goConstArrayLenFromFacts(facts, expr.X)
	case *goast.UnaryExpr:
		value, err := goConstArrayLenFromFacts(facts, expr.X)
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
		left, err := goConstArrayLenFromFacts(facts, expr.X)
		if err != nil {
			return 0, err
		}
		right, err := goConstArrayLenFromFacts(facts, expr.Y)
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
		if facts == nil || facts.ConstExprs == nil {
			return 0, fmt.Errorf("%q is not an integer constant", expr.Name)
		}
		constExpr, ok := facts.ConstExprs[expr].(goast.Expr)
		if !ok || constExpr == nil || constExpr == expr {
			return 0, fmt.Errorf("%q is not an integer constant", expr.Name)
		}
		return goConstArrayLenFromFacts(facts, constExpr)
	default:
		return 0, fmt.Errorf("unsupported constant expression %T", expr)
	}
}

func tryGoPkgSelectorType(facts *Facts, expr *goast.SelectorExpr, pkgSym *Symbol) (semaTypes.Type, bool, error) {
	if facts == nil || facts.PackageScope == nil || pkgSym == nil {
		return nil, false, nil
	}
	for s := facts.PackageScope; s != nil; s = s.Parent {
		pkgScope, ok := s.Imported[pkgSym.Name]
		if !ok {
			continue
		}
		sym := pkgScope.LookupLocal(expr.Sel.Name)
		if sym == nil || !sym.Exported {
			return nil, false, nil
		}
		if sym.Type == nil {
			return nil, false, nil
		}
		return sym.Type, true, nil
	}
	return nil, false, nil
}

func tryGoExprType(facts *Facts, expr goast.Expr) (result semaTypes.Type, ok bool, err error) {
	orig := expr
	defer func() {
		if err == nil && ok && result != nil {
			recordGoExprType(facts, orig, result)
		}
	}()

	switch expr := expr.(type) {
	case nil:
		return semaTypes.InvalidType, true, nil
	case *goast.Ident:
		switch expr.Name {
		case "true", "false":
			recordGoConstExpr(facts, expr, expr)
			return semaTypes.BoolType, true, nil
		case "nil":
			recordGoConstExpr(facts, expr, expr)
			return semaTypes.NilType, true, nil
		}
		sym := facts.Bindings[expr]
		if sym == nil {
			return nil, false, nil
		}
		if sym.Kind == SymConst {
			recordGoConstExpr(facts, expr, expr)
		}
		return sym.Type, true, nil
	case *goast.BasicLit:
		recordGoConstExpr(facts, expr, expr)
		switch expr.Kind {
		case gotoken.STRING:
			return semaTypes.StringType, true, nil
		case gotoken.FLOAT:
			return semaTypes.Float64Type, true, nil
		default:
			return semaTypes.IntType, true, nil
		}
	case *goast.ParenExpr:
		return tryGoExprType(facts, expr.X)
	case *goast.UnaryExpr:
		xType, ok, err := tryGoExprType(facts, expr.X)
		if err != nil || !ok {
			return nil, ok, err
		}
		switch expr.Op {
		case gotoken.NOT:
			return semaTypes.BoolType, true, nil
		case gotoken.AND:
			if _, ok := expr.X.(*goast.CompositeLit); ok {
				return &semaTypes.PointerType{Base: xType}, true, nil
			}
			if !isGoAddressableExpr(facts, expr.X) {
				return nil, false, fmt.Errorf("sema: expression %T is not addressable", expr.X)
			}
			return &semaTypes.PointerType{Base: xType}, true, nil
		case gotoken.MUL:
			ptr, ok := semaTypes.Underlying(xType).(*semaTypes.PointerType)
			if !ok || ptr == nil {
				return nil, false, nil
			}
			return ptr.Base, true, nil
		default:
			return xType, true, nil
		}
	case *goast.StarExpr:
		xType, ok, err := tryGoExprType(facts, expr.X)
		if err != nil || !ok {
			return nil, ok, err
		}
		ptr, ok := semaTypes.Underlying(xType).(*semaTypes.PointerType)
		if !ok || ptr == nil {
			return nil, false, nil
		}
		return ptr.Base, true, nil
	case *goast.BinaryExpr:
		left, lok, err := tryGoExprType(facts, expr.X)
		if err != nil {
			return nil, false, err
		}
		right, rok, err := tryGoExprType(facts, expr.Y)
		if err != nil {
			return nil, false, err
		}
		if !lok || !rok {
			return nil, false, nil
		}
		switch expr.Op {
		case gotoken.LAND, gotoken.LOR, gotoken.LSS, gotoken.LEQ, gotoken.GTR, gotoken.GEQ:
			return semaTypes.BoolType, true, nil
		case gotoken.EQL, gotoken.NEQ:
			if !comparableGoOperands(left, right, expr.X, expr.Y) {
				return nil, false, fmt.Errorf("sema: cannot compare %s with %s", left.String(), right.String())
			}
			return semaTypes.BoolType, true, nil
		case gotoken.ADD:
			if semaTypes.IsString(left) && semaTypes.IsString(right) {
				return semaTypes.StringType, true, nil
			}
			return commonGoNumericType(left, right), true, nil
		case gotoken.SUB, gotoken.MUL, gotoken.QUO:
			return commonGoNumericType(left, right), true, nil
		case gotoken.REM, gotoken.AND, gotoken.OR, gotoken.XOR, gotoken.AND_NOT:
			if !semaTypes.IsInteger(left) || !semaTypes.IsInteger(right) {
				return nil, false, fmt.Errorf("sema: operator requires integer operands, got %s and %s", left.String(), right.String())
			}
			return left, true, nil
		case gotoken.SHL, gotoken.SHR:
			if !semaTypes.IsInteger(left) || !semaTypes.IsInteger(right) {
				return nil, false, fmt.Errorf("sema: shift operands must be integers, got %s and %s", left.String(), right.String())
			}
			return left, true, nil
		default:
			return left, true, nil
		}
	case *goast.CompositeLit:
		if expr.Type == nil {
			return nil, false, nil
		}
		typ, err := goTypeFromFacts(facts, expr.Type)
		if err != nil {
			return nil, false, err
		}
		if err := validateGoCompositeLit(expr, typ, facts); err != nil {
			return nil, false, err
		}
		return typ, true, nil
	case *goast.SelectorExpr:
		baseType, ok, err := tryGoExprType(facts, expr.X)
		if err != nil || !ok {
			return nil, ok, err
		}
		if baseType == nil {
			if ident, isIdent := expr.X.(*goast.Ident); isIdent {
				if sym := facts.Bindings[ident]; sym != nil && sym.Kind == SymPackage {
					return tryGoPkgSelectorType(facts, expr, sym)
				}
			}
			return nil, false, nil
		}
		if methodType, ok := semaTypes.LookupMethod(baseType, expr.Sel.Name); ok {
			recordGoSelection(facts, expr, &Selection{Kind: SelectionMethod, Type: methodType})
			return methodType, true, nil
		}
		if methodType, ok := semaTypes.LookupMethod(&semaTypes.PointerType{Base: baseType}, expr.Sel.Name); ok {
			if !isGoAddressableExpr(facts, expr.X) {
				return nil, false, fmt.Errorf("sema: cannot call pointer receiver method on non-addressable value")
			}
			recordGoSelection(facts, expr, &Selection{Kind: SelectionMethod, Type: methodType, NeedsAddress: true})
			return methodType, true, nil
		}
		strct, needsDeref, ok := structTypeForSelector(baseType)
		if !ok || strct == nil {
			return nil, false, nil
		}
		for i, name := range strct.FieldNames {
			if name == expr.Sel.Name {
				recordGoSelection(facts, expr, &Selection{
					Kind:       SelectionField,
					Type:       strct.Fields[i],
					FieldIndex: i,
					NeedsDeref: needsDeref,
				})
				return strct.Fields[i], true, nil
			}
		}
		return nil, false, goastError(nil, expr.Sel.Pos(), "unknown field %q", expr.Sel.Name)
	case *goast.IndexExpr:
		baseType, ok, err := tryGoExprType(facts, expr.X)
		if err != nil || !ok {
			return nil, ok, err
		}
		indexType, ok, err := tryGoExprType(facts, expr.Index)
		if err != nil || !ok {
			return nil, ok, err
		}
		switch t := semaTypes.Underlying(baseType).(type) {
		case *semaTypes.ArrayType:
			if !semaTypes.IsInteger(indexType) {
				return nil, false, fmt.Errorf("sema: index must be integer, got %s", indexType.String())
			}
			return t.Elem, true, nil
		case *semaTypes.SliceType:
			if !semaTypes.IsInteger(indexType) {
				return nil, false, fmt.Errorf("sema: index must be integer, got %s", indexType.String())
			}
			return t.Elem, true, nil
		case *semaTypes.MapType:
			if !goAssignmentCompatible(expr.Index, indexType, t.Key, facts) {
				return nil, false, fmt.Errorf("sema: cannot index %s with %s", baseType.String(), indexType.String())
			}
			return t.Value, true, nil
		default:
			return nil, false, fmt.Errorf("sema: cannot index %s", baseType.String())
		}
	case *goast.SliceExpr:
		baseType, ok, err := tryGoExprType(facts, expr.X)
		if err != nil || !ok {
			return nil, ok, err
		}
		if expr.Low != nil {
			lowType, ok, err := tryGoExprType(facts, expr.Low)
			if err != nil || !ok {
				return nil, ok, err
			}
			if !semaTypes.IsInteger(lowType) {
				return nil, false, fmt.Errorf("sema: slice low bound must be integer, got %s", lowType.String())
			}
		}
		if expr.High != nil {
			highType, ok, err := tryGoExprType(facts, expr.High)
			if err != nil || !ok {
				return nil, ok, err
			}
			if !semaTypes.IsInteger(highType) {
				return nil, false, fmt.Errorf("sema: slice high bound must be integer, got %s", highType.String())
			}
		}
		if expr.Max != nil {
			maxType, ok, err := tryGoExprType(facts, expr.Max)
			if err != nil || !ok {
				return nil, ok, err
			}
			if !semaTypes.IsInteger(maxType) {
				return nil, false, fmt.Errorf("sema: slice max bound must be integer, got %s", maxType.String())
			}
		}
		switch t := semaTypes.Underlying(baseType).(type) {
		case *semaTypes.ArrayType:
			return &semaTypes.SliceType{Elem: t.Elem}, true, nil
		case *semaTypes.SliceType:
			return &semaTypes.SliceType{Elem: t.Elem}, true, nil
		case *semaTypes.BasicType:
			if semaTypes.Identical(t, semaTypes.StringType) && expr.Max == nil {
				return semaTypes.StringType, true, nil
			}
			if semaTypes.Identical(t, semaTypes.StringType) {
				return nil, false, fmt.Errorf("sema: cannot use full slice expression on string")
			}
			return nil, false, fmt.Errorf("sema: cannot slice %s", baseType.String())
		default:
			return nil, false, fmt.Errorf("sema: cannot slice %s", baseType.String())
		}
	case *goast.FuncLit:
		typ, err := goTypeFromFacts(facts, expr.Type)
		if err != nil {
			return nil, false, err
		}
		return typ, true, nil
	case *goast.CallExpr:
		if ident, ok := expr.Fun.(*goast.Ident); ok {
			if builtin := semaTypes.LookupBuiltin(ident.Name); builtin != nil {
				if len(expr.Args) != 1 {
					return nil, false, fmt.Errorf("sema: %s expects 1 argument, got %d", builtin.String(), len(expr.Args))
				}
				argType, ok, err := tryGoExprType(facts, expr.Args[0])
				if err != nil || !ok {
					return nil, ok, err
				}
				if !conversionAllowed(argType, builtin) {
					return nil, false, fmt.Errorf("sema: cannot convert %s to %s", argType.String(), builtin.String())
				}
				return builtin, true, nil
			}
			switch ident.Name {
			case "make":
				if len(expr.Args) < 1 {
					return nil, false, fmt.Errorf("sema: make expects at least 1 argument, got %d", len(expr.Args))
				}
				typ, err := goTypeFromFacts(facts, expr.Args[0])
				if err != nil {
					return nil, false, err
				}
				switch semaTypes.Underlying(typ).(type) {
				case *semaTypes.MapType:
					if len(expr.Args) != 1 {
						return nil, false, fmt.Errorf("sema: make map expects 1 argument, got %d", len(expr.Args))
					}
				case *semaTypes.SliceType:
					if len(expr.Args) != 2 && len(expr.Args) != 3 {
						return nil, false, fmt.Errorf("sema: make slice expects 2 or 3 arguments, got %d", len(expr.Args))
					}
					for _, arg := range expr.Args[1:] {
						argType, ok, err := tryGoExprType(facts, arg)
						if err != nil || !ok {
							return nil, ok, err
						}
						if !semaTypes.IsInteger(argType) {
							return nil, false, fmt.Errorf("sema: make slice length and capacity must be integer, got %s", argType.String())
						}
					}
				default:
					return nil, false, fmt.Errorf("sema: make only supports map and slice types, got %s", typ.String())
				}
				return typ, true, nil
			case "len":
				if len(expr.Args) != 1 {
					return nil, false, fmt.Errorf("sema: len expects 1 argument, got %d", len(expr.Args))
				}
				argType, ok, err := tryGoExprType(facts, expr.Args[0])
				if err != nil || !ok {
					return nil, ok, err
				}
				switch semaTypes.Underlying(argType).(type) {
				case *semaTypes.ArrayType, *semaTypes.SliceType, *semaTypes.MapType:
					return semaTypes.IntType, true, nil
				}
				if semaTypes.IsString(argType) {
					return semaTypes.IntType, true, nil
				}
				return nil, false, fmt.Errorf("sema: len does not support %s", argType.String())
			case "delete":
				if len(expr.Args) != 2 {
					return nil, false, fmt.Errorf("sema: delete expects 2 arguments, got %d", len(expr.Args))
				}
				mapType, ok, err := tryGoExprType(facts, expr.Args[0])
				if err != nil || !ok {
					return nil, ok, err
				}
				m, ok := semaTypes.Underlying(mapType).(*semaTypes.MapType)
				if !ok || m == nil {
					return nil, false, fmt.Errorf("sema: delete expects a map, got %s", mapType.String())
				}
				keyType, ok, err := tryGoExprType(facts, expr.Args[1])
				if err != nil || !ok {
					return nil, ok, err
				}
				if !goAssignmentCompatible(expr.Args[1], keyType, m.Key, facts) {
					return nil, false, fmt.Errorf("sema: cannot use %s as %s", keyType.String(), m.Key.String())
				}
				return semaTypes.IntType, true, nil
			case "clear":
				if len(expr.Args) != 1 {
					return nil, false, fmt.Errorf("sema: clear expects 1 argument, got %d", len(expr.Args))
				}
				argType, ok, err := tryGoExprType(facts, expr.Args[0])
				if err != nil || !ok {
					return nil, ok, err
				}
				switch semaTypes.Underlying(argType).(type) {
				case *semaTypes.MapType, *semaTypes.SliceType:
					return semaTypes.IntType, true, nil
				default:
					return nil, false, fmt.Errorf("sema: clear expects map or slice, got %s", argType.String())
				}
			case "append":
				return checkGoAppendCallType(facts, expr)
			}
			if facts != nil && facts.Bindings != nil {
				if sym := facts.Bindings[ident]; sym != nil && sym.Kind == SymType {
					if len(expr.Args) != 1 {
						return nil, false, fmt.Errorf("sema: %s expects 1 argument, got %d", sym.Type.String(), len(expr.Args))
					}
					argType, ok, err := tryGoExprType(facts, expr.Args[0])
					if err != nil || !ok {
						return nil, ok, err
					}
					if !conversionAllowed(argType, sym.Type) {
						return nil, false, fmt.Errorf("sema: cannot convert %s to %s", argType.String(), sym.Type.String())
					}
					return sym.Type, true, nil
				}
			}
		}
		funType, ok, err := tryGoExprType(facts, expr.Fun)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		if fn, ok := semaTypes.Underlying(funType).(*semaTypes.FuncType); ok && len(fn.Results) == 1 {
			if fn.Variadic {
				fixedCount := len(fn.Params) - 1
				if expr.Ellipsis.IsValid() {
					if len(expr.Args) != len(fn.Params) {
						return nil, false, fmt.Errorf("sema: argument count mismatch: got %d, want %d", len(expr.Args), len(fn.Params))
					}
				} else if len(expr.Args) < fixedCount {
					return nil, false, fmt.Errorf("sema: argument count mismatch: got %d, want at least %d", len(expr.Args), fixedCount)
				}
				for i := 0; i < fixedCount; i++ {
					argTypes, ok, err := goExprResultTypes(facts, expr.Args[i])
					if err != nil || !ok {
						return nil, ok, err
					}
					if len(argTypes) != 1 {
						return nil, false, fmt.Errorf("sema: multiple values in single-value context")
					}
					argType := argTypes[0]
					if !semaTypes.AssignableTo(argType, fn.Params[i]) && !goExprAssignableTo(expr.Args[i], fn.Params[i], facts) {
						return nil, false, fmt.Errorf("sema: cannot pass %s as %s", argType.String(), fn.Params[i].String())
					}
				}
				lastParam := fn.Params[len(fn.Params)-1]
				sliceType, _ := semaTypes.Underlying(lastParam).(*semaTypes.SliceType)
				if sliceType == nil {
					return nil, false, fmt.Errorf("sema: variadic parameter must be a slice type")
				}
				if expr.Ellipsis.IsValid() {
					argTypes, ok, err := goExprResultTypes(facts, expr.Args[len(expr.Args)-1])
					if err != nil || !ok {
						return nil, ok, err
					}
					if len(argTypes) != 1 {
						return nil, false, fmt.Errorf("sema: multiple values in single-value context")
					}
					argType := argTypes[0]
					if !semaTypes.AssignableTo(argType, lastParam) && !goExprAssignableTo(expr.Args[len(expr.Args)-1], lastParam, facts) {
						return nil, false, fmt.Errorf("sema: cannot pass %s as %s", argType.String(), lastParam.String())
					}
				} else {
					for i := fixedCount; i < len(expr.Args); i++ {
						argTypes, ok, err := goExprResultTypes(facts, expr.Args[i])
						if err != nil || !ok {
							return nil, ok, err
						}
						if len(argTypes) != 1 {
							return nil, false, fmt.Errorf("sema: multiple values in single-value context")
						}
						argType := argTypes[0]
						if !semaTypes.AssignableTo(argType, sliceType.Elem) && !goExprAssignableTo(expr.Args[i], sliceType.Elem, facts) {
							return nil, false, fmt.Errorf("sema: cannot pass %s as %s", argType.String(), sliceType.Elem.String())
						}
					}
				}
			} else {
				if len(expr.Args) != len(fn.Params) {
					return nil, false, fmt.Errorf("sema: argument count mismatch: got %d, want %d", len(expr.Args), len(fn.Params))
				}
				for i, arg := range expr.Args {
					argTypes, ok, err := goExprResultTypes(facts, arg)
					if err != nil || !ok {
						return nil, ok, err
					}
					if len(argTypes) != 1 {
						return nil, false, fmt.Errorf("sema: multiple values in single-value context")
					}
					argType := argTypes[0]
					if !semaTypes.AssignableTo(argType, fn.Params[i]) && !goExprAssignableTo(arg, fn.Params[i], facts) {
						return nil, false, fmt.Errorf("sema: cannot pass %s as %s", argType.String(), fn.Params[i].String())
					}
				}
			}
			return fn.Results[0], true, nil
		}
		return nil, false, nil
	default:
		return nil, false, nil
	}
}

func recordGoExprType(facts *Facts, expr goast.Expr, typ semaTypes.Type) {
	if facts == nil || expr == nil || typ == nil {
		return
	}
	facts.Types[expr] = typ
}

func recordGoConstExpr(facts *Facts, expr goast.Expr, constExpr goast.Expr) {
	if facts == nil || expr == nil || constExpr == nil {
		return
	}
	if existing := facts.ConstExprs[expr]; existing != nil {
		return
	}
	facts.ConstExprs[expr] = constExpr
}

func recordGoSelection(facts *Facts, expr *goast.SelectorExpr, selection *Selection) {
	if facts == nil || expr == nil || selection == nil {
		return
	}
	facts.Selections[expr] = selection
}

func checkGoAppendCallType(facts *Facts, call *goast.CallExpr) (semaTypes.Type, bool, error) {
	if len(call.Args) < 2 {
		return nil, false, fmt.Errorf("sema: append expects at least 2 arguments, got %d", len(call.Args))
	}
	targetType, ok, err := tryGoExprType(facts, call.Args[0])
	if err != nil || !ok {
		return nil, ok, err
	}
	sliceType, ok := semaTypes.Underlying(targetType).(*semaTypes.SliceType)
	if !ok || sliceType == nil {
		return nil, false, fmt.Errorf("sema: append first argument must be slice, got %s", targetType.String())
	}
	if call.Ellipsis.IsValid() {
		if len(call.Args) != 2 {
			return nil, false, fmt.Errorf("sema: append slice expansion expects exactly 2 arguments, got %d", len(call.Args))
		}
		argType, ok, err := tryGoExprType(facts, call.Args[1])
		if err != nil || !ok {
			return nil, ok, err
		}
		if !semaTypes.AssignableTo(argType, targetType) && !goExprAssignableTo(call.Args[1], targetType, facts) {
			return nil, false, fmt.Errorf("sema: cannot append %s as %s", argType.String(), targetType.String())
		}
		return targetType, true, nil
	}
	for _, arg := range call.Args[1:] {
		argType, ok, err := tryGoExprType(facts, arg)
		if err != nil || !ok {
			return nil, ok, err
		}
		if !semaTypes.AssignableTo(argType, sliceType.Elem) && !goExprAssignableTo(arg, sliceType.Elem, facts) {
			return nil, false, fmt.Errorf("sema: cannot append %s as %s", argType.String(), sliceType.Elem.String())
		}
	}
	return targetType, true, nil
}

func commonGoNumericType(a, b semaTypes.Type) semaTypes.Type {
	if semaTypes.Underlying(a).Kind() == semaTypes.Float64 || semaTypes.Underlying(b).Kind() == semaTypes.Float64 {
		return semaTypes.Float64Type
	}
	if semaTypes.Underlying(a).Kind() == semaTypes.Int64 || semaTypes.Underlying(b).Kind() == semaTypes.Int64 {
		return semaTypes.Int64Type
	}
	return semaTypes.IntType
}

func comparableGoOperands(left, right semaTypes.Type, leftExpr, rightExpr goast.Expr) bool {
	if semaTypes.IsComparable(left) && semaTypes.IsComparable(right) {
		return true
	}
	if isGoNilExpr(leftExpr) && semaTypes.IsNilable(right) {
		return true
	}
	if isGoNilExpr(rightExpr) && semaTypes.IsNilable(left) {
		return true
	}
	return false
}

func isGoNilExpr(expr goast.Expr) bool {
	ident, ok := expr.(*goast.Ident)
	return ok && ident.Name == "nil"
}

func isGoAddressableExpr(facts *Facts, expr goast.Expr) bool {
	switch expr := expr.(type) {
	case *goast.Ident:
		if expr.Name == "_" || facts == nil || facts.Bindings == nil {
			return false
		}
		sym := facts.Bindings[expr]
		return sym != nil && sym.Kind == SymVar
	case *goast.SelectorExpr:
		return isGoAddressableExpr(facts, expr.X)
	case *goast.IndexExpr:
		return isGoAddressableExpr(facts, expr.X)
	case *goast.StarExpr:
		return true
	default:
		return false
	}
}

func goExprAssignableTo(expr goast.Expr, dst semaTypes.Type, facts *Facts) bool {
	switch expr := expr.(type) {
	case *goast.BasicLit:
		switch expr.Kind {
		case gotoken.STRING:
			return semaTypes.IsString(dst)
		case gotoken.FLOAT:
			return semaTypes.Identical(dst, semaTypes.Float64Type) || semaTypes.IsNumeric(dst)
		default:
			return semaTypes.IsNumeric(dst)
		}
	case *goast.Ident:
		switch expr.Name {
		case "true", "false":
			return semaTypes.IsBoolean(dst)
		case "nil":
			return semaTypes.IsNilable(dst)
		}
	case *goast.UnaryExpr, *goast.BinaryExpr:
		if semaTypes.IsNumeric(dst) {
			_, err := goConstArrayLenFromFacts(facts, expr)
			return err == nil
		}
	case *goast.CallExpr:
		if ident, ok := expr.Fun.(*goast.Ident); ok {
			if builtin := semaTypes.LookupBuiltin(ident.Name); builtin != nil {
				return semaTypes.AssignableTo(builtin, dst)
			}
			if facts != nil && facts.Bindings != nil {
				if sym := facts.Bindings[ident]; sym != nil && sym.Kind == SymType {
					return semaTypes.AssignableTo(sym.Type, dst)
				}
			}
		}
	}
	return false
}

func goAssignmentCompatible(expr goast.Expr, src, dst semaTypes.Type, facts *Facts) bool {
	return semaTypes.AssignableTo(src, dst) || goExprAssignableTo(expr, dst, facts)
}

func goSwitchCaseCompatible(expr goast.Expr, caseType, tagType semaTypes.Type, facts *Facts) bool {
	return semaTypes.AssignableTo(caseType, tagType) ||
		semaTypes.AssignableTo(tagType, caseType) ||
		goExprAssignableTo(expr, tagType, facts)
}

func validateGoCompositeLit(lit *goast.CompositeLit, litType semaTypes.Type, facts *Facts) error {
	switch t := semaTypes.Underlying(litType).(type) {
	case *semaTypes.SliceType:
		for _, elt := range lit.Elts {
			if _, ok := elt.(*goast.KeyValueExpr); ok {
				return fmt.Errorf("sema: keyed slice literals are not supported")
			}
		}
		for i, elt := range lit.Elts {
			if nested, ok := elt.(*goast.CompositeLit); ok && nested.Type == nil {
				if err := validateGoCompositeLit(nested, t.Elem, facts); err != nil {
					return err
				}
				continue
			}
			eltType, ok, err := tryGoExprType(facts, elt)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if !semaTypes.AssignableTo(eltType, t.Elem) && !goExprAssignableTo(elt, t.Elem, facts) {
				return fmt.Errorf("sema: composite literal element %d cannot use %s as %s", i, eltType.String(), t.Elem.String())
			}
		}
	case *semaTypes.ArrayType:
		for _, elt := range lit.Elts {
			if _, ok := elt.(*goast.KeyValueExpr); ok {
				return fmt.Errorf("sema: keyed array literals are not supported")
			}
		}
		if len(lit.Elts) != t.Len {
			return fmt.Errorf("sema: composite literal has %d elements for %s", len(lit.Elts), litType.String())
		}
		for i, elt := range lit.Elts {
			if nested, ok := elt.(*goast.CompositeLit); ok && nested.Type == nil {
				if err := validateGoCompositeLit(nested, t.Elem, facts); err != nil {
					return err
				}
				continue
			}
			eltType, ok, err := tryGoExprType(facts, elt)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if !semaTypes.AssignableTo(eltType, t.Elem) && !goExprAssignableTo(elt, t.Elem, facts) {
				return fmt.Errorf("sema: composite literal element %d cannot use %s as %s", i, eltType.String(), t.Elem.String())
			}
		}
	case *semaTypes.StructType:
		hasKeyed := false
		hasPositional := false
		for _, elt := range lit.Elts {
			if _, ok := elt.(*goast.KeyValueExpr); ok {
				hasKeyed = true
			} else {
				hasPositional = true
			}
		}
		if hasKeyed && hasPositional {
			return fmt.Errorf("sema: keyed and positional struct literal elements cannot be mixed")
		}
		if hasKeyed {
			seen := make(map[string]struct{}, len(lit.Elts))
			for _, elt := range lit.Elts {
				kv, ok := elt.(*goast.KeyValueExpr)
				if !ok {
					return fmt.Errorf("sema: keyed and positional struct literal elements cannot be mixed")
				}
				key, ok := kv.Key.(*goast.Ident)
				if !ok {
					return fmt.Errorf("sema: struct literal key must be a field name")
				}
				if _, exists := seen[key.Name]; exists {
					return fmt.Errorf("sema: duplicate field %q in struct literal", key.Name)
				}
				fieldIndex := -1
				for i, fieldName := range t.FieldNames {
					if fieldName == key.Name {
						fieldIndex = i
						break
					}
				}
				if fieldIndex < 0 {
					return fmt.Errorf("sema: unknown field %q in struct literal", key.Name)
				}
				valueType, ok, err := tryGoExprType(facts, kv.Value)
				if err != nil {
					return err
				}
				if ok && !semaTypes.AssignableTo(valueType, t.Fields[fieldIndex]) && !goExprAssignableTo(kv.Value, t.Fields[fieldIndex], facts) {
					return fmt.Errorf("sema: struct literal field %s cannot use %s as %s", key.Name, valueType.String(), t.Fields[fieldIndex].String())
				}
				seen[key.Name] = struct{}{}
			}
		} else {
			if len(lit.Elts) != len(t.Fields) {
				return fmt.Errorf("sema: composite literal has %d elements for %s", len(lit.Elts), litType.String())
			}
			for i, elt := range lit.Elts {
				if nested, ok := elt.(*goast.CompositeLit); ok && nested.Type == nil {
					if err := validateGoCompositeLit(nested, t.Fields[i], facts); err != nil {
						return err
					}
					continue
				}
				eltType, ok, err := tryGoExprType(facts, elt)
				if err != nil {
					return err
				}
				if ok && !semaTypes.AssignableTo(eltType, t.Fields[i]) && !goExprAssignableTo(elt, t.Fields[i], facts) {
					return fmt.Errorf("sema: composite literal element %d cannot use %s as %s", i, eltType.String(), t.Fields[i].String())
				}
			}
		}
	}
	return nil
}
