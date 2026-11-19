package vm

import (
	"errors"
	"fmt"
	"strconv"

	"goes/internal/bytecode"
	"goes/runtime"
)

func Run(program *bytecode.Program) (runtime.Value, error) {
	return RunWithHost(program, nil, nil)
}

func RunWithDebug(program *bytecode.Program, debug bool) (runtime.Value, error) {
	return RunWithHostDebug(program, nil, nil, debug)
}

func RunWithHost(program *bytecode.Program, host *runtime.HostRegistry, ctx *runtime.HostContext) (runtime.Value, error) {
	return RunWithHostDebug(program, host, ctx, false)
}

func RunWithHostDebug(program *bytecode.Program, host *runtime.HostRegistry, ctx *runtime.HostContext, debug bool) (runtime.Value, error) {
	if program == nil {
		return runtime.Value{}, fmt.Errorf("vm: missing program")
	}
	if program.Entry < 0 || program.Entry >= len(program.Functions) {
		return runtime.Value{}, fmt.Errorf("vm: invalid entry point %d", program.Entry)
	}

	machine := &vm{program: program, host: host, hostCtx: ctx, debug: debug}
	if program.Init >= 0 {
		if _, err := machine.runFunction(program.Init, nil, nil); err != nil {
			return runtime.Value{}, err
		}
	}
	results, err := machine.runFunction(program.Entry, nil, nil)
	if err != nil {
		return runtime.Value{}, err
	}
	if len(results) != 1 {
		return runtime.Value{}, fmt.Errorf("vm: entry function returned %d results, want 1", len(results))
	}
	return results[0], nil
}

func RunFunction(program *bytecode.Program, index int, args []runtime.Value) (runtime.Value, error) {
	return RunFunctionWithHost(program, index, args, nil, nil)
}

func RunFunctionWithDebug(program *bytecode.Program, index int, args []runtime.Value, debug bool) (runtime.Value, error) {
	return RunFunctionWithHostDebug(program, index, args, nil, nil, debug)
}

func RunFunctionValues(program *bytecode.Program, index int, args []runtime.Value) ([]runtime.Value, error) {
	return RunFunctionWithHostValues(program, index, args, nil, nil)
}

func RunFunctionValuesWithDebug(program *bytecode.Program, index int, args []runtime.Value, debug bool) ([]runtime.Value, error) {
	return RunFunctionWithHostValuesDebug(program, index, args, nil, nil, debug)
}

func RunFunctionWithHost(program *bytecode.Program, index int, args []runtime.Value, host *runtime.HostRegistry, ctx *runtime.HostContext) (runtime.Value, error) {
	return RunFunctionWithHostDebug(program, index, args, host, ctx, false)
}

func RunFunctionWithHostDebug(program *bytecode.Program, index int, args []runtime.Value, host *runtime.HostRegistry, ctx *runtime.HostContext, debug bool) (runtime.Value, error) {
	if program == nil {
		return runtime.Value{}, fmt.Errorf("vm: missing program")
	}
	if index < 0 || index >= len(program.Functions) {
		return runtime.Value{}, fmt.Errorf("vm: invalid function index %d", index)
	}

	machine := &vm{program: program, host: host, hostCtx: ctx, debug: debug}
	results, err := machine.runFunction(index, args, nil)
	if err != nil {
		return runtime.Value{}, err
	}
	if len(results) != 1 {
		return runtime.Value{}, fmt.Errorf("vm: function returned %d results, want 1", len(results))
	}
	return results[0], nil
}

func RunFunctionWithHostValues(program *bytecode.Program, index int, args []runtime.Value, host *runtime.HostRegistry, ctx *runtime.HostContext) ([]runtime.Value, error) {
	return RunFunctionWithHostValuesDebug(program, index, args, host, ctx, false)
}

func RunFunctionWithHostValuesDebug(program *bytecode.Program, index int, args []runtime.Value, host *runtime.HostRegistry, ctx *runtime.HostContext, debug bool) ([]runtime.Value, error) {
	if program == nil {
		return nil, fmt.Errorf("vm: missing program")
	}
	if index < 0 || index >= len(program.Functions) {
		return nil, fmt.Errorf("vm: invalid function index %d", index)
	}

	machine := &vm{program: program, host: host, hostCtx: ctx, debug: debug}
	return machine.runFunction(index, args, nil)
}

type vm struct {
	program    *bytecode.Program
	host       *runtime.HostRegistry
	hostCtx    *runtime.HostContext
	globals    []runtime.Cell
	frames     []*frame
	freeFrames []*frame
	freeArgs   [][]runtime.Value
	debug      bool

	debugLastFile string
	debugLastLine int
}

func (m *vm) pushFrame(frame *frame) {
	m.frames = append(m.frames, frame)
}

func (m *vm) popFrame() {
	if len(m.frames) == 0 {
		return
	}
	frame := m.frames[len(m.frames)-1]
	if frame != nil {
		m.freeFrames = append(m.freeFrames, frame)
	}
	m.frames = m.frames[:len(m.frames)-1]
}

func (m *vm) traceError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*TraceError); ok {
		return err
	}
	stack := make([]frameTrace, 0, len(m.frames))
	for _, frame := range m.frames {
		name := "<unknown>"
		ip := 0
		var pos bytecode.SourcePos
		line := ""
		if frame != nil && frame.function != nil {
			name = frame.function.Name
			ip = frame.lastIP
			if ip < 0 {
				ip = frame.ip
			}
			if ip >= 0 && ip < len(frame.function.Code) {
				pos = frame.function.Code[ip].Pos
				line = m.program.SourceLine(pos)
			}
		}
		var operandStack []runtime.Value
		if frame != nil && len(frame.stack) > 0 {
			operandStack = append([]runtime.Value(nil), frame.stack...)
		}
		stack = append(stack, frameTrace{Function: name, IP: ip, Pos: pos, Line: line, Stack: operandStack})
	}
	return &TraceError{Err: err, Stack: stack}
}

func (m *vm) runFunction(index int, args []runtime.Value, captures []*runtime.Cell) (_ []runtime.Value, err error) {
	if m.globals == nil && m.program.GlobalCount > 0 {
		m.globals = make([]runtime.Cell, m.program.GlobalCount)
	}
	if index < 0 || index >= len(m.program.Functions) {
		return nil, m.traceError(fmt.Errorf("vm: function index %d out of range", index))
	}

	function := &m.program.Functions[index]
	var frame *frame
	if n := len(m.freeFrames); n > 0 {
		frame = m.freeFrames[n-1]
		m.freeFrames = m.freeFrames[:n-1]
		if err = frame.reset(function, args, captures); err != nil {
			return nil, m.traceError(err)
		}
	} else {
		frame, err = newFrame(function, args, captures)
		if err != nil {
			return nil, m.traceError(err)
		}
	}

	m.pushFrame(frame)
	result, err := m.executeFrame(function, frame)
	if err != nil {
		if isSuspension(err) {
			return nil, err
		}
		err = m.traceError(err)
		m.popFrame()
		return nil, err
	}
	m.popFrame()
	return result, nil
}

func isSuspension(err error) bool {
	var suspension *runtime.Suspension
	return errors.As(err, &suspension)
}

func (m *vm) resume(hostResults []runtime.Value, expectedHostResults int) ([]runtime.Value, error) {
	if len(m.frames) == 0 {
		return nil, fmt.Errorf("vm: cannot resume empty call stack")
	}
	if len(hostResults) != expectedHostResults {
		return nil, fmt.Errorf("vm: host resume expected %d results, got %d", expectedHostResults, len(hostResults))
	}
	frame := m.frames[len(m.frames)-1]
	for _, result := range hostResults {
		frame.push(result)
	}

	for len(m.frames) > 0 {
		current := m.frames[len(m.frames)-1]
		results, err := m.executeFrame(current.function, current)
		if err != nil {
			if isSuspension(err) {
				return nil, err
			}
			return nil, m.traceError(err)
		}
		m.popFrame()
		if len(m.frames) == 0 {
			return results, nil
		}
		caller := m.frames[len(m.frames)-1]
		for _, result := range results {
			caller.push(result)
		}
	}

	return nil, fmt.Errorf("vm: resumed call stack did not produce results")
}

