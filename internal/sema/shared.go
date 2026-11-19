package sema

import (
	"fmt"
	goast "go/ast"
	gotoken "go/token"

	"goes/runtime"
)

type CheckOptions struct {
	HostFunctions    map[string]runtime.TypedHostSignature
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
	hostFunctions    map[string]runtime.TypedHostSignature
	hostTypes        map[string]runtime.ScriptType
	hostConsts       map[string]hostConstBinding
	importedPackages map[string]*ImportedPackage
}

type hostConstBinding struct {
	typ  runtime.ScriptType
	expr goast.Expr
}

type ResolveOptions struct {
	HostFunctions    map[string]runtime.TypedHostSignature
	HostRegistry     *runtime.HostRegistry
	ImportedPackages map[string]*ImportedPackage
	PkgPath          string
}

func hostStructTypesFromRegistry(registry *runtime.HostRegistry) map[string]runtime.ScriptType {
	if registry == nil {
		return nil
	}
	schemas := registry.Types()
	if len(schemas) == 0 {
		return nil
	}
	result := make(map[string]runtime.ScriptType, len(schemas))
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

func namedTypeFromSchema(schema runtime.StructSchema) runtime.ScriptType {
	fields := make([]runtime.ScriptType, len(schema.FieldKinds))
	for i, kind := range schema.FieldKinds {
		fields[i] = typeForRuntimeKind(kind)
	}
	return &runtime.NamedType{
		Name: schema.Name,
		Underlying: &runtime.StructType{
			FieldNames: append([]string(nil), schema.FieldNames...),
			Fields:     fields,
		},
	}
}

func typeForRuntimeKind(kind runtime.Kind) runtime.ScriptType {
	switch kind {
	case runtime.Bool:
		return runtime.BuiltinBoolType
	case runtime.Int:
		return runtime.BuiltinIntType
	case runtime.Int64:
		return runtime.BuiltinInt64Type
	case runtime.Float64:
		return runtime.BuiltinFloat64Type
	case runtime.String:
		return runtime.BuiltinStringType
	default:
		return runtime.BuiltinInvalidType
	}
}

func semaTypeForHostConst(typ runtime.Type) runtime.ScriptType {
	switch typ {
	case runtime.BoolType:
		return runtime.BuiltinBoolType
	case runtime.IntType:
		return runtime.BuiltinIntType
	case runtime.Int64Type:
		return runtime.BuiltinInt64Type
	case runtime.Float64Type:
		return runtime.BuiltinFloat64Type
	case runtime.StringType:
		return runtime.BuiltinStringType
	default:
		return runtime.BuiltinInvalidType
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

func structTypeForSelector(typ runtime.ScriptType) (*runtime.StructType, bool, bool) {
	switch t := runtime.Underlying(typ).(type) {
	case *runtime.StructType:
		return t, false, true
	case *runtime.PointerType:
		strct, ok := runtime.Underlying(t.Base).(*runtime.StructType)
		return strct, true, ok && strct != nil
	default:
		return nil, false, false
	}
}

func conversionAllowed(src, dst runtime.ScriptType) bool {
	if runtime.IsInvalid(src) || runtime.IsInvalid(dst) {
		return false
	}
	if runtime.Identical(src, dst) || runtime.AssignableTo(src, dst) {
		return true
	}
	if runtime.IsNumeric(src) && runtime.IsNumeric(dst) {
		return true
	}
	if runtime.IsNumeric(src) && runtime.IsString(dst) {
		return true
	}
	if runtime.IsString(src) && runtime.IsNumeric(dst) {
		return true
	}
	return runtime.Identical(runtime.Underlying(src), runtime.Underlying(dst))
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
