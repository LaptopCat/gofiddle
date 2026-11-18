package main

import (
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"strings"
	"unicode/utf16"
)

func (s *server) signatureHelp(snap *snapshot, _ string, offset int) (any, error) {
	if help := s.signatureHelpIn(snap, offset); help != nil {
		return help, nil
	}
	// an unterminated call may not type-check, so close it and retry
	patched := s.analyze(snap.text[:offset] + ")" + snap.text[offset:])
	return s.signatureHelpIn(patched, offset), nil
}

func (s *server) signatureHelpIn(snap *snapshot, offset int) *SignatureHelp {
	pos := snap.pos(offset)
	var call *ast.CallExpr
	for _, n := range snap.pathEnclosing(pos) {
		if c, ok := n.(*ast.CallExpr); ok && c.Lparen < pos && (pos <= c.Rparen || !c.Rparen.IsValid()) {
			call = c
			break
		}
		if _, ok := n.(*ast.FuncLit); ok {
			return nil // don't show the signature of a call around a closure body
		}
	}
	if call == nil {
		return nil
	}

	var obj types.Object
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		obj = snap.info.Uses[fun]
	case *ast.SelectorExpr:
		obj = snap.info.Uses[fun.Sel]
	case *ast.IndexExpr:
		if id, ok := fun.X.(*ast.Ident); ok {
			obj = snap.info.Uses[id]
		}
	}

	var info *SignatureInformation
	if b, ok := obj.(*types.Builtin); ok {
		info = s.builtinSignature(b.Name())
	} else if tv, ok := snap.info.Types[call.Fun]; ok && !tv.IsType() && tv.Type != nil {
		if sig, ok := tv.Type.Underlying().(*types.Signature); ok {
			name := "func"
			if obj != nil {
				name = obj.Name()
			}
			info = signatureInfo(name, sig, snap.qualifier)
		}
	}
	if info == nil {
		return nil
	}
	if obj != nil {
		if doc := s.docOf(snap, obj); doc != "" {
			info.Documentation = &MarkupContent{Kind: "markdown", Value: doc}
		}
	}

	active := countArgs(snap.text, snap.offset(call.Lparen)+1, offset)
	if n := len(info.Parameters); n > 0 && active >= n && strings.Contains(info.Label, "...") {
		active = n - 1
	}
	return &SignatureHelp{Signatures: []SignatureInformation{*info}, ActiveParameter: active}
}

func countArgs(text string, from, to int) int {
	if from >= to {
		return 0
	}
	src := []byte(text[from:to])
	var sc scanner.Scanner
	sc.Init(token.NewFileSet().AddFile("", -1, len(src)), src, nil, 0)
	depth, commas := 0, 0
	for {
		_, tok, _ := sc.Scan()
		switch tok {
		case token.EOF:
			return commas
		case token.LPAREN, token.LBRACK, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACK, token.RBRACE:
			depth--
		case token.COMMA:
			if depth == 0 {
				commas++
			}
		}
	}
}

type signatureLabel struct {
	b      strings.Builder
	params []ParameterInformation
}

func (l *signatureLabel) write(s string) { l.b.WriteString(s) }

func (l *signatureLabel) param(s string) {
	start := utf16Len(l.b.String())
	l.b.WriteString(s)
	l.params = append(l.params, ParameterInformation{Label: [2]int{start, start + utf16Len(s)}})
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

func signatureInfo(name string, sig *types.Signature, qf types.Qualifier) *SignatureInformation {
	var l signatureLabel
	l.write(name + "(")
	params := sig.Params()
	for i := range params.Len() {
		if i > 0 {
			l.write(", ")
		}
		p := params.At(i)
		typ := types.TypeString(p.Type(), qf)
		if sig.Variadic() && i == params.Len()-1 {
			typ = "..." + types.TypeString(p.Type().(*types.Slice).Elem(), qf)
		}
		if p.Name() != "" {
			typ = p.Name() + " " + typ
		}
		l.param(typ)
	}
	l.write(")")
	if res := sig.Results(); res.Len() == 1 && res.At(0).Name() == "" {
		l.write(" " + types.TypeString(res.At(0).Type(), qf))
	} else if res.Len() > 0 {
		l.write(" " + types.TypeString(res, qf))
	}
	return &SignatureInformation{Label: l.b.String(), Parameters: l.params}
}

func (s *server) builtinSignature(name string) *SignatureInformation {
	decl := s.std.builtin.Decls[name]
	if decl == "" {
		return nil
	}
	f, err := parser.ParseFile(token.NewFileSet(), "", "package builtin\n"+decl, parser.SkipObjectResolution)
	if err != nil || len(f.Decls) == 0 {
		return nil
	}
	fn := f.Decls[0].(*ast.FuncDecl)

	var l signatureLabel
	l.write(name + "(")
	first := true
	for _, field := range fn.Type.Params.List {
		typ := types.ExprString(field.Type)
		names := field.Names
		if len(names) == 0 {
			names = []*ast.Ident{nil}
		}
		for _, n := range names {
			if !first {
				l.write(", ")
			}
			first = false
			if n != nil {
				l.param(n.Name + " " + typ)
			} else {
				l.param(typ)
			}
		}
	}
	l.write(")")
	if res := fn.Type.Results; res != nil {
		var parts []string
		for _, field := range res.List {
			parts = append(parts, types.ExprString(field.Type))
		}
		if len(parts) == 1 {
			l.write(" " + parts[0])
		} else {
			l.write(" (" + strings.Join(parts, ", ") + ")")
		}
	}
	return &SignatureInformation{Label: l.b.String(), Parameters: l.params}
}