func (m *vm) executeFrame(function *bytecode.Function, frame *frame) ([]runtime.Value, error) {
	for frame.ip < len(function.Code) {
		frame.lastIP = frame.ip
		inst := function.Code[frame.ip]
		frame.ip++
		if m.debug {
			if err := m.maybeDebugBreak(function, frame, inst); err != nil {
				return nil, err
			}
		}

		switch inst.Op {
		case bytecode.OpLoadInt:
			frame.push(runtime.IntValue(inst.I64))
		case bytecode.OpLoadNil:
			frame.push(runtime.NilValue())
		case bytecode.OpLoadBool:
			frame.push(runtime.BoolValue(inst.A != 0))
		case bytecode.OpLoadInt64:
			frame.push(runtime.Int64Value(inst.I64))
		case bytecode.OpLoadFloat64:
			value, err := strconv.ParseFloat(inst.S, 64)
			if err != nil {
				return nil, fmt.Errorf("vm: invalid float literal %q in %q", inst.S, function.Name)
			}
			frame.push(runtime.Float64Value(value))
		case bytecode.OpLoadString:
			frame.push(runtime.StringValue(inst.S))
		case bytecode.OpLoadLocal:
			if inst.A < 0 || inst.A >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot %d out of range in %q", inst.A, function.Name)
			}
			frame.push(frame.locals[inst.A].Value)
		case bytecode.OpLoadCapture:
			if inst.A < 0 || inst.A >= len(frame.captures) {
				return nil, fmt.Errorf("vm: capture slot %d out of range in %q", inst.A, function.Name)
			}
			frame.push(frame.captures[inst.A].Value)
		case bytecode.OpStoreLocal:
			value, err := frame.pop()
			if err != nil {
				return nil, err
			}
			if inst.A < 0 || inst.A >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot %d out of range in %q", inst.A, function.Name)
			}
			frame.locals[inst.A].Value = runtime.CloneForStore(value)
		case bytecode.OpLocalAddIntConst:
			slot := inst.A
			if slot < 0 || slot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot %d out of range in %q", slot, function.Name)
			}
			frame.locals[slot].Value = runtime.IntValue(frame.locals[slot].Value.Int() + inst.I64)
		case bytecode.OpLocalSubIntConst:
			slot := inst.A
			if slot < 0 || slot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot %d out of range in %q", slot, function.Name)
			}
			frame.locals[slot].Value = runtime.IntValue(frame.locals[slot].Value.Int() - inst.I64)
		case bytecode.OpLocalAddLocal:
			dst, src := inst.A, inst.B
			if dst < 0 || dst >= len(frame.locals) || src < 0 || src >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot out of range in %q", function.Name)
			}
			frame.locals[dst].Value = runtime.IntValue(frame.locals[dst].Value.Int() + frame.locals[src].Value.Int())
		case bytecode.OpLocalSubLocal:
			dst, src := inst.A, inst.B
			if dst < 0 || dst >= len(frame.locals) || src < 0 || src >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot out of range in %q", function.Name)
			}
			frame.locals[dst].Value = runtime.IntValue(frame.locals[dst].Value.Int() - frame.locals[src].Value.Int())
		case bytecode.OpStructFieldAddIntConst:
			baseSlot, fieldIndex := inst.A, inst.B
			if baseSlot < 0 || baseSlot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot %d out of range in %q", baseSlot, function.Name)
			}
			strct, ok := frame.locals[baseSlot].Value.Object().(*runtime.Struct)
			if frame.locals[baseSlot].Value.Kind != runtime.ObjectKind || !ok || strct == nil {
				return nil, fmt.Errorf("vm: expected struct local in %q", function.Name)
			}
			if fieldIndex < 0 || fieldIndex >= len(strct.Fields) {
				return nil, fmt.Errorf("vm: field index %d out of range in %q", fieldIndex, function.Name)
			}
			strct.Fields[fieldIndex].Value = runtime.IntValue(strct.Fields[fieldIndex].Value.Int() + inst.I64)
		case bytecode.OpStructFieldAddLocal:
			baseSlot, fieldIndex := inst.A, inst.B
			sourceSlot := int(inst.I64)
			if baseSlot < 0 || baseSlot >= len(frame.locals) || sourceSlot < 0 || sourceSlot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot out of range in %q", function.Name)
			}
			strct, ok := frame.locals[baseSlot].Value.Object().(*runtime.Struct)
			if frame.locals[baseSlot].Value.Kind != runtime.ObjectKind || !ok || strct == nil {
				return nil, fmt.Errorf("vm: expected struct local in %q", function.Name)
			}
			if fieldIndex < 0 || fieldIndex >= len(strct.Fields) {
				return nil, fmt.Errorf("vm: field index %d out of range in %q", fieldIndex, function.Name)
			}
			strct.Fields[fieldIndex].Value = runtime.IntValue(strct.Fields[fieldIndex].Value.Int() + frame.locals[sourceSlot].Value.Int())
		case bytecode.OpStructFieldAddField:
			baseSlot, fieldIndex := inst.A, inst.B
			sourceFieldIndex := int(inst.I64)
			if baseSlot < 0 || baseSlot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot %d out of range in %q", baseSlot, function.Name)
			}
			strct, ok := frame.locals[baseSlot].Value.Object().(*runtime.Struct)
			if frame.locals[baseSlot].Value.Kind != runtime.ObjectKind || !ok || strct == nil {
				return nil, fmt.Errorf("vm: expected struct local in %q", function.Name)
			}
			if fieldIndex < 0 || fieldIndex >= len(strct.Fields) || sourceFieldIndex < 0 || sourceFieldIndex >= len(strct.Fields) {
				return nil, fmt.Errorf("vm: field index out of range in %q", function.Name)
			}
			strct.Fields[fieldIndex].Value = runtime.IntValue(strct.Fields[fieldIndex].Value.Int() + strct.Fields[sourceFieldIndex].Value.Int())
		case bytecode.OpArrayElemAddIntConst:
			baseSlot, indexSlot := inst.A, inst.B
			if baseSlot < 0 || baseSlot >= len(frame.locals) || indexSlot < 0 || indexSlot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot out of range in %q", function.Name)
			}
			cell, err := indexedCell(frame.locals[baseSlot].Value, frame.locals[indexSlot].Value.Int())
			if err != nil {
				return nil, fmt.Errorf("vm: %w in %q", err, function.Name)
			}
			cell.Value = runtime.IntValue(cell.Value.Int() + inst.I64)
		case bytecode.OpArrayElemAddLocal:
			baseSlot, indexSlot := inst.A, inst.B
			sourceSlot := int(inst.I64)
			if baseSlot < 0 || baseSlot >= len(frame.locals) || indexSlot < 0 || indexSlot >= len(frame.locals) || sourceSlot < 0 || sourceSlot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot out of range in %q", function.Name)
			}
			cell, err := indexedCell(frame.locals[baseSlot].Value, frame.locals[indexSlot].Value.Int())
			if err != nil {
				return nil, fmt.Errorf("vm: %w in %q", err, function.Name)
			}
			cell.Value = runtime.IntValue(cell.Value.Int() + frame.locals[sourceSlot].Value.Int())
		case bytecode.OpArrayElemAddStructField:
			baseSlot, indexSlot := inst.A, inst.B
			sourceSlot := int(inst.I64)
			if baseSlot < 0 || baseSlot >= len(frame.locals) || indexSlot < 0 || indexSlot >= len(frame.locals) || sourceSlot < 0 || sourceSlot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot out of range in %q", function.Name)
			}
			fieldIndex, err := strconv.Atoi(inst.S)
			if err != nil {
				return nil, fmt.Errorf("vm: invalid struct field index %q in %q", inst.S, function.Name)
			}
			cell, err := indexedCell(frame.locals[baseSlot].Value, frame.locals[indexSlot].Value.Int())
			if err != nil {
				return nil, fmt.Errorf("vm: %w in %q", err, function.Name)
			}
			strct, ok := frame.locals[sourceSlot].Value.Object().(*runtime.Struct)
			if frame.locals[sourceSlot].Value.Kind != runtime.ObjectKind || !ok || strct == nil {
				return nil, fmt.Errorf("vm: expected struct local in %q", function.Name)
			}
			if fieldIndex < 0 || fieldIndex >= len(strct.Fields) {
				return nil, fmt.Errorf("vm: field index %d out of range in %q", fieldIndex, function.Name)
			}
			cell.Value = runtime.IntValue(cell.Value.Int() + strct.Fields[fieldIndex].Value.Int())
		case bytecode.OpLoadStructFieldLocal:
			baseSlot, fieldIndex := inst.A, inst.B
			if baseSlot < 0 || baseSlot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot %d out of range in %q", baseSlot, function.Name)
			}
			strct, ok := frame.locals[baseSlot].Value.Object().(*runtime.Struct)
			if frame.locals[baseSlot].Value.Kind != runtime.ObjectKind || !ok || strct == nil {
				return nil, fmt.Errorf("vm: expected struct local in %q", function.Name)
			}
			if fieldIndex < 0 || fieldIndex >= len(strct.Fields) {
				return nil, fmt.Errorf("vm: field index %d out of range in %q", fieldIndex, function.Name)
			}
			frame.push(strct.Fields[fieldIndex].Value)
		case bytecode.OpLoadArrayElemLocalConst:
			baseSlot := inst.A
			if baseSlot < 0 || baseSlot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot %d out of range in %q", baseSlot, function.Name)
			}
			cell, err := indexedCell(frame.locals[baseSlot].Value, inst.I64)
			if err != nil {
				return nil, fmt.Errorf("vm: %w in %q", err, function.Name)
			}
			frame.push(cell.Value)
		case bytecode.OpLocalAddStructField:
			targetSlot, baseSlot := inst.A, inst.B
			fieldIndex := int(inst.I64)
			if targetSlot < 0 || targetSlot >= len(frame.locals) || baseSlot < 0 || baseSlot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot out of range in %q", function.Name)
			}
			strct, ok := frame.locals[baseSlot].Value.Object().(*runtime.Struct)
			if frame.locals[baseSlot].Value.Kind != runtime.ObjectKind || !ok || strct == nil {
				return nil, fmt.Errorf("vm: expected struct local in %q", function.Name)
			}
			if fieldIndex < 0 || fieldIndex >= len(strct.Fields) {
				return nil, fmt.Errorf("vm: field index %d out of range in %q", fieldIndex, function.Name)
			}
			frame.locals[targetSlot].Value = runtime.IntValue(frame.locals[targetSlot].Value.Int() + strct.Fields[fieldIndex].Value.Int())
		case bytecode.OpLocalAddArrayElemConst:
			targetSlot, baseSlot := inst.A, inst.B
			if targetSlot < 0 || targetSlot >= len(frame.locals) || baseSlot < 0 || baseSlot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot out of range in %q", function.Name)
			}
			cell, err := indexedCell(frame.locals[baseSlot].Value, inst.I64)
			if err != nil {
				return nil, fmt.Errorf("vm: %w in %q", err, function.Name)
			}
			frame.locals[targetSlot].Value = runtime.IntValue(frame.locals[targetSlot].Value.Int() + cell.Value.Int())
		case bytecode.OpLoadGlobal:
			frame.push(m.globals[inst.A].Value)
		case bytecode.OpStoreGlobal:
			value, err := frame.pop()
			if err != nil {
				return nil, err
			}
			m.globals[inst.A].Value = runtime.CloneForStore(value)
		case bytecode.OpAdd:
			if err := m.execAdd(frame); err != nil {
				return nil, err
			}
		case bytecode.OpSub:
			if err := m.execSub(frame); err != nil {
				return nil, err
			}
		case bytecode.OpMul:
			if err := m.execMul(frame); err != nil {
				return nil, err
			}
		case bytecode.OpDiv:
			if err := m.execDiv(frame); err != nil {
				return nil, err
			}
		case bytecode.OpNeg:
			if err := m.execNeg(frame); err != nil {
				return nil, err
			}
		case bytecode.OpNot:
			value, err := frame.pop()
			if err != nil {
				return nil, err
			}
			if value.Kind != runtime.Bool {
				return nil, fmt.Errorf("vm: expected bool value in %q, got kind %d", function.Name, value.Kind)
			}
			frame.push(runtime.BoolValue(!value.Bool()))
		case bytecode.OpModInt:
			right, left, kind, err := popIntegerPair(frame)
			if err != nil {
				return nil, err
			}
			if right == 0 {
				return nil, fmt.Errorf("vm: modulo by zero in %q", function.Name)
			}
			if kind == runtime.Int64 {
				frame.push(runtime.Int64Value(left % right))
			} else {
				frame.push(runtime.IntValue(left % right))
			}
		case bytecode.OpBitAnd:
			if err := execBitwise(frame, func(left, right int64) int64 { return left & right }); err != nil {
				return nil, err
			}
		case bytecode.OpBitOr:
			if err := execBitwise(frame, func(left, right int64) int64 { return left | right }); err != nil {
				return nil, err
			}
		case bytecode.OpBitXor:
			if err := execBitwise(frame, func(left, right int64) int64 { return left ^ right }); err != nil {
				return nil, err
			}
		case bytecode.OpBitClear:
			if err := execBitwise(frame, func(left, right int64) int64 { return left &^ right }); err != nil {
				return nil, err
			}
		case bytecode.OpShiftLeft:
			if err := execBitwise(frame, func(left, right int64) int64 { return left << uint(right) }); err != nil {
				return nil, err
			}
		case bytecode.OpShiftRight:
			if err := execBitwise(frame, func(left, right int64) int64 { return left >> uint(right) }); err != nil {
				return nil, err
			}
		case bytecode.OpCmpEq:
			if err := m.execCmpEq(frame); err != nil {
				return nil, err
			}
		case bytecode.OpCmpLt:
			if err := execOrderedCmp(frame, func(cmp int) bool { return cmp < 0 }); err != nil {
				return nil, err
			}
		case bytecode.OpCmpLe:
			if err := execOrderedCmp(frame, func(cmp int) bool { return cmp <= 0 }); err != nil {
				return nil, err
			}
		case bytecode.OpCmpGt:
			if err := execOrderedCmp(frame, func(cmp int) bool { return cmp > 0 }); err != nil {
				return nil, err
			}
		case bytecode.OpCmpGe:
			if err := execOrderedCmp(frame, func(cmp int) bool { return cmp >= 0 }); err != nil {
				return nil, err
			}
		case bytecode.OpAndAnd:
			rv, err := frame.pop()
			if err != nil {
				return nil, err
			}
			lv, err := frame.pop()
			if err != nil {
				return nil, err
			}
			if rv.Kind != runtime.Bool || lv.Kind != runtime.Bool {
				got := rv.Kind
				if lv.Kind != runtime.Bool {
					got = lv.Kind
				}
				return nil, fmt.Errorf("vm: expected bool value in %q, got kind %d", function.Name, got)
			}
			frame.push(runtime.BoolValue(lv.Bool() && rv.Bool()))
		case bytecode.OpOrOr:
			rv, err := frame.pop()
			if err != nil {
				return nil, err
			}
			lv, err := frame.pop()
			if err != nil {
				return nil, err
			}
			if rv.Kind != runtime.Bool || lv.Kind != runtime.Bool {
				got := rv.Kind
				if lv.Kind != runtime.Bool {
					got = lv.Kind
				}
				return nil, fmt.Errorf("vm: expected bool value in %q, got kind %d", function.Name, got)
			}
			frame.push(runtime.BoolValue(lv.Bool() || rv.Bool()))
		case bytecode.OpJump:
			if inst.A < 0 || inst.A > len(function.Code) {
				return nil, fmt.Errorf("vm: jump target %d out of range in %q", inst.A, function.Name)
			}
			frame.ip = inst.A
		case bytecode.OpJumpIfFalse:
			condVal, err := frame.pop()
			if err != nil {
				return nil, err
			}
			if condVal.Kind != runtime.Bool {
				return nil, fmt.Errorf("vm: expected bool value in %q, got kind %d", function.Name, condVal.Kind)
			}
			if !condVal.Bool() {
				if inst.A < 0 || inst.A > len(function.Code) {
					return nil, fmt.Errorf("vm: jump target %d out of range in %q", inst.A, function.Name)
				}
				frame.ip = inst.A
			}
		case bytecode.OpJumpIfLocalIntLtConstFalse:
			slot := inst.B
			if slot < 0 || slot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot %d out of range in %q", slot, function.Name)
			}
			if !(frame.locals[slot].Value.Int() < inst.I64) {
				if inst.A < 0 || inst.A > len(function.Code) {
					return nil, fmt.Errorf("vm: jump target %d out of range in %q", inst.A, function.Name)
				}
				frame.ip = inst.A
			}
		case bytecode.OpJumpIfLocalBitAndIntEqConstFalse:
			slot := inst.B
			if slot < 0 || slot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot %d out of range in %q", slot, function.Name)
			}
			expected, err := strconv.ParseInt(inst.S, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("vm: invalid fused jump constant %q in %q", inst.S, function.Name)
			}
			if (frame.locals[slot].Value.Int() & inst.I64) != expected {
				if inst.A < 0 || inst.A > len(function.Code) {
					return nil, fmt.Errorf("vm: jump target %d out of range in %q", inst.A, function.Name)
				}
				frame.ip = inst.A
			}
		case bytecode.OpCall:
			if len(frame.stack) < inst.B {
				return nil, fmt.Errorf("vm: stack underflow in %q", function.Name)
			}
			base := len(frame.stack) - inst.B
			args := frame.stack[base:]
			frame.stack = frame.stack[:base]
			results, err := m.runFunction(inst.A, args, nil)
			if err != nil {
				return nil, err
			}
			for _, result := range results {
				frame.push(result)
			}
		case bytecode.OpCallValue:
			if len(frame.stack) < inst.A+1 {
				return nil, fmt.Errorf("vm: stack underflow in %q", function.Name)
			}
			base := len(frame.stack) - inst.A
			args := frame.stack[base:]
			callee := frame.stack[base-1]
			frame.stack = frame.stack[:base-1]
			if callee.Kind != runtime.ObjectKind || callee.Object() == nil {
				return nil, fmt.Errorf("vm: expected closure value in %q", function.Name)
			}
			switch fn := callee.Object().(type) {
			case *runtime.Closure:
				if fn == nil {
					return nil, fmt.Errorf("vm: expected closure value in %q", function.Name)
				}
				results, err := m.runFunction(fn.FuncIndex, args, fn.Captures)
				if err != nil {
					return nil, err
				}
				for _, result := range results {
					frame.push(result)
				}
			case *runtime.MethodValue:
				if fn == nil {
					return nil, fmt.Errorf("vm: expected closure value in %q", function.Name)
				}
				callArgs := m.allocArgs(len(args) + 1)
				callArgs[0] = runtime.CloneForStore(fn.Receiver)
				copy(callArgs[1:], args)
				results, err := m.runFunction(fn.FuncIndex, callArgs, nil)
				m.freeArgSlice(callArgs)
				if err != nil {
					return nil, err
				}
				for _, result := range results {
					frame.push(result)
				}
			default:
				return nil, fmt.Errorf("vm: expected closure value in %q", function.Name)
			}
		case bytecode.OpMakeStruct:
			fields := make([]runtime.Cell, inst.A)
			for i := inst.A - 1; i >= 0; i-- {
				value, err := frame.pop()
				if err != nil {
					return nil, err
				}
				fields[i].Value = runtime.CloneForStore(value)
			}
			frame.push(runtime.ObjectValue(&runtime.Struct{TypeName: inst.S, Fields: fields}))
		case bytecode.OpMakeArray:
			elems := make([]runtime.Cell, inst.A)
			for i := inst.A - 1; i >= 0; i-- {
				value, err := frame.pop()
				if err != nil {
					return nil, err
				}
				elems[i].Value = runtime.CloneForStore(value)
			}
			frame.push(runtime.ObjectValue(&runtime.Array{Elements: elems}))
		case bytecode.OpMakeSlice:
			elems := make([]runtime.Cell, inst.A)
			for i := inst.A - 1; i >= 0; i-- {
				value, err := frame.pop()
				if err != nil {
					return nil, err
				}
				elems[i].Value = runtime.CloneForStore(value)
			}
			frame.push(runtime.ObjectValue(&runtime.Slice{Elements: elems}))
		case bytecode.OpMakeSliceLenCap:
			var capVal runtime.Value
			var err error
			if inst.B != 0 {
				capVal, err = frame.pop()
				if err != nil {
					return nil, err
				}
			}
			lenVal, err := frame.pop()
			if err != nil {
				return nil, err
			}
			if inst.B == 0 {
				capVal = lenVal
			}
			if !isIntegerValue(lenVal) || !isIntegerValue(capVal) {
				return nil, fmt.Errorf("vm: make slice length and capacity must be integers in %q", function.Name)
			}
			length, capacity := lenVal.Int(), capVal.Int()
			if length < 0 || capacity < 0 || length > capacity {
				return nil, fmt.Errorf("vm: invalid make slice length/capacity %d/%d in %q", length, capacity, function.Name)
			}
			elems := make([]runtime.Cell, int(length), int(capacity))
			var zero runtime.Value
			if inst.S != "" {
				schema, ok := m.program.StructSchemas[inst.S]
				if !ok {
					return nil, fmt.Errorf("vm: missing struct schema %q in %q", inst.S, function.Name)
				}
				fields := make([]runtime.Cell, len(schema.FieldKinds))
				for i, fieldKind := range schema.FieldKinds {
					fields[i].Value = zeroValueForKind(int(fieldKind))
				}
				zero = runtime.ObjectValue(&runtime.Struct{TypeName: schema.Name, Fields: fields})
			} else {
				zero = zeroValueForKind(inst.A)
			}
			for i := range elems {
				elems[i].Value = runtime.CloneForStore(zero)
			}
			frame.push(runtime.ObjectValue(&runtime.Slice{Elements: elems}))
		case bytecode.OpAppendSlice:
			if inst.B != 0 {
				source, err := frame.pop()
				if err != nil {
					return nil, err
				}
				target, err := frame.pop()
				if err != nil {
					return nil, err
				}
				result, err := appendSliceExpansion(target, source)
				if err != nil {
					return nil, fmt.Errorf("vm: %w in %q", err, function.Name)
				}
				frame.push(result)
				continue
			}
			values := make([]runtime.Value, inst.A)
			for i := inst.A - 1; i >= 0; i-- {
				value, err := frame.pop()
				if err != nil {
					return nil, err
				}
				values[i] = value
			}
			target, err := frame.pop()
			if err != nil {
				return nil, err
			}
			result, err := appendSliceValues(target, values)
			if err != nil {
				return nil, fmt.Errorf("vm: %w in %q", err, function.Name)
			}
			frame.push(result)
		case bytecode.OpLoadField:
			obj, err := frame.pop()
			if err != nil {
				return nil, err
			}
			strct, ok := obj.Object().(*runtime.Struct)
			if !ok || strct == nil {
				return nil, fmt.Errorf("vm: expected struct for field access in %q", function.Name)
			}
			if inst.A < 0 || inst.A >= len(strct.Fields) {
				return nil, fmt.Errorf("vm: field index %d out of range in %q", inst.A, function.Name)
			}
			frame.push(strct.Fields[inst.A].Value)
		case bytecode.OpAddrField:
			obj, err := frame.pop()
			if err != nil {
				return nil, err
			}
			strct, ok := obj.Object().(*runtime.Struct)
			if !ok || strct == nil {
				return nil, fmt.Errorf("vm: expected struct for field address in %q", function.Name)
			}
			if inst.A < 0 || inst.A >= len(strct.Fields) {
				return nil, fmt.Errorf("vm: field index %d out of range in %q", inst.A, function.Name)
			}
			frame.push(runtime.PointerValue(&strct.Fields[inst.A]))
		case bytecode.OpLoadIndex:
			indexVal, err := frame.pop()
			if err != nil {
				return nil, err
			}
			target, err := frame.pop()
			if err != nil {
				return nil, err
			}
			if indexVal.Kind != runtime.Int && indexVal.Kind != runtime.Int64 {
				return nil, fmt.Errorf("vm: expected int value in %q, got kind %d", function.Name, indexVal.Kind)
			}
			cell, err := indexedCell(target, indexVal.Int())
			if err != nil {
				return nil, fmt.Errorf("vm: %w in %q", err, function.Name)
			}
			frame.push(cell.Value)
		case bytecode.OpSlice:
			var maxVal runtime.Value
			var highVal runtime.Value
			var lowVal runtime.Value
			var err error
			if inst.I64 != 0 {
				maxVal, err = frame.pop()
				if err != nil {
					return nil, err
				}
			}
			if inst.B != 0 {
				highVal, err = frame.pop()
				if err != nil {
					return nil, err
				}
			}
			if inst.A != 0 {
				lowVal, err = frame.pop()
				if err != nil {
					return nil, err
				}
			}
			target, err := frame.pop()
			if err != nil {
				return nil, err
			}
			if inst.A == 0 {
				lowVal = runtime.IntValue(0)
			}
			length, capacity := 0, 0
			switch target.Kind {
			case runtime.Nil:
			case runtime.String:
				length = len(target.String())
				capacity = length
			case runtime.ObjectKind:
				switch obj := target.Object().(type) {
				case *runtime.Array:
					if obj != nil {
						length = len(obj.Elements)
						capacity = cap(obj.Elements)
					}
				case *runtime.Slice:
					if obj != nil {
						length = len(obj.Elements)
						capacity = cap(obj.Elements)
					}
				default:
					return nil, fmt.Errorf("vm: cannot slice value in %q", function.Name)
				}
			default:
				return nil, fmt.Errorf("vm: cannot slice kind %d in %q", target.Kind, function.Name)
			}
			if inst.B == 0 {
				highVal = runtime.IntValue(int64(length))
			}
			if inst.I64 == 0 {
				maxVal = runtime.IntValue(int64(capacity))
			}
			if !isIntegerValue(lowVal) || !isIntegerValue(highVal) || !isIntegerValue(maxVal) {
				return nil, fmt.Errorf("vm: slice bounds must be integers in %q", function.Name)
			}
			low, high, max := lowVal.Int(), highVal.Int(), maxVal.Int()
			if inst.I64 == 0 && high > int64(length) {
				return nil, fmt.Errorf("vm: invalid slice bounds [%d:%d] in %q", low, high, function.Name)
			}
			if low < 0 || high < low || max < high || max > int64(capacity) {
				return nil, fmt.Errorf("vm: invalid slice bounds [%d:%d:%d] in %q", low, high, max, function.Name)
			}
			if target.Kind == runtime.Nil {
				frame.push(runtime.NilValue())
				continue
			}
			if target.Kind == runtime.String {
				if inst.I64 != 0 {
					return nil, fmt.Errorf("vm: cannot use full slice expression on string in %q", function.Name)
				}
				s := target.String()
				frame.push(runtime.StringValue(s[int(low):int(high)]))
				continue
			}
			switch obj := target.Object().(type) {
			case *runtime.Array:
				if target.Kind != runtime.ObjectKind || obj == nil {
					return nil, fmt.Errorf("vm: invalid slice bounds [%d:%d:%d] in %q", low, high, max, function.Name)
				}
				elems := obj.Elements[int(low):int(high):int(max)]
				frame.push(runtime.ObjectValue(&runtime.Slice{Elements: elems}))
			case *runtime.Slice:
				if target.Kind != runtime.ObjectKind || obj == nil {
					frame.push(runtime.NilValue())
					continue
				}
				elems := obj.Elements[int(low):int(high):int(max)]
				frame.push(runtime.ObjectValue(&runtime.Slice{Elements: elems}))
			case nil:
				frame.push(runtime.NilValue())
			default:
				return nil, fmt.Errorf("vm: cannot slice value in %q", function.Name)
			}
		case bytecode.OpMapGetOK:
			key, err := frame.pop()
			if err != nil {
				return nil, err
			}
			target, err := frame.pop()
			if err != nil {
				return nil, err
			}
			if target.Kind == runtime.Nil || (target.Kind == runtime.ObjectKind && target.Object() == nil) {
				if inst.S != "" {
					schema, ok := m.program.StructSchemas[inst.S]
					if !ok {
						return nil, fmt.Errorf("vm: missing struct schema %q in %q", inst.S, function.Name)
					}
					fields := make([]runtime.Cell, len(schema.FieldKinds))
					for i, fieldKind := range schema.FieldKinds {
						fields[i].Value = zeroValueForKind(int(fieldKind))
					}
					frame.push(runtime.ObjectValue(&runtime.Struct{TypeName: schema.Name, Fields: fields}))
				} else {
					frame.push(zeroValueForKind(inst.A))
				}
				frame.push(runtime.BoolValue(false))
				continue
			}
			mm, ok := target.Object().(*runtime.Map)
			if target.Kind != runtime.ObjectKind || !ok || mm == nil {
				return nil, fmt.Errorf("vm: expected map for index in %q", function.Name)
			}
			encoded, err := runtime.EncodeMapKey(key)
			if err != nil {
				return nil, fmt.Errorf("vm: %w in %q", err, function.Name)
			}
			if entry, ok := mm.Entries[encoded]; ok {
				frame.push(runtime.CloneForStore(entry.Value))
				frame.push(runtime.BoolValue(true))
			} else {
				frame.push(runtime.CloneForStore(mm.Zero))
				frame.push(runtime.BoolValue(false))
			}
		case bytecode.OpAddrIndex:
			indexVal, err := frame.pop()
			if err != nil {
				return nil, err
			}
			target, err := frame.pop()
			if err != nil {
				return nil, err
			}
			if indexVal.Kind != runtime.Int && indexVal.Kind != runtime.Int64 {
				return nil, fmt.Errorf("vm: expected int value in %q, got kind %d", function.Name, indexVal.Kind)
			}
			cell, err := indexedCell(target, indexVal.Int())
			if err != nil {
				return nil, fmt.Errorf("vm: %w in %q", err, function.Name)
			}
			frame.push(runtime.PointerValue(cell))
		case bytecode.OpAddrLocal:
			if inst.A < 0 || inst.A >= len(frame.locals) {
				return nil, fmt.Errorf("vm: local slot %d out of range in %q", inst.A, function.Name)
			}
			frame.push(runtime.PointerValue(&frame.locals[inst.A]))
		case bytecode.OpAddrCapture:
			if inst.A < 0 || inst.A >= len(frame.captures) {
				return nil, fmt.Errorf("vm: capture slot %d out of range in %q", inst.A, function.Name)
			}
			frame.push(runtime.PointerValue(frame.captures[inst.A]))
		case bytecode.OpAddrGlobal:
			frame.push(runtime.PointerValue(&m.globals[inst.A]))
		case bytecode.OpDeref:
			ptrVal, err := frame.pop()
			if err != nil {
				return nil, err
			}
			if ptrVal.Kind != runtime.PointerKind || ptrVal.Pointer() == nil {
				return nil, fmt.Errorf("vm: expected pointer value in %q, got kind %d", function.Name, ptrVal.Kind)
			}
			frame.push(ptrVal.Pointer().Value)
		case bytecode.OpStoreIndirect:
			value, err := frame.pop()
			if err != nil {
				return nil, err
			}
			ptrVal, err := frame.pop()
			if err != nil {
				return nil, err
			}
			if ptrVal.Kind != runtime.PointerKind || ptrVal.Pointer() == nil {
				return nil, fmt.Errorf("vm: expected pointer value in %q", function.Name)
			}
			if runtime.NeedsCloneForStore(value) {
				ptrVal.Pointer().Value = runtime.CloneForStore(value)
			} else {
				ptrVal.Pointer().Value = value
			}
		case bytecode.OpMakeMap:
			frame.push(runtime.ObjectValue(&runtime.Map{
				Entries:   make(map[runtime.MapKey]runtime.MapEntry),
				Keys:      make([]runtime.MapKey, 0),
				KeysDirty: true,
				Zero:      zeroValueForKind(inst.A),
			}))
		case bytecode.OpMakeFuncValue:
			frame.push(runtime.ObjectValue(&runtime.Closure{FuncIndex: inst.A, Captures: nil}))
		case bytecode.OpMakeMethodValue:
			recv, err := frame.pop()
			if err != nil {
				return nil, err
			}
			frame.push(runtime.ObjectValue(&runtime.MethodValue{FuncIndex: inst.A, Receiver: runtime.CloneForStore(recv)}))
		case bytecode.OpMakeClosure:
			captures := make([]*runtime.Cell, inst.B)
			for i := inst.B - 1; i >= 0; i-- {
				ptr, err := frame.popPointer()
				if err != nil {
					return nil, err
				}
				captures[i] = ptr
			}
			frame.push(runtime.ObjectValue(&runtime.Closure{FuncIndex: inst.A, Captures: captures}))
		case bytecode.OpMapSet:
			value, err := frame.pop()
			if err != nil {
				return nil, err
			}
			key, err := frame.pop()
			if err != nil {
				return nil, err
			}
			target, err := frame.pop()
			if err != nil {
				return nil, err
			}
			mm, ok := target.Object().(*runtime.Map)
			if target.Kind != runtime.ObjectKind || !ok || mm == nil {
				return nil, fmt.Errorf("vm: expected map for assignment in %q", function.Name)
			}
			encoded, err := runtime.EncodeMapKey(key)
			if err != nil {
				return nil, fmt.Errorf("vm: %w in %q", err, function.Name)
			}
			mm.Entries[encoded] = runtime.MapEntry{Key: runtime.CloneForStore(key), Value: runtime.CloneForStore(value)}
			mm.KeysDirty = true
		case bytecode.OpMapGet:
			key, err := frame.pop()
			if err != nil {
				return nil, err
			}
			target, err := frame.pop()
			if err != nil {
				return nil, err
			}
			if target.Kind == runtime.Nil || (target.Kind == runtime.ObjectKind && target.Object() == nil) {
				if inst.S != "" {
					schema, ok := m.program.StructSchemas[inst.S]
					if !ok {
						return nil, fmt.Errorf("vm: missing struct schema %q in %q", inst.S, function.Name)
					}
					fields := make([]runtime.Cell, len(schema.FieldKinds))
					for i, fieldKind := range schema.FieldKinds {
						fields[i].Value = zeroValueForKind(int(fieldKind))
					}
					frame.push(runtime.ObjectValue(&runtime.Struct{TypeName: schema.Name, Fields: fields}))
				} else {
					frame.push(zeroValueForKind(inst.A))
				}
				continue
			}
			mm, ok := target.Object().(*runtime.Map)
			if target.Kind != runtime.ObjectKind || !ok || mm == nil {
				return nil, fmt.Errorf("vm: expected map for index in %q", function.Name)
			}
			encoded, err := runtime.EncodeMapKey(key)
			if err != nil {
				return nil, fmt.Errorf("vm: %w in %q", err, function.Name)
			}
			if entry, ok := mm.Entries[encoded]; ok {
				frame.push(runtime.CloneForStore(entry.Value))
			} else {
				frame.push(runtime.CloneForStore(mm.Zero))
			}
		case bytecode.OpMapDelete:
			key, err := frame.pop()
			if err != nil {
				return nil, err
			}
			target, err := frame.pop()
			if err != nil {
				return nil, err
			}
			mm, ok := target.Object().(*runtime.Map)
			if target.Kind == runtime.Nil || (target.Kind == runtime.ObjectKind && target.Object() == nil) {
				continue
			}
			if target.Kind != runtime.ObjectKind || !ok || mm == nil {
				return nil, fmt.Errorf("vm: expected map for delete in %q", function.Name)
			}
			encoded, err := runtime.EncodeMapKey(key)
			if err != nil {
				return nil, fmt.Errorf("vm: %w in %q", err, function.Name)
			}
			delete(mm.Entries, encoded)
			mm.KeysDirty = true
		case bytecode.OpClear:
			target, err := frame.pop()
			if err != nil {
				return nil, err
			}
			if err := clearValue(target); err != nil {
				return nil, fmt.Errorf("vm: %w in %q", err, function.Name)
			}
		case bytecode.OpBuiltinLen:
			value, err := frame.pop()
			if err != nil {
				return nil, err
			}
			n, err := builtinLen(value)
			if err != nil {
				return nil, fmt.Errorf("vm: %w in %q", err, function.Name)
			}
			frame.push(runtime.IntValue(int64(n)))
		case bytecode.OpRangeInitIndexed:
			value, err := frame.pop()
			if err != nil {
				return nil, err
			}
			sourceSlot, indexSlot := inst.A, inst.B
			if sourceSlot < 0 || sourceSlot >= len(frame.locals) || indexSlot < 0 || indexSlot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: range local slot out of range in %q", function.Name)
			}
			frame.locals[sourceSlot].Value = value
			frame.locals[indexSlot].Value = runtime.IntValue(-1)
		case bytecode.OpRangeNextIndexed:
			sourceSlot, indexSlot := inst.A, inst.B
			keySlot := int(int32(uint32(uint64(inst.I64) >> 32)))
			valueSlot := int(int32(uint32(inst.I64)))
			if sourceSlot < 0 || sourceSlot >= len(frame.locals) ||
				indexSlot < 0 || indexSlot >= len(frame.locals) ||
				keySlot < 0 || keySlot >= len(frame.locals) ||
				valueSlot < 0 || valueSlot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: range local slot out of range in %q", function.Name)
			}
			index := frame.locals[indexSlot].Value.Int() + 1
			source := frame.locals[sourceSlot].Value
			switch source.Kind {
			case runtime.Nil:
				frame.push(runtime.BoolValue(false))
			case runtime.Int, runtime.Int64:
				if index >= source.Int() {
					frame.push(runtime.BoolValue(false))
					continue
				}
				frame.locals[indexSlot].Value = runtime.IntValue(index)
				if source.Kind == runtime.Int64 {
					frame.locals[keySlot].Value = runtime.Int64Value(index)
				} else {
					frame.locals[keySlot].Value = runtime.IntValue(index)
				}
				frame.locals[valueSlot].Value = runtime.NilValue()
				frame.push(runtime.BoolValue(true))
			case runtime.ObjectKind:
				switch obj := source.Object().(type) {
				case *runtime.Array:
					if obj == nil || index >= int64(len(obj.Elements)) {
						frame.push(runtime.BoolValue(false))
						continue
					}
					frame.locals[indexSlot].Value = runtime.IntValue(index)
					frame.locals[keySlot].Value = runtime.IntValue(index)
					frame.locals[valueSlot].Value = runtime.CloneForStore(obj.Elements[index].Value)
					frame.push(runtime.BoolValue(true))
				case *runtime.Slice:
					if obj == nil || index >= int64(len(obj.Elements)) {
						frame.push(runtime.BoolValue(false))
						continue
					}
					frame.locals[indexSlot].Value = runtime.IntValue(index)
					frame.locals[keySlot].Value = runtime.IntValue(index)
					frame.locals[valueSlot].Value = runtime.CloneForStore(obj.Elements[index].Value)
					frame.push(runtime.BoolValue(true))
				default:
					return nil, fmt.Errorf("vm: expected indexed range source in %q", function.Name)
				}
			default:
				return nil, fmt.Errorf("vm: expected indexed range source in %q", function.Name)
			}
		case bytecode.OpRangeInitMap:
			value, err := frame.pop()
			if err != nil {
				return nil, err
			}
			sourceSlot, indexSlot := inst.A, inst.B
			if sourceSlot < 0 || sourceSlot >= len(frame.locals) || indexSlot < 0 || indexSlot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: range local slot out of range in %q", function.Name)
			}
			if value.Kind != runtime.Nil {
				if value.Kind != runtime.ObjectKind {
					return nil, fmt.Errorf("vm: expected map range source in %q", function.Name)
				}
				if obj := value.Object(); obj != nil {
					mm, ok := obj.(*runtime.Map)
					if !ok {
						return nil, fmt.Errorf("vm: expected map range source in %q", function.Name)
					}
					if mm.KeysDirty {
						mm.Keys = mm.Keys[:0]
						for key := range mm.Entries {
							mm.Keys = append(mm.Keys, key)
						}
						mm.KeysDirty = false
					}
				}
			}
			frame.locals[sourceSlot].Value = value
			frame.locals[indexSlot].Value = runtime.IntValue(0)
		case bytecode.OpRangeNextMap:
			sourceSlot, indexSlot := inst.A, inst.B
			keySlot := int(int32(uint32(uint64(inst.I64) >> 32)))
			valueSlot := int(int32(uint32(inst.I64)))
			if sourceSlot < 0 || sourceSlot >= len(frame.locals) ||
				indexSlot < 0 || indexSlot >= len(frame.locals) ||
				keySlot < 0 || keySlot >= len(frame.locals) ||
				valueSlot < 0 || valueSlot >= len(frame.locals) {
				return nil, fmt.Errorf("vm: range local slot out of range in %q", function.Name)
			}
			source := frame.locals[sourceSlot].Value
			if source.Kind == runtime.Nil || (source.Kind == runtime.ObjectKind && source.Object() == nil) {
				frame.push(runtime.BoolValue(false))
				continue
			}
			mm, ok := source.Object().(*runtime.Map)
			if source.Kind != runtime.ObjectKind || !ok || mm == nil {
				return nil, fmt.Errorf("vm: expected map range source in %q", function.Name)
			}
			index := int(frame.locals[indexSlot].Value.Int())
			found := false
			for index < len(mm.Keys) {
				key := mm.Keys[index]
				index++
				entry, ok := mm.Entries[key]
				if !ok {
					continue
				}
				frame.locals[indexSlot].Value = runtime.IntValue(int64(index))
				frame.locals[keySlot].Value = runtime.CloneForStore(entry.Key)
				frame.locals[valueSlot].Value = runtime.CloneForStore(entry.Value)
				frame.push(runtime.BoolValue(true))
				found = true
				break
			}
			if !found {
				frame.locals[indexSlot].Value = runtime.IntValue(int64(index))
				frame.push(runtime.BoolValue(false))
			}
		case bytecode.OpCallHost:
			if len(frame.stack) < inst.B {
				return nil, fmt.Errorf("vm: stack underflow in %q", function.Name)
			}
			base := len(frame.stack) - inst.B
			args := frame.stack[base:]
			frame.stack = frame.stack[:base]
			if inst.A < 0 || inst.A >= len(m.program.HostFunctions) {
				return nil, fmt.Errorf("vm: host function index %d out of range in %q", inst.A, function.Name)
			}
			name := m.program.HostFunctions[inst.A].Name
			if m.host == nil {
				return nil, fmt.Errorf("vm: host function %q is not registered", name)
			}
			fn, ok := m.host.Lookup(name)
			if !ok {
				return nil, fmt.Errorf("vm: host function %q is not registered", name)
			}
			results, err := fn(m.hostCtx, args)
			if err != nil {
				var yield *runtime.YieldRequest
				if errors.As(err, &yield) {
					return nil, m.suspend(yield.Payload, name, m.program.HostFunctions[inst.A].Results)
				}
				return nil, fmt.Errorf("vm: host function %q failed: %w", name, err)
			}
			for _, result := range results {
				frame.push(result)
			}
		case bytecode.OpConvertInt:
			value, err := frame.pop()
			if err != nil {
				return nil, err
			}
			switch value.Kind {
			case runtime.Int:
				frame.push(value)
			case runtime.Int64:
				frame.push(runtime.IntValue(value.Int()))
			case runtime.Float64:
				frame.push(runtime.IntValue(int64(value.Float64())))
			case runtime.String:
				parsed, err := strconv.ParseInt(value.String(), 10, 64)
				if err != nil {
					return nil, fmt.Errorf("vm: cannot convert %q to int in %q: %w", value.String(), function.Name, err)
				}
				frame.push(runtime.IntValue(parsed))
			default:
				return nil, fmt.Errorf("vm: cannot convert kind %d to int in %q", value.Kind, function.Name)
			}
		case bytecode.OpConvertInt64:
			value, err := frame.pop()
			if err != nil {
				return nil, err
			}
			switch value.Kind {
			case runtime.Int, runtime.Int64:
				frame.push(runtime.Int64Value(value.Int()))
			case runtime.Float64:
				frame.push(runtime.Int64Value(int64(value.Float64())))
			case runtime.String:
				parsed, err := strconv.ParseInt(value.String(), 10, 64)
				if err != nil {
					return nil, fmt.Errorf("vm: cannot convert %q to int64 in %q: %w", value.String(), function.Name, err)
				}
				frame.push(runtime.Int64Value(parsed))
			default:
				return nil, fmt.Errorf("vm: cannot convert kind %d to int64 in %q", value.Kind, function.Name)
			}
		case bytecode.OpConvertFloat64:
			value, err := frame.pop()
			if err != nil {
				return nil, err
			}
			switch value.Kind {
			case runtime.Int, runtime.Int64:
				frame.push(runtime.Float64Value(float64(value.Int())))
			case runtime.Float64:
				frame.push(value)
			case runtime.String:
				parsed, err := strconv.ParseFloat(value.String(), 64)
				if err != nil {
					return nil, fmt.Errorf("vm: cannot convert %q to float64 in %q: %w", value.String(), function.Name, err)
				}
				frame.push(runtime.Float64Value(parsed))
			default:
				return nil, fmt.Errorf("vm: cannot convert kind %d to float64 in %q", value.Kind, function.Name)
			}
		case bytecode.OpConvertString:
			value, err := frame.pop()
			if err != nil {
				return nil, err
			}
			switch value.Kind {
			case runtime.String:
				frame.push(value)
			case runtime.Int, runtime.Int64:
				frame.push(runtime.StringValue(strconv.FormatInt(value.Int(), 10)))
			case runtime.Float64:
				frame.push(runtime.StringValue(strconv.FormatFloat(value.Float64(), 'g', -1, 64)))
			default:
				return nil, fmt.Errorf("vm: cannot convert kind %d to string in %q", value.Kind, function.Name)
			}
		case bytecode.OpPop:
			if _, err := frame.pop(); err != nil {
				return nil, err
			}
		case bytecode.OpReturn:
			results := make([]runtime.Value, function.Results)
			for i := function.Results - 1; i >= 0; i-- {
				value, err := frame.pop()
				if err != nil {
					return nil, err
				}
				results[i] = value
			}
			return results, nil
		default:
			return nil, fmt.Errorf("vm: unsupported opcode %d in %q", inst.Op, function.Name)
		}
	}

	return nil, fmt.Errorf("vm: function %q terminated without return", function.Name)
}

