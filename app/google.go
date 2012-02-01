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
	"doc"
	"os"
	"regexp"
)

var googlePattern = regexp.MustCompile(`^code\.google\.com/p/([a-z0-9\-]+)(\.[a-z0-9\-]+)?(/[a-z0-9A-Z_.\-/]+)?$`)
var googleRepoRe = regexp.MustCompile(`id="checkoutcmd">(hg|git|svn)`)
var googleFilePattern = regexp.MustCompile(`<li><a href="([^"/]+)"`)

func getGoogleIndexTokens(match []string) []string {
	return []string{"code.google.com/p/" + match[1] + match[2]}
}

func getGoogleDoc(c appengine.Context, match []string) (*doc.Package, os.Error) {

	importPath := match[0]
	projectName := match[1]
	subrepo := match[2]
	if len(subrepo) > 0 {
		subrepo = subrepo[1:] + "."
	}
	dir := match[3]
	if len(dir) > 0 {
		dir = dir[1:] + "/"
	}

	// Scrape the HTML project page to find the VCS.
	p, err := httpGet(c, "http://code.google.com/p/"+projectName+"/source/checkout")
	if err != nil {
		return nil, err
	}

	var vcs string
	if m := googleRepoRe.FindSubmatch(p); m != nil {
		vcs = string(m[1])
	} else {
		return nil, doc.ErrPackageNotFound
	}

	// Scrape the repo browser to find indvidual Go files.
	p, err = httpGet(c, "http://"+subrepo+projectName+".googlecode.com/"+vcs+"/"+dir)
	if err != nil {
		return nil, err
	}

	var files []doc.Source
	query := ""
	if subrepo != "" {
		query = "?repo=" + subrepo[:len(subrepo)-1]
	}
	for _, m := range googleFilePattern.FindAllSubmatch(p, -1) {
		fname := string(m[1])
		if doc.IsGoFile(fname) {
			files = append(files, doc.Source{
				fname,
				"http://code.google.com/p/" + projectName + "/source/browse/" + dir + fname + query,
				newAsyncReader(c, "http://"+subrepo+projectName+".googlecode.com/"+vcs+"/"+dir+fname, nil)})
		}
	}

	pdoc, err := doc.Build(importPath, "#%d", files)
	if err != nil {
		return nil, err
	}

	pdoc.ProjectName = projectName
	pdoc.ProjectURL = "http://code.google.com/p/" + projectName + "/"
	return pdoc, nil
}
