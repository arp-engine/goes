package sema_test

import (
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goes/internal/sema"
	"goes/runtime"
)

func TestResolveRecordsClosureCaptureSymbol(t *testing.T) {
	src := `package main
func main() {
	x := 1
	_ = func() int { return x }
	_ = x
}
`

	file, info, err := resolveGoSource(src)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	funcDecl := file.Decls[0].(*goast.FuncDecl)
	funcLit := funcDecl.Body.List[1].(*goast.AssignStmt).Rhs[0].(*goast.FuncLit)
	capturedIdent := funcLit.Body.List[0].(*goast.ReturnStmt).Results[0].(*goast.Ident)
	outerIdent := funcDecl.Body.List[2].(*goast.AssignStmt).Rhs[0].(*goast.Ident)

	if got := len(info.Captures); got != 1 {
		t.Fatalf("capture count: want %d, got %d", 1, got)
	}

	capturedSym := info.Captures[capturedIdent]
	if capturedSym == nil {
		t.Fatalf("capture binding: got nil")
	}
	if capturedSym.Name != "x" {
		t.Fatalf("capture symbol name: want %q, got %q", "x", capturedSym.Name)
	}
	if capturedSym != info.Bindings[outerIdent] {
		t.Fatalf("capture symbol: want same symbol as outer x binding")
	}
}

func TestResolveRejectsFunctionBodyVarRedeclaringParam(t *testing.T) {
	src := `package main
func f(x int) {
	var x int
}
`

	_, _, err := resolveGoSource(src)
	if err == nil {
		t.Fatalf("Resolve: expected error")
	}
	if !strings.Contains(err.Error(), `duplicate declaration of "x"`) {
		t.Fatalf("Resolve error: want duplicate x, got %q", err)
	}
}

func TestResolveRejectsFunctionBodyShortDeclReusingOnlyParam(t *testing.T) {
	src := `package main
func f(x int) {
	x := 1
}
`

	_, _, err := resolveGoSource(src)
	if err == nil {
		t.Fatalf("Resolve: expected error")
	}
	if !strings.Contains(err.Error(), "must introduce at least one new variable") {
		t.Fatalf("Resolve error: want short decl error, got %q", err)
	}
}

func TestResolveAllowsSameMethodNameOnDifferentReceiversButBareNameStaysUndefined(t *testing.T) {
	src := `package main
type A struct{}
type B struct{}
func (a A) M() {}
func (b B) M() {}
func main() {
	M()
}
`

	_, _, err := resolveGoSource(src)
	if err == nil {
		t.Fatalf("Resolve: expected error")
	}
	if strings.Contains(err.Error(), `duplicate declaration of "M"`) {
		t.Fatalf("Resolve error: method name should not conflict in package scope, got %q", err)
	}
	if !strings.Contains(err.Error(), `undefined name "M"`) {
		t.Fatalf("Resolve error: bare method name should be undefined, got %q", err)
	}
}

func TestResolveRejectsMissingTypeName(t *testing.T) {
	src := `package main
func main() {
	var x Missing
	_ = x
}
`

	_, _, err := resolveGoSource(src)
	if err == nil {
		t.Fatalf("Resolve: expected error")
	}
	if !strings.Contains(err.Error(), `undefined type "Missing"`) {
		t.Fatalf("Resolve error: want undefined type, got %q", err)
	}
}

func TestResolveRejectsValueNameInTypePosition(t *testing.T) {
	src := `package main
func main() {
	y := 1
	var x y
	_ = x
}
`

	_, _, err := resolveGoSource(src)
	if err == nil {
		t.Fatalf("Resolve: expected error")
	}
	if !strings.Contains(err.Error(), `"y" is not a type`) {
		t.Fatalf("Resolve error: want non-type name error, got %q", err)
	}
}

func TestResolvePackageForwardTypeUpdatesSymbolTypes(t *testing.T) {
	src := `package main
var x T
func f() T {
	var zero T
	return zero
}
type T int
func main() {
	_ = x
	_ = f
}
`

	file, info, err := resolveGoSource(src)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	mainDecl := file.Decls[3].(*goast.FuncDecl)
	xUse := mainDecl.Body.List[0].(*goast.AssignStmt).Rhs[0].(*goast.Ident)
	fUse := mainDecl.Body.List[1].(*goast.AssignStmt).Rhs[0].(*goast.Ident)

	if got := info.Bindings[xUse].Type.String(); got != "T" {
		t.Fatalf("x symbol type: want %q, got %q", "T", got)
	}
	if got := info.Bindings[fUse].Type.String(); got != "func() T" {
		t.Fatalf("f symbol type: want %q, got %q", "func() T", got)
	}
}

func TestResolveRejectsDuplicateParameterNames(t *testing.T) {
	src := `package main
func f(x int, x int) {}
`

	_, _, err := resolveGoSource(src)
	if err == nil {
		t.Fatalf("Resolve: expected error")
	}
	if !strings.Contains(err.Error(), `duplicate declaration of "x"`) {
		t.Fatalf("Resolve error: want duplicate x, got %q", err)
	}
}

func TestResolveBindsArrayLengthName(t *testing.T) {
	src := `package main
const N = 3
var _ [N]int
`

	file, info, err := resolveGoSource(src)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	varDecl := file.Decls[1].(*goast.GenDecl).Specs[0].(*goast.ValueSpec)
	arrayType := varDecl.Type.(*goast.ArrayType)
	lenIdent := arrayType.Len.(*goast.Ident)

	binding := info.Bindings[lenIdent]
	if binding == nil {
		t.Fatalf("array length binding: got nil")
	}
	if binding.Name != "N" {
		t.Fatalf("array length symbol name: want %q, got %q", "N", binding.Name)
	}
}

func TestResolveArrayTypeKeepsLength(t *testing.T) {
	src := `package main
var a [3]int
func main() {
	_ = a
}
`

	file, info, err := resolveGoSource(src)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	mainDecl := file.Decls[1].(*goast.FuncDecl)
	aUse := mainDecl.Body.List[0].(*goast.AssignStmt).Rhs[0].(*goast.Ident)
	if got := info.Bindings[aUse].Type.String(); got != "[3]int" {
		t.Fatalf("a symbol type: want %q, got %q", "[3]int", got)
	}
}

func TestResolveRejectsMissingArrayLengthName(t *testing.T) {
	src := `package main
var _ [Missing]int
`

	_, _, err := resolveGoSource(src)
	if err == nil {
		t.Fatalf("Resolve: expected error")
	}
	if !strings.Contains(err.Error(), `undefined name "Missing"`) {
		t.Fatalf("Resolve error: want undefined array length name, got %q", err)
	}
}

