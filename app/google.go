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
	"os"
	"regexp"
)

var googlePattern = regexp.MustCompile(`^code\.google\.com/p/([a-z0-9\-]+(\.[a-z0-9\-]+)?)(/[a-z0-9A-Z_.\-/]+)?$`)
var googleRepoRe = regexp.MustCompile(`id="checkoutcmd">(hg|git|svn)`)
var googleFilePattern = regexp.MustCompile(`<li><a href="([^"/]+)"`)

func getGoogleIndexTokens(match []string) []string {
	return []string{"code.google.com/p/" + match[1]}
}

func getGoogleDoc(c appengine.Context, match []string) (*packageDoc, os.Error) {

	importPath := match[0]
	projectName := match[1] + match[2] // TODO: handle sub repo
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
		return nil, errPackageNotFound
	}

	// Scrape the repo browser to find indvidual Go files.
	p, err = httpGet(c, "http://"+projectName+".googlecode.com/"+vcs+"/"+dir)
	if err != nil {
		return nil, err
	}

	var files []file
	for _, m := range googleFilePattern.FindAllSubmatch(p, -1) {
		fname := string(m[1])
		if includeFileInDoc(fname) {
			files = append(files, file{
				"http://code.google.com/p/" + projectName + "/source/browse/" + dir + fname,
				newAsyncReader(c, "http://"+projectName+".googlecode.com/"+vcs+"/"+dir+fname, nil)})
		}
	}

	doc, err := createPackageDoc(importPath, "#%d", files)
	if err != nil {
		return nil, err
	}

	doc.ProjectName = projectName
	doc.ProjectURL = "http://code.google.com/p/" + projectName + "/"
	return doc, nil
}
