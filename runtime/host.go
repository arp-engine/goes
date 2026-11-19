package runtime

import (
	"fmt"
	"reflect"
)

type HostFunc func(ctx *HostContext, args []Value) ([]Value, error)

type HostContext struct {
	Env any
}

type HostSignature struct {
	ParamKinds  []Kind
	ResultKinds []Kind
	Variadic    bool
}

type HostRegistry struct {
	funcs      map[string]HostFunc
	signatures map[string]HostSignature
	types      map[string]StructSchema
	consts     map[string]ConstValue
}

type ConstValue struct {
	Type Type
	Go   any
}

type Type int

const (
	InvalidType Type = iota
	BoolType
	IntType
	Int64Type
	Float64Type
	StringType
)

func NewHostRegistry() *HostRegistry {
	return &HostRegistry{
		funcs:      make(map[string]HostFunc),
		signatures: make(map[string]HostSignature),
		types:      make(map[string]StructSchema),
		consts:     make(map[string]ConstValue),
	}
}

func (r *HostRegistry) Register(name string, fn any) error {
	wrapped, paramKinds, resultKinds, variadic, err := WrapHostFunction(fn)
	if err != nil {
		return fmt.Errorf("runtime: host function %q: %w", name, err)
	}
	var signature *HostSignature
	if paramKinds != nil {
		sig := HostSignature{
			ParamKinds:  append([]Kind(nil), paramKinds...),
			ResultKinds: append([]Kind(nil), resultKinds...),
			Variadic:    variadic,
		}
		signature = &sig
	}
	if signature != nil {
		if err := r.Declare(name, derefHostSignature(signature)); err != nil {
			return err
		}
	}
	return r.registerHostFunc(name, wrapped)
}

func (r *HostRegistry) RegisterWithSignature(name string, signature HostSignature, fn HostFunc) error {
	if err := r.Declare(name, signature); err != nil {
		return err
	}
	return r.registerHostFunc(name, fn)
}

func (r *HostRegistry) Declare(name string, signature HostSignature) error {
	if r == nil {
		return fmt.Errorf("runtime: missing host registry")
	}
	if name == "" {
		return fmt.Errorf("runtime: host function name is empty")
	}
	if r.signatures == nil {
		r.signatures = make(map[string]HostSignature)
	}
	if existing, ok := r.signatures[name]; ok {
		if !equalHostSignature(existing, signature) {
			return fmt.Errorf("runtime: conflicting host signature for %q", name)
		}
		return nil
	}
	r.signatures[name] = HostSignature{
		ParamKinds:  append([]Kind(nil), signature.ParamKinds...),
		ResultKinds: append([]Kind(nil), signature.ResultKinds...),
		Variadic:    signature.Variadic,
	}
	return nil
}

func (r *HostRegistry) registerHostFunc(name string, fn HostFunc) error {
	if r == nil {
		return fmt.Errorf("runtime: missing host registry")
	}
	if name == "" {
		return fmt.Errorf("runtime: host function name is empty")
	}
	if fn == nil {
		return fmt.Errorf("runtime: host function %q is nil", name)
	}
	if r.funcs == nil {
		r.funcs = make(map[string]HostFunc)
	}
	if _, exists := r.funcs[name]; exists {
		return fmt.Errorf("runtime: duplicate host function %q", name)
	}
	r.funcs[name] = fn
	return nil
}

func (r *HostRegistry) MustRegister(name string, fn any) {
	if err := r.Register(name, fn); err != nil {
		panic(err)
	}
}

func (r *HostRegistry) Lookup(name string) (HostFunc, bool) {
	if r == nil || r.funcs == nil {
		return nil, false
	}
	fn, ok := r.funcs[name]
	return fn, ok
}

func (r *HostRegistry) Signatures() map[string]HostSignature {
	if r == nil || len(r.signatures) == 0 {
		return nil
	}
	result := make(map[string]HostSignature, len(r.signatures))
	for name, sig := range r.signatures {
		result[name] = HostSignature{
			ParamKinds:  append([]Kind(nil), sig.ParamKinds...),
			ResultKinds: append([]Kind(nil), sig.ResultKinds...),
			Variadic:    sig.Variadic,
		}
	}
	return result
}

