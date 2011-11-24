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
	"regexp"
	"sync"
)

var (
	fetchStandardPackagesOnce sync.Once
	standardPackages          = make(map[string]bool)
)

// isStandardPackage returns true if importPath is a standard package on
// golang.org.
func isStandardPackage(c appengine.Context, importPath string) bool {
	fetchStandardPackagesOnce.Do(func() {
		p, err := httpGet(c, "http://golang.org/pkg/")
		if err != nil {
			c.Errorf("Error getting standard packages, %v", err)
			return
		}
		// Scrape the HTML.
		pattern := regexp.MustCompile(`<td align="left" colspan="[1-9]"><a href="([^"]+)`)
		for _, m := range pattern.FindAllSubmatch(p, -1) {
			standardPackages[string(m[1])] = true
		}
	})
	return standardPackages[importPath]
}
