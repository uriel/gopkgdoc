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
	mydoc "mygo/doc"
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

func UseFile(p string) bool {
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
	// Split off first paragraph.
	if parts := strings.SplitN(s, "\n\n", 2); len(parts) > 1 {
		s = parts[0]
	}

	// Find first sentence.
	prev := 'A'
	for i, ch := range s {
		if (ch == '.' || ch == '!' || ch == '?') &&
			i+1 < len(s) &&
			(s[i+1] == ' ' || s[i+1] == '\n') &&
			!unicode.IsUpper(prev) {
			s = s[:i+1]
			break
		}
		prev = ch
	}

	// Ensure that synopsis fits in datastore text property.
	if len(s) > 400 {
		s = s[:400]
	}

	return s
}

type builder struct {
	fset     *token.FileSet
	lineFmt  string
	examples []*mydoc.Example
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
			Decl:     b.printNode(d.Decl),
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
	Synopsis    string
	Files       []*File
	Funcs       []*Func
	ImportPath  string
	Name        string
	Types       []*Type
	Updated     int64
	Vars        []*Value
	ProjectURL  string
	ProjectName string
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

	pkgs := make(map[string]*ast.Package)
	for _, f := range files {
		if strings.HasSuffix(f.URL, "_test.go") {
			continue
		}
		if src, err := parser.ParseFile(b.fset, f.URL, f.Content, parser.ParseComments); err == nil {
			name := src.Name.Name
			pkg, found := pkgs[name]
			if !found {
				pkg = &ast.Package{name, nil, nil, make(map[string]*ast.File)}
				pkgs[name] = pkg
			}
			pkg.Files[f.URL] = src
		}
	}
	var pkg *ast.Package
	score := 0
	for _, p := range pkgs {
		switch {
		case score < 3 && strings.HasSuffix(importPath, p.Name):
			pkg = p
			score = 3
		case score < 2 && p.Name != "main":
			pkg = p
			score = 2
		case score < 1:
			pkg = p
			score = 1
		}
	}

	if pkg == nil {
		return nil, ErrPackageNotFound
	}

	ast.PackageExports(pkg)
	pdoc := doc.NewPackageDoc(pkg, importPath)

	for _, f := range files {
		if !strings.HasSuffix(f.URL, "_test.go") {
			continue
		}
		if src, err := parser.ParseFile(b.fset, f.URL, f.Content, parser.ParseComments); err == nil {
			for _, e := range mydoc.Examples(&ast.Package{src.Name.Name, nil, nil, map[string]*ast.File{f.URL: src}}) {
				if i := strings.LastIndex(e.Name, "_"); i >= 0 {
					if i < len(e.Name)-1 && !startsWithUppercase(e.Name[i+1:]) {
						e.Name = e.Name[:i]
					}
				}
				b.examples = append(b.examples, e)
			}
		}
	}

	return &Package{
		Consts:     b.values(pdoc.Consts),
		Doc:        pdoc.Doc,
		Files:      b.files(pdoc.Filenames),
		Funcs:      b.funcs(pdoc.Funcs),
		ImportPath: pdoc.ImportPath,
		Name:       pdoc.PackageName,
		Synopsis:   synopsis(pdoc.Doc),
		Types:      b.types(pdoc.Types),
		Updated:    time.Seconds(),
		Vars:       b.values(pdoc.Vars),
	}, nil
}
