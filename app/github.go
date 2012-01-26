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
	"appengine/memcache"
	"doc"
	"http"
	"json"
	"os"
	"path"
	"regexp"
)

type gitBlob struct {
	Path string
	Url  string
}

var githubRawHeader = http.Header{"Accept": {"application/vnd.github-blob.raw"}}

var gitPattern = regexp.MustCompile(`^github\.com/([a-z0-9A-Z_.\-]+)/([a-z0-9A-Z_.\-]+)(/[a-z0-9A-Z_.\-/]*)?$`)

func getGithubIndexTokens(match []string) []string {
	return []string{"github.com/" + match[1] + "/" + match[2]}
}

func getGithubDoc(c appengine.Context, match []string) (*doc.Package, os.Error) {
	importPath := match[0]
	userName := match[1]
	repoName := match[2]
	userRepo := userName + "/" + repoName

	// Normalize to "" or string with trailing '/'.
	dir := match[3]
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

	blobs, err := getGithubBlobs(c, userRepo)
	if err != nil {
		return nil, err
	}

	var files []doc.Source
	for _, blob := range blobs {
		d, f := path.Split(blob.Path)
		if d == dir {
			files = append(files, doc.Source{
				"https://github.com/" + userRepo + "/blob/master/" + dir + f,
				newAsyncReader(c, blob.Url, githubRawHeader)})
		}
	}

	pdoc, err := doc.Build(importPath, "#L%d", files)
	if err != nil {
		return nil, err
	}

	pdoc.ProjectURL = "https://github.com/" + userRepo
	pdoc.ProjectName = repoName
	return pdoc, nil
}

func getGithubBlobs(c appengine.Context, userRepo string) ([]gitBlob, os.Error) {
	var blobs []gitBlob

	cacheKey := "gitblobs:" + userRepo
	if err := cacheGet(c, cacheKey, &blobs); err != memcache.ErrCacheMiss {
		return blobs, err
	}

	c.Infof("Reading Github repo %s", userRepo)

	p, err := httpGet(c, "https://api.github.com/repos/"+userRepo+"/git/trees/master?recursive=1")
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
		if node.Type == "blob" && doc.UseFile(node.Path) {
			blobs = append(blobs, gitBlob{Path: node.Path, Url: node.Url})
		}
	}
	if err := cacheSet(c, cacheKey, blobs, 3600); err != nil {
		return nil, err
	}
	return blobs, nil
}
