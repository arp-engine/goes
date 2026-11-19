package runtime

type Kind int

const (
	Invalid Kind = iota
	Nil
	Bool
	Int
	Int64
	Float64
	String
	PointerKind
	ObjectKind
)

type Value struct {
	Kind Kind
	B    bool
	I64  int64
	F64  float64
	Str  string
	Cell *Cell
	Obj  Object
}

func BoolValue(v bool) Value {
	return Value{Kind: Bool, B: v}
}

func NilValue() Value {
	return Value{Kind: Nil}
}

func IntValue(v int64) Value {
	return Value{Kind: Int, I64: v}
}

func Int64Value(v int64) Value {
	return Value{Kind: Int64, I64: v}
}

func Float64Value(v float64) Value {
	return Value{Kind: Float64, F64: v}
}

func PointerValue(cell *Cell) Value {
	return Value{Kind: PointerKind, Cell: cell}
}

func StringValue(v string) Value {
	return Value{Kind: String, Str: v}
}

func ObjectValue(obj Object) Value {
	return Value{Kind: ObjectKind, Obj: obj}
}

func (v Value) Int() int64 {
	return v.I64
}

func (v Value) Bool() bool {
	return v.B
}

func (v Value) Float64() float64 {
	return v.F64
}

func (v Value) Pointer() *Cell {
	return v.Cell
}

func (v Value) String() string {
	return v.Str
}

func (v Value) Object() Object {
	return v.Obj
}

func CloneForStore(v Value) Value {
	switch v.Kind {
	case ObjectKind:
		return ObjectValue(cloneObject(v.Obj))
	case PointerKind:
		return PointerValue(v.Cell)
	default:
		return v
	}
}

func NeedsCloneForStore(v Value) bool {
	switch v.Kind {
	case ObjectKind:
		return true
	default:
		return false
	}
}

func cloneObject(obj Object) Object {
	switch obj := obj.(type) {
	case *Struct:
		if obj == nil {
			return (*Struct)(nil)
		}
		fields := make([]Cell, len(obj.Fields))
		for i, field := range obj.Fields {
			fields[i].Value = CloneForStore(field.Value)
		}
		return &Struct{TypeName: obj.TypeName, Fields: fields}
	case *Array:
		if obj == nil {
			return (*Array)(nil)
		}
		elems := make([]Cell, len(obj.Elements))
		for i, elem := range obj.Elements {
			elems[i].Value = CloneForStore(elem.Value)
		}
		return &Array{Elements: elems}
	case *Slice:
		if obj == nil {
			return (*Slice)(nil)
		}
		return &Slice{Elements: obj.Elements}
	case *Map:
		return obj
	case *Closure:
		return obj
	case *MethodValue:
		if obj == nil {
			return (*MethodValue)(nil)
		}
		return &MethodValue{FuncIndex: obj.FuncIndex, Receiver: CloneForStore(obj.Receiver)}
	default:
		return obj
	}
}