func (m *vm) allocArgs(n int) []runtime.Value {
	for i := len(m.freeArgs) - 1; i >= 0; i-- {
		buf := m.freeArgs[i]
		if cap(buf) >= n {
			m.freeArgs = append(m.freeArgs[:i], m.freeArgs[i+1:]...)
			return buf[:n]
		}
	}
	return make([]runtime.Value, n)
}

func (m *vm) freeArgSlice(args []runtime.Value) {
	for i := range args {
		args[i] = runtime.Value{}
	}
	m.freeArgs = append(m.freeArgs, args[:0])
}

func zeroValueForKind(kind int) runtime.Value {
	switch runtime.Kind(kind) {
	case runtime.Nil:
		return runtime.NilValue()
	case runtime.Bool:
		return runtime.BoolValue(false)
	case runtime.String:
		return runtime.StringValue("")
	case runtime.Int64:
		return runtime.Int64Value(0)
	case runtime.Float64:
		return runtime.Float64Value(0)
	default:
		return runtime.IntValue(0)
	}
}

func isIntegerValue(value runtime.Value) bool {
	return value.Kind == runtime.Int || value.Kind == runtime.Int64
}

func appendSliceValues(target runtime.Value, values []runtime.Value) (runtime.Value, error) {
	var elems []runtime.Cell
	if target.Kind == runtime.Nil {
		elems = nil
	} else {
		obj, ok := target.Object().(*runtime.Slice)
		if target.Kind != runtime.ObjectKind || !ok || obj == nil {
			return runtime.Value{}, fmt.Errorf("append first argument must be slice")
		}
		elems = obj.Elements
	}
	for _, value := range values {
		elems = append(elems, runtime.Cell{Value: runtime.CloneForStore(value)})
	}
	return runtime.ObjectValue(&runtime.Slice{Elements: elems}), nil
}

