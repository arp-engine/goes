package compiler

import (
	"fmt"
	gotoken "go/token"

	"goes/internal/bytecode"
	"goes/internal/sema"
	"goes/runtime"
)

func newCompiler(opts CompileOptions, checked *sema.Checked) *compiler {
	return &compiler{
		program: &bytecode.Program{
			Entry:         -1,
			Init:          -1,
			FuncIndex:     make(map[string]int),
			MethodSets:    make(map[string]map[string]int),
			HostIndex:     make(map[string]int),
			HostFunctions: make([]bytecode.HostFunction, 0),
			StructSchemas: make(map[string]runtime.StructSchema),
			SourceLines:   cloneSourceLines(checked.SourceLines),
		},
		facts:         checked.Facts,
		fset:          checked.FileSet,
		funcIndex:     make(map[string]int),
		pkgFuncIndex:  make(map[string]int),
		funcTypes:     make(map[string]*runtime.FuncType),
		types:         make(map[string]runtime.ScriptType),
		globalIndex:   make(map[string]int),
		globalTypes:   make(map[int]runtime.ScriptType),
		hostFunctions: opts.HostFunctions,
	}
}

type compiler struct {
	program       *bytecode.Program
	facts         *sema.Facts
	fset          *gotoken.FileSet
	funcIndex     map[string]int
	pkgFuncIndex  map[string]int
	funcTypes     map[string]*runtime.FuncType
	types         map[string]runtime.ScriptType
	globalIndex   map[string]int
	globalTypes   map[int]runtime.ScriptType
	hostFunctions map[string]runtime.TypedHostSignature
}

type scope struct {
	parent *scope
	slots  map[string]int
	types  map[string]runtime.ScriptType
}

type funcCompiler struct {
	parent        *compiler
	parentFunc    *funcCompiler
	functionIndex int
	function      *bytecode.Function
	scope         *scope
	nextSlot      int
	slotTypes     map[int]runtime.ScriptType
	resultTypes   []runtime.ScriptType
	namedResults  []int
	captureSlots  map[string]int
	captureTypes  map[int]runtime.ScriptType
	captures      []captureRef
	controlStack  []*controlContext
	currentPos    gotoken.Pos
	debugLocals   []bytecode.DebugLocal
}

type controlContext struct {
	allowsContinue  bool
	breakPatches    []int
	continuePatches []int
}

type captureRef struct {
	fromCapture bool
	index       int
	typ         runtime.ScriptType
}

func (c *compiler) compileImportedModules(imports []*ImportedModule) error {
	if len(imports) == 0 {
		return nil
	}
	for _, imp := range imports {
		if imp == nil || imp.Checked == nil {
			continue
		}
		subOpts := CompileOptions{
			HostFunctions: c.hostFunctions,
			RequireMain:   false,
		}
		subProgram, handled, err := compileDirectGoFiles(imp.Checked, subOpts)
		if err != nil {
			return fmt.Errorf("compiler: failed to compile imported package %q: %w", imp.PkgPath, err)
		}
		if !handled {
			return fmt.Errorf("compiler: direct go/ast lowering could not handle imported package %q", imp.PkgPath)
		}
		if err := c.mergeImportedProgram(imp.PkgPath, subProgram); err != nil {
			return err
		}
	}
	return nil
}

func (c *compiler) mergeImportedProgram(pkgPath string, sub *bytecode.Program) error {
	offset := len(c.program.Functions)
	for _, fn := range sub.Functions {
		c.program.Functions = append(c.program.Functions, fn)
	}
	qualifiedName := func(name string) string {
		return pkgPath + "." + name
	}
	for name, index := range sub.FuncIndex {
		qualified := qualifiedName(name)
		c.program.FuncIndex[qualified] = index + offset
		c.pkgFuncIndex[qualified] = index + offset
	}
	for typeName, methods := range sub.MethodSets {
		qualifiedType := qualifiedName(typeName)
		for methodName, methodIndex := range methods {
			if _, ok := c.program.MethodSets[qualifiedType]; !ok {
				c.program.MethodSets[qualifiedType] = make(map[string]int)
			}
			c.program.MethodSets[qualifiedType][methodName] = methodIndex + offset
		}
	}
	for name, schema := range sub.StructSchemas {
		qualified := qualifiedName(name)
		if _, exists := c.program.StructSchemas[qualified]; !exists {
			c.program.StructSchemas[qualified] = schema
		}
	}
	for path, lines := range sub.SourceLines {
		if _, exists := c.program.SourceLines[path]; !exists {
			c.program.SourceLines[path] = lines
		}
	}
	return nil
}

