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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"io/ioutil"
	"net/http"
	"path"
	"regexp"
	"strings"
)

var launchpadPattern = regexp.MustCompile(`^launchpad\.net/(([a-z0-9A-Z_.\-]+)(/[a-z0-9A-Z_.\-]+)?|~[a-z0-9A-Z_.\-]+/(\+junk|[a-z0-9A-Z_.\-]+)/[a-z0-9A-Z_.\-]+)(/[a-z0-9A-Z_.\-/]+)*$`)

func getLaunchpadDoc(client *http.Client, m []string, etag string) (*Package, error) {

	if m[2] != "" && m[3] != "" {
		_, err := httpGet(client, "https://code.launchpad.net/"+m[2]+m[3]+"/.bzr/branch-format", nil, notFoundNotFound)
		switch err {
		case ErrPackageNotFound:
			// The structure of the import path is is launchpad.net/{project}/{dir}.
			m[1] = m[2]
			m[5] = m[3] + m[5]
		case nil:
			// The structure of the import path is launchpad.net/{project}/{series}/{dir}. 
			// No fix up is needed.
		default:
			return nil, err
		}
	}

	importPath := m[0]
	projectName := m[2]
	if projectName == "" {
		projectName = m[1]
	}
	projectPrefix := "launchpad.net/" + projectName
	projectURL := "https://launchpad.net/" + projectName + "/"

	repo := m[1]
	dir := m[5]
	if len(dir) > 0 {
		dir = dir[1:] + "/"
	}

	p, err := httpGet(client, "http://bazaar.launchpad.net/+branch/"+repo+"/tarball", nil, notFoundNotFound)
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
	var files []*source
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
		if !isDocFile(f) {
			continue
		}
		if d == dir {
			b, err := ioutil.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			files = append(files, &source{
				name:      f,
				browseURL: "http://bazaar.launchpad.net/+branch/" + repo + "/view/head:/" + hdr.Name[len(prefix):],
				data:      b})
		}
	}

	return buildDoc(importPath, projectPrefix, projectName, projectURL, etag, "#L%d", files)
}