func appendSliceExpansion(target runtime.Value, source runtime.Value) (runtime.Value, error) {
	var sourceElems []runtime.Cell
	if source.Kind == runtime.Nil {
		sourceElems = nil
	} else {
		sourceObj, ok := source.Object().(*runtime.Slice)
		if source.Kind != runtime.ObjectKind || !ok || sourceObj == nil {
			return runtime.Value{}, fmt.Errorf("append expansion argument must be slice")
		}
		sourceElems = sourceObj.Elements
	}
	values := make([]runtime.Value, len(sourceElems))
	for i, elem := range sourceElems {
		values[i] = elem.Value
	}
	return appendSliceValues(target, values)
}

func clearValue(target runtime.Value) error {
	if target.Kind == runtime.Nil || (target.Kind == runtime.ObjectKind && target.Object() == nil) {
		return nil
	}
	if target.Kind != runtime.ObjectKind {
		return fmt.Errorf("clear expects map or slice")
	}
	switch obj := target.Object().(type) {
	case *runtime.Map:
		if obj == nil {
			return nil
		}
		clear(obj.Entries)
		obj.Keys = obj.Keys[:0]
		obj.KeysDirty = false
		return nil
	case *runtime.Slice:
		if obj == nil {
			return nil
		}
		for i := range obj.Elements {
			obj.Elements[i].Value = zeroValueLike(obj.Elements[i].Value)
		}
		return nil
	default:
		return fmt.Errorf("clear expects map or slice")
	}
}

