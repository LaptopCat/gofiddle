package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"
)

const (
	sortLocal = iota + 4 // lower values are field and method depths
	sortPackage
	sortUniverse
	sortKeyword
)

var keywords = []string{
	"break", "case", "chan", "const", "continue", "default", "defer", "else",
	"fallthrough", "for", "func", "go", "goto", "if", "import", "interface",
	"map", "package", "range", "return", "select", "struct", "switch", "type", "var",
}

type completer struct {
	s      *server
	snap   *snapshot // may be a patched copy of the document
	offset int
	start  int
	items  []CompletionItem
}

func (s *server) completion(snap *snapshot, _ string, offset int) (any, error) {
	text := snap.text
	start := offset
	for start > 0 && isIdentByte(text[start-1]) {
		start--
	}
	c := &completer{s: s, snap: snap, offset: offset, start: start, items: []CompletionItem{}}
	if snap.inCommentOrString(offset) {
		return &CompletionList{Items: c.items}, nil
	}

	if start > 0 && text[start-1] == '.' {
		if start == offset {
			// "x." doesn't parse, so complete "x._" instead
			c.snap = s.analyze(text[:offset] + "_" + text[offset:])
		}
		c.selector()
	} else {
		c.scope()
		if start < offset {
			for _, kw := range keywords {
				c.add(CompletionItem{Label: kw, Kind: KindKeyword}, sortKeyword)
			}
		}
	}
	return &CompletionList{Items: c.items}, nil
}

func (snap *snapshot) inCommentOrString(offset int) bool {
	pos := snap.pos(offset)
	for _, cg := range snap.file.Comments {
		for _, c := range cg.List {
			if c.Pos() < pos && (pos < c.End() || pos == c.End() && strings.HasPrefix(c.Text, "//")) {
				return true
			}
		}
	}
	found := false
	ast.Inspect(snap.file, func(n ast.Node) bool {
		if n == nil || found || pos < n.Pos() || pos > n.End() {
			return false
		}
		if lit, ok := n.(*ast.BasicLit); ok && (lit.Kind == token.STRING || lit.Kind == token.CHAR) {
			found = lit.Pos() < pos && pos < lit.End()
		}
		return true
	})
	return found
}

func (c *completer) add(item CompletionItem, group int) {
	item.SortText = fmt.Sprintf("%d%s", group, item.Label)
	if item.TextEdit == nil {
		item.TextEdit = &TextEdit{Range: c.snap.m.rangeOf(c.start, c.offset), NewText: item.Label}
	}
	c.items = append(c.items, item)
}

func (c *completer) addObject(obj types.Object, group int) {
	item := CompletionItem{Label: obj.Name(), Kind: completionKind(obj), Detail: c.detail(obj)}
	if doc := c.s.docOf(c.snap, obj); doc != "" {
		item.Documentation = &MarkupContent{Kind: "markdown", Value: doc}
	}

	_, isFunc := obj.(*types.Func)
	_, isBuiltin := obj.(*types.Builtin)
	text := c.snap.text
	end := c.offset
	for end < len(text) && isIdentByte(text[end]) {
		end++
	}
	if (isFunc || isBuiltin) && (end == len(text) || text[end] != '(') {
		newText := obj.Name() + "($0)"
		if sig, ok := obj.Type().(*types.Signature); ok && sig.Params().Len() == 0 {
			newText = obj.Name() + "()"
		} else {
			item.Command = &Command{Title: "Signature help", Command: "editor.action.triggerParameterHints"}
		}
		item.InsertTextFormat = insertTextFormatSnippet
		item.TextEdit = &TextEdit{Range: c.snap.m.rangeOf(c.start, c.offset), NewText: newText}
	}
	c.add(item, group)
}

func completionKind(obj types.Object) CompletionItemKind {
	switch obj := obj.(type) {
	case *types.Func:
		if obj.Type().(*types.Signature).Recv() != nil {
			return KindMethod
		}
		return KindFunction
	case *types.Builtin:
		return KindFunction
	case *types.Var:
		if obj.IsField() {
			return KindField
		}
	case *types.Const, *types.Nil:
		return KindConstant
	case *types.PkgName:
		return KindModule
	case *types.TypeName:
		switch obj.Type().Underlying().(type) {
		case *types.Struct:
			return KindStruct
		case *types.Interface:
			return KindInterface
		}
		return KindClass
	}
	return KindVariable
}

