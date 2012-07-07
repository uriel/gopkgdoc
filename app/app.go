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

// +build appengine

package app

import (
	"appengine"
	"appengine/datastore"
	"appengine/memcache"
	"appengine/urlfetch"
	"bytes"
	"doc"
	"encoding/gob"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	docKeyPrefix = "doc-" + doc.PackageVersion + ":"
)

func filterCmds(in []*Package) (out []*Package, cmds []*Package) {
	out = in[0:0]
	for _, pkg := range in {
		if pkg.IsCmd {
			cmds = append(cmds, pkg)
		} else {
			out = append(out, pkg)
		}
	}
	return
}

func childPackages(c appengine.Context, projectRoot, importPath string) ([]*Package, error) {
	projectPkgs, err := queryPackages(c, projectListKeyPrefix+projectRoot,
		datastore.NewQuery("Package").
			Filter("__key__ >", datastore.NewKey(c, "Package", projectRoot+"/", 0, nil)).
			Filter("__key__ <", datastore.NewKey(c, "Package", projectRoot+"0", 0, nil)))
	if err != nil {
		return nil, err
	}

	prefix := importPath + "/"
	pkgs := projectPkgs[0:0]
	for _, pkg := range projectPkgs {
		if strings.HasPrefix(pkg.ImportPath, prefix) {
			pkgs = append(pkgs, pkg)
		}
	}
	return pkgs, nil
}

// getDoc gets the package documentation and child packages for the given import path.
func getDoc(c appengine.Context, importPath string) (*doc.Package, []*Package, error) {

	// 1. Look for doc in cache.

	cacheKey := docKeyPrefix + importPath
	var pdoc *doc.Package
	item, err := cacheGet(c, cacheKey, &pdoc)
	switch err {
	case nil:
		pkgs, err := childPackages(c, pdoc.ProjectRoot, importPath)
		if err != nil {
			return nil, nil, err
		}
		return pdoc, pkgs, err
	case memcache.ErrCacheMiss:
		// OK
	default:
		return nil, nil, err
	}

	// 2. Look for doc in store.

	pdocSaved, etag, err := loadDoc(c, importPath)
	if err != nil {
		return nil, nil, err
	}

	// 3. Get documentation from the version control service and update
	// datastore and cache as needed.

	pdoc, err = doc.Get(urlfetch.Client(c), importPath, etag)
	c.Infof("doc.Get(%q, %q) -> %v", importPath, etag, err)

	switch err {
	case nil:
		if err := updatePackage(c, importPath, pdoc); err != nil {
			return nil, nil, err
		}
		item.Object = pdoc
		item.Expiration = time.Hour
		if err := cacheSet(c, item); err != nil {
			return nil, nil, err
		}
	case doc.ErrPackageNotFound:
		if err := updatePackage(c, importPath, nil); err != nil {
			return nil, nil, err
		}
		return nil, nil, doc.ErrPackageNotFound
	case doc.ErrPackageNotModified:
		pdoc = pdocSaved
	default:
		if pdocSaved == nil {
			return nil, nil, err
		}
		c.Errorf("Serving %s from store after error from VCS.", importPath)
		pdoc = pdocSaved
	}

	// 4. Find the child packages.

	pkgs, err := childPackages(c, pdoc.ProjectRoot, importPath)
	if err != nil {
		return nil, nil, err
	}

	// 5. Convert to not found if package is empty.

	if len(pkgs) == 0 && pdoc.Name == "" && len(pdoc.Errors) == 0 {
		return nil, nil, doc.ErrPackageNotFound
	}

	// 6. Done

	return pdoc, pkgs, nil
}

// handlerFunc adapts a function to an http.Handler. 
type handlerFunc func(http.ResponseWriter, *http.Request) error

