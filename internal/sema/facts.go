package sema

import (
	goast "go/ast"
	gotoken "go/token"

	"goes/runtime"
)

type Facts struct {
	Bindings     map[any]*Symbol
	Captures     map[any]*Symbol
	Types        map[any]runtime.ScriptType
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
	Type         runtime.ScriptType
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