func (c *compiler) collectHostFunctions() {
	if len(c.hostFunctions) == 0 {
		return
	}
	for name, sig := range c.hostFunctions {
		if _, exists := c.program.HostIndex[name]; exists {
			continue
		}
		index := len(c.program.HostFunctions)
		c.program.HostIndex[name] = index
		c.program.HostFunctions = append(c.program.HostFunctions, bytecode.HostFunction{
			Name:     name,
			Params:   len(sig.Params),
			Results:  len(sig.Results),
			Variadic: sig.Variadic,
		})
	}
}

func structSchemaForNamed(name string, strct *runtime.StructType) (runtime.StructSchema, error) {
	schema := runtime.StructSchema{
		Name:       name,
		FieldNames: append([]string(nil), strct.FieldNames...),
		FieldKinds: make([]runtime.Kind, len(strct.Fields)),
	}
	for i, field := range strct.Fields {
		kind, err := runtimeKindForType(field)
		if err != nil {
			return runtime.StructSchema{}, fmt.Errorf("compiler: struct %s field %s: %w", name, schema.FieldNames[i], err)
		}
		schema.FieldKinds[i] = kind
	}
	return schema, nil
}

func runtimeKindForType(typ runtime.ScriptType) (runtime.Kind, error) {
	switch runtime.Underlying(typ).Kind() {
	case runtime.TypeKindBool:
		return runtime.Bool, nil
	case runtime.TypeKindInt:
		return runtime.Int, nil
	case runtime.TypeKindInt64:
		return runtime.Int64, nil
	case runtime.TypeKindFloat64:
		return runtime.Float64, nil
	case runtime.TypeKindString:
		return runtime.String, nil
	default:
		return runtime.Invalid, fmt.Errorf("unsupported bridge field type %s", typ.String())
	}
}

func runtimeZeroKindForType(typ runtime.ScriptType) (runtime.Kind, string, error) {
	if named, ok := typ.(*runtime.NamedType); ok {
		if _, ok := runtime.Underlying(named).(*runtime.StructType); ok {
			return runtime.Invalid, named.Name, nil
		}
	}
	switch runtime.Underlying(typ).Kind() {
	case runtime.TypeKindBool:
		return runtime.Bool, "", nil
	case runtime.TypeKindInt:
		return runtime.Int, "", nil
	case runtime.TypeKindInt64:
		return runtime.Int64, "", nil
	case runtime.TypeKindFloat64:
		return runtime.Float64, "", nil
	case runtime.TypeKindString:
		return runtime.String, "", nil
	case runtime.TypeKindPointer, runtime.TypeKindSlice, runtime.TypeKindMap, runtime.TypeKindFunc:
		return runtime.Nil, "", nil
	default:
		return runtime.Invalid, "", fmt.Errorf("unsupported make slice element type %s", typ.String())
	}
}

func rangeKeyValueTypes(rangeType runtime.ScriptType) (runtime.ScriptType, runtime.ScriptType, error) {
	if runtime.IsInteger(rangeType) {
		return rangeType, runtime.BuiltinInvalidType, nil
	}
	switch t := runtime.Underlying(rangeType).(type) {
	case *runtime.MapType:
		return t.Key, t.Value, nil
	case *runtime.ArrayType:
		return runtime.BuiltinIntType, t.Elem, nil
	case *runtime.SliceType:
		return runtime.BuiltinIntType, t.Elem, nil
	default:
		return nil, nil, fmt.Errorf("compiler: cannot range over %s", rangeType.String())
	}
}

func (fc *funcCompiler) declareSlot(name string, typ runtime.ScriptType) (int, error) {
	return fc.declareSlotKind(name, typ, bytecode.DebugLocalLocal)
}