func (f handlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	if r.Host == "gopkgdoc.appspot.com" {
		p := r.URL.Path
		if strings.HasPrefix(p, "/pkg/") {
			p = p[len("/pkg"):]
		}
		if r.URL.RawQuery != "" {
			p = p + "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, "http://go.pkgdoc.org"+p, 301)
		return
	}

	err := f(w, r)
	if err != nil {
		appengine.NewContext(r).Errorf("Error %s", err.Error())
		if e, ok := err.(doc.GetError); ok {
			http.Error(w, "Error getting files from "+e.Host+".", http.StatusInternalServerError)
		} else if appengine.IsCapabilityDisabled(err) || appengine.IsOverQuota(err) {
			http.Error(w, "Internal error: "+err.Error(), http.StatusInternalServerError)
		} else {
			http.Error(w, "Internal Error", http.StatusInternalServerError)
		}
	}
}

func servePackage(w http.ResponseWriter, r *http.Request) error {
	c := appengine.NewContext(r)

	p := path.Clean(r.URL.Path)
	if p != r.URL.Path {
		http.Redirect(w, r, p, 301)
		return nil
	}

	importPath := r.URL.Path[1:]
	pdoc, pkgs, err := getDoc(c, importPath)
	switch err {
	case doc.ErrPackageNotFound:
		return executeTemplate(w, "notfound.html", 404, nil)
	case nil:
		//ok
	default:
		return err
	}

	pkgs, cmds := filterCmds(pkgs)
	return executeTemplate(w, "pkg.html", 200, map[string]interface{}{
		"pkgs": pkgs,
		"cmds": cmds,
		"pdoc": pdoc,
	})
}

func serveClearPackageCache(w http.ResponseWriter, r *http.Request) error {
	if r.Method != "POST" {
		http.Error(w, "Method not supported.", http.StatusMethodNotAllowed)
		return nil
	}
	c := appengine.NewContext(r)
	importPath := r.FormValue("importPath")
	cacheKey := docKeyPrefix + importPath
	err := memcache.Delete(c, cacheKey)
	c.Infof("memcache.Delete(%s) -> %v", cacheKey, err)
	removeDoc(c, importPath)
	http.Redirect(w, r, "/"+importPath, 302)
	return nil
}

func serveGoIndex(w http.ResponseWriter, r *http.Request) error {
	c := appengine.NewContext(r)
	pkgs, err := queryPackages(c, projectListKeyPrefix,
		datastore.NewQuery("Package").
			Filter("__key__ >", datastore.NewKey(c, "Package", "/", 0, nil)).
			Filter("__key__ <", datastore.NewKey(c, "Package", "0", 0, nil)))
	if err != nil {
		return err
	}
	pkgs, cmds := filterCmds(pkgs)
	return executeTemplate(w, "std.html", 200, map[string]interface{}{
		"pkgs": pkgs,
		"cmds": cmds,
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request) error {
	c := appengine.NewContext(r)
	pkgs, err := queryPackages(c, packageListKey, datastore.NewQuery("Package").Filter("Hide=", false))
	if err != nil {
		return err
	}
	pkgs, cmds := filterCmds(pkgs)
	return executeTemplate(w, "index.html", 200, map[string]interface{}{
		"pkgs": pkgs,
		"cmds": cmds,
	})
}

func rediretIndex(w http.ResponseWriter, r *http.Request) error {
	http.Redirect(w, r, "/-/index", 301)
	return nil
}

func serveAPIIndex(w http.ResponseWriter, r *http.Request) error {
	c := appengine.NewContext(r)
	keys, err := datastore.NewQuery("Package").KeysOnly().GetAll(c, nil)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, key := range keys {
		importPath := key.StringID()
		if importPath[0] == '/' {
			// fix standard package.
			importPath = importPath[1:]
		}
		buf.WriteString(importPath)
		buf.WriteByte('\n')
	}
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, err = w.Write(buf.Bytes())
	return err
}

func serveAPIDump(w http.ResponseWriter, r *http.Request) error {
	c := appengine.NewContext(r)
	var pkgs []*Package
	keys, err := datastore.NewQuery("Package").GetAll(c, &pkgs)
	if err != nil {
		return err
	}
	for i := range keys {
		importPath := keys[i].StringID()
		pkgs[i].ImportPath = importPath
	}
	return gob.NewEncoder(w).Encode(pkgs)
}

func serveAPILoad(w http.ResponseWriter, r *http.Request) error {
	if r.Method != "POST" {
		http.Error(w, "Method not supported.", http.StatusMethodNotAllowed)
		return nil
	}
	c := appengine.NewContext(r)
	var pkgs []*Package
	err := gob.NewDecoder(r.Body).Decode(&pkgs)
	if err != nil {
		return err
	}
	for _, pkg := range pkgs {
		key := datastore.NewKey(c, "Package", pkg.ImportPath, 0, nil)
		if _, err := datastore.Put(c, key, pkg); err != nil {
			c.Infof("%s %v", pkg.ImportPath, err)
		}
	}
	err = memcache.Delete(c, packageListKey)
	if err != nil {
		c.Infof("clear %v", err)
	}
	return nil
}

func serveAPIHide(w http.ResponseWriter, r *http.Request) error {
	if r.Method != "POST" {
		http.Error(w, "Method not supported.", http.StatusMethodNotAllowed)
		return nil
	}
	c := appengine.NewContext(r)
	importPath := r.FormValue("importPath")
	key := datastore.NewKey(c, "Package", importPath, 0, nil)
	var pkg Package
	err := datastore.Get(c, key, &pkg)
	if err == datastore.ErrNoSuchEntity {
		io.WriteString(w, "no entity\n")
		return nil
	}
	if err != nil {
		return err
	}
	if pkg.Hide {
		io.WriteString(w, "hide\n")
		return nil
	}
	pkg.Hide = true
	_, err = datastore.Put(c, key, &pkg)
	io.WriteString(w, "ok\n")
	return nil
}

func serveAPIUpdate(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	if r.Method != "POST" {
		http.Error(w, "Method not supported.", http.StatusMethodNotAllowed)
		return
	}
	importPath := r.FormValue("importPath")
	pdoc, err := doc.Get(urlfetch.Client(c), importPath, "")
	if err == nil || err == doc.ErrPackageNotFound {
		err = updatePackage(c, importPath, pdoc)
	}

	if err != nil {
		c.Errorf("Error %s", err.Error())
		io.WriteString(w, "INTERNAL ERROR\n")
	} else if pdoc == nil {
		io.WriteString(w, "NOT FOUND\n")
	} else {
		io.WriteString(w, "OK\n")
	}
}

func importPathFromGoogleBrowse(m []string) string {
	project := m[1]
	dir := m[2]
	if dir == "" {
		dir = "/"
	} else if dir[len(dir)-1] == '/' {
		dir = dir[:len(dir)-1]
	}
	subrepo := ""
	if len(m[3]) > 0 {
		v, _ := url.ParseQuery(m[3][1:])
		subrepo = v.Get("repo")
		if len(subrepo) > 0 {
			subrepo = "." + subrepo
		}
	}
	if strings.HasPrefix(m[4], "#hg%2F") {
		d, _ := url.QueryUnescape(m[4][len("#hg%2f"):])
		if i := strings.IndexRune(d, '%'); i >= 0 {
			d = d[:i]
		}
		dir = dir + "/" + d
	}
	return "code.google.com/p/" + project + subrepo + dir
}

var queryCleaners = []struct {
	pat *regexp.Regexp
	fn  func([]string) string
}{
	{
		// Github source browser.
		regexp.MustCompile(`^https?:/+github\.com/([^/]+)/([^/]+)/tree/master/(.*)$`),
		func(m []string) string { return fmt.Sprintf("github.com/%s/%s/%s", m[1], m[2], m[3]) },
	},
	{
		// Bitbucket source borwser.
		regexp.MustCompile(`^https?:/+bitbucket\.org/([^/]+)/([^/]+)/src/[^/]+/(.*)$`),
		func(m []string) string { return fmt.Sprintf("bitbucket.org/%s/%s/%s", m[1], m[2], m[3]) },
	},
	{
		// Google Project Hosting source browser.
		regexp.MustCompile(`^http:/+code\.google\.com/p/([^/]+)/source/browse(/[^?#]*)?(\?[^#]*)?(#.*)?$`),
		importPathFromGoogleBrowse,
	},
	{
		// Launchpad source browser.
		regexp.MustCompile(`^https?:/+bazaar\.(launchpad\.net/.*)/files$`),
		func(m []string) string { return m[1] },
	},
	{
		// http or https prefix.
		regexp.MustCompile(`^https?:/+(.*)$`),
		func(m []string) string { return m[1] },
	},
}

func cleanQuery(q string) string {
	q = strings.TrimSpace(q)
	if len(q) == 0 {
		return q
	}
	if q[0] == '"' && q[len(q)-1] == '"' && !strings.Contains(q, " ") {
		q = q[1 : len(q)-1]
	}
	for _, c := range queryCleaners {
		if m := c.pat.FindStringSubmatch(q); m != nil {
			q = c.fn(m)
			break
		}
	}
	if len(q) == 0 {
		return q
	}
	if q[len(q)-1] == '/' {
		q = q[:len(q)-1]
	}
	return q
}

func serveHome(w http.ResponseWriter, r *http.Request) error {
	if r.URL.Path != "/" {
		return servePackage(w, r)
	}

	c := appengine.NewContext(r)

	q := r.FormValue("q")
	if q == "" {
		return executeTemplate(w, "home.html", 200, nil)
	}

	q = cleanQuery(q)
	if len(q) == 0 {
		return executeTemplate(w, "results.html", 200, map[string]interface{}{"q": q, "pkgs": nil})
	}

	// If the query looks like a go-gettable import path, then get the
	// documentation by import path. This will fetch the documentation from the
	// VCS if we have not seen this import path before.
	if doc.ValidRemotePath(q) {
		_, _, err := getDoc(c, q)
		switch err {
		case nil:
			// Automatic I'm feeling lucky.
			http.Redirect(w, r, "/"+q, 302)
			return nil
		case doc.ErrPackageNotFound:
			// Continue on to search.
		default:
			return err
		}
	}

	// Search for the package. Replace this with real search.

	_, token := path.Split(q)
	var pkgs []*Package
	keys, err := datastore.NewQuery("Package").Filter("IndexTokens=", token).GetAll(c, &pkgs)
	if err != nil {
		return err
	}
	for i := range keys {
		importPath := keys[i].StringID()
		if importPath[0] == '/' {
			// Standard packages start with "/"
			importPath = importPath[1:]
		}
		pkgs[i].ImportPath = importPath
	}

	return executeTemplate(w, "results.html", 200, map[string]interface{}{"q": q, "pkgs": pkgs})
}

func serveAbout(w http.ResponseWriter, r *http.Request) error {
	return executeTemplate(w, "about.html", 200, map[string]interface{}{"Host": r.Host})
}

func init() {
	http.Handle("/", handlerFunc(serveHome))
	http.Handle("/index", handlerFunc(rediretIndex)) // Delete this in late 2012.
	http.Handle("/-/about", handlerFunc(serveAbout))
	http.Handle("/-/index", handlerFunc(serveIndex))
	http.Handle("/-/go", handlerFunc(serveGoIndex))
	http.Handle("/-/refresh", handlerFunc(serveClearPackageCache))
	http.Handle("/a/index", handlerFunc(serveAPIIndex))
	http.Handle("/a/update", http.HandlerFunc(serveAPIUpdate))
	//http.Handle("/a/dump", handlerFunc(serveAPIDump))
	//http.Handle("/a/load", handlerFunc(serveAPILoad))
	//http.Handle("/a/hide", handlerFunc(serveAPIHide))
}
