package runtime

import (
	"strconv"
	"strings"
)

type TypeKind int

const (
	TypeKindInvalid TypeKind = iota
	TypeKindNil
	TypeKindBool
	TypeKindInt
	TypeKindInt64
	TypeKindFloat64
	TypeKindString
	TypeKindArray
	TypeKindSlice
	TypeKindMap
	TypeKindStruct
	TypeKindPointer
	TypeKindFunc
	TypeKindNamed
)

type ScriptType interface {
	Kind() TypeKind
	String() string
}

type Method struct {
	Name        string
	Type        ScriptType
	PointerRecv bool
}

type TypedHostSignature struct {
	Params   []ScriptType
	Results  []ScriptType
	Variadic bool
}

type BasicType struct {
	kind TypeKind
	name string
}

func (t *BasicType) Kind() TypeKind {
	return t.kind
}

func (t *BasicType) String() string {
	return t.name
}

type ArrayType struct {
	Len  int
	Elem ScriptType
}

func (*ArrayType) Kind() TypeKind {
	return TypeKindArray
}

func (t *ArrayType) String() string {
	if t == nil || t.Elem == nil {
		return "[invalid]invalid"
	}
	return "[" + strconv.Itoa(t.Len) + "]" + t.Elem.String()
}

type SliceType struct {
	Elem ScriptType
}

func (*SliceType) Kind() TypeKind {
	return TypeKindSlice
}

func (t *SliceType) String() string {
	if t == nil || t.Elem == nil {
		return "[]invalid"
	}
	return "[]" + t.Elem.String()
}

type MapType struct {
	Key   ScriptType
	Value ScriptType
}

func (*MapType) Kind() TypeKind {
	return TypeKindMap
}

func (t *MapType) String() string {
	if t == nil {
		return "map[invalid]invalid"
	}
	return "map[" + stringify(t.Key) + "]" + stringify(t.Value)
}

type StructType struct {
	FieldNames []string
	Fields     []ScriptType
}

func (*StructType) Kind() TypeKind {
	return TypeKindStruct
}

func (t *StructType) String() string {
	if t == nil || len(t.Fields) == 0 {
		return "struct{}"
	}
	parts := make([]string, 0, len(t.Fields))
	for i, field := range t.Fields {
		name := ""
		if i < len(t.FieldNames) {
			name = t.FieldNames[i]
		}
		if name == "" {
			parts = append(parts, stringify(field))
			continue
		}
		parts = append(parts, name+" "+stringify(field))
	}
	return "struct{" + strings.Join(parts, "; ") + "}"
}

type PointerType struct {
	Base ScriptType
}

func (*PointerType) Kind() TypeKind {
	return TypeKindPointer
}

func (t *PointerType) String() string {
	return "*" + stringify(t.Base)
}

type FuncType struct {
	Params   []ScriptType
	Results  []ScriptType
	Variadic bool
}

func (*FuncType) Kind() TypeKind {
	return TypeKindFunc
}

func (t *FuncType) String() string {
	if t == nil {
		return "func()"
	}
	params := formatParamList(t.Params, t.Variadic)
	results := stringifyList(t.Results)
	if len(t.Results) == 0 {
		return "func(" + params + ")"
	}
	if len(t.Results) == 1 {
		return "func(" + params + ") " + results
	}
	return "func(" + params + ") (" + results + ")"
}

func formatParamList(params []ScriptType, variadic bool) string {
	parts := make([]string, 0, len(params))
	for i, item := range params {
		if variadic && i == len(params)-1 {
			if slice, ok := Underlying(item).(*SliceType); ok {
				parts = append(parts, "..."+stringify(slice.Elem))
				continue
			}
		}
		parts = append(parts, stringify(item))
	}
	return strings.Join(parts, ", ")
}

type NamedType struct {
	Name       string
	PkgPath    string
	Underlying ScriptType
	Methods    []Method
}

func (*NamedType) Kind() TypeKind {
	return TypeKindNamed
}

func (t *NamedType) String() string {
	if t == nil {
		return "invalid"
	}
	if t.PkgPath == "" {
		return t.Name
	}
	return t.PkgPath + "." + t.Name
}

func stringify(t ScriptType) string {
	if t == nil {
		return "invalid"
	}
	return t.String()
}

func stringifyList(items []ScriptType) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, stringify(item))
	}
	return strings.Join(parts, ", ")
}

func stringifyMethodType(t ScriptType) string {
	if t == nil {
		return " invalid"
	}
	if fn, ok := t.(*FuncType); ok && fn != nil {
		params := formatParamList(fn.Params, fn.Variadic)
		results := stringifyList(fn.Results)
		if len(fn.Results) == 0 {
			return "(" + params + ")"
		}
		if len(fn.Results) == 1 {
			return "(" + params + ") " + results
		}
		return "(" + params + ") (" + results + ")"
	}
	return " " + t.String()
}