func (c *completer) detail(obj types.Object) string {
	switch obj := obj.(type) {
	case *types.Func:
		return strings.TrimPrefix(types.TypeString(obj.Type(), c.snap.qualifier), "func")
	case *types.Builtin:
		return strings.TrimPrefix(c.s.std.builtin.Decls[obj.Name()], "func "+obj.Name())
	case *types.PkgName:
		return `"` + obj.Imported().Path() + `"`
	case *types.TypeName:
		return "type"
	}
	return types.TypeString(obj.Type(), c.snap.qualifier)
}

func (c *completer) selector() {
	snap := c.snap
	pos := snap.pos(c.start)
	var sel *ast.SelectorExpr
	for _, n := range snap.pathEnclosing(pos) {
		if s, ok := n.(*ast.SelectorExpr); ok && s.Sel.Pos() == pos {
			sel = s
			break
		}
	}
	if sel == nil {
		return
	}

	if id, ok := sel.X.(*ast.Ident); ok {
		if pn, ok := snap.info.Uses[id].(*types.PkgName); ok {
			scope := pn.Imported().Scope()
			for _, name := range scope.Names() {
				if obj := scope.Lookup(name); obj.Exported() && c.s.std.supported(pn.Imported(), name) {
					c.addObject(obj, 0)
				}
			}
			return
		}
	}

	tv, ok := snap.info.Types[sel.X]
	if !ok || tv.Type == nil {
		return
	}
	t := tv.Type
	if _, isPtr := t.Underlying().(*types.Pointer); !isPtr && !types.IsInterface(t) && !tv.IsType() {
		t = types.NewPointer(t)
	}
	ms := types.NewMethodSet(t)
	for i := range ms.Len() {
		if sel := ms.At(i); c.accessible(sel.Obj()) {
			c.addObject(sel.Obj(), len(sel.Index())-1)
		}
	}
	if !tv.IsType() {
		c.fields(tv.Type)
	}
}

func (c *completer) accessible(obj types.Object) bool {
	return obj.Exported() || obj.Pkg() == c.snap.pkg
}

// breadth-first so that promoted fields come after the ones they're promoted from
func (c *completer) fields(t types.Type) {
	type entry struct {
		t     types.Type
		depth int
	}
	queue := []entry{{t, 0}}
	seen := map[string]bool{}
	for len(queue) > 0 {
		e := queue[0]
		queue = queue[1:]
		if p, ok := e.t.Underlying().(*types.Pointer); ok {
			e.t = p.Elem()
		}
		st, ok := e.t.Underlying().(*types.Struct)
		if !ok {
			continue
		}
		for i := range st.NumFields() {
			f := st.Field(i)
			if c.accessible(f) && !seen[f.Name()] {
				seen[f.Name()] = true
				c.addObject(f, e.depth)
			}
			if f.Embedded() && e.depth < 3 {
				queue = append(queue, entry{f.Type(), e.depth + 1})
			}
		}
	}
}

func (c *completer) scope() {
	snap := c.snap
	pos := snap.pos(c.start)
	innermost := snap.info.Scopes[snap.file]
	for _, n := range snap.pathEnclosing(pos) {
		// a function body shares the scope recorded for its signature
		switch fn := n.(type) {
		case *ast.FuncDecl:
			n = fn.Type
		case *ast.FuncLit:
			n = fn.Type
		}
		if sc, ok := snap.info.Scopes[n]; ok {
			innermost = sc
			break
		}
	}

	shadowed := map[string]bool{}
	for sc := innermost; sc != nil; sc = sc.Parent() {
		group := sortLocal
		switch sc {
		case types.Universe:
			group = sortUniverse
		case snap.pkg.Scope():
			group = sortPackage
		}
		for _, name := range sc.Names() {
			obj := sc.Lookup(name)
			declaredLater := group == sortLocal && sc != snap.info.Scopes[snap.file] && obj.Pos() > pos
			if shadowed[name] || name == "_" || declaredLater {
				continue
			}
			shadowed[name] = true
			c.addObject(obj, group)
		}
	}
}
