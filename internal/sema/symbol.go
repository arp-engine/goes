package sema

import sematypes "goes/types"

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
	Type     sematypes.Type
}