func TestCheckRejectsIncompatibleAssignment(t *testing.T) {
	src := `package main
func main() int {
	var x int
	x = "nope"
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "cannot assign") {
		t.Fatalf("Check error: want assignment type error, got %q", err)
	}
}

func TestCheckAcceptsWellTypedProgram(t *testing.T) {
	src := `package main
func less(a int, b int) bool {
	return a < b
}

func id(x int) int {
	return x
}

func main() int {
	var a int = 1
	var b int = 2
	if a < b {
		a = id(b)
	}
	switch a {
	case b:
		a = id(a)
	default:
		a = id(0)
	}
	return 0
}
`

	if err := checkSource(src); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckPackageReturnsFacts(t *testing.T) {
	src := `package main
const N = 3

func main() int {
	x := N + 1
	return x
}
`

	file, checked, err := checkGoSourceDetailed(src)
	if err != nil {
		t.Fatalf("CheckPackage: %v", err)
	}
	if checked == nil || checked.Facts == nil {
		t.Fatalf("checked facts: got nil")
	}
	if len(checked.GoFiles) != 1 || checked.GoFiles[0] != file {
		t.Fatalf("checked go files: want original file slice")
	}

	mainDecl := file.Decls[1].(*goast.FuncDecl)
	shortDecl := mainDecl.Body.List[0].(*goast.AssignStmt)
	bin := shortDecl.Rhs[0].(*goast.BinaryExpr)
	nIdent := bin.X.(*goast.Ident)

	if checked.Facts.Bindings[nIdent] == nil {
		t.Fatalf("binding for N: got nil")
	}
	if checked.Facts.Bindings[nIdent].Name != "N" {
		t.Fatalf("binding name: want %q, got %q", "N", checked.Facts.Bindings[nIdent].Name)
	}
	if got := checked.Facts.Types[bin].String(); got != "int" {
		t.Fatalf("binary type: want %q, got %q", "int", got)
	}
	if checked.Facts.ConstExprs[nIdent] == nil {
		t.Fatalf("const expr for N: got nil")
	}
}

func TestCheckPackageKeepsCaptureFacts(t *testing.T) {
	src := `package main
func main() int {
	x := 1
	f := func() int { return x }
	return f()
}
`

	file, checked, err := checkGoSourceDetailed(src)
	if err != nil {
		t.Fatalf("CheckPackage: %v", err)
	}

	mainDecl := file.Decls[0].(*goast.FuncDecl)
	funcLit := mainDecl.Body.List[1].(*goast.AssignStmt).Rhs[0].(*goast.FuncLit)
	capturedIdent := funcLit.Body.List[0].(*goast.ReturnStmt).Results[0].(*goast.Ident)

	if checked.Facts.Captures[capturedIdent] == nil {
		t.Fatalf("capture fact: got nil")
	}
	if checked.Facts.Captures[capturedIdent].Name != "x" {
		t.Fatalf("capture name: want %q, got %q", "x", checked.Facts.Captures[capturedIdent].Name)
	}
}

func TestCheckPackageRecordsSelectorFacts(t *testing.T) {
	src := `package main
type pair struct { a int }
type counter struct { n int }

func (c counter) Add(v int) int {
	return c.n + v
}

func main() int {
	p := pair{a: 1}
	c := counter{n: 2}
	_ = p.a
	f := c.Add
	return f(3)
}
`

	file, checked, err := checkGoSourceDetailed(src)
	if err != nil {
		t.Fatalf("CheckPackage: %v", err)
	}

	mainDecl := file.Decls[3].(*goast.FuncDecl)
	fieldSel := mainDecl.Body.List[2].(*goast.AssignStmt).Rhs[0].(*goast.SelectorExpr)
	methodSel := mainDecl.Body.List[3].(*goast.AssignStmt).Rhs[0].(*goast.SelectorExpr)

	fieldSelection := checked.Facts.Selections[fieldSel]
	if fieldSelection == nil {
		t.Fatalf("field selection: got nil")
	}
	if fieldSelection.Kind != sema.SelectionField {
		t.Fatalf("field selection kind: want field, got %v", fieldSelection.Kind)
	}
	if fieldSelection.FieldIndex != 0 {
		t.Fatalf("field index: want %d, got %d", 0, fieldSelection.FieldIndex)
	}

	methodSelection := checked.Facts.Selections[methodSel]
	if methodSelection == nil {
		t.Fatalf("method selection: got nil")
	}
	if methodSelection.Kind != sema.SelectionMethod {
		t.Fatalf("method selection kind: want method, got %v", methodSelection.Kind)
	}
	if methodSelection.NeedsAddress {
		t.Fatalf("method selection needs address: want false, got true")
	}
}

func TestCheckPackageRecordsPointerReceiverSelectorCallFacts(t *testing.T) {
	src := `package main
type counter struct { n int }

func (c *counter) Add(v int) int {
	c.n = c.n + v
	return c.n
}

func main() int {
	var c counter
	return c.Add(3)
}
`

	file, checked, err := checkGoSourceDetailed(src)
	if err != nil {
		t.Fatalf("CheckPackage: %v", err)
	}

	mainDecl := file.Decls[2].(*goast.FuncDecl)
	call := mainDecl.Body.List[1].(*goast.ReturnStmt).Results[0].(*goast.CallExpr)
	sel := call.Fun.(*goast.SelectorExpr)
	selection := checked.Facts.Selections[sel]
	if selection == nil {
		t.Fatalf("selection: got nil")
	}
	if selection.Kind != sema.SelectionMethod {
		t.Fatalf("selection kind: want method, got %v", selection.Kind)
	}
	if !selection.NeedsAddress {
		t.Fatalf("selection needs address: want true, got false")
	}
}

func TestCheckPackageRecordsPointerFieldSelectorFacts(t *testing.T) {
	src := `package main
type pair struct { a int }

func main() int {
	v := pair{a: 3}
	p := &v
	return p.a
}
`

	file, checked, err := checkGoSourceDetailed(src)
	if err != nil {
		t.Fatalf("CheckPackage: %v", err)
	}

	mainDecl := file.Decls[1].(*goast.FuncDecl)
	sel := mainDecl.Body.List[2].(*goast.ReturnStmt).Results[0].(*goast.SelectorExpr)
	selection := checked.Facts.Selections[sel]
	if selection == nil {
		t.Fatalf("selection: got nil")
	}
	if selection.Kind != sema.SelectionField {
		t.Fatalf("selection kind: want field, got %v", selection.Kind)
	}
	if !selection.NeedsDeref {
		t.Fatalf("selection needs deref: want true, got false")
	}
}

func TestCheckGoPackageWithOptionsCarriesGoASTMetadata(t *testing.T) {
	src := `package main
func main() int { return 0 }
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	checked, err := sema.CheckGoPackageWithOptions(fset, []*goast.File{goFile}, sema.CheckOptions{})
	if err != nil {
		t.Fatalf("CheckGoPackageWithOptions: %v", err)
	}
	if checked.FileSet != fset {
		t.Fatalf("FileSet: want original file set")
	}
	if len(checked.GoFiles) != 1 || checked.GoFiles[0] != goFile {
		t.Fatalf("GoFiles: want original go/ast file slice")
	}
	if checked.Facts == nil {
		t.Fatalf("Facts: got nil")
	}
}

