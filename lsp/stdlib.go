package main

//go:generate go run gen.go

import (
	"bytes"
	_ "embed"
	"encoding/gob"
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"io"

	"github.com/klauspost/compress/zstd"
)

//go:embed stdlib.bin
var stdlibData []byte

// keep in sync with stdPackage in gen.go
type stdPackage struct {
	Path    string
	Name    string
	Export  []byte
	Docs    map[string]string
	Runtime []string
	Decls   map[string]string

	runtime map[string]bool
}

type stdlib struct {
	packages map[string]*stdPackage
	builtin  *stdPackage
	importer types.ImporterFrom
}

func loadStdlib() (*stdlib, error) {
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	raw, err := dec.DecodeAll(stdlibData, nil)
	if err != nil {
		return nil, err
	}

	var pkgs []*stdPackage
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&pkgs); err != nil {
		return nil, err
	}

	s := &stdlib{packages: map[string]*stdPackage{}}
	for _, p := range pkgs {
		if p.Path == "builtin" {
			s.builtin = p
			continue
		}
		p.runtime = map[string]bool{}
		for _, name := range p.Runtime {
			p.runtime[name] = true
		}
		s.packages[p.Path] = p
	}

	lookup := func(path string) (io.ReadCloser, error) {
		p, ok := s.packages[path]
		if !ok {
			return nil, fmt.Errorf("package %s is not available in GoFiddle", path)
		}
		return io.NopCloser(bytes.NewReader(p.Export)), nil
	}
	// the importer caches packages, so each is decoded once
	s.importer = importer.ForCompiler(token.NewFileSet(), "gc", lookup).(types.ImporterFrom)
	return s, nil
}

func (s *stdlib) Import(path string) (*types.Package, error) {
	return s.ImportFrom(path, "", 0)
}

func (s *stdlib) ImportFrom(path, dir string, mode types.ImportMode) (*types.Package, error) {
	if _, ok := s.packages[path]; !ok {
		return nil, fmt.Errorf("package %s is not available in GoFiddle", path)
	}
	return s.importer.ImportFrom(path, dir, mode)
}

// symbols newer than the interpreter's bindings type-check but fail at run time
func (s *stdlib) supported(pkg *types.Package, name string) bool {
	p, ok := s.packages[pkg.Path()]
	return !ok || p.runtime[name]
}

func (s *stdlib) doc(obj types.Object) string {
	if obj.Pkg() == nil {
		if s.builtin == nil {
			return ""
		}
		return s.builtin.Docs[obj.Name()]
	}
	p, ok := s.packages[obj.Pkg().Path()]
	if !ok {
		return ""
	}
	if owner := ownerName(obj); owner != "" {
		return p.Docs[owner+"."+obj.Name()]
	}
	return p.Docs[obj.Name()]
}

func ownerName(obj types.Object) string {
	switch obj := obj.(type) {
	case *types.Var:
		if !obj.IsField() {
			return ""
		}
	case *types.Func:
		sig, _ := obj.Type().(*types.Signature)
		if sig == nil || sig.Recv() == nil {
			return ""
		}
		if named := namedOf(sig.Recv().Type()); named != nil {
			if _, isIface := named.Underlying().(*types.Interface); !isIface {
				return named.Obj().Name()
			}
		}
	default:
		return ""
	}

	// fields and interface methods don't know their parent, so search for it
	scope := obj.Pkg().Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		switch u := tn.Type().Underlying().(type) {
		case *types.Struct:
			for i := range u.NumFields() {
				if u.Field(i) == obj {
					return name
				}
			}
		case *types.Interface:
			for i := range u.NumMethods() {
				if u.Method(i) == obj {
					return name
				}
			}
		}
	}
	return ""
}

func namedOf(t types.Type) *types.Named {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, _ := types.Unalias(t).(*types.Named)
	return n
}
