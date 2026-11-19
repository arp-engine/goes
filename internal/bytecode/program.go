package bytecode

import rt "goes/runtime"

type Instruction struct {
	Op  Opcode
	C   int32
	A   int
	B   int
	I64 int64
	S   string
	Pos SourcePos
}

type Function struct {
	Name     string
	Params   int
	Results  int
	Variadic bool
	Locals   int
	MaxStack int
	Code     []Instruction

	DebugLocals []DebugLocal
}

type Program struct {
	Functions     []Function
	Entry         int
	Init          int
	FuncIndex     map[string]int
	MethodSets    map[string]map[string]int
	GlobalCount   int
	HostFunctions []HostFunction
	HostIndex     map[string]int
	StructSchemas map[string]rt.StructSchema
	SourceLines   map[string][]string
	DebugGlobals  []DebugGlobal
}

type SourcePos struct {
	File string
	Line int
}

type HostFunction struct {
	Name     string
	Params   int
	Results  int
	Variadic bool
}

type DebugLocalKind string

const (
	DebugLocalParam   DebugLocalKind = "param"
	DebugLocalResult  DebugLocalKind = "result"
	DebugLocalLocal   DebugLocalKind = "local"
	DebugLocalCapture DebugLocalKind = "capture"
)

type DebugLocal struct {
	Name    string
	Slot    int
	Kind    DebugLocalKind
	Type    string
	StartPC int
	EndPC   int
}

type DebugGlobal struct {
	Name string
	Slot int
	Type string
}

func (p *Program) LookupFunction(name string) (int, bool) {
	if p == nil || p.FuncIndex == nil {
		return 0, false
	}
	index, ok := p.FuncIndex[name]
	return index, ok
}

func (p *Program) LookupHostFunction(name string) (int, bool) {
	if p == nil || p.HostIndex == nil {
		return 0, false
	}
	index, ok := p.HostIndex[name]
	return index, ok
}

func (p *Program) SourceLine(pos SourcePos) string {
	if p == nil || pos.File == "" || pos.Line <= 0 || p.SourceLines == nil {
		return ""
	}
	lines := p.SourceLines[pos.File]
	if pos.Line > len(lines) {
		return ""
	}
	return lines[pos.Line-1]
}
