//go:build ignore

// gen writes stdlib.bin, rerun it with `go generate ./lsp` after changing the go or yaegi version
package main

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/traefik/yaegi/stdlib"
	"github.com/traefik/yaegi/stdlib/generic"
)

// keep in sync with stdPackage in stdlib.go
type stdPackage struct {
	Path    string
	Name    string
	Export  []byte
	Docs    map[string]string
	Runtime []string
	Decls   map[string]string // only for the builtin pseudo-package
}

type listedPackage struct {
	ImportPath string
	Name       string
	Dir        string
	Export     string
	GoFiles    []string
}

func main() {
	runtime := runtimeSymbols()

	paths := make([]string, 0, len(runtime))
	for path := range runtime {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	paths = append(paths, "builtin")

	cmd := exec.Command("go", append([]string{"list", "-e", "-export", "-json"}, paths...)...)
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		log.Fatal(err)
	}

	var pkgs []stdPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var lp listedPackage
		if err := dec.Decode(&lp); err == io.EOF {
			break
		} else if err != nil {
			log.Fatal(err)
		}
		if lp.ImportPath == "builtin" {
			pkgs = append(pkgs, stdPackage{
				Path:  lp.ImportPath,
				Name:  lp.Name,
				Docs:  packageDocs(lp, doc.AllDecls),
				Decls: builtinDecls(lp),
			})
			continue
		}
		if lp.Export == "" {
			// not buildable for js/wasm, e.g. log/syslog
			continue
		}

		export, err := readExportData(lp.Export)
		if err != nil {
			log.Fatalf("%s: %v", lp.ImportPath, err)
		}

		pkgs = append(pkgs, stdPackage{
			Path:    lp.ImportPath,
			Name:    lp.Name,
			Export:  export,
			Docs:    packageDocs(lp, 0),
			Runtime: runtime[lp.ImportPath],
		})
	}

	var raw bytes.Buffer
	if err := gob.NewEncoder(&raw).Encode(pkgs); err != nil {
		log.Fatal(err)
	}

	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		log.Fatal(err)
	}
	data := enc.EncodeAll(raw.Bytes(), nil)

	if err := os.WriteFile("stdlib.bin", data, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote stdlib.bin: %d packages, %d bytes (%d uncompressed)\n", len(pkgs), len(data), raw.Len())
}

func runtimeSymbols() map[string][]string {
	names := map[string]map[string]bool{}
	add := func(path, name string) {
		if names[path] == nil {
			names[path] = map[string]bool{}
		}
		names[path][name] = true
	}

	for key, syms := range stdlib.Symbols {
		i := strings.LastIndexByte(key, '/')
		if i < 0 || strings.Contains(key, ".") {
			continue
		}
		path := key[:i]
		for name := range syms {
			if token.IsExported(name) {
				add(path, name)
			}
		}
	}

	// generic packages are interpreted from source, not bound natively
	for _, src := range generic.Sources {
		f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.SkipObjectResolution)
		if err != nil {
			log.Fatal(err)
		}
		for name := range fileScope(f) {
			if token.IsExported(name) {
				add(f.Name.Name, name)
			}
		}
	}

	out := map[string][]string{}
	for path, set := range names {
		for name := range set {
			out[path] = append(out[path], name)
		}
		sort.Strings(out[path])
	}
	return out
}

func fileScope(f *ast.File) map[string]bool {
	names := map[string]bool{}
	for _, decl := range f.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if decl.Recv == nil {
				names[decl.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					names[spec.Name.Name] = true
				case *ast.ValueSpec:
					for _, n := range spec.Names {
						names[n.Name] = true
					}
				}
			}
		}
	}
	return names
}

// go/importer only needs the __.PKGDEF member
func readExportData(file string) ([]byte, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	const magic = "!<arch>\n"
	if !bytes.HasPrefix(data, []byte(magic)) || len(data) < len(magic)+60 {
		return nil, fmt.Errorf("not an archive")
	}
	hdr := data[len(magic) : len(magic)+60]
	if name := strings.TrimSpace(string(hdr[:16])); name != "__.PKGDEF" {
		return nil, fmt.Errorf("first archive member is %q", name)
	}
	size, err := strconv.Atoi(strings.TrimSpace(string(hdr[48:58])))
	if err != nil {
		return nil, err
	}
	end := len(magic) + 60 + size
	if end > len(data) {
		return nil, fmt.Errorf("truncated archive")
	}
	return data[:end], nil
}

// keyed by "Name" or "Type.Member", "" is the package doc
func packageDocs(lp listedPackage, mode doc.Mode) map[string]string {
	fset, files := parsePackage(lp)
	pkg, err := doc.NewFromFiles(fset, files, lp.ImportPath, mode)
	if err != nil {
		log.Fatal(err)
	}

	pr := pkg.Printer()
	pr.DocLinkBaseURL = "https://pkg.go.dev"
	pr.HeadingLevel = 4
	render := func(text string) string {
		return strings.TrimSpace(string(pr.Markdown(pkg.Parser().Parse(text))))
	}

	docs := map[string]string{}
	set := func(key, text string) {
		if text = strings.TrimSpace(text); text != "" {
			docs[key] = render(text)
		}
	}

	set("", pkg.Doc)
	values := func(vals []*doc.Value) {
		for _, v := range vals {
			for _, spec := range v.Decl.Specs {
				vs := spec.(*ast.ValueSpec)
				text := v.Doc
				if vs.Doc != nil {
					text = vs.Doc.Text()
				} else if vs.Comment != nil && text == "" {
					text = vs.Comment.Text()
				}
				for _, n := range vs.Names {
					set(n.Name, text)
				}
			}
		}
	}
	funcs := func(fns []*doc.Func) {
		for _, f := range fns {
			set(f.Name, f.Doc)
		}
	}

	values(pkg.Consts)
	values(pkg.Vars)
	funcs(pkg.Funcs)
	for _, t := range pkg.Types {
		set(t.Name, t.Doc)
		values(t.Consts)
		values(t.Vars)
		funcs(t.Funcs)
		for _, m := range t.Methods {
			set(t.Name+"."+m.Name, m.Doc)
		}
		for _, spec := range t.Decl.Specs {
			ts := spec.(*ast.TypeSpec)
			var fields *ast.FieldList
			switch typ := ts.Type.(type) {
			case *ast.StructType:
				fields = typ.Fields
			case *ast.InterfaceType:
				fields = typ.Methods
			}
			if fields == nil {
				continue
			}
			for _, field := range fields.List {
				text := ""
				if field.Doc != nil {
					text = field.Doc.Text()
				} else if field.Comment != nil {
					text = field.Comment.Text()
				}
				for _, n := range field.Names {
					if n.IsExported() {
						set(ts.Name.Name+"."+n.Name, text)
					}
				}
			}
		}
	}
	return docs
}

func parsePackage(lp listedPackage) (*token.FileSet, []*ast.File) {
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range lp.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(lp.Dir, name), nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			log.Fatal(err)
		}
		files = append(files, f)
	}
	return fset, files
}

func builtinDecls(lp listedPackage) map[string]string {
	fset, files := parsePackage(lp)
	decls := map[string]string{}
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			fn.Doc = nil
			var buf bytes.Buffer
			if err := printer.Fprint(&buf, fset, fn); err != nil {
				log.Fatal(err)
			}
			decls[fn.Name.Name] = buf.String()
		}
	}
	return decls
}
