package runtime

type Object interface {
	object()
}

type StructSchema struct {
	Name       string
	FieldNames []string
	FieldKinds []Kind
}

type Struct struct {
	TypeName string
	Fields   []Cell
}

func (*Struct) object() {}

type Array struct {
	Elements []Cell
}

func (*Array) object() {}

type Slice struct {
	Elements []Cell
}

func (*Slice) object() {}

type MapKey struct {
	Kind Kind
	B    bool
	I64  int64
	F64  float64
	Str  string
	Cell *Cell
}

type Map struct {
	Entries   map[MapKey]MapEntry
	Keys      []MapKey
	KeysDirty bool
	Zero      Value
}

type MapEntry struct {
	Key   Value
	Value Value
}

func (*Map) object() {}

type Closure struct {
	FuncIndex int
	Captures  []*Cell
}

func (*Closure) object() {}

type MethodValue struct {
	FuncIndex int
	Receiver  Value
}

func (*MethodValue) object() {}
