package app

import (
	"appengine/datastore"
	"bytes"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/printer"
	"go/token"
	"json"
	"os"
	"strings"
	"time"
)

type PackageDoc struct {
	PackageName string
	ImportPath  string
	ProjectURL  string
	ProjectName string
	Updated     datastore.Time
	Data        []byte
}

type file struct {
	Name    string
	Content interface{}
}

func sprintNode(fset *token.FileSet, decl interface{}) string {
	var buf bytes.Buffer
	_, err := (&printer.Config{Mode: printer.UseSpaces}).Fprint(&buf, fset, decl)
	if err != nil {
		buf.Reset()
		buf.WriteString(err.String())
	}
	return buf.String()
}

func sprintURL(fset *token.FileSet, srcURLFmt string, pos token.Pos) string {
	position := fset.Position(pos)
	return fmt.Sprintf(srcURLFmt, position.Filename, position.Line)
}

func valueDocs(fset *token.FileSet, srcURLFmt string, values []*doc.ValueDoc) interface{} {
	var result []map[string]interface{}
	for _, d := range values {
		result = append(result, map[string]interface{}{
			"decl": sprintNode(fset, d.Decl),
			"url":  sprintURL(fset, srcURLFmt, d.Decl.Pos()),
			"doc":  d.Doc,
		})
	}
	return result
}

func funcDocs(fset *token.FileSet, srcURLFmt string, funcs []*doc.FuncDoc) interface{} {
	var result []map[string]interface{}
	for _, d := range funcs {
		recv := ""
		if d.Recv != nil {
			recv = sprintNode(fset, d.Recv)
		}
		result = append(result, map[string]interface{}{
			"decl": sprintNode(fset, d.Decl),
			"url":  sprintURL(fset, srcURLFmt, d.Decl.Pos()),
			"doc":  d.Doc,
			"name": d.Name,
			"recv": recv,
		})
	}
	return result
}

var errPackageNotFound = os.NewError("package not found")

func createPackageDoc(importpath string, fileURLFmt string, srcURLFmt string, projectURL string, projectName string, files []file) (*PackageDoc, os.Error) {
	fset := token.NewFileSet()
	pkgs := make(map[string]*ast.Package)
	for _, file := range files {
		if src, err := parser.ParseFile(fset, file.Name, file.Content, parser.ParseComments); err == nil {
			name := src.Name.Name
			pkg, found := pkgs[name]
			if !found {
				pkg = &ast.Package{name, nil, nil, make(map[string]*ast.File)}
				pkgs[name] = pkg
			}
			pkg.Files[file.Name] = src
		}
	}
	var pkg *ast.Package
	score := 0
	for _, p := range pkgs {
		switch {
		case score < 2 && strings.HasSuffix(importpath, p.Name):
			pkg = p
			score = 2
		case score < 1 && p.Name != "main":
			pkg = p
			score = 1
		}
	}

	if pkg == nil {
		return nil, errPackageNotFound
	}

	ast.PackageExports(pkg)
	pdoc := doc.NewPackageDoc(pkg, importpath)

	var types []map[string]interface{}
	for _, d := range pdoc.Types {
		types = append(types, map[string]interface{}{
			"doc":       d.Doc,
			"name":      sprintNode(fset, d.Type.Name),
			"decl":      sprintNode(fset, d.Decl),
			"url":       sprintURL(fset, srcURLFmt, d.Decl.Pos()),
			"factories": funcDocs(fset, srcURLFmt, d.Factories),
			"methods":   funcDocs(fset, srcURLFmt, d.Methods),
		})
	}

	var fileNames []map[string]interface{}
	for _, f := range pdoc.Filenames {
		fileNames = append(fileNames, map[string]interface{}{
			"name": f,
			"url":  fmt.Sprintf(fileURLFmt, f),
		})
	}

	m := map[string]interface{}{
		"doc":    pdoc.Doc,
		"files":  fileNames,
		"types":  types,
		"funcs":  funcDocs(fset, srcURLFmt, pdoc.Funcs),
		"consts": valueDocs(fset, srcURLFmt, pdoc.Consts),
		"vars":   valueDocs(fset, srcURLFmt, pdoc.Vars),
	}
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}

	return &PackageDoc{
		PackageName: pdoc.PackageName,
		ImportPath:  pdoc.ImportPath,
		ProjectURL:  projectURL,
		ProjectName: projectName,
		Updated:     datastore.SecondsToTime(time.Seconds()),
		Data:        data}, nil
}