func DisabledDuplicateCheckGoPackageWithOptionsExposesGoASTBindings(t *testing.T) {
	src := `package main
func main() int {
	x := 1
	return x
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	checked, err := sema.CheckGoPackageWithOptions(fset, []*goast.File{goFile}, sema.CheckOptions{})
	if err != nil {
		t.Fatalf("CheckGoPackageWithOptions: %v", err)
	}

	mainDecl := checked.GoFiles[0].Decls[0].(*goast.FuncDecl)
	ident := mainDecl.Body.List[1].(*goast.ReturnStmt).Results[0].(*goast.Ident)
	binding := checked.Facts.Bindings[ident]
	if binding == nil {
		t.Fatalf("binding: got nil")
	}
	if binding.Name != "x" {
		t.Fatalf("binding name: want %q, got %q", "x", binding.Name)
	}
}

func DisabledDuplicateCheckGoPackageWithOptionsRecordsGoASTConstExprs(t *testing.T) {
	src := `package main
const N = 1 + 2

func main() int {
	return N
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	checked, err := sema.CheckGoPackageWithOptions(fset, []*goast.File{goFile}, sema.CheckOptions{})
	if err != nil {
		t.Fatalf("CheckGoPackageWithOptions: %v", err)
	}

	mainDecl := goFile.Decls[1].(*goast.FuncDecl)
	ident := mainDecl.Body.List[0].(*goast.ReturnStmt).Results[0].(*goast.Ident)
	constExpr := checked.Facts.ConstExprs[ident]
	if constExpr == nil {
		t.Fatalf("const expr: got nil")
	}
	if _, ok := constExpr.(*goast.BinaryExpr); !ok {
		t.Fatalf("const expr type: want %T, got %T", &goast.BinaryExpr{}, constExpr)
	}
}

func DisabledDuplicateCheckGoPackageWithOptionsAcceptsWellTypedProgram(t *testing.T) {
	src := `package main
func less(a int, b int) bool { return a < b }
func id(x int) int { return x }

func main() int {
	a := 1
	b := 2
	if less(a, b) {
		a = id(b)
	}
	return a
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if _, err := sema.CheckGoPackageWithOptions(fset, []*goast.File{goFile}, sema.CheckOptions{}); err != nil {
		t.Fatalf("CheckGoPackageWithOptions: %v", err)
	}
}

func DisabledDuplicateCheckGoPackageWithOptionsRejectsNonBoolIfCondition(t *testing.T) {
	src := `package main
func main() int {
	if 1 {
		return 1
	}
	return 0
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	_, err = sema.CheckGoPackageWithOptions(fset, []*goast.File{goFile}, sema.CheckOptions{})
	if err == nil {
		t.Fatalf("CheckGoPackageWithOptions: expected error")
	}
	if !strings.Contains(err.Error(), "condition must be bool") {
		t.Fatalf("error: got %q", err)
	}
}

func DisabledDuplicateCheckGoPackageWithOptionsRejectsIncompatibleAssignment(t *testing.T) {
	src := `package main
func main() int {
	var x int
	x = "nope"
	return 0
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	_, err = sema.CheckGoPackageWithOptions(fset, []*goast.File{goFile}, sema.CheckOptions{})
	if err == nil {
		t.Fatalf("CheckGoPackageWithOptions: expected error")
	}
	if !strings.Contains(err.Error(), "cannot assign") {
		t.Fatalf("error: got %q", err)
	}
}

func DisabledDuplicateCheckGoPackageWithOptionsAcceptsSelectorIndexSliceAndCompositeExpressions(t *testing.T) {
	src := `package main
type pair struct { a int }

func main() int {
	p := pair{a: 1}
	xs := [3]int{1, 2, 3}
	ys := xs[0:2]
	return p.a + ys[0]
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	checked, err := sema.CheckGoPackageWithOptions(fset, []*goast.File{goFile}, sema.CheckOptions{})
	if err != nil {
		t.Fatalf("CheckGoPackageWithOptions: %v", err)
	}

	mainDecl := goFile.Decls[1].(*goast.FuncDecl)
	shortDecl := mainDecl.Body.List[2].(*goast.AssignStmt)
	slice := shortDecl.Rhs[0].(*goast.SliceExpr)
	ret := mainDecl.Body.List[3].(*goast.ReturnStmt)
	bin := ret.Results[0].(*goast.BinaryExpr)
	sel := bin.X.(*goast.SelectorExpr)
	index := bin.Y.(*goast.IndexExpr)

	if got := checked.Facts.Types[bin].String(); got != "int" {
		t.Fatalf("binary type: want %q, got %q", "int", got)
	}
	if got := checked.Facts.Types[slice].String(); got != "[]int" {
		t.Fatalf("slice type: want %q, got %q", "[]int", got)
	}
	if selection := checked.Facts.Selections[sel]; selection == nil || selection.Kind != sema.SelectionField {
		t.Fatalf("selector selection: got %#v", selection)
	}
	if got := checked.Facts.Types[index].String(); got != "int" {
		t.Fatalf("index type: want %q, got %q", "int", got)
	}
}

func DisabledDuplicateCheckGoPackageWithOptionsRejectsInvalidSelectorIndexAndSliceUse(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "unknown field",
			src: `package main
type pair struct { a int }
func main() int {
	p := pair{a: 1}
	return p.b
}
`,
			want: "unknown field",
		},
		{
			name: "bad index type",
			src: `package main
func main() int {
	xs := [2]int{1, 2}
	return xs["x"]
}
`,
			want: "index must be integer",
		},
		{
			name: "bad slice target",
			src: `package main
func main() int {
	x := 1
	_ = x[0:1]
	return 0
}
`,
			want: "cannot slice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := gotoken.NewFileSet()
			goFile, err := goparser.ParseFile(fset, "main.go", tt.src, goparser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("ParseFile: %v", err)
			}
			_, err = sema.CheckGoPackageWithOptions(fset, []*goast.File{goFile}, sema.CheckOptions{})
			if err == nil {
				t.Fatalf("CheckGoPackageWithOptions: expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error: got %q", err)
			}
		})
	}
}

func DisabledDuplicateCheckGoPackageWithOptionsRejectsUnsupportedGoASTShapes(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "non main package",
			src:  "package worker\nfunc main() int { return 0 }\n",
			want: "only package main is supported",
		},
		{
			name: "import declaration",
			src:  "package main\nimport \"fmt\"\nfunc main() int { return 0 }\n",
			want: "imported package \"fmt\" not found",
		},
		{
			name: "type parameter",
			src:  "package main\ntype Box[T any] struct{}\nfunc main() int { return 0 }\n",
			want: "type parameters are not supported",
		},
		{
			name: "interface type",
			src:  "package main\ntype Reader interface{}\nfunc main() int { return 0 }\n",
			want: "interface types are not supported",
		},
		{
			name: "embedded field",
			src:  "package main\ntype inner struct{}\ntype outer struct{ inner }\nfunc main() int { return 0 }\n",
			want: "embedded fields are not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := gotoken.NewFileSet()
			goFile, err := goparser.ParseFile(fset, "main.go", tt.src, goparser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("ParseFile: %v", err)
			}

			_, err = sema.CheckGoPackageWithOptions(fset, []*goast.File{goFile}, sema.CheckOptions{})
			if err == nil {
				t.Fatalf("CheckGoPackageWithOptions: expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error: want %q in %q", tt.want, err)
			}
		})
	}
}

func DisabledDuplicateResolveGoPackageWithOptionsRejectsDuplicateTopLevelNames(t *testing.T) {
	src := `package main
var x int
var x int
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	_, err = sema.ResolveGoPackageWithOptions(fset, []*goast.File{goFile}, sema.ResolveOptions{})
	if err == nil {
		t.Fatalf("ResolveGoPackageWithOptions: expected error")
	}
	if !strings.Contains(err.Error(), `duplicate declaration of "x"`) {
		t.Fatalf("error: got %q", err)
	}
}

func DisabledDuplicateResolveGoPackageWithOptionsRecordsArrayLengthBinding(t *testing.T) {
	src := `package main
const N = 3
var _ [N]int
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	info, err := sema.ResolveGoPackageWithOptions(fset, []*goast.File{goFile}, sema.ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveGoPackageWithOptions: %v", err)
	}

	varDecl := goFile.Decls[1].(*goast.GenDecl).Specs[0].(*goast.ValueSpec)
	arrayType := varDecl.Type.(*goast.ArrayType)
	lenIdent := arrayType.Len.(*goast.Ident)

	binding := info.Bindings[lenIdent]
	if binding == nil {
		t.Fatalf("binding: got nil")
	}
	if binding.Name != "N" {
		t.Fatalf("binding name: want %q, got %q", "N", binding.Name)
	}
}

