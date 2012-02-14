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
	"strings"
)

type gitBlob struct {
	Path string
	Url  string
}

var githubRawHeader = http.Header{"Accept": {"application/vnd.github-blob.raw"}}

var githubPattern = regexp.MustCompile(`^github\.com/([a-z0-9A-Z_.\-]+)/([a-z0-9A-Z_.\-]+)(/[a-z0-9A-Z_.\-/]*)?$`)

type githubPathInfo []string

func newGithubPathInfo(m []string) PathInfo    { return githubPathInfo(m) }
func (m githubPathInfo) ImportPath() string    { return m[0] }
func (m githubPathInfo) ProjectPrefix() string { return "github.com/" + m[1] + "/" + m[2] }
func (m githubPathInfo) ProjectName() string   { return m[2] }
func (m githubPathInfo) ProjectURL() string    { return "https://github.com/" + m[1] + "/" + m[2] + "/" }

func (m githubPathInfo) Package(client *http.Client) (*Package, error) {
	importPath := m[0]
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

	blobs, err := getGithubBlobs(client, userRepo)
	if err != nil {
		return nil, err
	}

	var files []source
	children := make(map[string]bool)
	for _, blob := range blobs {
		d, f := path.Split(blob.Path)
		switch {
		case d == dir:
			files = append(files, source{
				f,
				"https://github.com/" + userRepo + "/blob/master/" + dir + f,
				blob.Url,
			})
		case strings.HasPrefix(d, dir) && !strings.HasSuffix(f, "_test.go"):
			children["github.com/"+userRepo+"/"+d[:len(d)-1]] = true
		}
	}

	err = getFiles(client, files, githubRawHeader)
	if err != nil {
		return nil, err
	}

	return buildDoc(importPath, "#L%d", files, children)
}

func getGithubBlobs(client *http.Client, userRepo string) ([]gitBlob, error) {
	var blobs []gitBlob

	p, err := httpGet(client, "https://api.github.com/repos/"+userRepo+"/git/trees/master?recursive=1", nil, true)
	if err != nil {
		return nil, err
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
	for _, node := range tree.Tree {
		if node.Type == "blob" && isDocFile(node.Path) {
			blobs = append(blobs, gitBlob{Path: node.Path, Url: node.Url})
		}
	}
	return blobs, nil
}
