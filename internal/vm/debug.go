package vm

import (
	"goes/internal/bytecode"
	"goes/runtime"
)

func (m *vm) maybeDebugBreak(function *bytecode.Function, frame *frame, inst bytecode.Instruction) error {
	if m == nil || !m.debug || function == nil || frame == nil {
		return nil
	}
	pos := inst.Pos
	if pos.File == "" || pos.Line <= 0 {
		return nil
	}
	if pos.File == m.debugLastFile && pos.Line == m.debugLastLine {
		return nil
	}
	m.debugLastFile = ""
	m.debugLastLine = 0
	if !runtime.ShouldBreak(pos.File, pos.Line) {
		return nil
	}
	m.debugLastFile = pos.File
	m.debugLastLine = pos.Line
	runtime.HandleTrap(m.buildDebugTrap(function, frame, inst))
	return nil
}

func (m *vm) buildDebugTrap(function *bytecode.Function, frame *frame, inst bytecode.Instruction) *runtime.Trap {
	pos := inst.Pos
	trap := &runtime.Trap{
		File:       pos.File,
		Line:       pos.Line,
		SourceLine: m.program.SourceLine(pos),
		Function:   function.Name,
		Stack:      m.debugStackFrames(),
		Locals:     debugLocalVariables(function, frame),
		Globals:    m.debugGlobalVariables(),
	}
	return trap
}

func (m *vm) debugStackFrames() []runtime.StackFrame {
	if m == nil || len(m.frames) == 0 {
		return nil
	}
	stack := make([]runtime.StackFrame, 0, len(m.frames))
	for i := len(m.frames) - 1; i >= 0; i-- {
		frame := m.frames[i]
		if frame == nil || frame.function == nil {
			continue
		}
		pos := bytecode.SourcePos{}
		ip := frame.lastIP
		if ip < 0 {
			ip = frame.ip
		}
		if ip >= 0 && ip < len(frame.function.Code) {
			pos = frame.function.Code[ip].Pos
		}
		stack = append(stack, runtime.StackFrame{
			Function:   frame.function.Name,
			File:       pos.File,
			Line:       pos.Line,
			SourceLine: m.program.SourceLine(pos),
		})
	}
	return stack
}

func debugLocalVariables(function *bytecode.Function, frame *frame) []runtime.Variable {
	if function == nil || frame == nil || len(function.DebugLocals) == 0 {
		return nil
	}
	pc := frame.lastIP
	variables := make([]runtime.Variable, 0, len(function.DebugLocals))
	for _, local := range function.DebugLocals {
		if !debugLocalVisible(local, pc) {
			continue
		}
		value, ok := debugLocalValue(local, frame)
		if !ok {
			continue
		}
		variables = append(variables, runtime.Variable{
			Name:         local.Name,
			Type:         local.Type,
			Value:        formatTraceValue(value, 0),
			RuntimeValue: value,
		})
	}
	return variables
}

func debugLocalVisible(local bytecode.DebugLocal, pc int) bool {
	if pc < local.StartPC {
		return false
	}
	return local.EndPC == 0 || pc < local.EndPC
}

func debugLocalValue(local bytecode.DebugLocal, frame *frame) (runtime.Value, bool) {
	switch local.Kind {
	case bytecode.DebugLocalCapture:
		if local.Slot < 0 || local.Slot >= len(frame.captures) || frame.captures[local.Slot] == nil {
			return runtime.Value{}, false
		}
		return frame.captures[local.Slot].Value, true
	default:
		if local.Slot < 0 || local.Slot >= len(frame.locals) {
			return runtime.Value{}, false
		}
		return frame.locals[local.Slot].Value, true
	}
}

func (m *vm) debugGlobalVariables() []runtime.Variable {
	if m == nil || m.program == nil || len(m.program.DebugGlobals) == 0 {
		return nil
	}
	variables := make([]runtime.Variable, 0, len(m.program.DebugGlobals))
	for _, global := range m.program.DebugGlobals {
		if global.Slot < 0 || global.Slot >= len(m.globals) {
			continue
		}
		value := m.globals[global.Slot].Value
		variables = append(variables, runtime.Variable{
			Name:         global.Name,
			Type:         global.Type,
			Value:        formatTraceValue(value, 0),
			RuntimeValue: value,
		})
	}
	return variables
}
