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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"doc"
	"io"
	"io/ioutil"
	"path"
	"regexp"
	"strings"
)

var launchpadPattern = regexp.MustCompile(`^launchpad\.net/(([a-z0-9A-Z_.\-]+(/[a-z0-9A-Z_.\-]+)?|~[a-z0-9A-Z_.\-]+/(\+junk|[a-z0-9A-Z_.\-]+)/[a-z0-9A-Z_.\-]+))(/[a-z0-9A-Z_.\-/]+)?$`)

func getLaunchpadIndexTokens(match []string) []string {
	return []string{"launchpad.net/" + match[1]}
}

func getLaunchpadDoc(c appengine.Context, match []string) (*doc.Package, error) {

	importPath := match[0]
	repo := match[1]
	dir := match[5]
	if len(dir) > 0 {
		dir = dir[1:] + "/"
	}
	projectName := match[2]
	if projectName == "" {
		projectName = match[4]
	}
	projectURL := "https://launchpad.net/" + match[1]

	p, err := httpGet(c, "http://bazaar.launchpad.net/+branch/"+repo+"/tarball")
	if err != nil {
		return nil, err
	}

	gzr, err := gzip.NewReader(bytes.NewBuffer(p))
	if err != nil {
		return nil, err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	prefix := "+branch/" + repo + "/"
	var files []doc.Source
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(hdr.Name, prefix) {
			continue
		}
		d, f := path.Split(hdr.Name[len(prefix):])
		if d != dir {
			continue
		}
		if !doc.IsGoFile(f) {
			continue
		}
		b, err := ioutil.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		files = append(files, doc.Source{
			f,
			"http://bazaar.launchpad.net/+branch/" + repo + "/view/head:/" + hdr.Name[len(prefix):],
			b})
	}

	pdoc, err := doc.Build(importPath, "#L%d", files)
	if err != nil {
		return nil, err
	}

	pdoc.ProjectName = projectName
	pdoc.ProjectURL = projectURL
	return pdoc, nil
}
