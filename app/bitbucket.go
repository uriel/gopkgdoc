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
	"json"
	"os"
	"regexp"
)

var bitbucketPattern = regexp.MustCompile(`^bitbucket\.org/([a-z0-9A-Z_.\-]+)/([a-z0-9A-Z_.\-]+)(/[a-z0-9A-Z_.\-/]*)?$`)

func getBitbucketIndexTokens(match []string) []string {
	return []string{"bitbucket.org/" + match[1] + "/" + match[2]}
}

func getBitbucketDoc(c appengine.Context, match []string) (*doc.Package, os.Error) {

	importPath := match[0]
	userName := match[1]
	repoName := match[2]

	// Normalize dir to "" or string with trailing '/'.
	dir := match[3]
	if len(dir) > 0 {
		dir = dir[1:] + "/"
	}

	p, err := httpGet(c, "https://api.bitbucket.org/1.0/repositories/"+userName+"/"+repoName+"/src/tip/"+dir)
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

	var files []doc.Source
	for _, f := range directory.Files {
		if doc.UseFile(f.Path) {
			files = append(files, doc.Source{
				"https://bitbucket.org/" + userName + "/" + repoName + "/src/tip/" + f.Path,
				newAsyncReader(c, "https://api.bitbucket.org/1.0/repositories/"+userName+"/"+repoName+"/raw/tip/"+f.Path, nil)})
		}
	}

	pdoc, err := doc.Build(importPath, "#cl-%d", files)
	if err != nil {
		return nil, err
	}

	pdoc.ProjectName = repoName
	pdoc.ProjectURL = "https://bitbucket.org/" + userName + "/" + repoName + "/"
	return pdoc, nil
}
