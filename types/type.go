package types

import (
	"strconv"
	"strings"
)

type Kind int

const (
	Invalid Kind = iota
	Nil
	Bool
	Int
	Int64
	Float64
	String
	Array
	Slice
	Map
	Struct
	Pointer
	Func
	Named
)

type Type interface {
	Kind() Kind
	String() string
}

type Method struct {
	Name        string
	Type        Type
	PointerRecv bool
}

type HostSignature struct {
	Params   []Type
	Results  []Type
	Variadic bool
}

type BasicType struct {
	kind Kind
	name string
}

func (t *BasicType) Kind() Kind {
	return t.kind
}

func (t *BasicType) String() string {
	return t.name
}

type ArrayType struct {
	Len  int
	Elem Type
}

func (*ArrayType) Kind() Kind {
	return Array
}

func (t *ArrayType) String() string {
	if t == nil || t.Elem == nil {
		return "[invalid]invalid"
	}
	return "[" + strconv.Itoa(t.Len) + "]" + t.Elem.String()
}

type SliceType struct {
	Elem Type
}

func (*SliceType) Kind() Kind {
	return Slice
}

func (t *SliceType) String() string {
	if t == nil || t.Elem == nil {
		return "[]invalid"
	}
	return "[]" + t.Elem.String()
}

type MapType struct {
	Key   Type
	Value Type
}

func (*MapType) Kind() Kind {
	return Map
}

func (t *MapType) String() string {
	if t == nil {
		return "map[invalid]invalid"
	}
	return "map[" + stringify(t.Key) + "]" + stringify(t.Value)
}

type StructType struct {
	FieldNames []string
	Fields     []Type
}

func (*StructType) Kind() Kind {
	return Struct
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
	Base Type
}

func (*PointerType) Kind() Kind {
	return Pointer
}

func (t *PointerType) String() string {
	return "*" + stringify(t.Base)
}

type FuncType struct {
	Params   []Type
	Results  []Type
	Variadic bool
}

func (*FuncType) Kind() Kind {
	return Func
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

func formatParamList(params []Type, variadic bool) string {
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
	Underlying Type
	Methods    []Method
}

func (*NamedType) Kind() Kind {
	return Named
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

func stringify(t Type) string {
	if t == nil {
		return "invalid"
	}
	return t.String()
}

func stringifyList(items []Type) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, stringify(item))
	}
	return strings.Join(parts, ", ")
}

func stringifyMethodType(t Type) string {
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

func Underlying(t Type) Type {
	seen := make(map[Type]struct{})
	for {
		if t == nil {
			return InvalidType
		}
		if _, ok := seen[t]; ok {
			return InvalidType
		}
		seen[t] = struct{}{}
		named, ok := t.(*NamedType)
		if !ok || named == nil || named.Underlying == nil || named.Underlying == t {
			return t
		}
		t = named.Underlying
	}
}

func IsInvalid(t Type) bool {
	if t == nil {
		return true
	}
	return Underlying(t).Kind() == Invalid
}

func IsBoolean(t Type) bool {
	if IsInvalid(t) {
		return false
	}
	return Underlying(t).Kind() == Bool
}

func IsUntypedNil(t Type) bool {
	if IsInvalid(t) {
		return false
	}
	return Underlying(t).Kind() == Nil
}

func IsInteger(t Type) bool {
	if IsInvalid(t) {
		return false
	}
	switch Underlying(t).Kind() {
	case Int, Int64:
		return true
	default:
		return false
	}
}

func IsNumeric(t Type) bool {
	if IsInvalid(t) {
		return false
	}
	switch Underlying(t).Kind() {
	case Int, Int64, Float64:
		return true
	default:
		return false
	}
}

func IsString(t Type) bool {
	if IsInvalid(t) {
		return false
	}
	return Underlying(t).Kind() == String
}

func IsComparable(t Type) bool {
	if IsInvalid(t) {
		return false
	}
	switch u := Underlying(t).(type) {
	case *BasicType:
		switch u.Kind() {
		case Bool, Int, Int64, Float64, String:
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

func IsNilable(t Type) bool {
	if IsInvalid(t) {
		return false
	}
	switch Underlying(t).Kind() {
	case Pointer, Slice, Map, Func:
		return true
	default:
		return false
	}
}

func IsOrdered(t Type) bool {
	return IsNumeric(t) || IsString(t)
}

func AssignableTo(src, dst Type) bool {
	if IsInvalid(src) || IsInvalid(dst) {
		return false
	}
	if IsUntypedNil(src) {
		return IsNilable(dst)
	}
	return Identical(src, dst)
}

func Identical(a, b Type) bool {
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

func LookupMethod(t Type, name string) (Type, bool) {
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
