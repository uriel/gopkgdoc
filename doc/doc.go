// Copyright 2011 Gary Burd
//
// Licensed under the Apache License, Version 2.0 (the "License"): you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
// License for the specific language governing permissions and limitations
// under the License.

package doc

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"utf8"
)

var ErrPackageNotFound = os.NewError("package not found")

func ToHTML(w io.Writer, s []byte) {
	doc.ToHTML(w, s, nil)
}

func IsGoFile(p string) bool {
	_, f := path.Split(p)
	return strings.HasSuffix(f, ".go") &&
		len(f) > 0 &&
		f[0] != '_' &&
		f[0] != '.' &&
		f != "deprecated.go"
}

func startsWithUppercase(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}

// synopsis extracts the first sentence from s. All runs of whitespace are
// replaced by a single space.
func synopsis(s string) string {
	var buf []byte
	const (
		other = iota
		period
		space
	)
	last := space
Loop:
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch b {
		case ' ', '\t', '\r', '\n':
			switch last {
			case period:
				break Loop
			case other:
				buf = append(buf, ' ')
				last = space
			}
		case '.':
			last = period
			buf = append(buf, b)
		default:
			last = other
			buf = append(buf, b)
		}
	}
	// Ensure that synopsis fits in datastore text property.
	// TODO: don't chop buf in middle of a rune.
	if len(buf) > 400 {
		buf = buf[:400]
	}
	return string(buf)
}

type builder struct {
	fset        *token.FileSet
	lineFmt     string
	examples    []*goExample
	buf         bytes.Buffer // scratch space for printNode method.
	importPaths map[string]map[string]string
	pkg         *ast.Package
	urls        map[string]string
}

func (b *builder) fileImportPaths(filename string) map[string]string {
	importPaths := b.importPaths[filename]
	if importPaths == nil {
		importPaths = make(map[string]string)
		b.importPaths[filename] = importPaths
		for _, i := range b.pkg.Files[filename].Imports {
			importPath, _ := strconv.Unquote(i.Path.Value)
			var name string
			if i.Name != nil {
				name = i.Name.Name
			} else {
				// TODO: find name using Package entities in datastore.
				name = path.Base(importPath)
				switch {
				case strings.HasPrefix(name, "go-"):
					name = name[len("go-"):]
				case strings.HasSuffix(name, ".go"):
					name = name[:len(name)-len(".go")]
				}
			}
			importPaths[name] = importPath
		}
	}
	return importPaths
}

type TypeAnnotation struct {
	Pos, End   int
	ImportPath string
	Name       string
}

type Decl struct {
	Text        string
	Annotations []TypeAnnotation
}

// annotationVisitor collects type annoations.
type annotationVisitor struct {
	annotations []TypeAnnotation
	fset        *token.FileSet
	b           *builder
	importPaths map[string]string
}

func (v *annotationVisitor) Visit(n ast.Node) ast.Visitor {
	switch n := n.(type) {
	case *ast.TypeSpec:
		if n.Type != nil {
			ast.Walk(v, n.Type)
		}
		return nil
	case *ast.FuncDecl:
		if n.Recv != nil {
			ast.Walk(v, n.Recv)
		}
		if n.Type != nil {
			ast.Walk(v, n.Type)
		}
		return nil
	case *ast.Field:
		if n.Type != nil {
			ast.Walk(v, n.Type)
		}
		return nil
	case *ast.ValueSpec:
		if n.Type != nil {
			ast.Walk(v, n.Type)
		}
		return nil
	case *ast.FuncLit:
		if n.Type != nil {
			ast.Walk(v, n.Type)
		}
		return nil
	case *ast.CompositeLit:
		if n.Type != nil {
			ast.Walk(v, n.Type)
		}
		return nil
	case *ast.Ident:
		if !ast.IsExported(n.Name) {
			return nil
		}
		v.addAnnoation(n, "", n.Name)
		return nil
	case *ast.SelectorExpr:
		if !ast.IsExported(n.Sel.Name) {
			return nil
		}
		if i, ok := n.X.(*ast.Ident); ok {
			v.addAnnoation(n, i.Name, n.Sel.Name)
			return nil
		}
	}
	return v
}

const packageWrapper = "package p\n"

