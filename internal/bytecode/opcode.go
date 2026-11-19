package bytecode

import "fmt"

type Opcode byte

const (
	OpLoadInt Opcode = iota
	OpLoadNil
	OpLoadBool
	OpLoadInt64
	OpLoadFloat64
	OpLoadString
	OpLoadLocal
	OpLoadCapture
	OpStoreLocal
	OpLocalAddIntConst
	OpLocalSubIntConst
	OpLocalAddLocal
	OpLocalSubLocal
	OpStructFieldAddIntConst
	OpStructFieldAddLocal
	OpStructFieldAddField
	OpArrayElemAddIntConst
	OpArrayElemAddLocal
	OpArrayElemAddStructField
	OpLoadStructFieldLocal
	OpLoadArrayElemLocalConst
	OpLocalAddStructField
	OpLocalAddArrayElemConst
	OpSlice
	OpAdd
	OpSub
	OpMul
	OpDiv
	OpNeg
	OpNot
	OpModInt
	OpBitAnd
	OpBitOr
	OpBitXor
	OpBitClear
	OpShiftLeft
	OpShiftRight
	OpCmpEq
	OpCmpLt
	OpCmpLe
	OpCmpGt
	OpCmpGe
	OpAndAnd
	OpOrOr
	OpJump
	OpJumpIfFalse
	OpJumpIfLocalIntLtConstFalse
	OpJumpIfLocalBitAndIntEqConstFalse
	OpCall
	OpMakeFuncValue
	OpMakeMethodValue
	OpMakeStruct
	OpMakeArray
	OpMakeSlice
	OpMakeSliceLenCap
	OpAppendSlice
	OpLoadField
	OpAddrField
	OpLoadIndex
	OpAddrIndex
	OpAddrLocal
	OpAddrCapture
	OpBox
	OpDeref
	OpStoreIndirect
	OpMakeMap
	OpMakeClosure
	OpMapSet
	OpMapGet
	OpMapGetOK
	OpMapDelete
	OpClear
	OpBuiltinLen
	OpRangeInitIndexed
	OpRangeNextIndexed
	OpRangeInitMap
	OpRangeNextMap
	OpCallValue
	OpCallHost
	OpLoadGlobal
	OpStoreGlobal
	OpAddrGlobal
	OpConvertInt
	OpConvertInt64
	OpConvertFloat64
	OpConvertString
	OpPop
	OpReturn
)

