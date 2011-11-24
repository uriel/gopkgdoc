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
	"fmt"
	"go/doc"
	"http"
	"os"
	"path"
	"regexp"
	"strings"
	"template"
	"time"
	"url"
)

type Package struct {
	ImportPath   string `datastore:"-"`
	Synopsis     string `datastore:",noindex"`
	PackageName  string `datastore:",noindex"`
	IndexTokens  []string
	RelatedPaths []string
}

var hosts = []struct {
	pattern        *regexp.Regexp
	getDoc         func(appengine.Context, []string) (*packageDoc, os.Error)
	getIndexTokens func([]string) []string
}{
	{gitPattern, getGithubDoc, getGithubIndexTokens},
	{googlePattern, getGoogleDoc, getGoogleIndexTokens},
	{bitbucketPattern, getBitbucketDoc, getBitbucketIndexTokens},
	{launchpadPattern, getLaunchpadDoc, getLaunchpadIndexTokens},
}

func canonicalizeTokens(tokens []string) []string {
	for i := range tokens {
		tokens[i] = strings.ToLower(tokens[i])
	}
	return tokens
}

// getDoc returns the document for the given import path or a list of search
// tokens for the import path.
func getDoc(c appengine.Context, importPath string) (*packageDoc, []string, os.Error) {
	cacheKey := "doc:" + importPath

	// Cached?
	var doc *packageDoc
	err := cacheGet(c, cacheKey, &doc)
	if err == nil {
		return doc, nil, nil
	}
	if err != memcache.ErrCacheMiss {
		return nil, nil, err
	}

	for _, h := range hosts {
		if m := h.pattern.FindStringSubmatch(importPath); m != nil {
			c.Infof("Reading package %s", importPath)
			doc, err = h.getDoc(c, m)
			switch {
			case err == errPackageNotFound:
				if err := datastore.Delete(c,
					datastore.NewKey(c, "Package", importPath, 0, nil)); err != datastore.ErrNoSuchEntity && err != nil {
					c.Errorf("Delete(%s) -> %v", importPath, err)
				}
				return nil, canonicalizeTokens(h.getIndexTokens(m)), nil
			case err != nil:
				return nil, nil, err
			default:
				if err := cacheSet(c, cacheKey, doc, 3600); err != nil {
					return nil, nil, err
				}
				indexTokens := h.getIndexTokens(m)
				if doc.PackageName != "main" {
					indexTokens = append(indexTokens, doc.PackageName)
				}
				indexTokens = canonicalizeTokens(indexTokens)
				if _, err := datastore.Put(c,
					datastore.NewKey(c, "Package", importPath, 0, nil),
					&Package{
						Synopsis:    doc.Synopsis,
						PackageName: doc.PackageName,
						IndexTokens: indexTokens,
					}); err != nil {
					c.Errorf("Put(%s) -> %v", importPath, err)
				}
				return doc, nil, nil
			}
		}
	}
	return nil, canonicalizeTokens([]string{importPath}), nil
}

func relativeTime(t int64) string {
	d := time.Seconds() - t
	switch {
	case d < 1:
		return "just now"
	case d < 2:
		return "one second ago"
	case d < 60:
		return fmt.Sprintf("%d seconds ago", d)
	case d < 120:
		return "one minute ago"
	}
	return fmt.Sprintf("%d minutes ago", d/60)
}

func commentFmt(v string) string {
	var buf bytes.Buffer
	doc.ToHTML(&buf, []byte(v), nil)
	return buf.String()
}

var fmap = template.FuncMap{"comment": commentFmt, "relativeTime": relativeTime}

