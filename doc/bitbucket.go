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

package doc

import (
	"encoding/json"
	"net/http"
	"path"
	"regexp"
)

var bitbucketPattern = regexp.MustCompile(`^bitbucket\.org/([a-z0-9A-Z_.\-]+)/([a-z0-9A-Z_.\-]+)(/[a-z0-9A-Z_.\-/]*)?$`)

func getBitbucketDoc(client *http.Client, m []string, savedEtag string) (*Package, error) {

	importPath := m[0]
	projectPrefix := "bitbucket.org/" + m[1] + "/" + m[2]
	projectName := m[2]
	projectURL := "https://bitbucket.org/" + m[1] + "/" + m[2] + "/"
	userRepo := m[1] + "/" + m[2]

	// Normalize dir to "" or string with trailing '/'.
	dir := m[3]
	if len(dir) > 0 {
		dir = dir[1:] + "/"
	}

	// Find the revision tag for tip and fetch the directory listing for that
	// tag.  Mercurial repositories use the tag "tip". Git repositories use the
	// tag "master".
	var tag string
	var p []byte
	for _, t := range []string{"tip", "master"} {
		var err error
		p, err = httpGet(client, "https://api.bitbucket.org/1.0/repositories/"+userRepo+"/src/"+t+"/"+dir, nil, notFoundNotFound)
		if err == nil {
			tag = t
			break
		} else if err != ErrPackageNotFound {
			return nil, err
		}
	}
	if tag == "" {
		return nil, ErrPackageNotFound
	}

	etag := hashBytes(p)
	if etag == savedEtag {
		return nil, ErrPackageNotModified
	}

	var directory struct {
		Files []struct {
			Path string
		}
	}
	err := json.Unmarshal(p, &directory)
	if err != nil {
		return nil, err
	}

	var files []*source
	for _, f := range directory.Files {
		if isDocFile(f.Path) {
			_, name := path.Split(f.Path)
			files = append(files, &source{
				name:      name,
				browseURL: "https://bitbucket.org/" + userRepo + "/src/" + tag + "/" + f.Path,
				rawURL:    "https://api.bitbucket.org/1.0/repositories/" + userRepo + "/raw/" + tag + "/" + f.Path,
			})
		}
	}

	err = fetchFiles(client, files, nil)
	if err != nil {
		return nil, err
	}

	return buildDoc(importPath, projectPrefix, projectName, projectURL, etag, "#cl-%d", files)
}
