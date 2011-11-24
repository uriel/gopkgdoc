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

var googlePattern = regexp.MustCompile(`^([a-z0-9\-]+)\.googlecode\.com/(hg|git|svn)(/[a-z0-9A-Z_.\-/]*)?$`)
var googleFilePattern = regexp.MustCompile(`<li><a href="([^"/]+)"`)

func getGoogleIndexTokens(match []string) []string {
	return []string{match[1] + ".googlecode.com"}
}

func getGoogleDoc(c appengine.Context, match []string) (*packageDoc, os.Error) {

	importPath := match[0]
	projectName := match[1]
	dir := match[3]
	if len(dir) > 0 {
		dir = dir[1:] + "/"
	}

	p, err := httpGet(c, "http://"+importPath+"/")
	if err != nil {
		return nil, err
	}

	// Scrape the HTML to find individual Go files.
	var files []file
	for _, m := range googleFilePattern.FindAllSubmatch(p, -1) {
		fname := string(m[1])
		if includeFileInDoc(fname) {
			files = append(files, file{
				"http://code.google.com/p/" + projectName + "/source/browse/" + dir + fname,
				newAsyncReader(c, "http://"+importPath+"/"+fname, nil)})
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
