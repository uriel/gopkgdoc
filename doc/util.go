package doc

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"path"
	"strings"
)

// isDocFile returns true if a file with the path p should be included in the
// documentation.
func isDocFile(p string) bool {
	_, n := path.Split(p)
	return strings.HasSuffix(n, ".go") && len(n) > 0 && n[0] != '_' && n[0] != '.'
}

type GetError struct {
	Host string
	err  error
}

func (e GetError) Error() string {
	return e.err.Error()
}

// fetchFiles fetches the source files specified by the rawURL field in parallel.
func fetchFiles(client *http.Client, files []*source, header http.Header) error {
	ch := make(chan error, len(files))
	for i := range files {
		go func(i int) {
			req, err := http.NewRequest("GET", files[i].rawURL, nil)
			if err != nil {
				ch <- err
				return
			}
			for k, vs := range header {
				req.Header[k] = vs
			}
			resp, err := client.Do(req)
			if err != nil {
				ch <- GetError{req.URL.Host, err}
				return
			}
			if resp.StatusCode != 200 {
				ch <- GetError{req.URL.Host, fmt.Errorf("get %s -> %d", req.URL, resp.StatusCode)}
				return
			}
			files[i].data, err = ioutil.ReadAll(resp.Body)
			if err != nil {
				ch <- GetError{req.URL.Host, err}
				return
			}
			ch <- nil
		}(i)
	}
	for _ = range files {
		if err := <-ch; err != nil {
			return err
		}
	}
	return nil
}

// httpGet gets the specified resource. ErrPackageNotFound is returned if the
// server responds with status 404.
func httpGet(client *http.Client, url string) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, GetError{req.URL.Host, err}
	}
	if resp.StatusCode == 200 {
		return resp.Body, nil
	}
	resp.Body.Close()
	if resp.StatusCode == 404 {
		err = ErrPackageNotFound
	} else {
		err = GetError{req.URL.Host, fmt.Errorf("get %s -> %d", url, resp.StatusCode)}
	}
	return nil, err
}

// httpGet gets the specified resource. ErrPackageNotFound is returned if the
// server responds with status 404.
func httpGetBytes(client *http.Client, url string) ([]byte, error) {
	rc, err := httpGet(client, url)
	if err != nil {
		return nil, err
	}
	p, err := ioutil.ReadAll(rc)
	rc.Close()
	return p, err
}

// httpGet gets the specified resource. ErrPackageNotFound is returned if the
// server responds with status 404. ErrPackageNotModified is returned if the
// hash of the resource equals savedEtag.
func httpGetBytesCompare(client *http.Client, url string, savedEtag string) ([]byte, string, error) {
	p, err := httpGetBytes(client, url)
	if err != nil {
		return nil, "", err
	}
	h := md5.New()
	h.Write(p)
	etag := hex.EncodeToString(h.Sum(nil))
	if savedEtag == etag {
		err = ErrPackageNotModified
	}
	return p, etag, err
}
