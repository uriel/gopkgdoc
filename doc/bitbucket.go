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
	"regexp"
)

var bitbucketPattern = regexp.MustCompile(`^bitbucket\.org/([a-z0-9A-Z_.\-]+)/([a-z0-9A-Z_.\-]+)(/[a-z0-9A-Z_.\-/]*)?$`)

type bitbucketPathInfo []string

func newBitbucketPathInfo(m []string) PathInfo    { return bitbucketPathInfo(m) }
func (m bitbucketPathInfo) ImportPath() string    { return m[0] }
func (m bitbucketPathInfo) ProjectPrefix() string { return "bitbucket.org/" + m[1] + "/" + m[2] }
func (m bitbucketPathInfo) ProjectName() string   { return m[2] }
func (m bitbucketPathInfo) ProjectURL() string {
	return "https://bitbucket.org/" + m[1] + "/" + m[2] + "/"
}

func (m bitbucketPathInfo) Package(client *http.Client) (*Package, error) {

	importPath := m[0]
	userRepo := m[1] + "/" + m[2]

	// Normalize dir to "" or string with trailing '/'.
	dir := m[3]
	if len(dir) > 0 {
		dir = dir[1:] + "/"
	}

	p, err := httpGet(client, "https://api.bitbucket.org/1.0/repositories/"+userRepo+"/src/tip/"+dir, nil, true)
	if err != nil {
		return nil, err
	}

	var directory struct {
		Files []struct {
			Path string
		}
	}
	err = json.Unmarshal(p, &directory)
	if err != nil {
		return nil, err
	}

	var files []source
	for _, f := range directory.Files {
		if isDocFile(f.Path) {
			files = append(files, source{
				f.Path,
				"https://bitbucket.org/" + userRepo + "/src/tip/" + f.Path,
				"https://api.bitbucket.org/1.0/repositories/" + userRepo + "/raw/tip/" + f.Path,
			})
		}
	}

	err = getFiles(client, files, nil)
	if err != nil {
		return nil, err
	}

	return buildDoc(importPath, "#cl-%d", files)
}
