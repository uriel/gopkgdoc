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
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
)

// service represents a source code control service.
type service struct {
	pattern *regexp.Regexp
	getDoc  func(*http.Client, []string, string) (*Package, error)
	prefix  string
}

// services is the list of source code control services handled by gopkgdoc.
var services = []*service{
	&service{githubPattern, getGithubDoc, "github.com/"},
	&service{googlePattern, getGoogleDoc, "code.google.com/"},
	&service{bitbucketPattern, getBitbucketDoc, "bitbucket.org/"},
	&service{launchpadPattern, getLaunchpadDoc, "launchpad.net/"},
}

func Get(client *http.Client, importPath string, etag string) (*Package, error) {
	if StandardPackages[importPath] {
		return getStandardDoc(client, importPath, etag)
	}
	for _, s := range services {
		if !strings.HasPrefix(importPath, s.prefix) {
			continue
		}
		m := s.pattern.FindStringSubmatch(importPath)
		if m == nil && s.prefix != "" {
			// Import path is bad if prefix matches and regexp does not.
			return nil, ErrPackageNotFound
		}
		log.Println("!!!!!!!!")
		return s.getDoc(client, m, etag)
	}
	log.Println("XXXXXXX!")
	return nil, ErrPackageNotFound
}

var (
	ErrPackageNotFound    = errors.New("package not found")
	ErrPackageNotModified = errors.New("package not modified")
)

// IsSupportedService returns true if the source code control service for
// import path is supported by this package.
func IsSupportedService(importPath string) bool {
	if StandardPackages[importPath] {
		return true
	}
	for _, s := range services {
		if strings.HasPrefix(importPath, s.prefix) {
			return true
		}
	}
	return false
}