func (r *HostRegistry) RegisterType(v any) error {
	schema, err := StructSchemaFromGoType(v)
	if err != nil {
		return err
	}
	return r.DeclareType(schema)
}

func (r *HostRegistry) MustRegisterType(v any) {
	if err := r.RegisterType(v); err != nil {
		panic(err)
	}
}

func (r *HostRegistry) DeclareType(schema StructSchema) error {
	if r == nil {
		return fmt.Errorf("runtime: missing host registry")
	}
	if schema.Name == "" {
		return fmt.Errorf("runtime: host type name is empty")
	}
	if r.types == nil {
		r.types = make(map[string]StructSchema)
	}
	if existing, ok := r.types[schema.Name]; ok {
		if !equalStructSchema(existing, schema) {
			return fmt.Errorf("runtime: conflicting host type %q", schema.Name)
		}
		return nil
	}
	r.types[schema.Name] = cloneStructSchema(schema)
	return nil
}

func (r *HostRegistry) Types() map[string]StructSchema {
	if r == nil || len(r.types) == 0 {
		return nil
	}
	result := make(map[string]StructSchema, len(r.types))
	for name, schema := range r.types {
		result[name] = cloneStructSchema(schema)
	}
	return result
}

func (r *HostRegistry) RegisterConst(name string, value any) error {
	typed, err := hostConstValue(value)
	if err != nil {
		return fmt.Errorf("runtime: host const %q: %w", name, err)
	}
	return r.DeclareConst(name, typed)
}

func (r *HostRegistry) MustRegisterConst(name string, value any) {
	if err := r.RegisterConst(name, value); err != nil {
		panic(err)
	}
}

func (r *HostRegistry) DeclareConst(name string, value ConstValue) error {
	if r == nil {
		return fmt.Errorf("runtime: missing host registry")
	}
	if name == "" {
		return fmt.Errorf("runtime: host const name is empty")
	}
	if value.Type == InvalidType {
		return fmt.Errorf("runtime: host const %q has invalid type", name)
	}
	if r.consts == nil {
		r.consts = make(map[string]ConstValue)
	}
	if existing, ok := r.consts[name]; ok {
		if !equalConstValue(existing, value) {
			return fmt.Errorf("runtime: conflicting host const %q", name)
		}
		return nil
	}
	r.consts[name] = value
	return nil
}

func (r *HostRegistry) Consts() map[string]ConstValue {
	if r == nil || len(r.consts) == 0 {
		return nil
	}
	result := make(map[string]ConstValue, len(r.consts))
	for name, value := range r.consts {
		result[name] = value
	}
	return result
}

func StructSchemaFromGoType(v any) (StructSchema, error) {
	if v == nil {
		return StructSchema{}, fmt.Errorf("runtime: host type is nil")
	}
	rt := reflect.TypeOf(v)
	for rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt.Kind() != reflect.Struct || rt.Name() == "" {
		return StructSchema{}, fmt.Errorf("runtime: host type must be a named struct, got %s", rt.String())
	}
	schema := StructSchema{
		Name:       rt.Name(),
		FieldNames: make([]string, 0, rt.NumField()),
		FieldKinds: make([]Kind, 0, rt.NumField()),
	}
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if !field.IsExported() {
			return StructSchema{}, fmt.Errorf("runtime: field %s on %s must be exported", field.Name, rt.Name())
		}
		kind := runtimeKindFromReflect(field.Type)
		if kind == Invalid {
			return StructSchema{}, fmt.Errorf("runtime: unsupported field type %s on %s.%s", field.Type.String(), rt.Name(), field.Name)
		}
		schema.FieldNames = append(schema.FieldNames, field.Name)
		schema.FieldKinds = append(schema.FieldKinds, kind)
	}
	return schema, nil
}