func zeroValueLike(value runtime.Value) runtime.Value {
	switch value.Kind {
	case runtime.ObjectKind:
		switch obj := value.Object().(type) {
		case *runtime.Struct:
			if obj == nil {
				return runtime.NilValue()
			}
			fields := make([]runtime.Cell, len(obj.Fields))
			for i, field := range obj.Fields {
				fields[i].Value = zeroValueLike(field.Value)
			}
			return runtime.ObjectValue(&runtime.Struct{TypeName: obj.TypeName, Fields: fields})
		case *runtime.Array:
			if obj == nil {
				return runtime.NilValue()
			}
			elems := make([]runtime.Cell, len(obj.Elements))
			for i, elem := range obj.Elements {
				elems[i].Value = zeroValueLike(elem.Value)
			}
			return runtime.ObjectValue(&runtime.Array{Elements: elems})
		default:
			return runtime.NilValue()
		}
	case runtime.PointerKind:
		return runtime.NilValue()
	default:
		return zeroValueForKind(int(value.Kind))
	}
}

func builtinLen(value runtime.Value) (int, error) {
	switch value.Kind {
	case runtime.Nil:
		return 0, nil
	case runtime.String:
		return len(value.String()), nil
	case runtime.ObjectKind:
		switch obj := value.Object().(type) {
		case *runtime.Array:
			return len(obj.Elements), nil
		case *runtime.Slice:
			if obj == nil {
				return 0, nil
			}
			return len(obj.Elements), nil
		case *runtime.Map:
			if obj == nil {
				return 0, nil
			}
			return len(obj.Entries), nil
		}
	}
	return 0, fmt.Errorf("len unsupported for kind %d", value.Kind)
}

