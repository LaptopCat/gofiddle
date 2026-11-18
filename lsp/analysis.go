package main

import (
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"strings"
)

// keep in line with the newest stdlib bindings shipped by yaegi
const interpreterGoVersion = "go1.22"

type snapshot struct {
	text   string
	m      *mapper
	fset   *token.FileSet
	file   *ast.File
	tf     *token.File
	prefix int // bytes the interpreter's wrapping adds before the user's code

	pkg  *types.Package
	info *types.Info

	parseErrs scanner.ErrorList
	typeErrs  []types.Error
}

// mirrors how yaegi evaluates code: as a file, as top-level declarations or as statements
func (s *server) analyze(text string) *snapshot {
	snap := &snapshot{text: text, m: newMapper(text)}

	prefix, suffix := "", ""
	switch firstToken(text) {
	case token.PACKAGE:
	case token.CONST, token.FUNC, token.IMPORT, token.TYPE, token.VAR:
		prefix = "package main;"
	default:
		prefix, suffix = "package main; func main() {", "\n}"
	}

	parse := func(prefix, suffix string) (*token.FileSet, *ast.File, error) {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "main.go", prefix+text+suffix, parser.AllErrors|parser.ParseComments|parser.SkipObjectResolution)
		return fset, f, err
	}
	fset, f, err := parse(prefix, suffix)
	if err != nil && prefix == "package main;" && firstToken(text) == token.FUNC {
		// yaegi retries function literals used as statements the same way
		if fset2, f2, err2 := parse("package main; func main() {", "\n}"); err2 == nil {
			fset, f, err, prefix = fset2, f2, nil, "package main; func main() {"
		}
	}
	if f == nil {
		f = &ast.File{Name: ast.NewIdent("main")}
	}

	snap.fset, snap.file, snap.prefix = fset, f, len(prefix)
	fset.Iterate(func(tf *token.File) bool { snap.tf = tf; return false })
	if list, ok := err.(scanner.ErrorList); ok {
		snap.parseErrs = list
	}

	snap.info = &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Instances:  map[*ast.Ident]types.Instance{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Implicits:  map[ast.Node]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
		Scopes:     map[ast.Node]*types.Scope{},
	}
	conf := types.Config{
		Importer:  s.std,
		GoVersion: interpreterGoVersion,
		Sizes:     types.SizesFor("gc", "wasm"),
		Error: func(err error) {
			if terr, ok := err.(types.Error); ok {
				snap.typeErrs = append(snap.typeErrs, terr)
			}
		},
	}
	snap.pkg, _ = conf.Check("main", fset, []*ast.File{f}, snap.info)
	return snap
}

func firstToken(text string) token.Token {
	var s scanner.Scanner
	fset := token.NewFileSet()
	s.Init(fset.AddFile("", -1, len(text)), []byte(text), nil, 0)
	_, tok, _ := s.Scan()
	return tok
}

func (snap *snapshot) wrapped() bool {
	return snap.prefix > len("package main;")
}

func (snap *snapshot) offset(pos token.Pos) int {
	if snap.tf == nil || !pos.IsValid() || int(pos) < snap.tf.Base() || int(pos) > snap.tf.Base()+snap.tf.Size() {
		return -1
	}
	return max(0, min(snap.tf.Offset(pos)-snap.prefix, len(snap.text)))
}

func (snap *snapshot) pos(offset int) token.Pos {
	if snap.tf == nil {
		return token.NoPos
	}
	return snap.tf.Pos(min(offset+snap.prefix, snap.tf.Size()))
}

func (snap *snapshot) inFile(pos token.Pos) bool {
	off := snap.offset(pos)
	return off >= 0 && snap.tf.Offset(pos) >= snap.prefix
}

func (snap *snapshot) rangeOf(start, end token.Pos) Range {
	return snap.m.rangeOf(snap.offset(start), snap.offset(end))
}

func (snap *snapshot) nodeRange(n ast.Node) Range {
	return snap.rangeOf(n.Pos(), n.End())
}