func derefHostSignature(signature *HostSignature) HostSignature {
	if signature == nil {
		return HostSignature{}
	}
	return HostSignature{
		ParamKinds:  append([]Kind(nil), signature.ParamKinds...),
		ResultKinds: append([]Kind(nil), signature.ResultKinds...),
		Variadic:    signature.Variadic,
	}
}

func equalHostSignature(a, b HostSignature) bool {
	if a.Variadic != b.Variadic || len(a.ParamKinds) != len(b.ParamKinds) || len(a.ResultKinds) != len(b.ResultKinds) {
		return false
	}
	for i := range a.ParamKinds {
		if a.ParamKinds[i] != b.ParamKinds[i] {
			return false
		}
	}
	for i := range a.ResultKinds {
		if a.ResultKinds[i] != b.ResultKinds[i] {
			return false
		}
	}
	return true
}

func cloneStructSchema(schema StructSchema) StructSchema {
	return StructSchema{
		Name:       schema.Name,
		FieldNames: append([]string(nil), schema.FieldNames...),
		FieldKinds: append([]Kind(nil), schema.FieldKinds...),
	}
}

func equalStructSchema(a, b StructSchema) bool {
	if a.Name != b.Name || len(a.FieldNames) != len(b.FieldNames) || len(a.FieldKinds) != len(b.FieldKinds) {
		return false
	}
	for i := range a.FieldNames {
		if a.FieldNames[i] != b.FieldNames[i] || a.FieldKinds[i] != b.FieldKinds[i] {
			return false
		}
	}
	return true
}

func hostConstValue(value any) (ConstValue, error) {
	switch v := value.(type) {
	case bool:
		return ConstValue{Type: BoolType, Go: v}, nil
	case int:
		return ConstValue{Type: IntType, Go: v}, nil
	case int64:
		return ConstValue{Type: Int64Type, Go: v}, nil
	case float64:
		return ConstValue{Type: Float64Type, Go: v}, nil
	case string:
		return ConstValue{Type: StringType, Go: v}, nil
	default:
		return ConstValue{}, fmt.Errorf("unsupported const type %T", value)
	}
}

func equalConstValue(a, b ConstValue) bool {
	if a.Type != b.Type {
		return false
	}
	return a.Go == b.Go
}

func WrapHostFunction(fn any) (HostFunc, []Kind, []Kind, bool, error) {
	if fn == nil {
		return nil, nil, nil, false, fmt.Errorf("host function is nil")
	}
	if hostFn, ok := fn.(HostFunc); ok {
		return hostFn, nil, nil, false, nil
	}

	rv := reflect.ValueOf(fn)
	rt := rv.Type()
	paramKinds, resultKinds, variadic, hasError, err := inferHostFunctionSignature(rt)
	if err != nil {
		return nil, nil, nil, false, err
	}

	wrapped := func(ctx *HostContext, args []Value) ([]Value, error) {
		if !variadic && len(args) != len(paramKinds) {
			return nil, fmt.Errorf("runtime: host function expected %d args, got %d", len(paramKinds), len(args))
		}
		if variadic && len(args) < len(paramKinds)-1 {
			return nil, fmt.Errorf("runtime: host function expected at least %d args, got %d", len(paramKinds)-1, len(args))
		}
		callArgs := make([]reflect.Value, 0, rt.NumIn())
		if ctx == nil {
			callArgs = append(callArgs, reflect.Zero(rt.In(0)))
		} else {
			callArgs = append(callArgs, reflect.ValueOf(ctx))
		}
		if variadic {
			fixedCount := rt.NumIn() - 2
			for i := 1; i <= fixedCount; i++ {
				converted, err := toReflectValue(args[i-1], rt.In(i), nil)
				if err != nil {
					return nil, fmt.Errorf("runtime: arg %d: %w", i-1, err)
				}
				callArgs = append(callArgs, converted)
			}
			variadicType := rt.In(rt.NumIn() - 1)
			if len(args) != fixedCount+1 {
				return nil, fmt.Errorf("runtime: host variadic function expected %d normalized args, got %d", fixedCount+1, len(args))
			}
			variadicSlice, err := toReflectValue(args[fixedCount], variadicType, nil)
			if err != nil {
				return nil, fmt.Errorf("runtime: arg %d: %w", fixedCount, err)
			}
			callArgs = append(callArgs, variadicSlice)
		} else {
			for i := 1; i < rt.NumIn(); i++ {
				converted, err := toReflectValue(args[i-1], rt.In(i), nil)
				if err != nil {
					return nil, fmt.Errorf("runtime: arg %d: %w", i-1, err)
				}
				callArgs = append(callArgs, converted)
			}
		}
		var results []reflect.Value
		if variadic {
			results = rv.CallSlice(callArgs)
		} else {
			results = rv.Call(callArgs)
		}
		if hasError && !results[len(results)-1].IsNil() {
			return nil, results[len(results)-1].Interface().(error)
		}
		valueCount := len(results)
		if hasError {
			valueCount--
		}
		values := make([]Value, valueCount)
		for i := 0; i < valueCount; i++ {
			value, err := FromGoValue(results[i].Interface(), nil)
			if err != nil {
				return nil, err
			}
			values[i] = value
		}
		return values, nil
	}

	return wrapped, paramKinds, resultKinds, variadic, nil
}