func indexedCell(value runtime.Value, index int64) (*runtime.Cell, error) {
	if value.Kind != runtime.ObjectKind || value.Object() == nil {
		return nil, fmt.Errorf("expected indexable object")
	}
	if index < 0 {
		return nil, fmt.Errorf("index %d out of range", index)
	}

	switch obj := value.Object().(type) {
	case *runtime.Array:
		if int(index) >= len(obj.Elements) {
			return nil, fmt.Errorf("index %d out of range", index)
		}
		return &obj.Elements[index], nil
	case *runtime.Slice:
		if int(index) >= len(obj.Elements) {
			return nil, fmt.Errorf("index %d out of range", index)
		}
		return &obj.Elements[index], nil
	default:
		return nil, fmt.Errorf("expected array or slice")
	}
}

func popIntegerPair(frame *frame) (right int64, left int64, kind runtime.Kind, err error) {
	rv, err := frame.pop()
	if err != nil {
		return 0, 0, runtime.Invalid, err
	}
	lv, err := frame.pop()
	if err != nil {
		return 0, 0, runtime.Invalid, err
	}
	if rv.Kind == runtime.Int64 || lv.Kind == runtime.Int64 {
		if (rv.Kind != runtime.Int && rv.Kind != runtime.Int64) || (lv.Kind != runtime.Int && lv.Kind != runtime.Int64) {
			return 0, 0, runtime.Invalid, fmt.Errorf("expected integer operands in %q", frame.function.Name)
		}
		return rv.Int(), lv.Int(), runtime.Int64, nil
	}
	if rv.Kind != runtime.Int || lv.Kind != runtime.Int {
		return 0, 0, runtime.Invalid, fmt.Errorf("expected int operands in %q", frame.function.Name)
	}
	return rv.Int(), lv.Int(), runtime.Int, nil
}

