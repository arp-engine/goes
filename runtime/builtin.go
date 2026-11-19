package runtime

var (
	BuiltinInvalidType = &BasicType{kind: TypeKindInvalid, name: "invalid"}
	BuiltinNilType     = &BasicType{kind: TypeKindNil, name: "nil"}
	BuiltinBoolType    = &BasicType{kind: TypeKindBool, name: "bool"}
	BuiltinIntType     = &BasicType{kind: TypeKindInt, name: "int"}
	BuiltinInt64Type   = &BasicType{kind: TypeKindInt64, name: "int64"}
	BuiltinFloat64Type = &BasicType{kind: TypeKindFloat64, name: "float64"}
	BuiltinStringType  = &BasicType{kind: TypeKindString, name: "string"}
)

var Builtins = map[string]ScriptType{
	"bool":    BuiltinBoolType,
	"int":     BuiltinIntType,
	"int64":   BuiltinInt64Type,
	"float64": BuiltinFloat64Type,
	"string":  BuiltinStringType,
}

func LookupBuiltin(name string) ScriptType {
	return Builtins[name]
}
