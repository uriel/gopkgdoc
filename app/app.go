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
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	docKeyPrefix = "docb2:"
)

// getDoc gets the package documentation and child packages for the given import path.
func getDoc(c appengine.Context, importPath string) (*doc.Package, []*Package, error) {

	pi := doc.NewPathInfo(importPath)
	if pi == nil {
		return nil, nil, doc.ErrPackageNotFound
	}

	// Fetch child packages for project.

	projectPrefix := pi.ProjectPrefix()
	projectPkgs, err := queryPackages(c, projectListKeyPrefix+projectPrefix,
		datastore.NewQuery("Package").
			Filter("__key__ >", datastore.NewKey(c, "Package", projectPrefix+"/", 0, nil)).
			Filter("__key__ <", datastore.NewKey(c, "Package", projectPrefix+"0", 0, nil)))
	if err != nil {
		return nil, nil, err
	}

	// Filter project packages to children of this package.

	prefix := importPath + "/"
	pkgs := projectPkgs[0:0]
	for _, pkg := range projectPkgs {
		if strings.HasPrefix(pkg.ImportPath, prefix) {
			pkgs = append(pkgs, pkg)
		}
	}

	// Fetch documentation for this package.

	cacheKey := docKeyPrefix + importPath
	var pdoc *doc.Package
	item, err := cacheGet(c, cacheKey, &pdoc)
	switch err {
	case memcache.ErrCacheMiss:
		pdoc, err = pi.Package(urlfetch.Client(c))
		switch err {
		case doc.ErrPackageNotFound:
			if len(pkgs) > 0 {
				// Create a tombstone.
				pdoc = &doc.Package{}
			}
		case nil:
			// Fix standard packages.
			const standardPackagesPrefix = "code.google.com/p/go/src/pkg/"
			if strings.HasPrefix(pdoc.ImportPath, standardPackagesPrefix) {
				pdoc.ImportPath = pdoc.ImportPath[len(standardPackagesPrefix):]
				pdoc.Hide = true
			}
		default:
			return nil, nil, err
		}
		if pdoc != nil {
			// Cache the document that we loaded or the tombstone.
			item.Object = pdoc
			item.Expiration = time.Hour
			if err := cacheSet(c, item); err != nil {
				return nil, nil, err
			}
		}
		// Update the Packages table in the datastore.
		if err := updatePackage(c, pi, pdoc); err != nil {
			return nil, nil, err
		}
	case nil:
		// OK
	default:
		return nil, nil, err
	}

	if pdoc != nil {

		// Merge document children into package list loaded from datastore.

		m := make(map[string]*Package, len(pkgs)+len(pdoc.Children))
		for _, pkg := range pkgs {
			m[pkg.ImportPath] = pkg
		}
		for _, child := range pdoc.Children {
			if _, found := m[child]; !found {
				m[child] = &Package{ImportPath: child}
			}
		}
		keys := make([]string, 0, len(m))
		for key := range m {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		pkgs = pkgs[0:0]
		for _, key := range keys {
			pkgs = append(pkgs, m[key])
		}

		// Ignore tombstones and docs with children only.

		if pdoc.Name == "" {
			pdoc = nil
		}
	}

	if pdoc == nil && len(pkgs) == 0 {
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
		if err, ok := err.(doc.GetError); ok {
			http.Error(w, "Error getting files from "+err.Host+".", http.StatusInternalServerError)
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

	// Fix old Google Project Hosting path
	if m := oldGooglePattern.FindStringSubmatch(importPath); m != nil {
		http.Redirect(w, r, "/pkg/"+newGooglePath(m), 301)
		return nil
	}

	pdoc, pkgs, err := getDoc(c, importPath)
	switch {
	case err == doc.ErrPackageNotFound:
		return executeTemplate(w, "notfound.html", 404, nil)
	case err != nil:
		return err
	}

	if pdoc == nil {
		return executeTemplate(w, "packages.html", 200, map[string]interface{}{
			"pkgs": pkgs,
			"pi":   doc.NewPathInfo(importPath),
		})
	}

	return executeTemplate(w, "pkg.html", 200, map[string]interface{}{
		"pkgs": pkgs,
		"pdoc": pdoc,
		"pi":   doc.NewPathInfo(importPath),
	})
}

func servePackages(w http.ResponseWriter, r *http.Request) error {
	c := appengine.NewContext(r)
	pkgs, err := queryPackages(c, packageListKey, datastore.NewQuery("Package").Filter("Hide=", false))
	if err != nil {
		return err
	}
	return executeTemplate(w, "packages.html", 200, map[string]interface{}{"pkgs": pkgs})
}

func serveAPIPackages(w http.ResponseWriter, r *http.Request) error {
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

var oldGooglePattern = regexp.MustCompile(`^([a-z0-9\-]+)\.googlecode\.com/(svn|git|hg)(/[a-z0-9A-Z_.\-/]+)?$`)

func newGooglePath(m []string) string {
	return "code.google.com/p/" + m[1] + m[3]
}

var importPathCleaners = []struct {
	pat *regexp.Regexp
	fn  func([]string) string
}{
	{
		regexp.MustCompile(`^https?:/+github\.com/([^/]+)/([^/]+)/tree/master/(.*)$`),
		func(m []string) string { return fmt.Sprintf("github.com/%s/%s/%s", m[1], m[2], m[3]) },
	},
	{
		regexp.MustCompile(`^https?:/+bitbucket\.org/([^/]+)/([^/]+)/src/[^/]+/(.*)$`),
		func(m []string) string { return fmt.Sprintf("bitbucket.org/%s/%s/%s", m[1], m[2], m[3]) },
	},
	{
		regexp.MustCompile(`^http:/+code\.google\.com/p/([^/]+)/source/browse(/[^?#]*)?(\?[^#]*)?(#.*)?$`),
		importPathFromGoogleBrowse,
	},
	{
		regexp.MustCompile(`^https?:/+bazaar\.(launchpad\.net/.*)/files$`),
		func(m []string) string { return m[1] },
	},
	{
		regexp.MustCompile(`^https?:/+(.*)$`),
		func(m []string) string { return m[1] },
	},
	{
		oldGooglePattern,
		newGooglePath,
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

	// Logs show that people are looking for the standard packages. Help them
	// out with a redirect to golang.org.
	if standardPackages[importPath] {
		http.Redirect(w, r, standardPackagePath+importPath, 302)
		return nil
	}

	_, _, err := getDoc(c, importPath)

	if err == nil {
		http.Redirect(w, r, "/pkg/"+importPath, 302)
		return nil
	}

	if err != doc.ErrPackageNotFound {
		return err
	}

	// Find suggested import paths.

	indexTokens := make([]string, 1, 3)
	indexTokens[0] = strings.ToLower(importPath)
	if _, name := path.Split(indexTokens[0]); name != indexTokens[0] {
		indexTokens = append(indexTokens, name)
	}
	if pi := doc.NewPathInfo(indexTokens[0]); pi != nil && pi.ProjectPrefix() != indexTokens[0] {
		indexTokens = append(indexTokens, pi.ProjectPrefix())
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
		importPaths = append(importPaths, p)
	}

	return executeTemplate(w, "search.html", 200,
		map[string]interface{}{"importPath": importPath, "suggestions": importPaths})
}

func serveAbout(w http.ResponseWriter, r *http.Request) error {
	return executeTemplate(w, "about.html", 200, map[string]interface{}{"Host": r.Host})
}

func serveGithbHook(w http.ResponseWriter, r *http.Request) {}

func init() {

	http.Handle("/", handlerFunc(serveHome))
	http.Handle("/about", handlerFunc(serveAbout))
	http.Handle("/packages", handlerFunc(servePackages))
	http.Handle("/pkg/", handlerFunc(servePackage))
	http.Handle("/api/packages", handlerFunc(serveAPIPackages))

	// To avoid errors, register handler for the previously documented Github
	// post-receive hook. Consider clearing cache from the hook.
	http.HandleFunc("/hook/github", serveGithbHook)
}
