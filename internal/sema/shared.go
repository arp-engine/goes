package sema

import (
	"fmt"
	goast "go/ast"
	gotoken "go/token"

	"goes/runtime"
	semaTypes "goes/types"
)

type CheckOptions struct {
	HostFunctions    map[string]semaTypes.HostSignature
	HostRegistry     *runtime.HostRegistry
	ImportedPackages map[string]*ImportedPackage
	PkgPath          string
}

type ImportedPackage struct {
	PkgPath string
	Scope   *Scope
}

type scopeKind int

const (
	scopePackage scopeKind = iota
	scopeFunc
	scopeBlock
)

type funcFrame struct {
	parent *funcFrame
}

type resolver struct {
	info             *Info
	constValues      map[*Symbol]any
	pkgPath          string
	packageName      string
	scopeKinds       map[*Scope]scopeKind
	scopeFuncs       map[*Scope]*funcFrame
	currentFunc      *funcFrame
	hostFunctions    map[string]semaTypes.HostSignature
	hostTypes        map[string]semaTypes.Type
	hostConsts       map[string]hostConstBinding
	importedPackages map[string]*ImportedPackage
}

type hostConstBinding struct {
	typ  semaTypes.Type
	expr goast.Expr
}

type ResolveOptions struct {
	HostFunctions    map[string]semaTypes.HostSignature
	HostRegistry     *runtime.HostRegistry
	ImportedPackages map[string]*ImportedPackage
	PkgPath          string
}

func hostStructTypesFromRegistry(registry *runtime.HostRegistry) map[string]semaTypes.Type {
	if registry == nil {
		return nil
	}
	schemas := registry.Types()
	if len(schemas) == 0 {
		return nil
	}
	result := make(map[string]semaTypes.Type, len(schemas))
	for name, schema := range schemas {
		result[name] = namedTypeFromSchema(schema)
	}
	return result
}

func hostConstsFromRegistry(registry *runtime.HostRegistry) map[string]hostConstBinding {
	if registry == nil {
		return nil
	}
	consts := registry.Consts()
	if len(consts) == 0 {
		return nil
	}
	result := make(map[string]hostConstBinding, len(consts))
	for name, value := range consts {
		result[name] = hostConstBinding{
			typ:  semaTypeForHostConst(value.Type),
			expr: hostConstExpr(value),
		}
	}
	return result
}

func namedTypeFromSchema(schema runtime.StructSchema) semaTypes.Type {
	fields := make([]semaTypes.Type, len(schema.FieldKinds))
	for i, kind := range schema.FieldKinds {
		fields[i] = typeForRuntimeKind(kind)
	}
	return &semaTypes.NamedType{
		Name: schema.Name,
		Underlying: &semaTypes.StructType{
			FieldNames: append([]string(nil), schema.FieldNames...),
			Fields:     fields,
		},
	}
}

func typeForRuntimeKind(kind runtime.Kind) semaTypes.Type {
	switch kind {
	case runtime.Bool:
		return semaTypes.BoolType
	case runtime.Int:
		return semaTypes.IntType
	case runtime.Int64:
		return semaTypes.Int64Type
	case runtime.Float64:
		return semaTypes.Float64Type
	case runtime.String:
		return semaTypes.StringType
	default:
		return semaTypes.InvalidType
	}
}

func semaTypeForHostConst(typ runtime.Type) semaTypes.Type {
	switch typ {
	case runtime.BoolType:
		return semaTypes.BoolType
	case runtime.IntType:
		return semaTypes.IntType
	case runtime.Int64Type:
		return semaTypes.Int64Type
	case runtime.Float64Type:
		return semaTypes.Float64Type
	case runtime.StringType:
		return semaTypes.StringType
	default:
		return semaTypes.InvalidType
	}
}

func hostConstExpr(value runtime.ConstValue) goast.Expr {
	switch value.Type {
	case runtime.BoolType:
		if value.Go.(bool) {
			return &goast.Ident{Name: "true"}
		}
		return &goast.Ident{Name: "false"}
	case runtime.IntType:
		return &goast.BasicLit{Kind: gotoken.INT, Value: fmt.Sprintf("%d", value.Go.(int))}
	case runtime.Int64Type:
		return &goast.BasicLit{Kind: gotoken.INT, Value: fmt.Sprintf("%d", value.Go.(int64))}
	case runtime.Float64Type:
		return &goast.BasicLit{Kind: gotoken.FLOAT, Value: fmt.Sprintf("%g", value.Go.(float64))}
	case runtime.StringType:
		return &goast.BasicLit{Kind: gotoken.STRING, Value: fmt.Sprintf("%q", value.Go.(string))}
	default:
		return &goast.Ident{Name: "nil"}
	}
}

func structTypeForSelector(typ semaTypes.Type) (*semaTypes.StructType, bool, bool) {
	switch t := semaTypes.Underlying(typ).(type) {
	case *semaTypes.StructType:
		return t, false, true
	case *semaTypes.PointerType:
		strct, ok := semaTypes.Underlying(t.Base).(*semaTypes.StructType)
		return strct, true, ok && strct != nil
	default:
		return nil, false, false
	}
}

func conversionAllowed(src, dst semaTypes.Type) bool {
	if semaTypes.IsInvalid(src) || semaTypes.IsInvalid(dst) {
		return false
	}
	if semaTypes.Identical(src, dst) || semaTypes.AssignableTo(src, dst) {
		return true
	}
	if semaTypes.IsNumeric(src) && semaTypes.IsNumeric(dst) {
		return true
	}
	if semaTypes.IsNumeric(src) && semaTypes.IsString(dst) {
		return true
	}
	if semaTypes.IsString(src) && semaTypes.IsNumeric(dst) {
		return true
	}
	return semaTypes.Identical(semaTypes.Underlying(src), semaTypes.Underlying(dst))
}

func (r *resolver) declare(scope *Scope, sym *Symbol) error {
	if sym == nil || sym.Name == "_" {
		return nil
	}
	if !scope.Insert(sym) {
		return fmt.Errorf("sema: duplicate declaration of %q", sym.Name)
	}
	return nil
}

func (r *resolver) shouldCapture(symScope *Scope) bool {
	if r.currentFunc == nil || symScope == nil {
		return false
	}
	if r.scopeKinds[symScope] == scopePackage {
		return false
	}
	return r.scopeFuncs[symScope] != r.currentFunc
}

func isBuiltinValueName(name string) bool {
	switch name {
	case "len", "make", "delete", "append", "clear":
		return true
	default:
		return false
	}
}
