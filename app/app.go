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

func childPackages(c appengine.Context, projectPrefix, importPath string) ([]*Package, error) {
	projectPkgs, err := queryPackages(c, projectListKeyPrefix+projectPrefix,
		datastore.NewQuery("Package").
			Filter("__key__ >", datastore.NewKey(c, "Package", projectPrefix+"/", 0, nil)).
			Filter("__key__ <", datastore.NewKey(c, "Package", projectPrefix+"0", 0, nil)))
	if err != nil {
		return nil, err
	}

	prefix := importPath + "/"
	pkgs := projectPkgs[0:0]
	for _, pkg := range projectPkgs {
		if strings.HasPrefix(pkg.ImportPath, prefix) && !doc.IsHiddenPath(pkg.ImportPath) {
			pkgs = append(pkgs, pkg)
		}
	}
	return pkgs, nil
}

// getDoc gets the package documentation and child packages for the given import path.
func getDoc(c appengine.Context, importPath string) (*doc.Package, []*Package, error) {

	// Look for doc in memory cache.

	cacheKey := docKeyPrefix + importPath
	var pdoc *doc.Package
	item, err := cacheGet(c, cacheKey, &pdoc)
	switch err {
	case nil:
		pkgs, err := childPackages(c, pdoc.ProjectPrefix, importPath)
		if err != nil {
			return nil, nil, err
		}
		return pdoc, pkgs, err
	case memcache.ErrCacheMiss:
		// OK
	default:
		return nil, nil, err
	}

	// Look for doc in datastore.

	pdocSaved, etag, err := loadDoc(c, importPath)
	if err != nil {
		return nil, nil, err
	}

	// Get documentation from the version control service.

	pdoc, err = doc.Get(urlfetch.Client(c), importPath, etag)
	c.Infof("Fetched %s from source, err=%v.", importPath, err)

	if err == nil || err == doc.ErrPackageNotFound {
		if err := updatePackage(c, importPath, pdoc); err != nil {
			return nil, nil, err
		}
	}

	if err == doc.ErrPackageNotModified {
		pdoc = pdocSaved
		err = nil
	}

	if err != nil {
		return nil, nil, err
	}

	// Find the child packages.

	pkgs, err := childPackages(c, pdoc.ProjectPrefix, importPath)
	if err != nil {
		return nil, nil, err
	}

	// Cache the documentation. To prevent the cache from growing without
	// bound, we only cache the doc if it contains a valid package or if
	// it's a parent of a package in the index.
	if pdoc.Name != "" || len(pkgs) > 0 {
		item.Object = pdoc
		item.Expiration = time.Hour
		if err := cacheSet(c, item); err != nil {
			return nil, nil, err
		}
	}

	if len(pkgs) == 0 && pdoc.Name == "" && len(pdoc.Errors) == 0 {
		// If we don't have anything to display, then treat it as not found.
		return nil, nil, doc.ErrPackageNotFound
	}

	return pdoc, pkgs, nil
}

// handlerFunc adapts a function to an http.Handler. 
type handlerFunc func(http.ResponseWriter, *http.Request) error

func (f handlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	if p == "/pkg" {
		return executeTemplate(w, "notfound.html", 404, nil)
	}
	if p != r.URL.Path {
		http.Redirect(w, r, p, 301)
		return nil
	}
	importPath := p[len("/pkg/"):]

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
	http.Redirect(w, r, "/pkg/"+importPath, 302)
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

func servePackages(w http.ResponseWriter, r *http.Request) error {
	http.Redirect(w, r, "/index", 301)
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
		buf.WriteString(key.StringID())
		buf.WriteByte('\n')
	}
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, err = w.Write(buf.Bytes())
	return err
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

var importPathCleaners = []struct {
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

func cleanImportPath(q string) string {
	q = strings.Trim(q, "\"/ \t\n")
	if q == "" {
		return ""
	}
	for _, c := range importPathCleaners {
		if m := c.pat.FindStringSubmatch(q); m != nil {
			q = c.fn(m)
			break
		}
	}
	q = path.Clean(q)
	if q == "." {
		q = ""
	}
	return q
}

func serveHome(w http.ResponseWriter, r *http.Request) error {
	c := appengine.NewContext(r)

	if r.URL.Path != "/" {
		return executeTemplate(w, "notfound.html", 404, nil)
	}

	importPath := cleanImportPath(r.FormValue("q"))

	// Display simple home page when no query.
	if importPath == "" {
		return executeTemplate(w, "home.html", 200,
			map[string]interface{}{"Host": r.Host})
	}

	pdoc, _, err := getDoc(c, importPath)
	switch err {
	case nil:
		http.Redirect(w, r, "/pkg/"+importPath, 302)
		return nil
	case doc.ErrPackageNotFound:
		pdoc = nil
	default:
		return err
	}

	// Find suggested import paths.

	indexTokens := make([]string, 1, 3)
	indexTokens[0] = strings.ToLower(importPath)
	if _, name := path.Split(indexTokens[0]); name != indexTokens[0] {
		indexTokens = append(indexTokens, name)
	}
	if pdoc != nil {
		projectPrefix := strings.ToLower(pdoc.ProjectPrefix)
		if projectPrefix != indexTokens[0] {
			indexTokens = append(indexTokens, projectPrefix)
		}
	}

	ch := make(chan []*datastore.Key, len(indexTokens))
	for _, token := range indexTokens {
		go func(token string) {
			keys, err := datastore.NewQuery("Package").Filter("IndexTokens=", token).KeysOnly().GetAll(c, nil)
			if err != nil {
				c.Errorf("Query(IndexTokens=%s) -> %v", token, err)
			}
			ch <- keys
		}(token)
	}

	m := make(map[string]bool)
	for _ = range indexTokens {
		for _, key := range <-ch {
			m[key.StringID()] = true
		}
	}

	importPaths := make([]string, 0, len(m))
	for p := range m {
		if p[0] == '/' {
			p = p[1:]
		}
		importPaths = append(importPaths, p)
	}

	return executeTemplate(w, "search.html", 200,
		map[string]interface{}{"importPath": importPath, "suggestions": importPaths})
}

func serveAbout(w http.ResponseWriter, r *http.Request) error {
	return executeTemplate(w, "about.html", 200, map[string]interface{}{"Host": r.Host})
}

func init() {
	http.Handle("/", handlerFunc(serveHome))
	http.Handle("/about", handlerFunc(serveAbout))
	http.Handle("/index", handlerFunc(serveIndex))
	http.Handle("/pkg/std", handlerFunc(serveGoIndex))
	http.Handle("/packages", handlerFunc(servePackages))
	http.Handle("/pkg/", handlerFunc(servePackage))
	http.Handle("/a/refresh", handlerFunc(serveClearPackageCache))
	http.Handle("/api/index", handlerFunc(serveAPIIndex))
	http.Handle("/api/update", http.HandlerFunc(serveAPIUpdate))
}
