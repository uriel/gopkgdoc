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
	"appengine/memcache"
	"bytes"
	"doc"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Package struct {
	ImportPath  string `datastore:"-"`
	Synopsis    string `datastore:",noindex"`
	PackageName string `datastore:",noindex"`
	Hide        bool
	IndexTokens []string
}

// service represents a source code control service.
type service struct {
	pattern        *regexp.Regexp
	getDoc         func(appengine.Context, []string) (*doc.Package, error)
	getIndexTokens func([]string) []string
	prefix         string
}

// services is the list of source code control services handled by gopkgdoc.
var services = []*service{
	&service{gitPattern, getGithubDoc, getGithubIndexTokens, "github.com/"},
	&service{googlePattern, getGoogleDoc, getGoogleIndexTokens, "code.google.com/"},
	&service{bitbucketPattern, getBitbucketDoc, getBitbucketIndexTokens, "bitbucket.org/"},
	&service{launchpadPattern, getLaunchpadDoc, getLaunchpadIndexTokens, "launchpad.net/"},
}

func toLowerStrings(a []string) []string {
	for i := range a {
		a[i] = strings.ToLower(a[i])
	}
	return a
}

// lookupService returns service and regexp match for first service that
// matches importPath.
func lookupService(importPath string) (*service, []string) {
	for _, s := range services {
		if m := s.pattern.FindStringSubmatch(importPath); m != nil {
			return s, m
		}
	}
	return nil, nil
}

// getDoc returns the document for the given import path or a list of search
// tokens for the import path.
func getDoc(c appengine.Context, importPath string) (*doc.Package, []string, error) {
	cacheKey := "docb1:" + importPath

	// Cached?
	var pdoc *doc.Package
	err := cacheGet(c, cacheKey, &pdoc)
	if err == nil {
		return pdoc, nil, nil
	}
	if err != memcache.ErrCacheMiss {
		return nil, nil, err
	}

	s, m := lookupService(importPath)
	if s == nil {
		return nil, toLowerStrings([]string{importPath}), nil
	}

	c.Infof("Reading package %s", importPath)
	pdoc, err = s.getDoc(c, m)

	if err == doc.ErrPackageNotFound {
		if err := datastore.Delete(c,
			datastore.NewKey(c, "Package", importPath, 0, nil)); err != datastore.ErrNoSuchEntity && err != nil {
			c.Errorf("Delete(%s) -> %v", importPath, err)
		}
		return nil, toLowerStrings(s.getIndexTokens(m)), nil
	}

	if err != nil {
		return nil, nil, err
	}

	if err := cacheSet(c, cacheKey, pdoc, time.Hour); err != nil {
		return nil, nil, err
	}

	indexTokens := s.getIndexTokens(m)
	if pdoc.Name != "main" {
		indexTokens = append(indexTokens, pdoc.Name)
	}

	indexTokens = toLowerStrings(indexTokens)
	if _, err := datastore.Put(c,
		datastore.NewKey(c, "Package", importPath, 0, nil),
		&Package{
			Synopsis:    pdoc.Synopsis,
			PackageName: pdoc.Name,
			Hide:        pdoc.Hide,
			IndexTokens: indexTokens,
		}); err != nil {
		c.Errorf("Put(%s) -> %v", importPath, err)
	}
	return pdoc, nil, nil
}

type handlerFunc func(http.ResponseWriter, *http.Request) error

func (f handlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	err := f(w, r)
	if err != nil {
		appengine.NewContext(r).Errorf("Error %s", err.Error())
		http.Error(w, "Internal Error", http.StatusInternalServerError)
	}
}

func servePackage(w http.ResponseWriter, r *http.Request) error {
	c := appengine.NewContext(r)

	path := path.Clean(r.URL.Path)
	if path == "/pkg" {
		return executeTemplate(w, "notfound.html", 404, nil)
	}
	if path != r.URL.Path {
		http.Redirect(w, r, path, 301)
		return nil
	}
	importPath := path[len("/pkg/"):]

	// Fix old Google Project Hosting path
	if m := oldGooglePattern.FindStringSubmatch(importPath); m != nil {
		http.Redirect(w, r, "/pkg/"+newGooglePath(m), 301)
		return nil
	}

	pdoc, _, err := getDoc(c, importPath)
	if err != nil {
		return err
	}

	if pdoc == nil {
		return executeTemplate(w, "notfound.html", 404, nil)
	}

	return executeTemplate(w, "pkg.html", 200, pdoc)
}

func servePackages(w http.ResponseWriter, r *http.Request) error {
	c := appengine.NewContext(r)

	var pkgList struct {
		Packages []Package
		Updated  time.Time
	}

	const cacheKey = "pkgListb1"
	err := cacheGet(c, cacheKey, &pkgList)
	switch err {
	case memcache.ErrCacheMiss:
		keys, err := datastore.NewQuery("Package").Filter("Hide=", false).GetAll(c, &pkgList.Packages)
		if err != nil {
			return err
		}
		for i := range pkgList.Packages {
			pkgList.Packages[i].ImportPath = keys[i].StringID()
		}
		pkgList.Updated = time.Now()
		if err := cacheSet(c, cacheKey, &pkgList, time.Hour); err != nil {
			return err
		}
	case nil:
		// nothing to do
	default:
		return err
	}

	return executeTemplate(w, "packages.html", 200, &pkgList)
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
	for _, c := range importPathCleaners {
		if m := c.pat.FindStringSubmatch(q); m != nil {
			return c.fn(m)
		}
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

	// Logs show that people are looking for the standard pacakges. Help them
	// out with a redirect to golang.org.
	if standardPackages[importPath] {
		http.Redirect(w, r, standardPackagePath+importPath, 302)
		return nil
	}

	// Get package documentation or index tokens. 
	pdoc, indexTokens, err := getDoc(c, importPath)
	if err != nil {
		return err
	}

	// We got it, 
	if pdoc != nil {
		http.Redirect(w, r, "/pkg/"+importPath, 302)
		return nil
	}

	// Use index tokens to find suggested import paths.
	resultChans := make([]chan []string, len(indexTokens))
	for i := range indexTokens {
		resultChans[i] = make(chan []string)
		go func(token string, resultChan chan []string) {
			keys, err := datastore.NewQuery("Package").Filter("IndexTokens=", token).KeysOnly().GetAll(c, nil)
			if err != nil {
				c.Errorf("Query(IndexTokens=%s) -> %v", token, err)
			}
			importPaths := make([]string, len(keys))
			for i, key := range keys {
				importPaths[i] = key.StringID()
			}
			resultChan <- importPaths
		}(indexTokens[i], resultChans[i])
	}

	var importPaths []string
	for _, resultChan := range resultChans {
		importPaths = append(importPaths, <-resultChan...)
	}

	return executeTemplate(w, "search.html", 200,
		map[string]interface{}{"importPath": importPath, "didYouMean": importPaths})
}

func redirectQuery(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/?q="+url.QueryEscape(r.URL.Path), 301)
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

	for _, s := range services {
		http.HandleFunc("/"+s.prefix, redirectQuery)
	}

	// To avoid errors, register handler for the previously documented Github
	// post-receive hook. Consider clearing cache from the hook.
	http.HandleFunc("/hook/github", serveGithbHook)
}
