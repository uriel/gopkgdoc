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
	if len(buf) > 400 {
		buf = buf[:400]
	}
	return string(buf)
}

type builder struct {
	fset     *token.FileSet
	lineFmt  string
	examples []*goExample
}

func (b *builder) printNode(decl interface{}) string {
	var buf bytes.Buffer
	_, err := (&printer.Config{Mode: printer.UseSpaces, Tabwidth: 4}).Fprint(&buf, b.fset, decl)
	if err != nil {
		buf.Reset()
		buf.WriteString(err.String())
	}
	return buf.String()
}

func (b *builder) printFunc(decl *ast.FuncDecl) string {
	return b.printNode(decl)
}

func (b *builder) printPos(pos token.Pos) string {
	position := b.fset.Position(pos)
	return position.Filename + fmt.Sprintf(b.lineFmt, position.Line)
}

type Value struct {
	Decl string
	URL  string
	Doc  string
}

func (b *builder) values(vdocs []*doc.ValueDoc) []*Value {
	var result []*Value
	for _, d := range vdocs {
		result = append(result, &Value{
			Decl: b.printNode(d.Decl),
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
	Decl     string
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
			Decl:     b.printFunc(d.Decl),
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
	Decl      string
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
			Name:      b.printNode(d.Type.Name),
			Decl:      b.printNode(d.Decl),
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

func (b *builder) files(urls []string) []*File {
	var result []*File
	for _, url := range urls {
		_, name := path.Split(url)
		result = append(result, &File{
			Name: name,
			URL:  url,
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
	URL     string
	Content interface{}
}

func Build(importPath string, lineFmt string, files []Source) (*Package, os.Error) {
	if len(files) == 0 {
		return nil, ErrPackageNotFound
	}

	b := &builder{
		lineFmt: lineFmt,
		fset:    token.NewFileSet(),
	}

	pasts := make(map[string]*ast.Package)
	for _, f := range files {
		if strings.HasSuffix(f.URL, "_test.go") {
			continue
		}
		if fast, err := parser.ParseFile(b.fset, f.URL, f.Content, parser.ParseComments); err == nil {
			name := fast.Name.Name
			past, found := pasts[name]
			if !found {
				past = &ast.Package{name, nil, nil, make(map[string]*ast.File)}
				pasts[name] = past
			}
			past.Files[f.URL] = fast
		}
	}
	var past *ast.Package
	score := 0
	for _, p := range pasts {
		switch {
		case score < 3 && strings.HasSuffix(importPath, p.Name):
			past = p
			score = 3
		case score < 2 && p.Name != "main":
			past = p
			score = 2
		case score < 1:
			past = p
			score = 1
		}
	}

	if past == nil {
		return nil, ErrPackageNotFound
	}

	// Determine if the directory contains a function that can be used as an
	// application main function. This check must be done before the AST is 
	// filtred by the call to ast.PackageExports below.
	hasApplicationMain := false
	if past, ok := pasts["main"]; ok {
	MainCheck:
		for _, fast := range past.Files {
			for _, d := range fast.Decls {
				if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == "main" {
					hasApplicationMain = (f.Type.Params == nil || len(f.Type.Params.List) == 0) &&
						(f.Type.Results == nil || len(f.Type.Results.List) == 0)
					break MainCheck
				}
			}
		}
	}

	ast.PackageExports(past)
	pdoc := doc.NewPackageDoc(past, importPath)

	for _, f := range files {
		if !strings.HasSuffix(f.URL, "_test.go") {
			continue
		}
		fast, err := parser.ParseFile(b.fset, f.URL, f.Content, parser.ParseComments)
		if err != nil || fast.Name.Name != pdoc.PackageName {
			continue
		}
		for _, e := range goExamples(&ast.Package{fast.Name.Name, nil, nil, map[string]*ast.File{f.URL: fast}}) {
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
		strings.HasSuffix(pdoc.Filenames[0], "/doc.go") &&
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
