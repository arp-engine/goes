package sema

import (
	goast "go/ast"
	gotoken "go/token"

	semaTypes "goes/types"
)

type Facts struct {
	Bindings     map[any]*Symbol
	Captures     map[any]*Symbol
	Types        map[any]semaTypes.Type
	ConstExprs   map[any]any
	Selections   map[any]*Selection
	PackageScope *Scope
}

type SelectionKind int

const (
	SelectionField SelectionKind = iota
	SelectionMethod
)

type Selection struct {
	Kind         SelectionKind
	Type         semaTypes.Type
	FieldIndex   int
	NeedsDeref   bool
	NeedsAddress bool
}

type Info = Facts

type Checked struct {
	FileSet        *gotoken.FileSet
	GoFiles        []*goast.File
	Facts          *Facts
	SourceLines    map[string][]string
	ImportedChecks []*Checked
	PkgPath        string
}