func execBitwise(frame *frame, fn func(left, right int64) int64) error {
	right, left, kind, err := popIntegerPair(frame)
	if err != nil {
		return err
	}
	if kind == runtime.Int64 {
		frame.push(runtime.Int64Value(fn(left, right)))
	} else {
		frame.push(runtime.IntValue(fn(left, right)))
	}
	return nil
}

func (m *vm) execAdd(frame *frame) error {
	rv, err := frame.pop()
	if err != nil {
		return err
	}
	lv, err := frame.pop()
	if err != nil {
		return err
	}
	switch {
	case lv.Kind == runtime.String && rv.Kind == runtime.String:
		frame.push(runtime.StringValue(lv.String() + rv.String()))
	case lv.Kind == runtime.Float64 || rv.Kind == runtime.Float64:
		frame.push(runtime.Float64Value(asFloat(lv) + asFloat(rv)))
	case lv.Kind == runtime.Int64 || rv.Kind == runtime.Int64:
		frame.push(runtime.Int64Value(lv.Int() + rv.Int()))
	default:
		frame.push(runtime.IntValue(lv.Int() + rv.Int()))
	}
	return nil
}

func (m *vm) execSub(frame *frame) error {
	rv, err := frame.pop()
	if err != nil {
		return err
	}
	lv, err := frame.pop()
	if err != nil {
		return err
	}
	switch {
	case lv.Kind == runtime.Float64 || rv.Kind == runtime.Float64:
		frame.push(runtime.Float64Value(asFloat(lv) - asFloat(rv)))
	case lv.Kind == runtime.Int64 || rv.Kind == runtime.Int64:
		frame.push(runtime.Int64Value(lv.Int() - rv.Int()))
	default:
		frame.push(runtime.IntValue(lv.Int() - rv.Int()))
	}
	return nil
}

