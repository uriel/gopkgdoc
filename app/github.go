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
	"appengine/datastore"
	"appengine/delay"
	"appengine/urlfetch"
	"gob"
	"http"
	"io"
	"io/ioutil"
	"json"
	"os"
	"path"
	"strconv"
	"strings"
	"url"
)

type gitBlob struct {
	Name string
	Url  string
}

func init() {
	gob.Register([]gitBlob{})
}

func httpGet(client *http.Client, urlStr string) ([]byte, os.Error) {
	resp, err := client.Get(urlStr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		os.NewError("http status " + strconv.Itoa(resp.StatusCode))
	}
	var p []byte
	if resp.ContentLength >= 0 {
		p = make([]byte, int(resp.ContentLength))
		_, err := io.ReadFull(resp.Body, p)
		if err != nil {
			return nil, err
		}
	} else {
		var err os.Error
		p, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
	}
	return p, nil
}

type sliceReaderAt []byte

func (r sliceReaderAt) ReadAt(p []byte, off int64) (int, os.Error) {
	if int(off) >= len(r) || off < 0 {
		return 0, os.EINVAL
	}
	return copy(p, r[int(off):]), nil
}

func githubHook(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || r.Body == nil {
		http.Redirect(w, r, "/#info", 302)
		return
	}
	c := appengine.NewContext(r)
	p, err := ioutil.ReadAll(r.Body)
	if err != nil {
		c.Warningf("error reading req body, %v", err)
		return
	}
	m, err := url.ParseQuery(string(p))
	if err != nil {
		c.Warningf("error parsing query, %v", err)
		return
	}
	if len(m["payload"]) == 0 {
		c.Warningf("payload missing")
		return
	}
	var n struct {
		Ref        string
		Repository struct {
			Url string
		}
	}
	err = json.Unmarshal([]byte(m["payload"][0]), &n)
	if err != nil {
		c.Warningf("error decoding hook, %v", err)
		return
	}
	if n.Ref != "refs/heads/master" {
		c.Infof("ignoring ref %s", n.Ref)
		return
	}
	u, err := url.Parse(n.Repository.Url)
	if err != nil {
		c.Errorf("error parsing url %s, %v", n.Repository.Url, err)
		return
	}
	userRepo := u.Path[1:]
	findPackagesInRepoFunc.Call(c, userRepo)
}

var findPackagesInRepoFunc = delay.Func("github.repo", findPackagesInRepo)

func findPackagesInRepo(c appengine.Context, userRepo string) {
	client := urlfetch.Client(c)
	p, err := httpGet(client, "https://api.github.com/repos/"+userRepo+"/git/trees/master?recursive=1")
	if err != nil {
		c.Errorf("could not get repo %s, %v", userRepo, err)
		panic(err)
	}
	var repo struct {
		Tree []struct {
			Url  string
			Path string
			Type string
		}
	}
	err = json.Unmarshal(p, &repo)
	if err != nil {
		c.Errorf("could not unmarshal repo %s, %v", userRepo, err)
		return
	}
	pkgs := make(map[string][]gitBlob)
	for _, node := range repo.Tree {
		if node.Type != "blob" {
			continue
		}
		dir, name := path.Split(node.Path)
		if !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") ||
			name == "deprecated.go" {
			continue
		}
		pkgs[dir] = append(pkgs[dir], gitBlob{Name: name, Url: node.Url})
	}
	for dir, blobs := range pkgs {
		buildPackageDocFunc.Call(c, userRepo, dir, blobs)
	}
}

var buildPackageDocFunc = delay.Func("github.doc", buildPackageDoc)

func buildPackageDoc(c appengine.Context, userRepo string, dir string, blobs []gitBlob) {
	c.Infof("Starting build  for %s %s", userRepo, dir)
	defer cacheClear(c, "/")
	client := urlfetch.Client(c)
	var files []file
	for _, blob := range blobs {
		req, err := http.NewRequest("GET", blob.Url, nil)
		if err != nil {
			c.Errorf("error creating request for %s", blob.Url)
			return
		}
		req.Header.Set("Accept", "application/vnd.github-blob.raw")
		resp, err := client.Do(req)
		if err != nil {
			c.Errorf("Error doing %s, %v", blob.Url, err)
			panic(err)
		}
		if resp.StatusCode != 200 {
			c.Errorf("Error fetching %s, %d", blob.Url, resp.StatusCode)
			panic("bad status")
		}
		defer resp.Body.Close()
		files = append(files, file{Name: blob.Name, Content: resp.Body})
	}

	importpath := "github.com/" + userRepo
	fileURLFmt := "http://github.com/" + userRepo + "/blob/master"
	if dir != "" {
		d := dir[:len(dir)-1]
		importpath += "/" + d
		fileURLFmt += "/" + d
	}
	fileURLFmt += "/%s"
	srcURLFmt := fileURLFmt + "#L%d"
	projectURL := "http://github.com/" + userRepo
	projectName := path.Base(userRepo)
	doc, err := createPackageDoc(importpath, fileURLFmt, srcURLFmt, projectURL, projectName, files)
	if err != nil {
		if err == errPackageNotFound {
			c.Infof("failure generating json for %s, %v", importpath, err)
			err := datastore.Delete(c, datastore.NewKey(c, "PackageDoc", importpath, 0, nil))
			if err != nil {
				c.Infof("error clearing package %s, %v", importpath, err)
			}
		} else {
			c.Errorf("failure generating json for %s, %v", importpath, err)
		}
		return
	}
	_, err = datastore.Put(c, datastore.NewKey(c, "PackageDoc", importpath, 0, nil), doc)
	if err != nil {
		c.Errorf("failure puting doc %s, %v", importpath, err)
		panic(err)
	}
	c.Infof("Created doc for %s %s", userRepo, dir)
}
