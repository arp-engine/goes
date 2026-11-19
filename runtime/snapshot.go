package runtime

type Snapshot struct {
	Version         int
	ProgramHash     string
	PendingHostCall PendingHostCallSnapshot
	Globals         []int
	Frames          []FrameSnapshot
	Heap            HeapSnapshot
}

type PendingHostCallSnapshot struct {
	Name            string
	ExpectedResults int
	Payload         any
}

type FrameSnapshot struct {
	FunctionIndex int
	IP            int
	LastIP        int
	Locals        []int
	Captures      []int
	Stack         []ValueSnapshot
}

type HeapSnapshot struct {
	Cells   []CellSnapshot
	Objects []ObjectSnapshot
}

type CellSnapshot struct {
	Value ValueSnapshot
}

type ValueSnapshot struct {
	Kind     Kind
	B        bool
	I64      int64
	F64      float64
	Str      string
	CellID   int
	ObjectID int
}

type ObjectSnapshot struct {
	Kind       string
	TypeName   string
	Cells      []int
	MapEntries []MapEntrySnapshot
	Zero       ValueSnapshot
	FuncIndex  int
	Captures   []int
	Receiver   ValueSnapshot
}

type MapEntrySnapshot struct {
	EncodedKey MapKeySnapshot
	Key        ValueSnapshot
	Value      ValueSnapshot
}

type MapKeySnapshot struct {
	Kind   Kind
	B      bool
	I64    int64
	F64    float64
	Str    string
	CellID int
}
