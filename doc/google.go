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
	"net/http"
	"regexp"
)

var googleRepoRe = regexp.MustCompile(`id="checkoutcmd">(hg|git|svn)`)
var googleFilePattern = regexp.MustCompile(`<li><a href="([^"/]+)"`)

var googlePattern = regexp.MustCompile(`^code\.google\.com/p/([a-z0-9\-]+)(\.[a-z0-9\-]+)?(/[a-z0-9A-Z_.\-/]+)?$`)

type googlePathInfo []string

func newGooglePathInfo(m []string) PathInfo    { return googlePathInfo(m) }
func (m googlePathInfo) ImportPath() string    { return m[0] }
func (m googlePathInfo) ProjectPrefix() string { return "code.google.com/p/" + m[1] + m[2] }
func (m googlePathInfo) ProjectName() string   { return m[1] + m[2] }
func (m googlePathInfo) ProjectURL() string    { return "https://code.google.com/p/" + m[1] + "/" }

func (m googlePathInfo) Package(client *http.Client) (*Package, error) {

	importPath := m[0]
	repo := m[1]
	subrepo := m[2]
	if len(subrepo) > 0 {
		subrepo = subrepo[1:] + "."
	}
	dir := m[3]
	if len(dir) > 0 {
		dir = dir[1:] + "/"
	}

	// Scrape the HTML project page to find the VCS.
	p, err := httpGet(client, "http://code.google.com/p/"+repo+"/source/checkout", nil, true)
	if err != nil {
		return nil, err
	}

	var vcs string
	if m := googleRepoRe.FindSubmatch(p); m != nil {
		vcs = string(m[1])
	} else {
		return nil, ErrPackageNotFound
	}

	// Scrape the repo browser to find individual Go files.
	p, err = httpGet(client, "http://"+subrepo+repo+".googlecode.com/"+vcs+"/"+dir, nil, true)
	if err != nil {
		return nil, err
	}

	var files []source
	query := ""
	if subrepo != "" {
		query = "?repo=" + subrepo[:len(subrepo)-1]
	}
	for _, m := range googleFilePattern.FindAllSubmatch(p, -1) {
		fname := string(m[1])
		if isDocFile(fname) {
			files = append(files, source{
				fname,
				"http://code.google.com/p/" + repo + "/source/browse/" + dir + fname + query,
				"http://" + subrepo + repo + ".googlecode.com/" + vcs + "/" + dir + fname,
			})
		}
	}

	err = getFiles(client, files, nil)
	if err != nil {
		return nil, err
	}

	// TODO: find child directories.
	return buildDoc(importPath, "#%d", files, nil)
}
