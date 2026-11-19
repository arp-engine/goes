package types

var (
	InvalidType = &BasicType{kind: Invalid, name: "invalid"}
	NilType     = &BasicType{kind: Nil, name: "nil"}
	BoolType    = &BasicType{kind: Bool, name: "bool"}
	IntType     = &BasicType{kind: Int, name: "int"}
	Int64Type   = &BasicType{kind: Int64, name: "int64"}
	Float64Type = &BasicType{kind: Float64, name: "float64"}
	StringType  = &BasicType{kind: String, name: "string"}
)

var Builtins = map[string]Type{
	"bool":    BoolType,
	"int":     IntType,
	"int64":   Int64Type,
	"float64": Float64Type,
	"string":  StringType,
}

func LookupBuiltin(name string) Type {
	return Builtins[name]
}
