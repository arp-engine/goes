package vm

import (
	"fmt"
	"strconv"
	"strings"

	"goes/internal/bytecode"
	"goes/runtime"
)

type frameTrace struct {
	Function string
	IP       int
	Pos      bytecode.SourcePos
	Line     string
	Stack    []runtime.Value
}

type TraceError struct {
	Err   error
	Stack []frameTrace
}

func (e *TraceError) Error() string {
	if e == nil || e.Err == nil {
		return "vm: unknown error"
	}
	var b strings.Builder
	b.WriteString(e.Err.Error())
	if len(e.Stack) == 0 {
		return b.String()
	}
	b.WriteString("\nvm traceback:")
	for i := len(e.Stack) - 1; i >= 0; i-- {
		frame := e.Stack[i]
		b.WriteString("\n  ")
		b.WriteString(frame.Function)
		b.WriteString(" ip=")
		b.WriteString(strconv.Itoa(frame.IP))
		if frame.Pos.File != "" && frame.Pos.Line > 0 {
			b.WriteString(" ")
			b.WriteString(frame.Pos.File)
			b.WriteString(":")
			b.WriteString(strconv.Itoa(frame.Pos.Line))
		}
		if frame.Line != "" {
			b.WriteString("\n    ")
			b.WriteString(strings.TrimSpace(frame.Line))
		}
		if len(frame.Stack) > 0 {
			b.WriteString("\n    stack: [")
			for j, value := range frame.Stack {
				if j > 0 {
					b.WriteString(", ")
				}
				b.WriteString(formatTraceValue(value, 0))
			}
			b.WriteString("]")
		}
	}
	return b.String()
}

func (e *TraceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func formatTraceValue(value runtime.Value, depth int) string {
	if depth > 1 {
		return "..."
	}
	switch value.Kind {
	case runtime.Invalid:
		return "invalid"
	case runtime.Nil:
		return "nil"
	case runtime.Bool:
		if value.Bool() {
			return "bool=true"
		}
		return "bool=false"
	case runtime.Int:
		return "int=" + strconv.FormatInt(value.Int(), 10)
	case runtime.Int64:
		return "int64=" + strconv.FormatInt(value.Int(), 10)
	case runtime.Float64:
		return "float64=" + strconv.FormatFloat(value.Float64(), 'g', -1, 64)
	case runtime.String:
		return "string=" + strconv.Quote(value.String())
	case runtime.PointerKind:
		if value.Pointer() == nil {
			return "ptr=nil"
		}
		return "ptr->" + formatTraceValue(value.Pointer().Value, depth+1)
	case runtime.ObjectKind:
		switch obj := value.Object().(type) {
		case nil:
			return "object=nil"
		case *runtime.Struct:
			return "struct=" + obj.TypeName
		case *runtime.Array:
			return "array len=" + strconv.Itoa(len(obj.Elements))
		case *runtime.Slice:
			return "slice len=" + strconv.Itoa(len(obj.Elements))
		case *runtime.Map:
			return "map len=" + strconv.Itoa(len(obj.Entries))
		case *runtime.Closure:
			return "closure func=" + strconv.Itoa(obj.FuncIndex)
		case *runtime.MethodValue:
			return "method func=" + strconv.Itoa(obj.FuncIndex)
		default:
			return "object"
		}
	default:
		return "kind=" + strconv.Itoa(int(value.Kind))
	}
}

type frame struct {
	function *bytecode.Function
	ip       int
	lastIP   int
	locals   []runtime.Cell
	captures []*runtime.Cell
	stack    []runtime.Value
}

func newFrame(function *bytecode.Function, args []runtime.Value, captures []*runtime.Cell) (*frame, error) {
	if function == nil {
		return nil, fmt.Errorf("vm: missing function")
	}
	if len(args) != function.Params {
		return nil, fmt.Errorf("vm: function %q argument count mismatch: got %d, want %d", function.Name, len(args), function.Params)
	}

	locals := make([]runtime.Cell, function.Locals)
	for i := range locals {
		locals[i].Value = runtime.Value{}
	}
	for i := range args {
		locals[i].Value = runtime.CloneForStore(args[i])
	}
	stack := make([]runtime.Value, 0, function.MaxStack)
	return &frame{function: function, ip: 0, lastIP: -1, locals: locals, captures: captures, stack: stack}, nil
}

func (f *frame) reset(function *bytecode.Function, args []runtime.Value, captures []*runtime.Cell) error {
	if function == nil {
		return fmt.Errorf("vm: missing function")
	}
	if len(args) != function.Params {
		return fmt.Errorf("vm: function %q argument count mismatch: got %d, want %d", function.Name, len(args), function.Params)
	}

	f.function = function
	f.ip = 0
	f.lastIP = -1
	f.captures = captures
	f.stack = f.stack[:0]

	if cap(f.locals) < function.Locals {
		f.locals = make([]runtime.Cell, function.Locals)
	} else {
		f.locals = f.locals[:function.Locals]
		for i := range f.locals {
			f.locals[i].Value = runtime.Value{}
		}
	}
	for i := range args {
		f.locals[i].Value = runtime.CloneForStore(args[i])
	}
	if cap(f.stack) < function.MaxStack {
		f.stack = make([]runtime.Value, 0, function.MaxStack)
	} else {
		f.stack = f.stack[:0]
	}
	return nil
}

func (f *frame) push(v runtime.Value) {
	f.stack = append(f.stack, v)
}

func (f *frame) pop() (runtime.Value, error) {
	if len(f.stack) == 0 {
		return runtime.Value{}, fmt.Errorf("vm: stack underflow in %q", f.function.Name)
	}
	last := len(f.stack) - 1
	v := f.stack[last]
	f.stack = f.stack[:last]
	return v, nil
}

func (f *frame) popPointer() (*runtime.Cell, error) {
	v, err := f.pop()
	if err != nil {
		return nil, err
	}
	if v.Kind != runtime.PointerKind || v.Pointer() == nil {
		return nil, fmt.Errorf("vm: expected pointer value in %q, got kind %d", f.function.Name, v.Kind)
	}
	return v.Pointer(), nil
}
