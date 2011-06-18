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

package app

import (
	"appengine"
	"appengine/datastore"
	"bytes"
	"go/doc"
	"path"
	"http"
	"io"
	"json"
	"os"
	"template"
	"time"
)

func commentFmt(v string) string {
	var buf bytes.Buffer
	doc.ToHTML(&buf, []byte(v), nil)
	return buf.String()
}

var fmap = template.FuncMap{"comment": commentFmt}

func parseTemplate(name string) func(io.Writer, interface{}) os.Error {
	if appengine.IsDevAppServer() {
		return func(w io.Writer, value interface{}) os.Error {
			return template.Must(template.New(name).Funcs(fmap).ParseFile(name)).Execute(w, value)
		}
	}
	t := template.Must(template.New(name).Funcs(fmap).ParseFile(name))
	return func(w io.Writer, value interface{}) os.Error {
		return t.Execute(w, value)
	}
}

var (
	homeTemplate = parseTemplate("template/home.html")
	pkgTemplate  = parseTemplate("template/pkg.html")
)

func internalError(w http.ResponseWriter, c appengine.Context, err os.Error) {
	c.Errorf("Error %s", err.String())
	http.Error(w, "Internal Error", http.StatusInternalServerError)
}

func servePkg(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	importPath := r.URL.Path[len("/pkg/"):]
	if importPath == "" {
		http.NotFound(w, r)
		return
	}
	key := datastore.NewKey(c, "PackageDoc", importPath, 0, nil)
	var doc PackageDoc
	err := datastore.Get(appengine.NewContext(r), key, &doc)
	if err == datastore.ErrNoSuchEntity {
		http.NotFound(w, r)
		return
	} else if err != nil {
		internalError(w, c, err)
		return
	}

	var m map[string]interface{}
	if err := json.Unmarshal(doc.Data, &m); err != nil {
		c.Errorf("error unmarshalling json", err)
	}

	userURL, _ := path.Split(doc.ProjectURL)
	m["userURL"] = userURL
	m["userName"] = path.Base(userURL)
	m["importPath"] = doc.ImportPath
	m["packageName"] = doc.PackageName
	m["projectURL"] = doc.ProjectURL
	m["projectName"] = doc.ProjectName
	m["updated"] = time.SecondsToLocalTime(int64(doc.Updated) / 1e6).String()
	if err := pkgTemplate(w, m); err != nil {
		c.Errorf("error rendering pkg template:", err)
	}
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	c := appengine.NewContext(r)
	var imports []string
	if item, found := cacheGet(c, "/", &imports); !found {
		q := datastore.NewQuery("PackageDoc").KeysOnly()
		keys, err := q.GetAll(c, nil)
		if err != nil {
			internalError(w, c, err)
			return
		}
		for _, key := range keys {
			imports = append(imports, key.StringID())
		}
		cacheSet(c, item, 7200, imports)
	}
	if err := homeTemplate(w, imports); err != nil {
		c.Errorf("error rendering home template:", err)
	}
}

func init() {
	http.HandleFunc("/", serveHome)
	http.HandleFunc("/pkg/", servePkg)
	http.HandleFunc("/hook/github", githubHook)
}
