package main

import (
	"fmt"
	"go/ast"
	"go/doc/comment"
	"go/token"
	"go/types"
	"strings"
)

func (s *server) hover(snap *snapshot, _ string, offset int) (any, error) {
	id := snap.identAt(offset)
	if id == nil {
		return nil, nil
	}
	obj := snap.objectOf(id)
	if obj == nil {
		return nil, nil
	}

	var b strings.Builder
	b.WriteString("```go\n" + s.declString(snap, obj) + "\n```")
	if doc := s.docOf(snap, obj); doc != "" {
		b.WriteString("\n\n---\n\n" + doc)
	}
	if link := docLink(obj); link != "" {
		b.WriteString("\n\n" + link)
	}

	r := snap.nodeRange(id)
	return &Hover{Contents: MarkupContent{Kind: "markdown", Value: b.String()}, Range: &r}, nil
}

func (snap *snapshot) qualifier(p *types.Package) string {
	if p == snap.pkg {
		return ""
	}
	return p.Name()
}

func (s *server) declString(snap *snapshot, obj types.Object) string {
	qf := snap.qualifier
	switch obj := obj.(type) {
	case *types.Builtin:
		if decl := s.std.builtin.Decls[obj.Name()]; decl != "" {
			return decl
		}
	case *types.PkgName:
		return "package " + obj.Imported().Name() + fmt.Sprintf(" (%q)", obj.Imported().Path())
	case *types.Const:
		str := types.ObjectString(obj, qf)
		if !strings.Contains(str, "=") {
			str += " = " + obj.Val().ExactString()
		}
		return str
	case *types.TypeName:
		return typeDeclString(obj, qf)
	}
	return types.ObjectString(obj, qf)
}

func typeDeclString(tn *types.TypeName, qf types.Qualifier) string {
	if tn.Pkg() == nil {
		return types.ObjectString(tn, qf)
	}
	name := tn.Name()
	if q := qf(tn.Pkg()); q != "" {
		name = q + "." + name
	}
	if tn.IsAlias() {
		return "type " + name + " = " + types.TypeString(types.Unalias(tn.Type()), qf)
	}

	var b strings.Builder
	b.WriteString("type " + name)
	named, _ := tn.Type().(*types.Named)
	if named != nil && named.TypeParams().Len() > 0 {
		b.WriteString("[")
		for i := range named.TypeParams().Len() {
			tp := named.TypeParams().At(i)
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(tp.Obj().Name() + " " + types.TypeString(tp.Constraint(), qf))
		}
		b.WriteString("]")
	}
	b.WriteString(" ")

	local := qf(tn.Pkg()) == ""
	switch u := tn.Type().Underlying().(type) {
	case *types.Struct:
		b.WriteString("struct {")
		hidden := false
		for i := range u.NumFields() {
			f := u.Field(i)
			if !f.Exported() && !local {
				hidden = true
				continue
			}
			b.WriteString("\n\t")
			if f.Embedded() {
				b.WriteString(types.TypeString(f.Type(), qf))
			} else {
				b.WriteString(f.Name() + " " + types.TypeString(f.Type(), qf))
			}
			if tag := u.Tag(i); tag != "" {
				b.WriteString(" `" + tag + "`")
			}
		}
		if hidden {
			b.WriteString("\n\t// contains filtered or unexported fields")
		}
		if u.NumFields() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("}")
	case *types.Interface:
		b.WriteString("interface {")
		for i := range u.NumEmbeddeds() {
			b.WriteString("\n\t" + types.TypeString(u.EmbeddedType(i), qf))
		}
		for i := range u.NumExplicitMethods() {
			m := u.ExplicitMethod(i)
			if !m.Exported() && !local {
				continue
			}
			b.WriteString("\n\t" + m.Name() + strings.TrimPrefix(types.TypeString(m.Type(), qf), "func"))
		}
		if u.NumEmbeddeds()+u.NumExplicitMethods() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("}")
		return b.String()
	default:
		b.WriteString(types.TypeString(u, qf))
	}

	if named != nil && named.NumMethods() > 0 {
		b.WriteString("\n")
		for i := range named.NumMethods() {
			m := named.Method(i)
			if m.Exported() || local {
				b.WriteString("\n" + types.ObjectString(m, qf))
			}
		}
	}
	return b.String()
}

func (s *server) docOf(snap *snapshot, obj types.Object) string {
	if pn, ok := obj.(*types.PkgName); ok {
		if p := s.std.packages[pn.Imported().Path()]; p != nil {
			return p.Docs[""]
		}
		return ""
	}
	if obj.Pkg() != snap.pkg {
		return s.std.doc(obj)
	}
	if !snap.inFile(obj.Pos()) {
		return ""
	}
	if group := snap.docComment(obj.Pos()); group != nil {
		var p comment.Parser
		pr := comment.Printer{HeadingLevel: 4, DocLinkBaseURL: "https://pkg.go.dev"}
		return strings.TrimSpace(string(pr.Markdown(p.Parse(group.Text()))))
	}
	return ""
}

func (snap *snapshot) docComment(pos token.Pos) *ast.CommentGroup {
	path := snap.pathEnclosing(pos)
	for i, n := range path {
		switch n := n.(type) {
		case *ast.FuncDecl:
			return n.Doc
		case *ast.Field:
			if n.Doc != nil {
				return n.Doc
			}
			return n.Comment
		case *ast.TypeSpec, *ast.ValueSpec:
			var doc, line *ast.CommentGroup
			if ts, ok := n.(*ast.TypeSpec); ok {
				doc, line = ts.Doc, ts.Comment
			} else {
				vs := n.(*ast.ValueSpec)
				doc, line = vs.Doc, vs.Comment
			}
			if doc != nil {
				return doc
			}
			if i+1 < len(path) {
				if gd, ok := path[i+1].(*ast.GenDecl); ok && gd.Doc != nil && len(gd.Specs) == 1 {
					return gd.Doc
				}
			}
			return line
		case *ast.BlockStmt, *ast.FuncLit:
			return nil
		}
	}
	return nil
}

func docLink(obj types.Object) string {
	if obj.Pkg() == nil {
		switch obj.(type) {
		case *types.Builtin, *types.TypeName, *types.Const, *types.Nil:
			return fmt.Sprintf("[`%s` on pkg.go.dev](https://pkg.go.dev/builtin#%s)", obj.Name(), obj.Name())
		}
		return ""
	}
	if obj.Pkg().Path() == "main" {
		return ""
	}
	if pn, ok := obj.(*types.PkgName); ok {
		return fmt.Sprintf("[`%s` on pkg.go.dev](https://pkg.go.dev/%s)", pn.Imported().Path(), pn.Imported().Path())
	}
	if !obj.Exported() {
		return ""
	}
	anchor := obj.Name()
	if owner := ownerName(obj); owner != "" {
		anchor = owner + "." + anchor
	} else if obj.Parent() != obj.Pkg().Scope() {
		return ""
	}
	return fmt.Sprintf("[`%s.%s` on pkg.go.dev](https://pkg.go.dev/%s#%s)", obj.Pkg().Name(), anchor, obj.Pkg().Path(), anchor)
}
