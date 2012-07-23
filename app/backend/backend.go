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

package backend

import (
	"appengine"
	"appengine/urlfetch"
	"encoding/gob"
	"github.com/garyburd/gopkgdoc/doc"
	"github.com/garyburd/gopkgdoc/index"
	"net/http"
	"strconv"
)

var idx *index.Index

// handlerFunc adapts a function to an http.Handler. 
type handlerFunc func(http.ResponseWriter, *http.Request) error

func (f handlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	if n, i := appengine.BackendInstance(c); n == "" || i == -1 {
		// Don't allow backend handler to run on frontend.
		http.Error(w, "Not Found", 404)
		return
	}
	if appengine.IsDevAppServer() {
		// Handle restarts.
		if err := ensureIndex(c); err != nil {
			c.Errorf("Error %v", err)
			http.Error(w, "ensureIndex() -> "+err.Error(), http.StatusInternalServerError)
		}
	}
	if err := f(w, r); err != nil {
		c.Errorf("%v", err)
		// It's safe to respond with the raw error message because /b/ is retricted to admins.
		http.Error(w, "Internal error: "+err.Error(), http.StatusInternalServerError)
	}
}

func ensureIndex(c appengine.Context) error {
	if idx == nil {
		c.Infof("Loading index.")
		idx = index.New()
		if err := loadIndex(c, idx); err != nil {
			return err
		}
	}

	return nil
}

func serveStart(w http.ResponseWriter, r *http.Request) error {
	if err := ensureIndex(appengine.NewContext(r)); err != nil {
		return err
	}
	http.Error(w, "OK", 200)
	return nil
}

// getPackage gets a package from the index if available or from the vcs.
func getPackage(c appengine.Context, importPath string) (*doc.Package, []index.Result, error) {
	subdirs, err := idx.Subdirs(importPath)
	if err != nil {
		return nil, nil, err
	}

	dpkg, err := idx.Get(importPath)
	if err == doc.ErrPackageNotFound {
		// Not in index. Fetch from vcs.
		dpkg, err = doc.Get(urlfetch.Client(c), importPath, "")
		c.Infof("doc.Get(%q) -> %v", importPath, err)
		switch {
		case err == nil && (dpkg.Name != "" || len(subdirs) > 0):
			// Store empty directory only if the directory has children.
			if err := PutPackage(c, dpkg); err != nil {
				c.Errorf("PutPackage(%q) -> %v", importPath, err)
			} else {
				idx.Put(dpkg)
			}
		case err == doc.ErrPackageNotFound:
			dpkg = nil
			err = nil
		default:
			dpkg = nil
		}
	}
	return dpkg, subdirs, err
}

type QueryResult struct {
	Results []index.Result
}

func serveQuery(w http.ResponseWriter, r *http.Request) error {
	var qr QueryResult
	var err error

	q := r.FormValue("q")

	if doc.ValidRemotePath(q) {
		dpkg, _, err := getPackage(appengine.NewContext(r), q)
		if err == nil {
			qr.Results = []index.Result{{ImportPath: dpkg.ImportPath, Synopsis: dpkg.Synopsis, IsCmd: dpkg.IsCmd}}
		}
	}

	if len(qr.Results) == 0 {
		sortBy, _ := strconv.Atoi(r.FormValue("sortBy"))
		qr.Results, err = idx.Query(q, sortBy)
		if err != nil {
			return err
		}
	}

	return gob.NewEncoder(w).Encode(&qr)
}

type GetPackageResult struct {
	Dpkg    *doc.Package
	Subdirs []index.Result
}

func serveGetPackage(w http.ResponseWriter, r *http.Request) error {
	var gpr GetPackageResult
	var err error

	importPath := r.FormValue("importPath")

	gpr.Dpkg, gpr.Subdirs, err = getPackage(appengine.NewContext(r), importPath)
	if err != nil {
		return err
	}

	return gob.NewEncoder(w).Encode(&gpr)
}

func init() {
	http.Handle("/_ah/start", handlerFunc(serveStart))
	http.Handle("/b/query", handlerFunc(serveQuery))
	http.Handle("/b/getPackage", handlerFunc(serveGetPackage))
}