func (fc *funcCompiler) declareSlotKind(name string, typ runtime.ScriptType, kind bytecode.DebugLocalKind) (int, error) {
	if name == "_" {
		slot := fc.nextSlot
		fc.nextSlot++
		fc.slotTypes[slot] = typ
		return slot, nil
	}
	if _, exists := fc.scope.lookupLocal(name); exists {
		return 0, fmt.Errorf("compiler: local %q is already declared", name)
	}
	slot := fc.nextSlot
	fc.scope.slots[name] = slot
	fc.nextSlot++
	fc.slotTypes[slot] = typ
	fc.addDebugLocal(name, slot, kind, typ)
	return slot, nil
}

func (fc *funcCompiler) addDebugLocal(name string, slot int, kind bytecode.DebugLocalKind, typ runtime.ScriptType) {
	if fc == nil || name == "" || name == "_" || typ == nil {
		return
	}
	fc.debugLocals = append(fc.debugLocals, bytecode.DebugLocal{
		Name:    name,
		Slot:    slot,
		Kind:    kind,
		Type:    typ.String(),
		StartPC: len(fc.function.Code),
	})
}

func (fc *funcCompiler) finishDebugLocals() []bytecode.DebugLocal {
	if fc == nil || len(fc.debugLocals) == 0 {
		return nil
	}
	locals := append([]bytecode.DebugLocal(nil), fc.debugLocals...)
	endPC := len(fc.function.Code)
	for i := range locals {
		if locals[i].EndPC == 0 {
			locals[i].EndPC = endPC
		}
	}
	return locals
}

func (fc *funcCompiler) lookupSlot(name string) (int, bool) {
	return fc.scope.lookup(name)
}

func (c *compiler) lookupGlobal(name string) (int, bool) {
	slot, ok := c.globalIndex[name]
	return slot, ok
}

func (fc *funcCompiler) emit(op bytecode.Opcode, a, b int, i64 int64) {
	inst := bytecode.Instruction{Op: op, A: a, B: b, I64: i64}
	if fc.parent != nil {
		inst.Pos = fc.parent.sourcePos(fc.currentPos)
	}
	fc.function.Code = append(fc.function.Code, inst)
}

func (c *compiler) sourcePos(pos gotoken.Pos) bytecode.SourcePos {
	if c == nil || c.fset == nil || !pos.IsValid() {
		return bytecode.SourcePos{}
	}
	position := c.fset.Position(pos)
	if !position.IsValid() || position.Filename == "" || position.Line <= 0 {
		return bytecode.SourcePos{}
	}
	return bytecode.SourcePos{File: position.Filename, Line: position.Line}
}

func (fc *funcCompiler) withPos(pos gotoken.Pos) func() {
	prev := fc.currentPos
	if pos.IsValid() {
		fc.currentPos = pos
	}
	return func() {
		fc.currentPos = prev
	}
}

func cloneSourceLines(sourceLines map[string][]string) map[string][]string {
	if len(sourceLines) == 0 {
		return nil
	}
	clone := make(map[string][]string, len(sourceLines))
	for path, lines := range sourceLines {
		clone[path] = append([]string(nil), lines...)
	}
	return clone
}

func computeMaxStack(code []bytecode.Instruction) int {
	depth := 0
	maxDepth := 0
	for _, inst := range code {
		depth += stackDelta(inst)
		if depth > maxDepth {
			maxDepth = depth
		}
		if depth < 0 {
			depth = 0
		}
	}
	return maxDepth
}

