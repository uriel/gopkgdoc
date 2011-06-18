package app

import (
	"appengine"
	"appengine/datastore"
	"appengine/taskqueue"
	"appengine/urlfetch"
	"archive/zip"
	"http"
	"io"
	"io/ioutil"
	"json"
	"os"
	"path"
	"strconv"
	"strings"
)

func httpGet(client *http.Client, url string) ([]byte, os.Error) {
	resp, _, err := client.Get(url)
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
	c := appengine.NewContext(r)
	p, err := ioutil.ReadAll(r.Body)
	if err != nil {
		c.Logf("error reading req body, %v", err)
		return
	}
	m, err := http.ParseQuery(string(p))
	if err != nil {
		c.Logf("error parsing query, %v", err)
		return
	}
	if len(m["payload"]) == 0 {
		c.Logf("payload missing")
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
		c.Logf("error decoding hook, %v", err)
		return
	}
	if n.Ref != "refs/heads/master" {
		c.Logf("ignoring ref %s", n.Ref)
		return
	}
	url, err := http.ParseURL(n.Repository.Url)
	if err != nil {
		c.Logf("error parsing url %s, %v", n.Repository.Url, err)
		return
	}
	taskqueue.Add(
		appengine.NewContext(r),
		taskqueue.NewPOSTTask("/admin/task/github", map[string][]string{"userRepo": []string{url.Path[1:]}}),
		"")
}

func githubTask(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	userRepo := r.FormValue("userRepo")
	client := urlfetch.Client(c)
	url := "http://github.com/" + userRepo + "/zipball/master"
	p, err := httpGet(client, "http://github.com/"+userRepo+"/zipball/master")
	if err != nil {
		c.Logf("failed to fetch %s, %v", url, err)
		return
	}
	zr, err := zip.NewReader(sliceReaderAt(p), int64(len(p)))
	if err != nil {
		c.Logf("failed to open zip for %s, %v", userRepo, err)
		return
	}
	pkgs := make(map[string][]file)
	for _, f := range zr.File {
		dir, fname := path.Split(f.Name)
		if !strings.HasSuffix(fname, ".go") ||
			strings.HasSuffix(fname, "_test.go") ||
			fname == "deprecated.go" {
			continue
		}
		rc, _ := f.Open()
		defer rc.Close()
		pkgs[dir] = append(pkgs[dir], file{Name: fname, Content: rc})
	}
	for dir, files := range pkgs {
		importpath := "github.com/" + userRepo
		fileURLFmt := "http://github.com/" + userRepo + "/blob/master"
		if i := strings.Index(dir, "/"); i >= 0 {
			d := dir[i : len(dir)-1]
			importpath += d
			fileURLFmt += d
		}
		fileURLFmt += "/%s"
		srcURLFmt := fileURLFmt + "#L%d"
		projectURL := "http://github.com/" + userRepo
		projectName := path.Base(userRepo)
		doc, err := createPackageDoc(importpath, fileURLFmt, srcURLFmt, projectURL, projectName, files)
		if err != nil {
			if err != errPackageNotFound {
				c.Logf("failure generating json for %s, %v", importpath, err)
			}
			continue
		}
		_, err = datastore.Put(c, datastore.NewKey("PackageDoc", importpath, 0, nil), doc)
		if err != nil {
			c.Logf("failure puting doc %s, %v", importpath, err)
			continue
		}
	}
}
