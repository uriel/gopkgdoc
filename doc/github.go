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

var githubRawHeader = http.Header{"Accept": {"application/vnd.github-blob.raw"}}
var githubPattern = regexp.MustCompile(`^github\.com/([a-z0-9A-Z_.\-]+)/([a-z0-9A-Z_.\-]+)(/[a-z0-9A-Z_.\-/]*)?$`)

func getGithubDoc(client *http.Client, m []string, savedEtag string) (*Package, error) {
	importPath := m[0]
	projectPrefix := "github.com/" + m[1] + "/" + m[2]
	projectName := m[2]
	projectURL := "https://github.com/" + m[1] + "/" + m[2] + "/"

	userRepo := m[1] + "/" + m[2]

	// Normalize to "" or string with trailing '/'.
	dir := m[3]
	if len(dir) > 0 {
		dir = dir[1:] + "/"
	}

	// There are two approaches for fetching files from Github:
	//
	// - Read the zipball or tarball.
	//
	// - Use the API to get a list of blobs in the repo and then fetch
	//   the individual blobs.
	//
	// The second approach is used because it is faster and more reliable.

	p, err := httpGet(client, "https://api.github.com/repos/"+userRepo+"/git/trees/master?recursive=1", nil, notFoundNotFound)
	if err != nil {
		return nil, err
	}

	etag := hashBytes(p)
	if etag == savedEtag {
		return nil, ErrPackageNotModified
	}

	var tree struct {
		Tree []struct {
			Url  string
			Path string
			Type string
		}
	}
	err = json.Unmarshal(p, &tree)
	if err != nil {
		return nil, err
	}

	var files []*source
	for _, node := range tree.Tree {
		if node.Type == "blob" && isDocFile(node.Path) {
			d, f := path.Split(node.Path)
			if d == dir {
				files = append(files, &source{
					name:      f,
					browseURL: "https://github.com/" + userRepo + "/blob/master/" + dir + f,
					rawURL:    node.Url,
				})
			}
		}
	}

	err = fetchFiles(client, files, githubRawHeader)
	if err != nil {
		return nil, err
	}

	return buildDoc(importPath, projectPrefix, projectName, projectURL, etag, "#L%d", files)
}