func (v *annotationVisitor) addAnnoation(n ast.Node, packageName string, name string) {
	pos := v.b.fset.Position(n.Pos())
	end := v.b.fset.Position(n.End())
	importPath := ""
	if packageName != "" {
		importPath = v.importPaths[packageName]
		if importPath == "" {
			return
		}
	}
	v.annotations = append(v.annotations, TypeAnnotation{
		pos.Offset - len(packageWrapper),
		end.Offset - len(packageWrapper),
		importPath,
		name})
}

func (b *builder) printDecl(decl ast.Node) Decl {
	b.buf.Reset()
	b.buf.WriteString(packageWrapper)
	_, err := (&printer.Config{Mode: printer.UseSpaces, Tabwidth: 4}).Fprint(&b.buf, b.fset, decl)
	if err != nil {
		return Decl{Text: err.String()}
	}
	text := string(b.buf.Bytes()[len(packageWrapper):])
	position := b.fset.Position(decl.Pos())
	v := &annotationVisitor{
		b:           b,
		fset:        token.NewFileSet(),
		importPaths: b.fileImportPaths(position.Filename),
	}
	f, err := parser.ParseFile(v.fset, "", b.buf.Bytes(), 0)
	if err != nil {
		panic(err)
		return Decl{Text: text}
	}
	ast.Walk(v, f)
	return Decl{Text: text, Annotations: v.annotations}
}

func (b *builder) printNode(node interface{}) string {
	b.buf.Reset()
	_, err := (&printer.Config{Mode: printer.UseSpaces, Tabwidth: 4}).Fprint(&b.buf, b.fset, node)
	if err != nil {
		b.buf.Reset()
		b.buf.WriteString(err.String())
	}
	return b.buf.String()
}

func (b *builder) printPos(pos token.Pos) string {
	position := b.fset.Position(pos)
	return b.urls[position.Filename] + fmt.Sprintf(b.lineFmt, position.Line)
}

type Value struct {
	Decl Decl
	URL  string
	Doc  string
}

func (b *builder) values(vdocs []*doc.ValueDoc) []*Value {
	var result []*Value
	for _, d := range vdocs {
		result = append(result, &Value{
			Decl: b.printDecl(d.Decl),
			URL:  b.printPos(d.Decl.Pos()),
			Doc:  d.Doc,
		})
	}
	return result
}

type Example struct {
	Code   string
	Output string
}

func (b *builder) getExamples(name string) []Example {
	var docs []Example
	for _, e := range b.examples {
		if e.Name == name {
			code := b.printNode(e.Body)
			code = strings.Replace(code, "\n    ", "\n", -1)
			code = code[2 : len(code)-2]
			docs = append(docs, Example{code, e.Output})
		}
	}
	return docs
}

type Func struct {
	Decl     Decl
	URL      string
	Doc      string
	Name     string
	Recv     string
	Examples []Example
}

func (b *builder) funcs(fdocs []*doc.FuncDoc) []*Func {
	var result []*Func
	for _, d := range fdocs {
		exampleName := d.Name
		recv := ""
		if d.Recv != nil {
			recv = b.printNode(d.Recv)
			r := d.Recv
			if t, ok := r.(*ast.StarExpr); ok {
				r = t.X
			}
			if t, ok := r.(*ast.Ident); ok {
				exampleName = t.Name + "_" + exampleName
			}
		}

		result = append(result, &Func{
			Decl:     b.printDecl(d.Decl),
			URL:      b.printPos(d.Decl.Pos()),
			Doc:      d.Doc,
			Name:     d.Name,
			Recv:     recv,
			Examples: b.getExamples(exampleName),
		})
	}
	return result
}

type Type struct {
	Doc       string
	Name      string
	Decl      Decl
	URL       string
	Consts    []*Value
	Vars      []*Value
	Factories []*Func
	Methods   []*Func
	Examples  []Example
}

func (b *builder) types(tdocs []*doc.TypeDoc) []*Type {
	var result []*Type
	for _, d := range tdocs {
		result = append(result, &Type{
			Doc:       d.Doc,
			Name:      d.Type.Name.Name,
			Decl:      b.printDecl(d.Decl),
			URL:       b.printPos(d.Decl.Pos()),
			Consts:    b.values(d.Consts),
			Vars:      b.values(d.Vars),
			Factories: b.funcs(d.Factories),
			Methods:   b.funcs(d.Methods),
			Examples:  b.getExamples(d.Type.Name.Name),
		})
	}
	return result
}

type File struct {
	Name string
	URL  string
}

func (b *builder) files(filenames []string) []*File {
	var result []*File
	for _, f := range filenames {
		_, name := path.Split(f)
		result = append(result, &File{
			Name: name,
			URL:  b.urls[f],
		})
	}
	return result
}

