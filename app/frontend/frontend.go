// Copyright 2012 Gary Burd
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

package frontend

import (
	"appengine"
	"appengine/datastore"
	"appengine/urlfetch"
	"backend"
	"compress/gzip"
	"encoding/gob"
	"errors"
	"fmt"
	"github.com/garyburd/gopkgdoc/doc"
	"github.com/garyburd/gopkgdoc/index"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

type backendDownError struct {
	error
}

func isBackendDownError(err error) bool {
	_, ok := err.(backendDownError)
	return ok
}

func callBackend(c appengine.Context, path string, params url.Values, v interface{}) error {
	u := url.URL{
		Scheme:   "http",
		Host:     appengine.BackendHostname(c, "index", 0),
		Path:     path,
		RawQuery: params.Encode(),
	}
	if u.Host == "" || u.Host == "localhost:" {
		return backendDownError{errors.New("backend unknown")}
	}
	req, err := http.NewRequest("POST", u.String(), nil)
	if err != nil {
		return backendDownError{err}
	}
	req.Header.Set("X-AppEngine-FailFast", "1")
	client := &http.Client{Transport: &urlfetch.Transport{Context: c, Deadline: 10 * time.Second}}
	resp, err := client.Do(req)
	if err != nil {
		return backendDownError{err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("Request for %s returned status %d", u.String(), resp.StatusCode)
	}
	return gob.NewDecoder(resp.Body).Decode(v)
}

func queryBackend(c appengine.Context, q string) ([]index.Result, error) {
	var v backend.QueryResult
	err := callBackend(c, "/b/query", url.Values{"q": {q}}, &v)
	return v.Results, err
}

func getPackageBackend(c appengine.Context, importPath string) (*doc.Package, []index.Result, error) {
	var v backend.GetPackageResult
	err := callBackend(c, "/b/getPackage", url.Values{"importPath": {importPath}}, &v)
	return v.Dpkg, v.Subdirs, err
}

// getPackageStore gets a package and children directly from the datastore.
// This function is used a a fallback when the backend is not available.
func getPackageStore(c appengine.Context, importPath string) (*doc.Package, []index.Result, error) {

	keys, err := datastore.NewQuery("Package").
		Filter("__key__ >", datastore.NewKey(c, "Package", importPath+"/", 0, nil)).
		Filter("__key__ <", datastore.NewKey(c, "Package", importPath+"0", 0, nil)).
		KeysOnly().GetAll(c, nil)
	if err != nil {
		return nil, nil, err
	}

	subdirs := make([]index.Result, len(keys))
	for i, key := range keys {
		subdirs[i].ImportPath = key.StringID()
	}

	dpkg, err := backend.GetPackage(c, importPath)
	switch {
	case err == doc.ErrPackageNotFound && len(subdirs) > 0:
		// Get project info from child.
		subPkg, err := backend.GetPackage(c, subdirs[0].ImportPath)
		if err != nil {
			if err == doc.ErrPackageNotFound {
				err = fmt.Errorf("Could not get child package %s", subdirs[0].ImportPath)
			}
			return nil, nil, err
		}
		dpkg = &doc.Package{
			ImportPath:  importPath,
			ProjectRoot: subPkg.ProjectRoot,
			ProjectName: subPkg.ProjectName,
			ProjectURL:  subPkg.ProjectURL,
		}
	case err == doc.ErrPackageNotFound:
		dpkg = nil
	case err != nil:
		return nil, nil, err
	}

	return dpkg, subdirs, nil
}

func splitCmds(in []index.Result) (out []index.Result, cmds []index.Result) {
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

// handlerFunc adapts a function to an http.Handler. 
type handlerFunc func(http.ResponseWriter, *http.Request) error

func (f handlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := f(w, r); err != nil {
		appengine.NewContext(r).Errorf("%v", err)
		switch {
		case isBackendDownError(err):
			http.Error(w, "GoPkgDoc temporarily unavailable. Try again later", http.StatusServiceUnavailable)
		case appengine.IsCapabilityDisabled(err) || appengine.IsOverQuota(err):
			http.Error(w, "Internal error: "+err.Error(), http.StatusServiceUnavailable)
		default:
			http.Error(w, "Internal Error", http.StatusInternalServerError)
		}
	}
}

// servePackage handles an individual package page.
func servePackage(w http.ResponseWriter, r *http.Request) error {
	c := appengine.NewContext(r)

	p := path.Clean(r.URL.Path)
	if p != r.URL.Path {
		http.Redirect(w, r, p, 301)
		return nil
	}

	importPath := r.URL.Path[1:]

	dpkg, subdirs, err := getPackageBackend(c, importPath)
	if isBackendDownError(err) {
		c.Infof("serving package directly from store, %v", err)
		dpkg, subdirs, err = getPackageStore(c, importPath)
	}

	if err != nil {
		return err
	}

	if dpkg == nil {
		return executeTemplate(w, "notfound.html", 404, nil)
	}

	pkgs, cmds := splitCmds(subdirs)
	data := struct {
		Dpkg       *doc.Package
		Pkgs, Cmds []index.Result
	}{
		dpkg,
		pkgs,
		cmds,
	}
	return executeTemplate(w, "pkg.html", 200, &data)
}

// serverQuery handles queries from the home page, the package index and the
// standard package list.
func serveQuery(w http.ResponseWriter, r *http.Request, tmpl string, q string) error {
	c := appengine.NewContext(r)
	results, err := queryBackend(c, q)
	if err != nil {
		return err
	}

	if len(results) == 1 && results[0].ImportPath == q {
		// I'm feeling lucky.
		http.Redirect(w, r, "/"+q, 302)
		return nil
	}

	pkgs, cmds := splitCmds(results)
	data := struct {
		Q          string
		Pkgs, Cmds []index.Result
	}{
		q,
		pkgs,
		cmds,
	}
	return executeTemplate(w, tmpl, 200, &data)
}

func serveGoIndex(w http.ResponseWriter, r *http.Request) error {
	return serveQuery(w, r, "std.html", "project:")
}

func serveIndex(w http.ResponseWriter, r *http.Request) error {
	return serveQuery(w, r, "index.html", "all:")
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
		// string containing space
		regexp.MustCompile(`^.*\s.*$`),
		func(m []string) string { return m[0] },
	},
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
	{
		// quoted string
		regexp.MustCompile(`^"([^"]+)"$`),
		func(m []string) string { return m[1] },
	},
}

func cleanQuery(q string) string {
	q = strings.TrimSpace(q)
	if len(q) == 0 {
		return q
	}
	for _, c := range queryCleaners {
		if m := c.pat.FindStringSubmatch(q); m != nil {
			q = c.fn(m)
			break
		}
	}
	return q
}

func serveHome(w http.ResponseWriter, r *http.Request) error {
	if r.URL.Path != "/" {
		return servePackage(w, r)
	}
	q := cleanQuery(r.FormValue("q"))
	if q == "" {
		return executeTemplate(w, "home.html", 200, nil)
	}
	return serveQuery(w, r, "results.html", q)
}

func serveAbout(w http.ResponseWriter, r *http.Request) error {
	data := struct {
		Host string
	}{
		r.Host,
	}
	return executeTemplate(w, "about.html", 200, &data)
}

func serveUpload(w http.ResponseWriter, r *http.Request) error {
	c := appengine.NewContext(r)
	rd, err := gzip.NewReader(r.Body)
	if err != nil {
		return err
	}
	defer rd.Close()
	d := gob.NewDecoder(rd)
	for {
		var dpkg doc.Package
		err := d.Decode(&dpkg)
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		err = datastore.RunInTransaction(c, func(c appengine.Context) error {
			return backend.PutPackage(c, &dpkg)
		}, nil)
		c.Infof("Put package %s -> %v", dpkg.ImportPath, err)
	}
	io.WriteString(w, "OK\n")
	return nil
}

func init() {
	http.Handle("/", handlerFunc(serveHome))
	http.Handle("/-/about", handlerFunc(serveAbout))
	http.Handle("/-/index", handlerFunc(serveIndex))
	http.Handle("/-/go", handlerFunc(serveGoIndex))
	http.Handle("/-/upload", handlerFunc(serveUpload))
	//http.Handle("/-/refresh", handlerFunc(serveClearPackageCache))
}
