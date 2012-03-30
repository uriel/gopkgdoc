package doc

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
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

// fetchFiles fetches the source files specified by the rawURL field in parallel..
func fetchFiles(client *http.Client, files []*source, header http.Header) error {
	type result struct {
		i   int
		b   []byte
		err error
	}
	ch := make(chan result, len(files))
	for i := range files {
		go func(i int, url string) {
			b, err := httpGet(client, url, header, notFoundErr)
			ch <- result{i, b, err}
		}(i, files[i].rawURL)
	}
	for _ = range files {
		r := <-ch
		if r.err != nil {
			return r.err
		}
		files[r.i].data = r.b
	}
	return nil
}

const (
	// Don't treat 404 response as an error.
	notFoundOK = iota
	// Treat 404 response as an error and return ErrPackageNotFound
	notFoundNotFound
	// Treat 404 response as an error and return generic HTTP error.
	notFoundErr
)

// httpGet gets the resource at url using the given headers. 
func httpGet(client *http.Client, url string, header http.Header, notFound int) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range header {
		req.Header[k] = vs
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, GetError{req.URL.Host, err}
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == 404:
		switch notFound {
		case notFoundOK:
			// ok
		case notFoundErr:
			return nil, GetError{req.URL.Host, fmt.Errorf("get %s -> %d", url, resp.StatusCode)}
		case notFoundNotFound:
			return nil, ErrPackageNotFound
		default:
			panic("unexpected")
		}
	case resp.StatusCode != 200:
		return nil, GetError{req.URL.Host, fmt.Errorf("get %s -> %d", url, resp.StatusCode)}
	}

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, GetError{req.URL.Host, err}
	}
	return b, nil
}

func hashBytes(p []byte) string {
	h := md5.New()
	h.Write(p)
	return hex.EncodeToString(h.Sum(nil))
}