func stackDelta(inst bytecode.Instruction) int {
	switch inst.Op {
	case bytecode.OpLoadInt, bytecode.OpLoadNil, bytecode.OpLoadBool, bytecode.OpLoadInt64,
		bytecode.OpLoadFloat64, bytecode.OpLoadString, bytecode.OpLoadLocal, bytecode.OpLoadCapture,
		bytecode.OpLoadGlobal, bytecode.OpLoadStructFieldLocal, bytecode.OpLoadArrayElemLocalConst,
		bytecode.OpAddrLocal, bytecode.OpAddrCapture, bytecode.OpAddrGlobal, bytecode.OpMakeFuncValue,
		bytecode.OpMakeMap:
		return 1
	case bytecode.OpStoreLocal, bytecode.OpStoreGlobal, bytecode.OpJumpIfFalse, bytecode.OpPop, bytecode.OpReturn:
		return -1
	case bytecode.OpAdd, bytecode.OpSub, bytecode.OpMul, bytecode.OpDiv, bytecode.OpModInt,
		bytecode.OpBitAnd, bytecode.OpBitOr, bytecode.OpBitXor, bytecode.OpBitClear,
		bytecode.OpShiftLeft, bytecode.OpShiftRight, bytecode.OpCmpEq, bytecode.OpCmpLt,
		bytecode.OpCmpLe, bytecode.OpCmpGt, bytecode.OpCmpGe, bytecode.OpAndAnd, bytecode.OpOrOr,
		bytecode.OpLoadIndex, bytecode.OpAddrIndex, bytecode.OpMapGet:
		return -1
	case bytecode.OpMapGetOK:
		return 0
	case bytecode.OpSlice:
		bounds := 0
		if inst.A != 0 {
			bounds++
		}
		if inst.B != 0 {
			bounds++
		}
		if inst.I64 != 0 {
			bounds++
		}
		return -bounds
	case bytecode.OpMakeSliceLenCap:
		if inst.B != 0 {
			return -1
		}
		return 0
	case bytecode.OpAppendSlice:
		if inst.B != 0 {
			return -1
		}
		return -inst.A
	case bytecode.OpNeg, bytecode.OpNot, bytecode.OpLoadField, bytecode.OpAddrField,
		bytecode.OpDeref, bytecode.OpBuiltinLen,
		bytecode.OpConvertInt, bytecode.OpConvertInt64, bytecode.OpConvertFloat64,
		bytecode.OpConvertString, bytecode.OpJump:
		return 0
	case bytecode.OpRangeInitIndexed, bytecode.OpRangeInitMap:
		return -1
	case bytecode.OpLocalAddIntConst, bytecode.OpLocalSubIntConst,
		bytecode.OpLocalAddLocal, bytecode.OpLocalSubLocal,
		bytecode.OpJumpIfLocalIntLtConstFalse, bytecode.OpJumpIfLocalBitAndIntEqConstFalse,
		bytecode.OpStructFieldAddIntConst, bytecode.OpStructFieldAddLocal, bytecode.OpStructFieldAddField,
		bytecode.OpArrayElemAddIntConst, bytecode.OpArrayElemAddLocal, bytecode.OpArrayElemAddStructField,
		bytecode.OpLocalAddStructField, bytecode.OpLocalAddArrayElemConst, bytecode.OpMakeMethodValue:
		return 0
	case bytecode.OpCall:
		return 1 - inst.B
	case bytecode.OpCallValue:
		return -inst.A
	case bytecode.OpCallHost:
		return 1 - inst.B
	case bytecode.OpMakeStruct, bytecode.OpMakeArray, bytecode.OpMakeSlice:
		return 1 - inst.A
	case bytecode.OpStoreIndirect:
		return -2
	case bytecode.OpMakeClosure:
		return 1 - inst.B
	case bytecode.OpMapSet:
		return -3
	case bytecode.OpMapDelete:
		return -2
	case bytecode.OpRangeNextIndexed, bytecode.OpRangeNextMap:
		return 1
	default:
		return 0
	}
}

func (c *compiler) rebindFunctionChain(fc *funcCompiler) {
	for current := fc; current != nil; current = current.parentFunc {
		current.function = &c.program.Functions[current.functionIndex]
	}
}

func (fc *funcCompiler) ensureCapture(name string) (int, runtime.ScriptType, error) {
	if index, ok := fc.captureSlots[name]; ok {
		return index, fc.captureTypes[index], nil
	}
	if fc.parentFunc == nil {
		return 0, nil, fmt.Errorf("compiler: undefined name %q", name)
	}
	ref, err := fc.parentFunc.captureRef(name)
	if err != nil {
		return 0, nil, err
	}
	index := len(fc.captures)
	fc.captureSlots[name] = index
	fc.captureTypes[index] = ref.typ
	fc.captures = append(fc.captures, ref)
	fc.addDebugLocal(name, index, bytecode.DebugLocalCapture, ref.typ)
	return index, ref.typ, nil
}