func parseTemplate(name string) func(http.ResponseWriter, int, interface{}) os.Error {
	if appengine.IsDevAppServer() {
		return func(w http.ResponseWriter, status int, value interface{}) os.Error {
			t, err := template.New(name).Funcs(fmap).ParseFile(name)
			if err != nil {
				return err
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(status)
			return t.Execute(w, value)
		}
	}
	t := template.Must(template.New(name).Funcs(fmap).ParseFile(name))
	return func(w http.ResponseWriter, status int, value interface{}) os.Error {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		return t.Execute(w, value)
	}
}

var (
	executeHomeTemplate     = parseTemplate("template/home.html")
	executePkgTemplate      = parseTemplate("template/pkg.html")
	executeNotFoundTemplate = parseTemplate("template/notfound.html")
	executePkgListTemplate  = parseTemplate("template/packageList.html")
)

type handlerFunc func(http.ResponseWriter, *http.Request) os.Error

func (f handlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	err := f(w, r)
	if err != nil {
		appengine.NewContext(r).Errorf("Error %s", err.String())
		http.Error(w, "Internal Error", http.StatusInternalServerError)
	}
}

func servePkg(w http.ResponseWriter, r *http.Request) os.Error {
	c := appengine.NewContext(r)

	path := path.Clean(r.URL.Path)
	if path == "/pkg" {
		return servePackageList(w, r)
	}
	if path != r.URL.Path {
		http.Redirect(w, r, path, 301)
		return nil
	}
	importPath := path[len("/pkg/"):]
	doc, _, err := getDoc(c, importPath)
	if err != nil {
		return err
	}

	if doc == nil {
		return executeNotFoundTemplate(w, 404, nil)
	}

	return executePkgTemplate(w, 200, doc)
}

func servePackageList(w http.ResponseWriter, r *http.Request) os.Error {
	c := appengine.NewContext(r)
	var pkgs []Package
	keys, err := datastore.NewQuery("Package").GetAll(c, &pkgs)
	if err != nil {
		return err
	}

	for i := range pkgs {
		pkgs[i].ImportPath = keys[i].StringID()
	}

	if r.FormValue("text") != "" {
		var buf bytes.Buffer
		for _, pkg := range pkgs {
			buf.WriteString(pkg.ImportPath)
			buf.WriteByte('\n')
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(buf.Bytes())
		return nil
	}

	return executePkgListTemplate(w, 200, pkgs)
}

func formatMatch(f string, m []string) string {
	v := make([]interface{}, 0, len(m))
	for _, s := range m[1:] {
		v = append(v, s)
	}
	return fmt.Sprintf(f, v...)
}

func importPathFromGoogleBrowse(m []string) string {
	dir, err := url.QueryUnescape(m[2])
	if err != nil {
		return m[0]
	}
	if i := strings.IndexRune(dir, '%'); i >= 0 {
		dir = dir[:i]
	}
	return m[1] + ".googlecode.com/hg" + dir
}

var importPathCleaners = []struct {
	pat *regexp.Regexp
	fn  func([]string) string
}{
	{
		regexp.MustCompile(`^https?:/+github\.com/([^/]+)/([^/]+)/tree/master/(.*)$`),
		func(m []string) string { return formatMatch("github.com/%s/%s/%s", m) },
	},
	{
		regexp.MustCompile(`^https?:/+bitbucket\.org/([^/]+)/([^/]+)/src/[^/]+/(.*)$`),
		func(m []string) string { return formatMatch("bitbucket.org/%s/%s/%s", m) },
	},
	{
		regexp.MustCompile(`^http:/+code.google.com/p/([^/]+)/source/browse/#hg(.*)$`),
		importPathFromGoogleBrowse,
	},
	{
		regexp.MustCompile(`^https?:/+code\.google\.com/p/([^/]+)$`),
		func(m []string) string { return formatMatch("%s.googlecode.com/hg", m) },
	},
	{
		regexp.MustCompile(`^https?:/+bazaar\.(launchpad\.net/.*)/files$`),
		func(m []string) string { return m[1] },
	},
	{
		regexp.MustCompile(`^https?:/+(.*)$`),
		func(m []string) string { return m[1] },
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

func serveHome(w http.ResponseWriter, r *http.Request) os.Error {
	c := appengine.NewContext(r)

	if r.URL.Path != "/" {
		return executeNotFoundTemplate(w, 404, nil)
	}

	importPath := cleanImportPath(r.FormValue("q"))
	data := map[string]interface{}{"Host": r.Host}

	// Display simple home page when no query.
	if importPath == "" {
		return executeHomeTemplate(w, 200, data)
	}

	// Logs show that people are looking for the standard pacakges. Help them
	// out with a redirect to golang.org.
	if isStandardPackage(c, importPath) {
		http.Redirect(w, r, "http://golang.org/pkg/"+importPath, 302)
		return nil
	}

	// Get package documentation or index tokens. 
	doc, indexTokens, err := getDoc(c, importPath)
	if err != nil {
		return err
	}

	// We got it, 
	if doc != nil {
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

	data["importPath"] = importPath
	data["didYouMean"] = importPaths
	data["oneDidYouMean"] = len(importPaths) == 1
	return executeHomeTemplate(w, 200, data)
}

func serveGithbHook(w http.ResponseWriter, r *http.Request) {
}

func init() {
	http.Handle("/", handlerFunc(serveHome))
	http.Handle("/pkg/", handlerFunc(servePkg))

	// To avoid errors, register handler for the previously documented Github
	// post-receive hook. Consider clearing cache from the hook.
	http.HandleFunc("/hook/github", serveGithbHook)
}
