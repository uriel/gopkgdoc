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

package app

import (
	"appengine"
	"appengine/datastore"
	"appengine/memcache"
	"doc"
	"path"
	"strings"
	"time"
)

const (
	packageListKey       = "pkglistb1"
	projectListKeyPrefix = "proj:"
)

type Package struct {
	ImportPath  string `datastore:"-"`
	Synopsis    string `datastore:",noindex"`
	PackageName string `datastore:",noindex"`
	Hide        bool
	IndexTokens []string
}

func queryPackages(c appengine.Context, cacheKey string, query *datastore.Query) ([]*Package, error) {
	var pkgs []*Package
	item, err := cacheGet(c, cacheKey, &pkgs)
	if err == memcache.ErrCacheMiss {
		keys, err := query.GetAll(c, &pkgs)
		if err != nil {
			return nil, err
		}
		for i := range keys {
			pkgs[i].ImportPath = keys[i].StringID()
		}
		item.Expiration = time.Hour
		item.Object = pkgs
		if err := cacheSafeSet(c, item); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	return pkgs, nil
}

func (pkg *Package) equal(other *Package) bool {
	if pkg.Synopsis != other.Synopsis {
		return false
	}
	if pkg.Hide != other.Hide {
		return false
	}
	if len(pkg.IndexTokens) != len(other.IndexTokens) {
		return false
	}
	for i := range pkg.IndexTokens {
		if pkg.IndexTokens[i] != other.IndexTokens[i] {
			return false
		}
	}
	return true
}

// updatePackage updates the package in the datastore and clears memcache as
// needed.
func updatePackage(c appengine.Context, pi doc.PathInfo, pdoc *doc.Package) error {

	importPath := pi.ImportPath()

	var pkg *Package
	if pdoc != nil && pdoc.Name != "" {
		indexTokens := make([]string, 1, 3)
		indexTokens[0] = strings.ToLower(pi.ProjectPrefix())
		if !pdoc.Hide {
			indexTokens = append(indexTokens, strings.ToLower(pdoc.Name))
			if _, name := path.Split(strings.ToLower(pdoc.ImportPath)); name != indexTokens[1] {
				indexTokens = append(indexTokens, name)
			}
		}
		pkg = &Package{
			Synopsis:    pdoc.Synopsis,
			PackageName: pdoc.Name,
			Hide:        pdoc.Hide,
			IndexTokens: indexTokens,
		}
	}

	// Update the datastore. To minimize datastore costs, the datastore is 
	// conditionally updated by comparing the package to the stored package.

	var invalidateCache bool

	key := datastore.NewKey(c, "Package", importPath, 0, nil)
	var storedPackage Package
	err := datastore.Get(c, key, &storedPackage)
	switch err {
	case datastore.ErrNoSuchEntity:
		if pkg != nil {
			invalidateCache = true
			c.Infof("Adding package %s", importPath)
			if _, err := datastore.Put(c, key, pkg); err != nil {
				c.Errorf("Put(%s) -> %v", importPath, err)
			}
		}
	case nil:
		if pkg == nil {
			invalidateCache = true
			c.Infof("Deleting package %s", importPath)
			if err := datastore.Delete(c, key); err != datastore.ErrNoSuchEntity && err != nil {
				c.Errorf("Delete(%s) -> %v", importPath, err)
			}
		} else if !pkg.equal(&storedPackage) {
			invalidateCache = storedPackage.Synopsis != pkg.Synopsis
			c.Infof("Updating package %s", importPath)
			if _, err := datastore.Put(c, key, pkg); err != nil {
				c.Errorf("Put(%s) -> %v", importPath, err)
			}
		}
	default:
		c.Errorf("Get(%s) -> %v", importPath, err)
	}

	// Update memcache.

	if invalidateCache {
		if err = cacheClear(c, packageListKey, projectListKeyPrefix+pi.ProjectPrefix()); err != nil {
			return err
		}
	}
	return nil
}
