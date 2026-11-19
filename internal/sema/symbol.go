package sema

import "goes/runtime"

type SymbolKind int

const (
	SymVar SymbolKind = iota
	SymConst
	SymFunc
	SymType
	SymMethod
	SymPackage
)

type Symbol struct {
	Name     string
	PkgPath  string
	Exported bool
	Kind     SymbolKind
	Type     runtime.ScriptType
}