func (fc *funcCompiler) captureRef(name string) (captureRef, error) {
	if slot, ok := fc.lookupSlot(name); ok {
		typ, ok := fc.slotTypes[slot]
		if !ok {
			return captureRef{}, fmt.Errorf("compiler: missing type for local %q", name)
		}
		return captureRef{index: slot, typ: typ}, nil
	}
	if index, ok := fc.captureSlots[name]; ok {
		return captureRef{fromCapture: true, index: index, typ: fc.captureTypes[index]}, nil
	}
	if fc.parentFunc == nil {
		return captureRef{}, fmt.Errorf("compiler: undefined name %q", name)
	}
	index, typ, err := fc.ensureCapture(name)
	if err != nil {
		return captureRef{}, err
	}
	return captureRef{fromCapture: true, index: index, typ: typ}, nil
}

func newScope(parent *scope) *scope {
	return &scope{
		parent: parent,
		slots:  make(map[string]int),
		types:  make(map[string]runtime.ScriptType),
	}
}

func (s *scope) lookupLocal(name string) (int, bool) {
	if s == nil {
		return 0, false
	}
	slot, ok := s.slots[name]
	return slot, ok
}

func (s *scope) lookup(name string) (int, bool) {
	for current := s; current != nil; current = current.parent {
		if slot, ok := current.lookupLocal(name); ok {
			return slot, true
		}
	}
	return 0, false
}

func (s *scope) lookupTypeLocal(name string) (runtime.ScriptType, bool) {
	if s == nil || s.types == nil {
		return nil, false
	}
	typ, ok := s.types[name]
	return typ, ok
}

func (s *scope) lookupType(name string) (runtime.ScriptType, bool) {
	for current := s; current != nil; current = current.parent {
		if typ, ok := current.lookupTypeLocal(name); ok {
			return typ, true
		}
	}
	return nil, false
}

func (fc *funcCompiler) pushScope() func() {
	prevScope := fc.scope
	fc.scope = newScope(prevScope)
	return func() {
		fc.scope = prevScope
	}
}

func (fc *funcCompiler) emitJump(op bytecode.Opcode) int {
	index := len(fc.function.Code)
	fc.emit(op, -1, 0, 0)
	return index
}

func (fc *funcCompiler) patchJump(index int, target int) {
	fc.function.Code[index].A = target
}

func (fc *funcCompiler) pushControl(allowsContinue bool) *controlContext {
	ctx := &controlContext{allowsContinue: allowsContinue}
	fc.controlStack = append(fc.controlStack, ctx)
	return ctx
}

func (fc *funcCompiler) popControl() {
	if len(fc.controlStack) == 0 {
		return
	}
	fc.controlStack = fc.controlStack[:len(fc.controlStack)-1]
}

func (fc *funcCompiler) currentBreakControl() *controlContext {
	for i := len(fc.controlStack) - 1; i >= 0; i-- {
		return fc.controlStack[i]
	}
	return nil
}

func (fc *funcCompiler) currentContinueControl() *controlContext {
	for i := len(fc.controlStack) - 1; i >= 0; i-- {
		if fc.controlStack[i].allowsContinue {
			return fc.controlStack[i]
		}
	}
	return nil
}

func (fc *funcCompiler) emitZeroValue(typ runtime.ScriptType) error {
	switch runtime.Underlying(typ).Kind() {
	case runtime.TypeKindNil:
		fc.emit(bytecode.OpLoadNil, 0, 0, 0)
	case runtime.TypeKindBool:
		fc.emit(bytecode.OpLoadBool, 0, 0, 0)
	case runtime.TypeKindInt:
		fc.emit(bytecode.OpLoadInt, 0, 0, 0)
	case runtime.TypeKindInt64:
		fc.emit(bytecode.OpLoadInt64, 0, 0, 0)
	case runtime.TypeKindFloat64:
		fc.emit(bytecode.OpLoadFloat64, 0, 0, 0)
		fc.function.Code[len(fc.function.Code)-1].S = "0"
	case runtime.TypeKindString:
		fc.emit(bytecode.OpLoadString, 0, 0, 0)
	case runtime.TypeKindPointer, runtime.TypeKindSlice, runtime.TypeKindMap, runtime.TypeKindFunc:
		fc.emit(bytecode.OpLoadNil, 0, 0, 0)
	default:
		return fmt.Errorf("compiler: zero values are unsupported for %s", typ.String())
	}
	return nil
}