func Underlying(t ScriptType) ScriptType {
	seen := make(map[ScriptType]struct{})
	for {
		if t == nil {
			return BuiltinInvalidType
		}
		if _, ok := seen[t]; ok {
			return BuiltinInvalidType
		}
		seen[t] = struct{}{}
		named, ok := t.(*NamedType)
		if !ok || named == nil || named.Underlying == nil || named.Underlying == t {
			return t
		}
		t = named.Underlying
	}
}

func IsInvalid(t ScriptType) bool {
	if t == nil {
		return true
	}
	return Underlying(t).Kind() == TypeKindInvalid
}

func IsBoolean(t ScriptType) bool {
	if IsInvalid(t) {
		return false
	}
	return Underlying(t).Kind() == TypeKindBool
}

func IsUntypedNil(t ScriptType) bool {
	if IsInvalid(t) {
		return false
	}
	return Underlying(t).Kind() == TypeKindNil
}

func IsInteger(t ScriptType) bool {
	if IsInvalid(t) {
		return false
	}
	switch Underlying(t).Kind() {
	case TypeKindInt, TypeKindInt64:
		return true
	default:
		return false
	}
}

func IsNumeric(t ScriptType) bool {
	if IsInvalid(t) {
		return false
	}
	switch Underlying(t).Kind() {
	case TypeKindInt, TypeKindInt64, TypeKindFloat64:
		return true
	default:
		return false
	}
}

func IsString(t ScriptType) bool {
	if IsInvalid(t) {
		return false
	}
	return Underlying(t).Kind() == TypeKindString
}

func IsComparable(t ScriptType) bool {
	if IsInvalid(t) {
		return false
	}
	switch u := Underlying(t).(type) {
	case *BasicType:
		switch u.Kind() {
		case TypeKindBool, TypeKindInt, TypeKindInt64, TypeKindFloat64, TypeKindString:
			return true
		default:
			return false
		}
	case *PointerType:
		return true
	case *ArrayType:
		return IsComparable(u.Elem)
	case *StructType:
		for _, field := range u.Fields {
			if !IsComparable(field) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func IsNilable(t ScriptType) bool {
	if IsInvalid(t) {
		return false
	}
	switch Underlying(t).Kind() {
	case TypeKindPointer, TypeKindSlice, TypeKindMap, TypeKindFunc:
		return true
	default:
		return false
	}
}

func IsOrdered(t ScriptType) bool {
	return IsNumeric(t) || IsString(t)
}

func AssignableTo(src, dst ScriptType) bool {
	if IsInvalid(src) || IsInvalid(dst) {
		return false
	}
	if IsUntypedNil(src) {
		return IsNilable(dst)
	}
	return Identical(src, dst)
}

func Identical(a, b ScriptType) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a == b {
		return true
	}

	if aNamed, aIsNamed := a.(*NamedType); aIsNamed {
		if bNamed, bIsNamed := b.(*NamedType); bIsNamed {
			return aNamed.PkgPath == bNamed.PkgPath && aNamed.Name == bNamed.Name
		}
		return false
	}
	if _, ok := b.(*NamedType); ok {
		return false
	}

	switch a := a.(type) {
	case *BasicType:
		b, ok := b.(*BasicType)
		return ok && a != nil && b != nil && a.kind == b.kind && a.name == b.name
	case *ArrayType:
		b, ok := b.(*ArrayType)
		return ok && a.Len == b.Len && Identical(a.Elem, b.Elem)
	case *SliceType:
		b, ok := b.(*SliceType)
		return ok && Identical(a.Elem, b.Elem)
	case *MapType:
		b, ok := b.(*MapType)
		return ok && Identical(a.Key, b.Key) && Identical(a.Value, b.Value)
	case *StructType:
		b, ok := b.(*StructType)
		if !ok || len(a.Fields) != len(b.Fields) || len(a.FieldNames) != len(b.FieldNames) {
			return false
		}
		for i := range a.Fields {
			if a.FieldNames[i] != b.FieldNames[i] {
				return false
			}
			if !Identical(a.Fields[i], b.Fields[i]) {
				return false
			}
		}
		return true
	case *PointerType:
		b, ok := b.(*PointerType)
		return ok && Identical(a.Base, b.Base)
	case *FuncType:
		b, ok := b.(*FuncType)
		if !ok || a.Variadic != b.Variadic || len(a.Params) != len(b.Params) || len(a.Results) != len(b.Results) {
			return false
		}
		for i := range a.Params {
			if !Identical(a.Params[i], b.Params[i]) {
				return false
			}
		}
		for i := range a.Results {
			if !Identical(a.Results[i], b.Results[i]) {
				return false
			}
		}
		return true
	default:
		return a.Kind() == b.Kind() && a.String() == b.String()
	}
}

func LookupMethod(t ScriptType, name string) (ScriptType, bool) {
	switch t := t.(type) {
	case *NamedType:
		for _, method := range t.Methods {
			if method.Name == name && !method.PointerRecv {
				return method.Type, true
			}
		}
	case *PointerType:
		if named, ok := t.Base.(*NamedType); ok {
			for _, method := range named.Methods {
				if method.Name == name {
					return method.Type, true
				}
			}
		}
	}
	return nil, false
}