func DisabledDuplicateResolveGoPackageWithOptionsRejectsFunctionBodyVarRedeclaringParam(t *testing.T) {
	src := `package main
func f(x int) int {
	var x int
	return x
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	_, err = sema.ResolveGoPackageWithOptions(fset, []*goast.File{goFile}, sema.ResolveOptions{})
	if err == nil {
		t.Fatalf("ResolveGoPackageWithOptions: expected error")
	}
	if !strings.Contains(err.Error(), `duplicate declaration of "x"`) {
		t.Fatalf("error: got %q", err)
	}
}

func DisabledDuplicateResolveGoPackageWithOptionsRejectsShortDeclReusingOnlyParam(t *testing.T) {
	src := `package main
func f(x int) int {
	x := 1
	return x
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	_, err = sema.ResolveGoPackageWithOptions(fset, []*goast.File{goFile}, sema.ResolveOptions{})
	if err == nil {
		t.Fatalf("ResolveGoPackageWithOptions: expected error")
	}
	if !strings.Contains(err.Error(), "must introduce at least one new variable") {
		t.Fatalf("error: got %q", err)
	}
}

func DisabledDuplicateResolveGoPackageWithOptionsRecordsClosureCapture(t *testing.T) {
	src := `package main
func main() int {
	x := 1
	f := func() int { return x }
	return f()
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	info, err := sema.ResolveGoPackageWithOptions(fset, []*goast.File{goFile}, sema.ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveGoPackageWithOptions: %v", err)
	}

	mainDecl := goFile.Decls[0].(*goast.FuncDecl)
	funcLit := mainDecl.Body.List[1].(*goast.AssignStmt).Rhs[0].(*goast.FuncLit)
	capturedIdent := funcLit.Body.List[0].(*goast.ReturnStmt).Results[0].(*goast.Ident)

	capturedSym := info.Captures[capturedIdent]
	if capturedSym == nil {
		t.Fatalf("capture: got nil")
	}
	if capturedSym.Name != "x" {
		t.Fatalf("capture name: want %q, got %q", "x", capturedSym.Name)
	}
}

func DisabledDuplicateResolveGoPackageWithOptionsBindsRangeDefineNames(t *testing.T) {
	src := `package main
func main() int {
	xs := []int{1, 2}
	for i, v := range xs {
		_ = i
		_ = v
	}
	return 0
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	info, err := sema.ResolveGoPackageWithOptions(fset, []*goast.File{goFile}, sema.ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveGoPackageWithOptions: %v", err)
	}

	mainDecl := goFile.Decls[0].(*goast.FuncDecl)
	rangeStmt := mainDecl.Body.List[1].(*goast.RangeStmt)
	iUse := rangeStmt.Body.List[0].(*goast.AssignStmt).Rhs[0].(*goast.Ident)
	vUse := rangeStmt.Body.List[1].(*goast.AssignStmt).Rhs[0].(*goast.Ident)

	if info.Bindings[iUse] == nil || info.Bindings[iUse].Name != "i" {
		t.Fatalf("range i binding: got %#v", info.Bindings[iUse])
	}
	if info.Bindings[vUse] == nil || info.Bindings[vUse].Name != "v" {
		t.Fatalf("range v binding: got %#v", info.Bindings[vUse])
	}
}

func DisabledDuplicateResolveGoPackageWithOptionsResolvesSelectorIndexSliceAndCompositeExprs(t *testing.T) {
	src := `package main
type pair struct { a int }

func main() int {
	p := pair{a: 1}
	xs := []int{1, 2, 3}
	_ = p.a
	_ = xs[0]
	_ = xs[0:2]
	return 0
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	info, err := sema.ResolveGoPackageWithOptions(fset, []*goast.File{goFile}, sema.ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveGoPackageWithOptions: %v", err)
	}

	mainDecl := goFile.Decls[1].(*goast.FuncDecl)
	sel := mainDecl.Body.List[2].(*goast.AssignStmt).Rhs[0].(*goast.SelectorExpr)
	index := mainDecl.Body.List[3].(*goast.AssignStmt).Rhs[0].(*goast.IndexExpr)
	slice := mainDecl.Body.List[4].(*goast.AssignStmt).Rhs[0].(*goast.SliceExpr)

	selBase := sel.X.(*goast.Ident)
	indexBase := index.X.(*goast.Ident)
	sliceBase := slice.X.(*goast.Ident)
	if info.Bindings[selBase] == nil || info.Bindings[selBase].Name != "p" {
		t.Fatalf("selector base binding: got %#v", info.Bindings[selBase])
	}
	if info.Bindings[indexBase] == nil || info.Bindings[indexBase].Name != "xs" {
		t.Fatalf("index base binding: got %#v", info.Bindings[indexBase])
	}
	if info.Bindings[sliceBase] == nil || info.Bindings[sliceBase].Name != "xs" {
		t.Fatalf("slice base binding: got %#v", info.Bindings[sliceBase])
	}
}

func TestCheckGoPackageWithOptionsExposesGoASTBindings(t *testing.T) {
	src := `package main
func main() int {
	x := 1
	return x
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	checked, err := sema.CheckGoPackageWithOptions(fset, []*goast.File{goFile}, sema.CheckOptions{})
	if err != nil {
		t.Fatalf("CheckGoPackageWithOptions: %v", err)
	}

	mainDecl := checked.GoFiles[0].Decls[0].(*goast.FuncDecl)
	ident := mainDecl.Body.List[1].(*goast.ReturnStmt).Results[0].(*goast.Ident)
	binding := checked.Facts.Bindings[ident]
	if binding == nil {
		t.Fatalf("binding: got nil")
	}
	if binding.Name != "x" {
		t.Fatalf("binding name: want %q, got %q", "x", binding.Name)
	}
}

func TestCheckGoPackageWithOptionsRecordsGoASTConstExprs(t *testing.T) {
	src := `package main
const N = 1 + 2

func main() int {
	return N
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	checked, err := sema.CheckGoPackageWithOptions(fset, []*goast.File{goFile}, sema.CheckOptions{})
	if err != nil {
		t.Fatalf("CheckGoPackageWithOptions: %v", err)
	}

	mainDecl := goFile.Decls[1].(*goast.FuncDecl)
	ident := mainDecl.Body.List[0].(*goast.ReturnStmt).Results[0].(*goast.Ident)
	constExpr := checked.Facts.ConstExprs[ident]
	if constExpr == nil {
		t.Fatalf("const expr: got nil")
	}
	if _, ok := constExpr.(*goast.BinaryExpr); !ok {
		t.Fatalf("const expr type: want %T, got %T", &goast.BinaryExpr{}, constExpr)
	}
}

func TestCheckGoPackageWithOptionsAcceptsWellTypedProgram(t *testing.T) {
	src := `package main
func less(a int, b int) bool { return a < b }
func id(x int) int { return x }

func main() int {
	a := 1
	b := 2
	if less(a, b) {
		a = id(b)
	}
	return a
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if _, err := sema.CheckGoPackageWithOptions(fset, []*goast.File{goFile}, sema.CheckOptions{}); err != nil {
		t.Fatalf("CheckGoPackageWithOptions: %v", err)
	}
}

func TestCheckGoPackageWithOptionsRejectsNonBoolIfCondition(t *testing.T) {
	src := `package main
func main() int {
	if 1 {
		return 1
	}
	return 0
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	_, err = sema.CheckGoPackageWithOptions(fset, []*goast.File{goFile}, sema.CheckOptions{})
	if err == nil {
		t.Fatalf("CheckGoPackageWithOptions: expected error")
	}
	if !strings.Contains(err.Error(), "condition must be bool") {
		t.Fatalf("error: got %q", err)
	}
}

func TestCheckGoPackageWithOptionsRejectsIncompatibleAssignment(t *testing.T) {
	src := `package main
func main() int {
	var x int
	x = "nope"
	return 0
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	_, err = sema.CheckGoPackageWithOptions(fset, []*goast.File{goFile}, sema.CheckOptions{})
	if err == nil {
		t.Fatalf("CheckGoPackageWithOptions: expected error")
	}
	if !strings.Contains(err.Error(), "cannot assign") {
		t.Fatalf("error: got %q", err)
	}
}

func TestCheckGoPackageWithOptionsAcceptsSelectorIndexSliceAndCompositeExpressions(t *testing.T) {
	src := `package main
type pair struct { a int }

func main() int {
	p := pair{a: 1}
	xs := [3]int{1, 2, 3}
	ys := xs[0:2]
	return p.a + ys[0]
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	checked, err := sema.CheckGoPackageWithOptions(fset, []*goast.File{goFile}, sema.CheckOptions{})
	if err != nil {
		t.Fatalf("CheckGoPackageWithOptions: %v", err)
	}

	mainDecl := goFile.Decls[1].(*goast.FuncDecl)
	shortDecl := mainDecl.Body.List[2].(*goast.AssignStmt)
	slice := shortDecl.Rhs[0].(*goast.SliceExpr)
	ret := mainDecl.Body.List[3].(*goast.ReturnStmt)
	bin := ret.Results[0].(*goast.BinaryExpr)
	sel := bin.X.(*goast.SelectorExpr)
	index := bin.Y.(*goast.IndexExpr)

	if got := checked.Facts.Types[bin].String(); got != "int" {
		t.Fatalf("binary type: want %q, got %q", "int", got)
	}
	if got := checked.Facts.Types[slice].String(); got != "[]int" {
		t.Fatalf("slice type: want %q, got %q", "[]int", got)
	}
	if selection := checked.Facts.Selections[sel]; selection == nil || selection.Kind != sema.SelectionField {
		t.Fatalf("selector selection: got %#v", selection)
	}
	if got := checked.Facts.Types[index].String(); got != "int" {
		t.Fatalf("index type: want %q, got %q", "int", got)
	}
}

func TestCheckGoPackageWithOptionsRejectsInvalidSelectorIndexAndSliceUse(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "unknown field",
			src: `package main
type pair struct { a int }
func main() int {
	p := pair{a: 1}
	return p.b
}
`,
			want: "unknown field",
		},
		{
			name: "bad index type",
			src: `package main
func main() int {
	xs := [2]int{1, 2}
	return xs["x"]
}
`,
			want: "index must be integer",
		},
		{
			name: "bad slice target",
			src: `package main
func main() int {
	x := 1
	_ = x[0:1]
	return 0
}
`,
			want: "cannot slice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := gotoken.NewFileSet()
			goFile, err := goparser.ParseFile(fset, "main.go", tt.src, goparser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("ParseFile: %v", err)
			}
			_, err = sema.CheckGoPackageWithOptions(fset, []*goast.File{goFile}, sema.CheckOptions{})
			if err == nil {
				t.Fatalf("CheckGoPackageWithOptions: expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error: got %q", err)
			}
		})
	}
}

func TestCheckGoPackageWithOptionsRejectsUnsupportedGoASTShapes(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "non main package",
			src:  "package worker\nfunc main() int { return 0 }\n",
			want: "only package main is supported",
		},
		{
			name: "import declaration",
			src:  "package main\nimport \"fmt\"\nfunc main() int { return 0 }\n",
			want: "imported package \"fmt\" not found",
		},
		{
			name: "type parameter",
			src:  "package main\ntype Box[T any] struct{}\nfunc main() int { return 0 }\n",
			want: "type parameters are not supported",
		},
		{
			name: "interface type",
			src:  "package main\ntype Reader interface{}\nfunc main() int { return 0 }\n",
			want: "interface types are not supported",
		},
		{
			name: "embedded field",
			src:  "package main\ntype inner struct{}\ntype outer struct{ inner }\nfunc main() int { return 0 }\n",
			want: "embedded fields are not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := gotoken.NewFileSet()
			goFile, err := goparser.ParseFile(fset, "main.go", tt.src, goparser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("ParseFile: %v", err)
			}

			_, err = sema.CheckGoPackageWithOptions(fset, []*goast.File{goFile}, sema.CheckOptions{})
			if err == nil {
				t.Fatalf("CheckGoPackageWithOptions: expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error: want %q in %q", tt.want, err)
			}
		})
	}
}

func TestResolveGoPackageWithOptionsRejectsDuplicateTopLevelNames(t *testing.T) {
	src := `package main
var x int
var x int
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	_, err = sema.ResolveGoPackageWithOptions(fset, []*goast.File{goFile}, sema.ResolveOptions{})
	if err == nil {
		t.Fatalf("ResolveGoPackageWithOptions: expected error")
	}
	if !strings.Contains(err.Error(), `duplicate declaration of "x"`) {
		t.Fatalf("error: got %q", err)
	}
}

func TestResolveGoPackageWithOptionsRecordsArrayLengthBinding(t *testing.T) {
	src := `package main
const N = 3
var _ [N]int
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	info, err := sema.ResolveGoPackageWithOptions(fset, []*goast.File{goFile}, sema.ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveGoPackageWithOptions: %v", err)
	}

	varDecl := goFile.Decls[1].(*goast.GenDecl).Specs[0].(*goast.ValueSpec)
	arrayType := varDecl.Type.(*goast.ArrayType)
	lenIdent := arrayType.Len.(*goast.Ident)

	binding := info.Bindings[lenIdent]
	if binding == nil {
		t.Fatalf("binding: got nil")
	}
	if binding.Name != "N" {
		t.Fatalf("binding name: want %q, got %q", "N", binding.Name)
	}
}

func TestResolveGoPackageWithOptionsRejectsFunctionBodyVarRedeclaringParam(t *testing.T) {
	src := `package main
func f(x int) int {
	var x int
	return x
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	_, err = sema.ResolveGoPackageWithOptions(fset, []*goast.File{goFile}, sema.ResolveOptions{})
	if err == nil {
		t.Fatalf("ResolveGoPackageWithOptions: expected error")
	}
	if !strings.Contains(err.Error(), `duplicate declaration of "x"`) {
		t.Fatalf("error: got %q", err)
	}
}

func TestResolveGoPackageWithOptionsRejectsShortDeclReusingOnlyParam(t *testing.T) {
	src := `package main
func f(x int) int {
	x := 1
	return x
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	_, err = sema.ResolveGoPackageWithOptions(fset, []*goast.File{goFile}, sema.ResolveOptions{})
	if err == nil {
		t.Fatalf("ResolveGoPackageWithOptions: expected error")
	}
	if !strings.Contains(err.Error(), "must introduce at least one new variable") {
		t.Fatalf("error: got %q", err)
	}
}

func TestResolveGoPackageWithOptionsRecordsClosureCapture(t *testing.T) {
	src := `package main
func main() int {
	x := 1
	f := func() int { return x }
	return f()
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	info, err := sema.ResolveGoPackageWithOptions(fset, []*goast.File{goFile}, sema.ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveGoPackageWithOptions: %v", err)
	}

	mainDecl := goFile.Decls[0].(*goast.FuncDecl)
	funcLit := mainDecl.Body.List[1].(*goast.AssignStmt).Rhs[0].(*goast.FuncLit)
	capturedIdent := funcLit.Body.List[0].(*goast.ReturnStmt).Results[0].(*goast.Ident)

	capturedSym := info.Captures[capturedIdent]
	if capturedSym == nil {
		t.Fatalf("capture: got nil")
	}
	if capturedSym.Name != "x" {
		t.Fatalf("capture name: want %q, got %q", "x", capturedSym.Name)
	}
}

func TestResolveGoPackageWithOptionsBindsRangeDefineNames(t *testing.T) {
	src := `package main
func main() int {
	xs := []int{1, 2}
	for i, v := range xs {
		_ = i
		_ = v
	}
	return 0
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	info, err := sema.ResolveGoPackageWithOptions(fset, []*goast.File{goFile}, sema.ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveGoPackageWithOptions: %v", err)
	}

	mainDecl := goFile.Decls[0].(*goast.FuncDecl)
	rangeStmt := mainDecl.Body.List[1].(*goast.RangeStmt)
	iUse := rangeStmt.Body.List[0].(*goast.AssignStmt).Rhs[0].(*goast.Ident)
	vUse := rangeStmt.Body.List[1].(*goast.AssignStmt).Rhs[0].(*goast.Ident)

	if info.Bindings[iUse] == nil || info.Bindings[iUse].Name != "i" {
		t.Fatalf("range i binding: got %#v", info.Bindings[iUse])
	}
	if info.Bindings[vUse] == nil || info.Bindings[vUse].Name != "v" {
		t.Fatalf("range v binding: got %#v", info.Bindings[vUse])
	}
}

func TestCheckRejectsIntegerRangeValueVariable(t *testing.T) {
	err := checkSource(`package main
func main() int {
	for i, v := range 5 {
		_ = i
		_ = v
	}
	return 0
}
`)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "integer range permits at most one iteration variable") {
		t.Fatalf("Check error: got %q", err)
	}
}

func TestCheckRejectsRangeAssignToIncompatibleExistingVariable(t *testing.T) {
	err := checkSource(`package main
func main() int {
	var key string
	for key = range []int{1} {
	}
	return 0
}
`)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "cannot assign") {
		t.Fatalf("Check error: got %q", err)
	}
}

func TestResolveGoPackageWithOptionsResolvesSelectorIndexSliceAndCompositeExprs(t *testing.T) {
	src := `package main
type pair struct { a int }

func main() int {
	p := pair{a: 1}
	xs := []int{1, 2, 3}
	_ = p.a
	_ = xs[0]
	_ = xs[0:2]
	return 0
}
`

	fset := gotoken.NewFileSet()
	goFile, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	info, err := sema.ResolveGoPackageWithOptions(fset, []*goast.File{goFile}, sema.ResolveOptions{})
	if err != nil {
		t.Fatalf("ResolveGoPackageWithOptions: %v", err)
	}

	mainDecl := goFile.Decls[1].(*goast.FuncDecl)
	sel := mainDecl.Body.List[2].(*goast.AssignStmt).Rhs[0].(*goast.SelectorExpr)
	index := mainDecl.Body.List[3].(*goast.AssignStmt).Rhs[0].(*goast.IndexExpr)
	slice := mainDecl.Body.List[4].(*goast.AssignStmt).Rhs[0].(*goast.SliceExpr)

	selBase := sel.X.(*goast.Ident)
	indexBase := index.X.(*goast.Ident)
	sliceBase := slice.X.(*goast.Ident)
	if info.Bindings[selBase] == nil || info.Bindings[selBase].Name != "p" {
		t.Fatalf("selector base binding: got %#v", info.Bindings[selBase])
	}
	if info.Bindings[indexBase] == nil || info.Bindings[indexBase].Name != "xs" {
		t.Fatalf("index base binding: got %#v", info.Bindings[indexBase])
	}
	if info.Bindings[sliceBase] == nil || info.Bindings[sliceBase].Name != "xs" {
		t.Fatalf("slice base binding: got %#v", info.Bindings[sliceBase])
	}
}

func TestCheckRejectsWrongFunctionArgCount(t *testing.T) {
	src := `package main
func f(x int) int {
	return x
}

func main() int {
	_ = f()
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "argument count") {
		t.Fatalf("Check error: want argument count error, got %q", err)
	}
}

func TestCheckComparisonProducesBoolForIf(t *testing.T) {
	src := `package main
func main() int {
	var a int = 1
	var b int = 2
	if a < b {
		a = b
	}
	return 0
}
`

	if err := checkSource(src); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckRejectsWrongReturnValueCount(t *testing.T) {
	src := `package main
func pair() (int, int) {
	return 1
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "return value count mismatch") {
		t.Fatalf("Check error: got %q", err)
	}
}

func TestCheckAcceptsZeroResultFunctions(t *testing.T) {
	src := `package main
func f() {
}
func main() int {
	return 0
}
`

	if err := checkSource(src); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckAcceptsMultiResultFunctions(t *testing.T) {
	src := `package main
func pair() (int, int) {
	return 1, 2
}
func main() int {
	return 0
}
`

	if err := checkSource(src); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckAcceptsCompoundAssignmentAndIncDec(t *testing.T) {
	src := `package main
func main() int {
	var i int = 1
	var j int64 = 2
	var f float64 = 1.5
	var s string = "go"
	i += 2
	i <<= 1
	j &= 3
	f -= 0.5
	s += "es"
	i++
	j--
	_ = i
	_ = j
	_ = f
	_ = s
	return 0
}
`

	if err := checkSource(src); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckAcceptsMultiValueAssignmentAndReturnForwarding(t *testing.T) {
	err := checkSource(`package main
func pair() (int, int) { return 1, 2 }
func wrap() (int, int) { return pair() }
func main() int {
	a, b := pair()
	return a + b
}
`)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckRejectsMultiValueInSingleValueContext(t *testing.T) {
	err := checkSource(`package main
func pair() (int, int) { return 1, 2 }
func main() int {
	x := pair()
	_ = x
	return 0
}
`)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "multiple values in single-value context") {
		t.Fatalf("Check error: got %q", err)
	}
}

func TestCheckAcceptsVariadicCallAndSliceExpansion(t *testing.T) {
	err := checkSource(`package main
func sum(xs ...int) int { return 0 }
func main() int {
	xs := []int{1, 2, 3}
	sum(1, 2, 3)
	sum(xs...)
	return 0
}
`)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckAcceptsMakeSliceAndAppend(t *testing.T) {
	err := checkSource(`package main
func main() int {
	xs := make([]int, 2)
	ys := make([]int, 0, 4)
	xs = append(xs, 3, 4)
	ys = append(ys, xs...)
	return len(xs) + len(ys)
}
`)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckAcceptsClearMapAndSlice(t *testing.T) {
	err := checkSource(`package main
func main() {
	m := make(map[string]int)
	xs := []int{1, 2}
	clear(m)
	clear(xs)
}
`)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckRejectsInvalidMakeSliceAndAppend(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "make slice missing len",
			src: `package main
func main() int {
	_ = make([]int)
	return 0
}
`,
			want: "make",
		},
		{
			name: "make slice non integer len",
			src: `package main
func main() int {
	_ = make([]int, "bad")
	return 0
}
`,
			want: "integer",
		},
		{
			name: "make slice float len",
			src: `package main
func main() int {
	_ = make([]int, 1.5)
	return 0
}
`,
			want: "integer",
		},
		{
			name: "append non slice",
			src: `package main
func main() int {
	_ = append(1, 2)
	return 0
}
`,
			want: "append",
		},
		{
			name: "append element type mismatch",
			src: `package main
func main() int {
	xs := []int{1}
	xs = append(xs, "bad")
	return 0
}
`,
			want: "cannot",
		},
		{
			name: "append expansion type mismatch",
			src: `package main
func main() int {
	xs := []int{1}
	ys := []string{"bad"}
	xs = append(xs, ys...)
	return 0
}
`,
			want: "cannot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkSource(tt.src)
			if err == nil {
				t.Fatalf("Check: expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Check error: want %q in %q", tt.want, err)
			}
		})
	}
}

func TestCheckRejectsClearMisuse(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "clear non map or slice",
			src: `package main
func main() {
	clear(1)
}
`,
			want: "clear",
		},
		{
			name: "clear wrong arity",
			src: `package main
func main() {
	xs := []int{1}
	clear(xs, xs)
}
`,
			want: "clear",
		},
		{
			name: "clear has no value",
			src: `package main
func main() {
	xs := []int{1}
	_ = clear(xs)
}
`,
			want: "assignment count",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkSource(tt.src)
			if err == nil {
				t.Fatalf("Check: expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Check error: want %q in %q", tt.want, err)
			}
		})
	}
}

func TestCheckRejectsNonFinalMultiValueArgument(t *testing.T) {
	err := checkSource(`package main
func pair() (int, int) { return 1, 2 }
func sum(a int, b int, c int) int { return 0 }
func main() int {
	return sum(pair(), 3)
}
`)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
}

func TestCheckAcceptsBitwiseAndShiftExpressions(t *testing.T) {
	src := `package main
func main() int {
	var i int = 3 & 1
	var j int = 8 >> 1
	var k int = (i | 4) ^ 2
	var m int = k &^ 1
	_ = i
	_ = j
	_ = m
	return 0
}
`

	if err := checkSource(src); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckRejectsBitwiseAndShiftOnNonIntegers(t *testing.T) {
	src := `package main
func main() int {
	var f float64 = 1.5
	_ = f << 1
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "integer") && !strings.Contains(err.Error(), "shift") {
		t.Fatalf("Check error: got %q", err)
	}
}

func TestCheckAcceptsSliceExpressionsOnSlicesAndArrays(t *testing.T) {
	src := `package main
func main() int {
	xs := []int{1, 2, 3}
	ys := xs[0:2]
	zs := [3]int{4, 5, 6}
	_ = zs[1:3]
	_ = ys
	return 0
}
`

	if err := checkSource(src); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckRejectsInvalidCompoundAssignmentTypes(t *testing.T) {
	src := `package main
func main() int {
	var s string = "go"
	s -= "x"
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "cannot assign") && !strings.Contains(err.Error(), "operator") && !strings.Contains(err.Error(), "arithmetic operands") {
		t.Fatalf("Check error: got %q", err)
	}
}

func TestCheckRejectsDuplicateStructLiteralKeys(t *testing.T) {
	src := `package main
type pair struct {
	a int
	b int
}
func main() int {
	_ = pair{a: 1, a: 2}
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "duplicate field") {
		t.Fatalf("Check error: got %q", err)
	}
}

func TestCheckRejectsKeyedArrayLiteralsOutsideV2Scope(t *testing.T) {
	src := `package main
func main() int {
	_ = [2]int{0: 1, 1: 2}
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "keyed array literals are not supported") {
		t.Fatalf("Check error: got %q", err)
	}
}

func TestCheckRejectsKeyedSliceLiteralsOutsideV2Scope(t *testing.T) {
	src := `package main
func main() int {
	_ = []int{0: 1, 1: 2}
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "keyed slice literals are not supported") {
		t.Fatalf("Check error: got %q", err)
	}
}

func TestCheckAcceptsNestedCompositeLiteralsInCurrentV2Scope(t *testing.T) {
	src := `package main
type pair struct {
	a int
	b int
}
func main() int {
	_ = [2]pair{{1, 2}, {3, 4}}
	_ = []pair{{1, 2}, {3, 4}}
	return 0
}
`

	if err := checkSource(src); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckRejectsNonBoolIfCondition(t *testing.T) {
	src := `package main
func main() int {
	if 1 {
	}
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "condition must be bool") {
		t.Fatalf("Check error: want bool condition error, got %q", err)
	}
}

func TestCheckRejectsNonCallExpressionStatement(t *testing.T) {
	src := `package main
func main() int {
	1 + 2
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "expression statement") {
		t.Fatalf("Check error: want expression statement error, got %q", err)
	}
}

func TestCheckRejectsSwitchCaseTypeMismatch(t *testing.T) {
	src := `package main
func main() int {
	var x int = 1
	switch x {
	case "nope":
	}
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "switch case") {
		t.Fatalf("Check error: want switch case type error, got %q", err)
	}
}

func TestCheckRejectsAssignmentBetweenDifferentNamedTypes(t *testing.T) {
	src := `package main
type A int
type B int
func main() int {
	var a A
	var b B
	a = b
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "cannot assign") {
		t.Fatalf("Check error: want named type assignment error, got %q", err)
	}
}

func TestCheckRejectsAssignmentBetweenNamedAndUnderlyingType(t *testing.T) {
	src := `package main
type A int
func main() int {
	var a A
	var i int
	a = i
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "cannot assign") {
		t.Fatalf("Check error: want named/basic assignment error, got %q", err)
	}
}

func TestCheckAcceptsExplicitNamedTypeConversions(t *testing.T) {
	src := `package main
type A int
type B int64
func main() int {
	var a A = A(1)
	var b B = B(a)
	var i int = int(b)
	var f float64 = float64(i)
	_ = f
	return 0
}
`

	if err := checkSource(src); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckRejectsInvalidExplicitConversion(t *testing.T) {
	src := `package main
func main() int {
	_ = int(true)
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "cannot convert") {
		t.Fatalf("Check error: want conversion error, got %q", err)
	}
}

func TestCheckRejectsAssigningNilToInt(t *testing.T) {
	src := `package main
func main() int {
	var x int = nil
	_ = x
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "cannot initialize") && !strings.Contains(err.Error(), "cannot assign") {
		t.Fatalf("Check error: got %q", err)
	}
}

func TestCheckRejectsAssignmentBetweenArraysOfDifferentLengths(t *testing.T) {
	src := `package main
func main() int {
	var a [1]int
	var b [2]int
	a = b
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "[2]int") || !strings.Contains(err.Error(), "[1]int") {
		t.Fatalf("Check error: want array length-sensitive error, got %q", err)
	}
}

func TestCheckRejectsNonAssignableLeftHandSide(t *testing.T) {
	src := `package main
func main() int {
	1 = 2
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "left side") {
		t.Fatalf("Check error: want invalid lhs error, got %q", err)
	}
}

func TestCheckRejectsCompositeLiteralElementTypeMismatch(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "slice element mismatch",
			src: `package main
func main() int {
	var xs []int = []int{"x"}
	_ = xs
	return 0
}
`,
		},
		{
			name: "array element mismatch",
			src: `package main
func main() int {
	var xs [2]int = [2]int{"x", "y"}
	_ = xs
	return 0
}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkSource(tt.src)
			if err == nil {
				t.Fatalf("Check: expected error")
			}
			if !strings.Contains(err.Error(), "composite literal") {
				t.Fatalf("Check error: want composite literal type error, got %q", err)
			}
		})
	}
}

func TestCheckRejectsTypeCycle(t *testing.T) {
	src := `package main
type A B
type B A
func main() int { return 0 }
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "type cycle") {
		t.Fatalf("Check error: want type cycle error, got %q", err)
	}
}

func TestCheckAllowsRecursiveStructViaPointer(t *testing.T) {
	src := `package main
type Node struct {
	next *Node
}
func main() int { return 0 }
`

	if err := checkSource(src); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckAllowsFieldSelectionThroughPointerToStruct(t *testing.T) {
	src := `package main
type state struct {
	counter int
}
func bump(s *state) int {
	s.counter = s.counter + 1
	return 0
}
func main() int { return 0 }
`

	if err := checkSource(src); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckAllowsBareReturnInZeroResultFunction(t *testing.T) {
	src := `package main
func f() {
	return
}
func main() int { return 0 }
`

	if err := checkSource(src); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckAllowsEqualArrayLengthsFromConstExpr(t *testing.T) {
	src := `package main
func main() int {
	var a [2]int
	var b [1+1]int
	a = b
	return 0
}
`

	if err := checkSource(src); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckRejectsSelfRecursivePointerAndSliceTypes(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "pointer",
			src: `package main
type T *T
func main() int { return 0 }
`,
		},
		{
			name: "slice",
			src: `package main
type T []T
func main() int { return 0 }
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkSource(tt.src)
			if err == nil {
				t.Fatalf("Check: expected error")
			}
			if !strings.Contains(err.Error(), "type cycle") {
				t.Fatalf("Check error: want type cycle error, got %q", err)
			}
		})
	}
}

func TestCheckRejectsNegativeArrayLength(t *testing.T) {
	src := `package main
var _ [-1]int
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "array length") || !strings.Contains(err.Error(), ">= 0") {
		t.Fatalf("Check error: want negative array length error, got %q", err)
	}
}

func TestCheckRejectsArrayCompositeLiteralWrongArity(t *testing.T) {
	src := `package main
func main() int {
	var xs [2]int = [2]int{1}
	_ = xs
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "composite literal has 1 elements for [2]int") {
		t.Fatalf("Check error: got %q", err)
	}
}

func TestCheckRejectsAssignmentToSelectorOnNonAddressableBase(t *testing.T) {
	src := `package main
type pair struct {
	a int
}
func main() int {
	pair{1}.a = 2
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "left side") && !strings.Contains(err.Error(), "addressable") {
		t.Fatalf("Check error: got %q", err)
	}
}

func TestCheckRejectsAddressOfSelectorOnNonAddressableBase(t *testing.T) {
	src := `package main
type pair struct {
	a int
}
func main() int {
	_ = &pair{1}.a
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "left side") && !strings.Contains(err.Error(), "addressable") {
		t.Fatalf("Check error: got %q", err)
	}
}

func TestCheckRejectsAssignmentToIndexOnNonAddressableBase(t *testing.T) {
	src := `package main
func main() int {
	[1]int{1}[0] = 2
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "left side") && !strings.Contains(err.Error(), "addressable") {
		t.Fatalf("Check error: got %q", err)
	}
}

func TestCheckAllowsCallingPointerReceiverMethodOnAddressableValue(t *testing.T) {
	src := `package main
type counter struct{ n int }
func (c *counter) Inc() int {
	c.n = c.n + 1
	return c.n
}
func main() int {
	var c counter
	_ = c.Inc()
	return 0
}
`

	if err := checkSource(src); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckRejectsCallingPointerReceiverMethodOnNonAddressableValue(t *testing.T) {
	src := `package main
type counter struct{}
func (c *counter) Inc() int { return 1 }
func main() int {
	_ = counter{}.Inc()
	return 0
}
`

	err := checkSource(src)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "unknown field") && !strings.Contains(err.Error(), "cannot call") {
		t.Fatalf("Check error: got %q", err)
	}
}

func TestCheckAcceptsBoolLogicStringAndFloatOps(t *testing.T) {
	src := `package main
func main() int {
	if !false && (true || false) {
		var s string = "go" + "es"
		var f float64 = -1.5 + 2.0
		var x int64 = 5 % 2
		_ = s == "goes"
		_ = "a" < "b"
		_ = f < 1.0
		_ = x == 1
	}
	return 0
}
`

	if err := checkSource(src); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckAcceptsHostRegisteredStructTypesAndMethods(t *testing.T) {
	host := runtime.NewHostRegistry()
	host.MustRegisterType(HostSkillState{})
	host.MustRegisterType(HostCastContext{})

	err := checkSourceWithRegistry(srcWithHostTypes("HostSkillState", "HostCastContext"), host)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckRejectsRedeclaringHostRegisteredStructType(t *testing.T) {
	host := runtime.NewHostRegistry()
	host.MustRegisterType(namedSkillStateForTest{})

	err := checkSourceWithRegistry(`package main
type namedSkillStateForTest struct {
	Counter int
}

func main() int {
	return 0
}
`, host)
	if err == nil {
		t.Fatalf("Check: expected error")
	}
	if !strings.Contains(err.Error(), "duplicate declaration") {
		t.Fatalf("Check error: got %q", err)
	}
}

func TestCheckAcceptsHostRegisteredConsts(t *testing.T) {
	host := runtime.NewHostRegistry()
	host.MustRegisterConst("HostLimit", 3)
	host.MustRegisterConst("HostEnabled", true)

	err := checkSourceWithRegistry(`package main
var _ [HostLimit]int

func main() int {
	if HostEnabled {
		return HostLimit
	}
	return 0
}
`, host)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
}

type namedSkillStateForTest struct {
	Counter int
}

type HostSkillState struct {
	Counter int
}

type HostCastContext struct {
	Level int
}

func srcWithHostTypes(stateType, ctxType string) string {
	return `package main
func (s *` + stateType + `) Add(v int) int {
	s.Counter = s.Counter + v
	return s.Counter
}

func OnCast(state ` + stateType + `, ctx ` + ctxType + `) ` + stateType + ` {
	state.Add(ctx.Level)
	return state
}

func main() int {
	return 0
}
`
}

func resolveGoSource(src string) (*goast.File, *sema.Info, error) {
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		return nil, nil, err
	}

	info, err := sema.ResolveGoPackageWithOptions(fset, []*goast.File{file}, sema.ResolveOptions{})
	if err != nil {
		return file, nil, err
	}

	return file, info, nil
}

func checkSource(src string) error {
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		return err
	}
	_, err = sema.CheckGoPackageWithOptions(fset, []*goast.File{file}, sema.CheckOptions{})
	return err
}

func checkGoSourceDetailed(src string) (*goast.File, *sema.Checked, error) {
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		return nil, nil, err
	}

	checked, err := sema.CheckGoPackageWithOptions(fset, []*goast.File{file}, sema.CheckOptions{})
	if err != nil {
		return file, nil, err
	}
	return file, checked, nil
}

func checkSourceWithRegistry(src string, host *runtime.HostRegistry) error {
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "main.go", src, goparser.SkipObjectResolution)
	if err != nil {
		return err
	}
	_, err = sema.CheckGoPackageWithOptions(fset, []*goast.File{file}, sema.CheckOptions{HostRegistry: host})
	return err
}

func TestLegacyCheckerFileHasBeenRemoved(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	path := filepath.Join(root, "..", "..", "internal", "sema", "checker.go")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("checker.go should be removed, stat err=%v", err)
	}
}

func TestLegacyResolverFileHasBeenRemoved(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	path := filepath.Join(root, "..", "..", "internal", "sema", "resolver.go")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("resolver.go should be removed, stat err=%v", err)
	}
}

func DisabledLegacyCheckerAPIFileHasBeenRemoved(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	path := filepath.Join(root, "..", "..", "internal", "sema", "legacy_api.go")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("legacy_api.go should be removed, stat err=%v", err)
	}
}

func TestLegacyResolverAPIFileHasBeenRemoved(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	path := filepath.Join(root, "..", "..", "internal", "sema", "legacy_resolve_api.go")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("legacy_resolve_api.go should be removed, stat err=%v", err)
	}
}

func DisabledDuplicateLegacyParserPackageHasBeenRemoved(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	path := filepath.Join(root, "..", "..", "internal", "parser", "parser.go")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("internal/parser/parser.go should be removed, stat err=%v", err)
	}
}

func DisabledLegacyParserPackageHasBeenRemoved(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	path := filepath.Join(root, "..", "..", "internal", "parser", "parser.go")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("internal/parser/parser.go should be removed, stat err=%v", err)
	}
}
