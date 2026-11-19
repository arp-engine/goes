package vm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"goes/internal/bytecode"
	"goes/runtime"
)

const snapshotVersion = 1

func RestoreSnapshot(program *bytecode.Program, snapshot *runtime.Snapshot, host *runtime.HostRegistry, ctx *runtime.HostContext) (*runtime.Suspension, error) {
	if program == nil {
		return nil, fmt.Errorf("vm: missing program")
	}
	if snapshot == nil {
		return nil, fmt.Errorf("vm: missing snapshot")
	}
	if snapshot.Version != snapshotVersion {
		return nil, fmt.Errorf("vm: unsupported snapshot version %d", snapshot.Version)
	}
	hash := ProgramHash(program)
	if snapshot.ProgramHash != hash {
		return nil, fmt.Errorf("vm: snapshot program hash mismatch")
	}

	machine, err := restoreVM(program, snapshot, host, ctx)
	if err != nil {
		return nil, err
	}
	return machine.suspend(
		snapshot.PendingHostCall.Payload,
		snapshot.PendingHostCall.Name,
		snapshot.PendingHostCall.ExpectedResults,
	), nil
}

func ProgramHash(program *bytecode.Program) string {
	if program == nil {
		return ""
	}
	h := sha256.New()
	fmt.Fprintf(h, "entry=%d init=%d globals=%d\n", program.Entry, program.Init, program.GlobalCount)
	for i, fn := range program.Functions {
		fmt.Fprintf(h, "fn %d %s params=%d results=%d variadic=%t locals=%d maxstack=%d\n", i, fn.Name, fn.Params, fn.Results, fn.Variadic, fn.Locals, fn.MaxStack)
		for _, inst := range fn.Code {
			fmt.Fprintf(h, "inst %d %d %d %d %q\n", inst.Op, inst.A, inst.B, inst.I64, inst.S)
		}
	}
	for i, fn := range program.HostFunctions {
		fmt.Fprintf(h, "host %d %s params=%d results=%d variadic=%t\n", i, fn.Name, fn.Params, fn.Results, fn.Variadic)
	}
	structNames := make([]string, 0, len(program.StructSchemas))
	for name := range program.StructSchemas {
		structNames = append(structNames, name)
	}
	sort.Strings(structNames)
	for _, name := range structNames {
		schema := program.StructSchemas[name]
		fmt.Fprintf(h, "struct %s %s\n", name, schema.Name)
		for i := range schema.FieldNames {
			fmt.Fprintf(h, "field %s %d\n", schema.FieldNames[i], schema.FieldKinds[i])
		}
	}
	methodTypeNames := make([]string, 0, len(program.MethodSets))
	for name := range program.MethodSets {
		methodTypeNames = append(methodTypeNames, name)
	}
	sort.Strings(methodTypeNames)
	for _, typeName := range methodTypeNames {
		methods := program.MethodSets[typeName]
		methodNames := make([]string, 0, len(methods))
		for method := range methods {
			methodNames = append(methodNames, method)
		}
		sort.Strings(methodNames)
		for _, method := range methodNames {
			fmt.Fprintf(h, "method %s %s %d\n", typeName, method, methods[method])
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (m *vm) suspend(payload any, hostName string, hostResults int) *runtime.Suspension {
	return runtime.NewSuspensionWithSnapshot(payload, func(results []runtime.Value) ([]runtime.Value, error) {
		return m.resume(results, hostResults)
	}, func() (*runtime.Snapshot, error) {
		return m.snapshot(payload, hostName, hostResults)
	})
}

func (m *vm) snapshot(payload any, hostName string, hostResults int) (*runtime.Snapshot, error) {
	builder := snapshotBuilder{
		cellIDs:   make(map[*runtime.Cell]int),
		objectIDs: make(map[runtime.Object]int),
		snapshot: runtime.Snapshot{
			Version:     snapshotVersion,
			ProgramHash: ProgramHash(m.program),
			PendingHostCall: runtime.PendingHostCallSnapshot{
				Name:            hostName,
				ExpectedResults: hostResults,
				Payload:         payload,
			},
		},
	}
	if len(m.globals) > 0 {
		builder.snapshot.Globals = make([]int, len(m.globals))
		for i := range m.globals {
			builder.snapshot.Globals[i] = builder.cellID(&m.globals[i])
		}
	}
	builder.snapshot.Frames = make([]runtime.FrameSnapshot, len(m.frames))
	for i, frame := range m.frames {
		frameIndex, err := m.functionIndex(frame.function)
		if err != nil {
			return nil, err
		}
		snapshot := runtime.FrameSnapshot{
			FunctionIndex: frameIndex,
			IP:            frame.ip,
			LastIP:        frame.lastIP,
			Locals:        make([]int, len(frame.locals)),
			Captures:      make([]int, len(frame.captures)),
			Stack:         make([]runtime.ValueSnapshot, len(frame.stack)),
		}
		for j := range frame.locals {
			snapshot.Locals[j] = builder.cellID(&frame.locals[j])
		}
		for j, capture := range frame.captures {
			snapshot.Captures[j] = builder.cellID(capture)
		}
		for j, value := range frame.stack {
			snapshot.Stack[j] = builder.value(value)
		}
		builder.snapshot.Frames[i] = snapshot
	}
	return &builder.snapshot, nil
}

func (m *vm) functionIndex(function *bytecode.Function) (int, error) {
	if function == nil {
		return 0, fmt.Errorf("vm: missing frame function")
	}
	for i := range m.program.Functions {
		if function == &m.program.Functions[i] {
			return i, nil
		}
	}
	return 0, fmt.Errorf("vm: frame function %q is not in program", function.Name)
}

type snapshotBuilder struct {
	cellIDs   map[*runtime.Cell]int
	objectIDs map[runtime.Object]int
	snapshot  runtime.Snapshot
}

func (b *snapshotBuilder) cellID(cell *runtime.Cell) int {
	if cell == nil {
		return -1
	}
	if id, ok := b.cellIDs[cell]; ok {
		return id
	}
	id := len(b.snapshot.Heap.Cells)
	b.cellIDs[cell] = id
	b.snapshot.Heap.Cells = append(b.snapshot.Heap.Cells, runtime.CellSnapshot{})
	b.snapshot.Heap.Cells[id].Value = b.value(cell.Value)
	return id
}

func (b *snapshotBuilder) objectID(obj runtime.Object) int {
	if obj == nil {
		return -1
	}
	if id, ok := b.objectIDs[obj]; ok {
		return id
	}
	id := len(b.snapshot.Heap.Objects)
	b.objectIDs[obj] = id
	b.snapshot.Heap.Objects = append(b.snapshot.Heap.Objects, runtime.ObjectSnapshot{})
	snapshot := runtime.ObjectSnapshot{}
	switch obj := obj.(type) {
	case *runtime.Struct:
		snapshot.Kind = "struct"
		snapshot.TypeName = obj.TypeName
		snapshot.Cells = make([]int, len(obj.Fields))
		for i := range obj.Fields {
			snapshot.Cells[i] = b.cellID(&obj.Fields[i])
		}
	case *runtime.Array:
		snapshot.Kind = "array"
		snapshot.Cells = make([]int, len(obj.Elements))
		for i := range obj.Elements {
			snapshot.Cells[i] = b.cellID(&obj.Elements[i])
		}
	case *runtime.Slice:
		snapshot.Kind = "slice"
		snapshot.Cells = make([]int, len(obj.Elements))
		for i := range obj.Elements {
			snapshot.Cells[i] = b.cellID(&obj.Elements[i])
		}
	case *runtime.Map:
		snapshot.Kind = "map"
		snapshot.Zero = b.value(obj.Zero)
		snapshot.MapEntries = make([]runtime.MapEntrySnapshot, 0, len(obj.Entries))
		for encoded, entry := range obj.Entries {
			snapshot.MapEntries = append(snapshot.MapEntries, runtime.MapEntrySnapshot{
				EncodedKey: b.mapKey(encoded),
				Key:        b.value(entry.Key),
				Value:      b.value(entry.Value),
			})
		}
	case *runtime.Closure:
		snapshot.Kind = "closure"
		snapshot.FuncIndex = obj.FuncIndex
		snapshot.Captures = make([]int, len(obj.Captures))
		for i, capture := range obj.Captures {
			snapshot.Captures[i] = b.cellID(capture)
		}
	case *runtime.MethodValue:
		snapshot.Kind = "method_value"
		snapshot.FuncIndex = obj.FuncIndex
		snapshot.Receiver = b.value(obj.Receiver)
	default:
		snapshot.Kind = "unknown"
	}
	b.snapshot.Heap.Objects[id] = snapshot
	return id
}

func (b *snapshotBuilder) value(value runtime.Value) runtime.ValueSnapshot {
	snapshot := runtime.ValueSnapshot{
		Kind:     value.Kind,
		B:        value.B,
		I64:      value.I64,
		F64:      value.F64,
		Str:      value.Str,
		CellID:   -1,
		ObjectID: -1,
	}
	if value.Kind == runtime.PointerKind {
		snapshot.CellID = b.cellID(value.Pointer())
	}
	if value.Kind == runtime.ObjectKind {
		if snapshotObjectIsNil(value.Object()) {
			snapshot.ObjectID = -1
		} else {
			snapshot.ObjectID = b.objectID(value.Object())
		}
	}
	return snapshot
}

func snapshotObjectIsNil(obj runtime.Object) bool {
	switch obj := obj.(type) {
	case nil:
		return true
	case *runtime.Struct:
		return obj == nil
	case *runtime.Array:
		return obj == nil
	case *runtime.Slice:
		return obj == nil
	case *runtime.Map:
		return obj == nil
	case *runtime.Closure:
		return obj == nil
	case *runtime.MethodValue:
		return obj == nil
	default:
		return false
	}
}

func (b *snapshotBuilder) mapKey(key runtime.MapKey) runtime.MapKeySnapshot {
	return runtime.MapKeySnapshot{
		Kind:   key.Kind,
		B:      key.B,
		I64:    key.I64,
		F64:    key.F64,
		Str:    key.Str,
		CellID: b.cellID(key.Cell),
	}
}

func restoreVM(program *bytecode.Program, snapshot *runtime.Snapshot, host *runtime.HostRegistry, ctx *runtime.HostContext) (*vm, error) {
	restorer := snapshotRestorer{
		snapshot: snapshot,
		cells:    make(map[int]*runtime.Cell, len(snapshot.Heap.Cells)),
		objects:  make([]runtime.Object, len(snapshot.Heap.Objects)),
	}
	machine := &vm{program: program, host: host, hostCtx: ctx}
	if len(snapshot.Globals) > 0 {
		machine.globals = make([]runtime.Cell, len(snapshot.Globals))
		for i, cellID := range snapshot.Globals {
			if err := restorer.registerCell(cellID, &machine.globals[i]); err != nil {
				return nil, err
			}
		}
	}
	machine.frames = make([]*frame, len(snapshot.Frames))
	for i, frameSnapshot := range snapshot.Frames {
		if frameSnapshot.FunctionIndex < 0 || frameSnapshot.FunctionIndex >= len(program.Functions) {
			return nil, fmt.Errorf("vm: snapshot frame function index %d out of range", frameSnapshot.FunctionIndex)
		}
		function := &program.Functions[frameSnapshot.FunctionIndex]
		restored := &frame{
			function: function,
			ip:       frameSnapshot.IP,
			lastIP:   frameSnapshot.LastIP,
			locals:   make([]runtime.Cell, len(frameSnapshot.Locals)),
			captures: make([]*runtime.Cell, len(frameSnapshot.Captures)),
			stack:    make([]runtime.Value, len(frameSnapshot.Stack)),
		}
		for j, cellID := range frameSnapshot.Locals {
			if err := restorer.registerCell(cellID, &restored.locals[j]); err != nil {
				return nil, err
			}
		}
		machine.frames[i] = restored
	}
	if err := restorer.allocateObjects(); err != nil {
		return nil, err
	}
	if err := restorer.fillCells(); err != nil {
		return nil, err
	}
	if err := restorer.fillObjects(); err != nil {
		return nil, err
	}
	for frameIndex, restored := range machine.frames {
		frameSnapshot := snapshot.Frames[frameIndex]
		for i, cellID := range frameSnapshot.Captures {
			cell, err := restorer.cell(cellID)
			if err != nil {
				return nil, err
			}
			restored.captures[i] = cell
		}
		for i, value := range frameSnapshot.Stack {
			decoded, err := restorer.value(value)
			if err != nil {
				return nil, err
			}
			restored.stack[i] = decoded
		}
	}
	return machine, nil
}

type snapshotRestorer struct {
	snapshot *runtime.Snapshot
	cells    map[int]*runtime.Cell
	objects  []runtime.Object
}

func (r *snapshotRestorer) registerCell(id int, cell *runtime.Cell) error {
	if id < 0 || id >= len(r.snapshot.Heap.Cells) {
		return fmt.Errorf("vm: snapshot cell id %d out of range", id)
	}
	if existing, ok := r.cells[id]; ok && existing != cell {
		return fmt.Errorf("vm: snapshot cell id %d has multiple owners", id)
	}
	r.cells[id] = cell
	return nil
}

func (r *snapshotRestorer) allocateObjects() error {
	for id, snapshot := range r.snapshot.Heap.Objects {
		switch snapshot.Kind {
		case "struct":
			fields := make([]runtime.Cell, len(snapshot.Cells))
			for i, cellID := range snapshot.Cells {
				if err := r.registerCell(cellID, &fields[i]); err != nil {
					return err
				}
			}
			r.objects[id] = &runtime.Struct{TypeName: snapshot.TypeName, Fields: fields}
		case "array":
			elements := make([]runtime.Cell, len(snapshot.Cells))
			for i, cellID := range snapshot.Cells {
				if err := r.registerCell(cellID, &elements[i]); err != nil {
					return err
				}
			}
			r.objects[id] = &runtime.Array{Elements: elements}
		case "slice":
			elements := make([]runtime.Cell, len(snapshot.Cells))
			for i, cellID := range snapshot.Cells {
				if err := r.registerCell(cellID, &elements[i]); err != nil {
					return err
				}
			}
			r.objects[id] = &runtime.Slice{Elements: elements}
		case "map":
			r.objects[id] = &runtime.Map{
				Entries: make(map[runtime.MapKey]runtime.MapEntry, len(snapshot.MapEntries)),
				Keys:    make([]runtime.MapKey, 0, len(snapshot.MapEntries)),
			}
		case "closure":
			r.objects[id] = &runtime.Closure{FuncIndex: snapshot.FuncIndex, Captures: make([]*runtime.Cell, len(snapshot.Captures))}
		case "method_value":
			r.objects[id] = &runtime.MethodValue{FuncIndex: snapshot.FuncIndex}
		default:
			return fmt.Errorf("vm: unsupported snapshot object kind %q", snapshot.Kind)
		}
	}
	return nil
}

func (r *snapshotRestorer) fillCells() error {
	for id, snapshot := range r.snapshot.Heap.Cells {
		cell, err := r.cell(id)
		if err != nil {
			return err
		}
		value, err := r.value(snapshot.Value)
		if err != nil {
			return err
		}
		cell.Value = value
	}
	return nil
}

func (r *snapshotRestorer) fillObjects() error {
	for id, snapshot := range r.snapshot.Heap.Objects {
		switch obj := r.objects[id].(type) {
		case *runtime.Map:
			zero, err := r.value(snapshot.Zero)
			if err != nil {
				return err
			}
			obj.Zero = zero
			for _, entry := range snapshot.MapEntries {
				key, err := r.mapKey(entry.EncodedKey)
				if err != nil {
					return err
				}
				keyValue, err := r.value(entry.Key)
				if err != nil {
					return err
				}
				value, err := r.value(entry.Value)
				if err != nil {
					return err
				}
				obj.Entries[key] = runtime.MapEntry{Key: keyValue, Value: value}
				obj.Keys = append(obj.Keys, key)
			}
		case *runtime.Closure:
			for i, cellID := range snapshot.Captures {
				cell, err := r.cell(cellID)
				if err != nil {
					return err
				}
				obj.Captures[i] = cell
			}
		case *runtime.MethodValue:
			receiver, err := r.value(snapshot.Receiver)
			if err != nil {
				return err
			}
			obj.Receiver = receiver
		}
	}
	return nil
}

func (r *snapshotRestorer) value(snapshot runtime.ValueSnapshot) (runtime.Value, error) {
	switch snapshot.Kind {
	case runtime.Invalid:
		return runtime.Value{}, nil
	case runtime.Nil:
		return runtime.NilValue(), nil
	case runtime.Bool:
		return runtime.BoolValue(snapshot.B), nil
	case runtime.Int:
		return runtime.IntValue(snapshot.I64), nil
	case runtime.Int64:
		return runtime.Int64Value(snapshot.I64), nil
	case runtime.Float64:
		return runtime.Float64Value(snapshot.F64), nil
	case runtime.String:
		return runtime.StringValue(snapshot.Str), nil
	case runtime.PointerKind:
		if snapshot.CellID < 0 {
			return runtime.PointerValue(nil), nil
		}
		cell, err := r.cell(snapshot.CellID)
		if err != nil {
			return runtime.Value{}, err
		}
		return runtime.PointerValue(cell), nil
	case runtime.ObjectKind:
		if snapshot.ObjectID < 0 {
			return runtime.ObjectValue(nil), nil
		}
		if snapshot.ObjectID >= len(r.objects) {
			return runtime.Value{}, fmt.Errorf("vm: snapshot object id %d out of range", snapshot.ObjectID)
		}
		return runtime.ObjectValue(r.objects[snapshot.ObjectID]), nil
	default:
		return runtime.Value{}, fmt.Errorf("vm: unsupported snapshot value kind %d", snapshot.Kind)
	}
}

func (r *snapshotRestorer) mapKey(snapshot runtime.MapKeySnapshot) (runtime.MapKey, error) {
	key := runtime.MapKey{
		Kind: snapshot.Kind,
		B:    snapshot.B,
		I64:  snapshot.I64,
		F64:  snapshot.F64,
		Str:  snapshot.Str,
	}
	if snapshot.Kind == runtime.PointerKind && snapshot.CellID >= 0 {
		cell, err := r.cell(snapshot.CellID)
		if err != nil {
			return runtime.MapKey{}, err
		}
		key.Cell = cell
	}
	return key, nil
}

func (r *snapshotRestorer) cell(id int) (*runtime.Cell, error) {
	if id < 0 || id >= len(r.snapshot.Heap.Cells) {
		return nil, fmt.Errorf("vm: snapshot cell id %d out of range", id)
	}
	cell, ok := r.cells[id]
	if !ok || cell == nil {
		return nil, fmt.Errorf("vm: snapshot cell id %d has no owner", id)
	}
	return cell, nil
}
