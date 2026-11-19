package sema

type Scope struct {
	Parent   *Scope
	PkgPath  string
	Symbols  map[string]*Symbol
	Imported map[string]*Scope
}

func NewScope(parent *Scope) *Scope {
	return &Scope{
		Parent:   parent,
		Symbols:  make(map[string]*Symbol),
		Imported: make(map[string]*Scope),
	}
}

func (s *Scope) LookupLocal(name string) *Symbol {
	if s == nil {
		return nil
	}
	return s.Symbols[name]
}

func (s *Scope) Lookup(name string) (*Symbol, *Scope) {
	for scope := s; scope != nil; scope = scope.Parent {
		if sym := scope.Symbols[name]; sym != nil {
			return sym, scope
		}
	}
	return nil, nil
}

func (s *Scope) LookupImported(pkgPath, name string) *Symbol {
	if s == nil {
		return nil
	}
	for scope := s; scope != nil; scope = scope.Parent {
		if pkgScope, ok := scope.Imported[pkgPath]; ok {
			if sym := pkgScope.Symbols[name]; sym != nil {
				return sym
			}
		}
	}
	return nil
}

func (s *Scope) Insert(sym *Symbol) bool {
	if s == nil || sym == nil {
		return false
	}
	if _, exists := s.Symbols[sym.Name]; exists {
		return false
	}
	s.Symbols[sym.Name] = sym
	return true
}