func (m *vm) execMul(frame *frame) error {
	rv, err := frame.pop()
	if err != nil {
		return err
	}
	lv, err := frame.pop()
	if err != nil {
		return err
	}
	switch {
	case lv.Kind == runtime.Float64 || rv.Kind == runtime.Float64:
		frame.push(runtime.Float64Value(asFloat(lv) * asFloat(rv)))
	case lv.Kind == runtime.Int64 || rv.Kind == runtime.Int64:
		frame.push(runtime.Int64Value(lv.Int() * rv.Int()))
	default:
		frame.push(runtime.IntValue(lv.Int() * rv.Int()))
	}
	return nil
}

func (m *vm) execDiv(frame *frame) error {
	rv, err := frame.pop()
	if err != nil {
		return err
	}
	lv, err := frame.pop()
	if err != nil {
		return err
	}
	if (rv.Kind == runtime.Float64 && rv.Float64() == 0) || ((rv.Kind == runtime.Int || rv.Kind == runtime.Int64) && rv.Int() == 0) {
		return fmt.Errorf("vm: division by zero in %q", frame.function.Name)
	}
	switch {
	case lv.Kind == runtime.Float64 || rv.Kind == runtime.Float64:
		frame.push(runtime.Float64Value(asFloat(lv) / asFloat(rv)))
	case lv.Kind == runtime.Int64 || rv.Kind == runtime.Int64:
		frame.push(runtime.Int64Value(lv.Int() / rv.Int()))
	default:
		frame.push(runtime.IntValue(lv.Int() / rv.Int()))
	}
	return nil
}

func (m *vm) execNeg(frame *frame) error {
	v, err := frame.pop()
	if err != nil {
		return err
	}
	switch v.Kind {
	case runtime.Float64:
		frame.push(runtime.Float64Value(-v.Float64()))
	case runtime.Int64:
		frame.push(runtime.Int64Value(-v.Int()))
	default:
		frame.push(runtime.IntValue(-v.Int()))
	}
	return nil
}

func (m *vm) execCmpEq(frame *frame) error {
	rv, err := frame.pop()
	if err != nil {
		return err
	}
	lv, err := frame.pop()
	if err != nil {
		return err
	}
	frame.push(runtime.BoolValue(valuesEqual(lv, rv)))
	return nil
}

func valuesEqual(lv, rv runtime.Value) bool {
	switch {
	case lv.Kind == runtime.Nil || rv.Kind == runtime.Nil:
		return isNilValue(lv) && isNilValue(rv)
	case lv.Kind == runtime.String && rv.Kind == runtime.String:
		return lv.String() == rv.String()
	case lv.Kind == runtime.Bool && rv.Kind == runtime.Bool:
		return lv.Bool() == rv.Bool()
	case lv.Kind == runtime.Float64 || rv.Kind == runtime.Float64:
		return asFloat(lv) == asFloat(rv)
	case lv.Kind == runtime.Int && rv.Kind == runtime.Int:
		return lv.Int() == rv.Int()
	case lv.Kind == runtime.Int64 && rv.Kind == runtime.Int64:
		return lv.Int() == rv.Int()
	case lv.Kind == runtime.PointerKind && rv.Kind == runtime.PointerKind:
		return lv.Pointer() == rv.Pointer()
	case lv.Kind == runtime.ObjectKind && rv.Kind == runtime.ObjectKind:
		return objectsEqual(lv.Object(), rv.Object())
	default:
		return lv.Int() == rv.Int()
	}
}

func objectsEqual(left, right runtime.Object) bool {
	switch left := left.(type) {
	case *runtime.Struct:
		right, ok := right.(*runtime.Struct)
		if !ok || left == nil || right == nil {
			return left == nil && right == nil
		}
		if left.TypeName != right.TypeName || len(left.Fields) != len(right.Fields) {
			return false
		}
		for i := range left.Fields {
			if !valuesEqual(left.Fields[i].Value, right.Fields[i].Value) {
				return false
			}
		}
		return true
	case *runtime.Array:
		right, ok := right.(*runtime.Array)
		if !ok || left == nil || right == nil {
			return left == nil && right == nil
		}
		if len(left.Elements) != len(right.Elements) {
			return false
		}
		for i := range left.Elements {
			if !valuesEqual(left.Elements[i].Value, right.Elements[i].Value) {
				return false
			}
		}
		return true
	case *runtime.Slice:
		return left == right
	case *runtime.Map:
		return left == right
	case *runtime.Closure:
		return left == right
	case *runtime.MethodValue:
		return left == right
	default:
		return left == right
	}
}

func isNilValue(v runtime.Value) bool {
	switch v.Kind {
	case runtime.Nil:
		return true
	case runtime.PointerKind:
		return v.Pointer() == nil
	case runtime.ObjectKind:
		return v.Object() == nil
	default:
		return false
	}
}

func execOrderedCmp(frame *frame, pred func(int) bool) error {
	if len(frame.stack) < 2 {
		return fmt.Errorf("vm: stack underflow in %q", frame.function.Name)
	}
	last := len(frame.stack) - 1
	rv := frame.stack[last]
	lv := frame.stack[last-1]
	frame.stack = frame.stack[:last-1]
	switch {
	case lv.Kind == runtime.String && rv.Kind == runtime.String:
		cmp := 0
		if lv.String() < rv.String() {
			cmp = -1
		} else if lv.String() > rv.String() {
			cmp = 1
		}
		frame.push(runtime.BoolValue(pred(cmp)))
	case lv.Kind == runtime.Float64 || rv.Kind == runtime.Float64:
		lf, rf := asFloat(lv), asFloat(rv)
		cmp := 0
		if lf < rf {
			cmp = -1
		} else if lf > rf {
			cmp = 1
		}
		frame.push(runtime.BoolValue(pred(cmp)))
	default:
		li, ri := lv.Int(), rv.Int()
		cmp := 0
		if li < ri {
			cmp = -1
		} else if li > ri {
			cmp = 1
		}
		frame.push(runtime.BoolValue(pred(cmp)))
	}
	return nil
}

func asFloat(v runtime.Value) float64 {
	switch v.Kind {
	case runtime.Float64:
		return v.Float64()
	default:
		return float64(v.Int())
	}
}