func (snap *snapshot) pathEnclosing(pos token.Pos) []ast.Node {
	var path []ast.Node
	ast.Inspect(snap.file, func(n ast.Node) bool {
		if n == nil || pos < n.Pos() || pos > n.End() {
			return false
		}
		path = append(path, n)
		return true
	})
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

func (snap *snapshot) identAt(offset int) *ast.Ident {
	pos := snap.pos(offset)
	var found *ast.Ident
	ast.Inspect(snap.file, func(n ast.Node) bool {
		if n == nil || found != nil || pos < n.Pos() || pos > n.End() {
			return false
		}
		if id, ok := n.(*ast.Ident); ok && snap.inFile(id.Pos()) {
			found = id
		}
		return true
	})
	return found
}

func (snap *snapshot) objectOf(id *ast.Ident) types.Object {
	if obj := snap.info.ObjectOf(id); obj != nil {
		return obj
	}
	// the name of an import spec without an explicit name
	for node, obj := range snap.info.Implicits {
		if spec, ok := node.(*ast.ImportSpec); ok && spec.Name == id {
			return obj
		}
	}
	return nil
}

func (s *server) diagnostics(snap *snapshot) []Diagnostic {
	diags := []Diagnostic{}

	for _, err := range snap.parseErrs {
		diags = append(diags, Diagnostic{
			Range:    snap.wordRange(err.Pos.Offset - snap.prefix),
			Severity: SeverityError,
			Source:   "syntax",
			Message:  err.Msg,
		})
	}
	if len(snap.parseErrs) > 0 {
		// type errors in code that doesn't parse are mostly noise
		return diags
	}

	lastExpr := snap.lastWrappedExpr()
	for _, err := range snap.typeErrs {
		if lastExpr != nil && err.Pos == lastExpr.Pos() && strings.HasSuffix(err.Msg, "is not used") {
			// the interpreter returns the value of a trailing expression
			continue
		}
		d := Diagnostic{
			Range:    snap.errorRange(err.Pos),
			Severity: SeverityError,
			Source:   "compiler",
			Message:  err.Msg,
		}
		if unused := isUnusedError(err); unused {
			if snap.wrapped() && !snap.inFuncLit(err.Pos) {
				// top-level statements declare globals, which may go unused
				continue
			}
			// the go compiler rejects these, but the interpreter doesn't
			d.Severity = SeverityWarning
			d.Tags = []int{TagUnnecessary}
		}
		if !snap.inFile(err.Pos) {
			d.Range = snap.m.rangeOf(0, 0)
		}
		diags = append(diags, d)
	}

	ast.Inspect(snap.file, func(n ast.Node) bool {
		rs, ok := n.(*ast.RangeStmt)
		if !ok {
			return true
		}
		if tv, ok := snap.info.Types[rs.X]; ok && tv.Type != nil {
			if b, ok := tv.Type.Underlying().(*types.Basic); ok && b.Info()&types.IsInteger != 0 {
				diags = append(diags, Diagnostic{
					Range:    snap.nodeRange(rs.X),
					Severity: SeverityWarning,
					Source:   "gofiddle",
					Message:  "the GoFiddle interpreter doesn't support ranging over an integer yet; use a three-clause for loop",
				})
			}
		}
		return true
	})

	for id, obj := range snap.info.Uses {
		if obj.Pkg() == nil || obj.Pkg() == snap.pkg || !obj.Exported() || !snap.inFile(id.Pos()) {
			continue
		}
		if obj.Parent() != obj.Pkg().Scope() {
			continue // fields and methods are bound together with their type
		}
		if !s.std.supported(obj.Pkg(), obj.Name()) {
			diags = append(diags, Diagnostic{
				Range:    snap.nodeRange(id),
				Severity: SeverityWarning,
				Source:   "gofiddle",
				Message:  obj.Pkg().Name() + "." + obj.Name() + " is not available in the GoFiddle interpreter (it was added after Go " + strings.TrimPrefix(interpreterGoVersion, "go") + ")",
			})
		}
	}
	return diags
}

func isUnusedError(err types.Error) bool {
	return err.Soft && (strings.HasPrefix(err.Msg, "declared and not used") || strings.HasSuffix(err.Msg, "and not used"))
}

func (snap *snapshot) inFuncLit(pos token.Pos) bool {
	for _, n := range snap.pathEnclosing(pos) {
		if _, ok := n.(*ast.FuncLit); ok {
			return true
		}
	}
	return false
}

func (snap *snapshot) lastWrappedExpr() ast.Expr {
	if !snap.wrapped() || len(snap.file.Decls) == 0 {
		return nil
	}
	fn, ok := snap.file.Decls[0].(*ast.FuncDecl)
	if !ok || fn.Body == nil || len(fn.Body.List) == 0 {
		return nil
	}
	if es, ok := fn.Body.List[len(fn.Body.List)-1].(*ast.ExprStmt); ok {
		return es.X
	}
	return nil
}

func (snap *snapshot) errorRange(pos token.Pos) Range {
	var best ast.Node
	ast.Inspect(snap.file, func(n ast.Node) bool {
		if n == nil || pos < n.Pos() || pos > n.End() {
			return false
		}
		if n.Pos() == pos {
			best = n
		}
		return true
	})
	if best != nil && snap.inFile(best.Pos()) {
		if r := snap.nodeRange(best); r.Start.Line == r.End.Line {
			return r
		}
	}
	return snap.wordRange(snap.offset(pos))
}

func (snap *snapshot) wordRange(offset int) Range {
	text := snap.text
	offset = max(0, min(offset, len(text)))
	end := offset
	for end < len(text) && isIdentByte(text[end]) {
		end++
	}
	if end == offset {
		if offset < len(text) && text[offset] != '\n' {
			end = offset + 1
		} else if offset > 0 {
			offset--
		}
	}
	return snap.m.rangeOf(offset, end)
}

func isIdentByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
}