func InferHostFunctionSignature(fn any) ([]Kind, []Kind, bool, error) {
	if fn == nil {
		return nil, nil, false, fmt.Errorf("host function is nil")
	}
	if _, ok := fn.(HostFunc); ok {
		return nil, nil, false, fmt.Errorf("HostFunc requires explicit signature")
	}
	paramKinds, resultKinds, variadic, _, err := inferHostFunctionSignature(reflect.TypeOf(fn))
	return paramKinds, resultKinds, variadic, err
}

func inferHostFunctionSignature(rt reflect.Type) ([]Kind, []Kind, bool, bool, error) {
	if rt.Kind() != reflect.Func {
		return nil, nil, false, false, fmt.Errorf("must be a function, got %s", rt)
	}
	if rt.NumIn() == 0 || rt.In(0) != reflect.TypeOf((*HostContext)(nil)) {
		return nil, nil, false, false, fmt.Errorf("first parameter must be *runtime.HostContext")
	}

	variadic := rt.IsVariadic()
	paramKinds := make([]Kind, rt.NumIn()-1)
	for i := 1; i < rt.NumIn(); i++ {
		paramType := rt.In(i)
		if variadic && i == rt.NumIn()-1 {
			paramType = paramType.Elem()
		}
		kind := runtimeKindFromReflect(paramType)
		if kind == Invalid {
			return nil, nil, false, false, fmt.Errorf("parameter %d has unsupported type %s", i, paramType)
		}
		paramKinds[i-1] = kind
	}

	resultKinds, hasError, err := hostResultKinds(rt)
	if err != nil {
		return nil, nil, false, false, err
	}
	return paramKinds, resultKinds, variadic, hasError, nil
}

func hostResultKinds(rt reflect.Type) ([]Kind, bool, error) {
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	if rt.NumOut() == 0 {
		return nil, false, fmt.Errorf("host function must return at least one value")
	}
	hasError := false
	valueCount := rt.NumOut()
	if rt.Out(rt.NumOut()-1).Implements(errorType) {
		hasError = true
		valueCount--
	}
	if valueCount == 0 {
		return nil, false, fmt.Errorf("host function must return a value before error")
	}
	resultKinds := make([]Kind, valueCount)
	for i := 0; i < valueCount; i++ {
		kind := runtimeKindFromReflect(rt.Out(i))
		if kind == Invalid {
			return nil, false, fmt.Errorf("result has unsupported type %s", rt.Out(i))
		}
		resultKinds[i] = kind
	}
	if hasError && !rt.Out(rt.NumOut()-1).Implements(errorType) {
		return nil, false, fmt.Errorf("last result must be error")
	}
	return resultKinds, hasError, nil
}
