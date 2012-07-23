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
	"appengine/datastore"
	"bytes"
	"encoding/gob"
	"github.com/garyburd/gopkgdoc/doc"
	"github.com/garyburd/gopkgdoc/index"
)

type Package struct {
	Gob []byte `datastore:",noindex"`
}

// GetPackage gets a package from the store.
func GetPackage(c appengine.Context, importPath string) (*doc.Package, error) {
	var pkg Package
	if err := datastore.Get(c, datastore.NewKey(c, "Package", importPath, 0, nil), &pkg); err != nil {
		if err == datastore.ErrNoSuchEntity {
			err = doc.ErrPackageNotFound
		}
		return nil, err
	}
	var dpkg doc.Package
	err := gob.NewDecoder(bytes.NewReader(pkg.Gob)).Decode(&dpkg)
	return &dpkg, err
}

// Put saves a package to the store.
func PutPackage(c appengine.Context, dpkg *doc.Package) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(dpkg); err != nil {
		return err
	}

	if buf.Len() > 800000 {
		// Trnuncate large packages.
		dpkg.Errors = append(dpkg.Errors, "Documentation truncated.")
		dpkg.Vars = nil
		dpkg.Funcs = nil
		dpkg.Types = nil
		dpkg.Consts = nil
		buf.Reset()
		err := gob.NewEncoder(&buf).Encode(dpkg)
		if err != nil {
			return err
		}
	}

	pkg := &Package{Gob: buf.Bytes()}
	_, err := datastore.Put(c, datastore.NewKey(c, "Package", dpkg.ImportPath, 0, nil), pkg)
	return err
}

// DeletePackage deletes a package from the store.
func DeletePackage(c appengine.Context, importPath string) error {
	err := datastore.Delete(c, datastore.NewKey(c, "Package", importPath, 0, nil))
	if err == datastore.ErrNoSuchEntity {
		err = nil
	}
	return err
}

// loadIndex adds all documents in the store to the index.
func loadIndex(c appengine.Context, idx *index.Index) error {
	i := 0
	q := datastore.NewQuery("Package")
	for t := q.Run(c); ; {
		var pkg Package
		key, err := t.Next(&pkg)
		if err == datastore.Done {
			break
		} else if err != nil {
			return err
		}
		var dpkg doc.Package
		if err := gob.NewDecoder(bytes.NewReader(pkg.Gob)).Decode(&dpkg); err != nil {
			c.Errorf("Error decoding %s, %v", key.StringID(), err)
			continue
		}
		idx.Put(&dpkg)
		i += 1
		if i%100 == 0 {
			c.Infof("Loaded %d packages.", i)
		}
	}
	return nil
}