var opcodeNames = [...]string{
	OpLoadInt:                          "OpLoadInt",
	OpLoadNil:                          "OpLoadNil",
	OpLoadBool:                         "OpLoadBool",
	OpLoadInt64:                        "OpLoadInt64",
	OpLoadFloat64:                      "OpLoadFloat64",
	OpLoadString:                       "OpLoadString",
	OpLoadLocal:                        "OpLoadLocal",
	OpLoadCapture:                      "OpLoadCapture",
	OpStoreLocal:                       "OpStoreLocal",
	OpLocalAddIntConst:                 "OpLocalAddIntConst",
	OpLocalSubIntConst:                 "OpLocalSubIntConst",
	OpLocalAddLocal:                    "OpLocalAddLocal",
	OpLocalSubLocal:                    "OpLocalSubLocal",
	OpStructFieldAddIntConst:           "OpStructFieldAddIntConst",
	OpStructFieldAddLocal:              "OpStructFieldAddLocal",
	OpStructFieldAddField:              "OpStructFieldAddField",
	OpArrayElemAddIntConst:             "OpArrayElemAddIntConst",
	OpArrayElemAddLocal:                "OpArrayElemAddLocal",
	OpArrayElemAddStructField:          "OpArrayElemAddStructField",
	OpLoadStructFieldLocal:             "OpLoadStructFieldLocal",
	OpLoadArrayElemLocalConst:          "OpLoadArrayElemLocalConst",
	OpLocalAddStructField:              "OpLocalAddStructField",
	OpLocalAddArrayElemConst:           "OpLocalAddArrayElemConst",
	OpSlice:                            "OpSlice",
	OpAdd:                              "OpAdd",
	OpSub:                              "OpSub",
	OpMul:                              "OpMul",
	OpDiv:                              "OpDiv",
	OpNeg:                              "OpNeg",
	OpNot:                              "OpNot",
	OpModInt:                           "OpModInt",
	OpBitAnd:                           "OpBitAnd",
	OpBitOr:                            "OpBitOr",
	OpBitXor:                           "OpBitXor",
	OpBitClear:                         "OpBitClear",
	OpShiftLeft:                        "OpShiftLeft",
	OpShiftRight:                       "OpShiftRight",
	OpCmpEq:                            "OpCmpEq",
	OpCmpLt:                            "OpCmpLt",
	OpCmpLe:                            "OpCmpLe",
	OpCmpGt:                            "OpCmpGt",
	OpCmpGe:                            "OpCmpGe",
	OpAndAnd:                           "OpAndAnd",
	OpOrOr:                             "OpOrOr",
	OpJump:                             "OpJump",
	OpJumpIfFalse:                      "OpJumpIfFalse",
	OpJumpIfLocalIntLtConstFalse:       "OpJumpIfLocalIntLtConstFalse",
	OpJumpIfLocalBitAndIntEqConstFalse: "OpJumpIfLocalBitAndIntEqConstFalse",
	OpCall:                             "OpCall",
	OpMakeFuncValue:                    "OpMakeFuncValue",
	OpMakeMethodValue:                  "OpMakeMethodValue",
	OpMakeStruct:                       "OpMakeStruct",
	OpMakeArray:                        "OpMakeArray",
	OpMakeSlice:                        "OpMakeSlice",
	OpMakeSliceLenCap:                  "OpMakeSliceLenCap",
	OpAppendSlice:                      "OpAppendSlice",
	OpLoadField:                        "OpLoadField",
	OpAddrField:                        "OpAddrField",
	OpLoadIndex:                        "OpLoadIndex",
	OpAddrIndex:                        "OpAddrIndex",
	OpAddrLocal:                        "OpAddrLocal",
	OpAddrCapture:                      "OpAddrCapture",
	OpBox:                              "OpBox",
	OpDeref:                            "OpDeref",
	OpStoreIndirect:                    "OpStoreIndirect",
	OpMakeMap:                          "OpMakeMap",
	OpMakeClosure:                      "OpMakeClosure",
	OpMapSet:                           "OpMapSet",
	OpMapGet:                           "OpMapGet",
	OpMapGetOK:                         "OpMapGetOK",
	OpMapDelete:                        "OpMapDelete",
	OpClear:                            "OpClear",
	OpBuiltinLen:                       "OpBuiltinLen",
	OpRangeInitIndexed:                 "OpRangeInitIndexed",
	OpRangeNextIndexed:                 "OpRangeNextIndexed",
	OpRangeInitMap:                     "OpRangeInitMap",
	OpRangeNextMap:                     "OpRangeNextMap",
	OpCallValue:                        "OpCallValue",
	OpCallHost:                         "OpCallHost",
	OpLoadGlobal:                       "OpLoadGlobal",
	OpStoreGlobal:                      "OpStoreGlobal",
	OpAddrGlobal:                       "OpAddrGlobal",
	OpConvertInt:                       "OpConvertInt",
	OpConvertInt64:                     "OpConvertInt64",
	OpConvertFloat64:                   "OpConvertFloat64",
	OpConvertString:                    "OpConvertString",
	OpPop:                              "OpPop",
	OpReturn:                           "OpReturn",
}

func (op Opcode) String() string {
	if int(op) >= 0 && int(op) < len(opcodeNames) && opcodeNames[op] != "" {
		return opcodeNames[op]
	}
	return fmt.Sprintf("Opcode(%d)", op)
}
