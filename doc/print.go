// Copyright 2012 Gary Burd
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

// +build ignore

// Command print fetches and prints package documentation.
//
// Usage: go run print.go importPath
package main

import (
	"fmt"
	"github.com/garyburd/gopkgdoc/doc"
	"github.com/kr/pretty"
	"log"
	"net/http"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("Usage: go run print.go importPath")
	}
	dpkg, err := doc.Get(http.DefaultClient, os.Args[1], "")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%# v", pretty.Formatter(dpkg))
}