type Package struct {
	Consts      []*Value
	Doc         string
	Files       []*File
	Funcs       []*Func
	Hide        bool
	ImportPath  string
	IsCmd       bool
	Name        string
	ProjectName string
	ProjectURL  string
	Synopsis    string
	Types       []*Type
	Updated     int64
	Vars        []*Value
}

type Source struct {
	Filename string
	URL      string
	Content  interface{}
}

func Build(importPath string, lineFmt string, files []Source) (*Package, os.Error) {
	if len(files) == 0 {
		return nil, ErrPackageNotFound
	}

	b := &builder{
		lineFmt:     lineFmt,
		fset:        token.NewFileSet(),
		importPaths: make(map[string]map[string]string),
		urls:        make(map[string]string),
	}

	pkgs := make(map[string]*ast.Package)
	for _, f := range files {
		b.urls[f.Filename] = f.URL
		if strings.HasSuffix(f.Filename, "_test.go") {
			continue
		}
		if src, err := parser.ParseFile(b.fset, f.Filename, f.Content, parser.ParseComments); err == nil {
			name := src.Name.Name
			pkg, found := pkgs[name]
			if !found {
				pkg = &ast.Package{name, nil, nil, make(map[string]*ast.File)}
				pkgs[name] = pkg
			}
			pkg.Files[f.Filename] = src
		}
	}
	score := 0
	for _, pkg := range pkgs {
		switch {
		case score < 3 && strings.HasSuffix(importPath, pkg.Name):
			b.pkg = pkg
			score = 3
		case score < 2 && pkg.Name != "main":
			b.pkg = pkg
			score = 2
		case score < 1:
			b.pkg = pkg
			score = 1
		}
	}

	if b.pkg == nil {
		return nil, ErrPackageNotFound
	}

	// Determine if the directory contains a function that can be used as an
	// application main function. This check must be done before the AST is 
	// filtred by the call to ast.PackageExports below.
	hasApplicationMain := false
	if pkg, ok := pkgs["main"]; ok {
	MainCheck:
		for _, src := range pkg.Files {
			for _, d := range src.Decls {
				if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == "main" {
					hasApplicationMain = (f.Type.Params == nil || len(f.Type.Params.List) == 0) &&
						(f.Type.Results == nil || len(f.Type.Results.List) == 0)
					break MainCheck
				}
			}
		}
	}

	ast.PackageExports(b.pkg)
	pdoc := doc.NewPackageDoc(b.pkg, importPath)

	// Collect examples.
	for _, f := range files {
		if !strings.HasSuffix(f.Filename, "_test.go") {
			continue
		}
		src, err := parser.ParseFile(b.fset, f.Filename, f.Content, parser.ParseComments)
		if err != nil || src.Name.Name != pdoc.PackageName {
			continue
		}
		for _, e := range goExamples(&ast.Package{src.Name.Name, nil, nil, map[string]*ast.File{f.Filename: src}}) {
			if i := strings.LastIndex(e.Name, "_"); i >= 0 {
				if i < len(e.Name)-1 && !startsWithUppercase(e.Name[i+1:]) {
					e.Name = e.Name[:i]
				}
			}
			b.examples = append(b.examples, e)
		}
	}

	noExports := len(pdoc.Consts) == 0 &&
		len(pdoc.Funcs) == 0 &&
		len(pdoc.Types) == 0 &&
		len(pdoc.Vars) == 0

	var isCmd bool
	if pdoc.PackageName == "documentation" &&
		len(pdoc.Filenames) == 1 &&
		path.Base(pdoc.Filenames[0]) == "doc.go" &&
		noExports &&
		hasApplicationMain {
		isCmd = true
		pdoc.PackageName = path.Base(importPath)
	}

	hide := (pdoc.PackageName == "main" && hasApplicationMain) || (noExports && !isCmd)

	return &Package{
		Consts:     b.values(pdoc.Consts),
		Doc:        pdoc.Doc,
		Files:      b.files(pdoc.Filenames),
		Funcs:      b.funcs(pdoc.Funcs),
		Hide:       hide,
		ImportPath: pdoc.ImportPath,
		IsCmd:      isCmd,
		Name:       pdoc.PackageName,
		Synopsis:   synopsis(pdoc.Doc),
		Types:      b.types(pdoc.Types),
		Updated:    time.Seconds(),
		Vars:       b.values(pdoc.Vars),
	}, nil
}
