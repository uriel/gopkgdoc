package doc

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"path"
	"strings"
)

// isDocFile returns true if a file with the path p should be included in the
// documentation.
func isDocFile(p string) bool {
	_, f := path.Split(p)
	return strings.HasSuffix(f, ".go") &&
		len(f) > 0 &&
		f[0] != '_' &&
		f[0] != '.' &&
		f != "deprecated.go"
}

type GetError struct {
	Host string
	err  error
}

func (e GetError) Error() string {
	return e.err.Error()
}

// getFiles replaces the URL string in the source content field with the
// resource at that URL.
func getFiles(client *http.Client, files []source, header http.Header) error {
	type result struct {
		i   int
		b   []byte
		err error
	}
	ch := make(chan result, len(files))
	for i := range files {
		go func(i int, url string) {
			b, err := httpGet(client, url, header, false)
			ch <- result{i, b, err}
		}(i, files[i].Content.(string))
	}
	for _ = range files {
		r := <-ch
		if r.err != nil {
			return r.err
		}
		files[r.i].Content = r.b
	}
	return nil
}

// httpGet gets the resource at url using the given headers. If notFound is
// true, then ErrPackageNotFound is for resource not found errors (404).
func httpGet(client *http.Client, url string, header http.Header, notFound bool) ([]byte, error) {
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
	case resp.StatusCode == 404 && notFound:
		return nil, ErrPackageNotFound
	case resp.StatusCode != 200:
		return nil, GetError{req.URL.Host, fmt.Errorf("get %s -> %d", url, resp.StatusCode)}
	}
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, GetError{req.URL.Host, err}
	}
	return b, nil
}
