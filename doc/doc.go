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
	"net/http"
	"regexp"
	"strings"
)

// service represents a source code control service.
type service struct {
	pattern     *regexp.Regexp
	newPathInfo func([]string) PathInfo
	prefix      string
}

// services is the list of source code control services handled by gopkgdoc.
var services = []*service{
	&service{githubPattern, newGithubPathInfo, "github.com/"},
	&service{googlePattern, newGooglePathInfo, "code.google.com/"},
	&service{bitbucketPattern, newBitbucketPathInfo, "bitbucket.org/"},
	&service{launchpadPattern, newLaunchpadPathInfo, "launchpad.net/"},
}

// Path represents an import path.
type PathInfo interface {
	ImportPath() string
	ProjectPrefix() string
	ProjectName() string
	ProjectURL() string
	Package(*http.Client) (*Package, error)
}

// NewPath returns information about a path or nil the path is not valid. 
func NewPathInfo(importPath string) PathInfo {
	for _, s := range services {
		if m := s.pattern.FindStringSubmatch(importPath); m != nil {
			return s.newPathInfo(m)
		}
	}
	return nil
}

// Package not found.
var ErrPackageNotFound = errors.New("package not found")

// IsSupportedService returns true if the source code control service for
// import path is supported by this package.
func IsSupportedService(importPath string) bool {
	for _, s := range services {
		if strings.HasPrefix(importPath, s.prefix) {
			return true
		}
	}
	return false
}
