package app

import (
	"appengine"
	"appengine/memcache"
	"appengine/urlfetch"
	"bytes"
	"doc"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"time"
)

func init() {
	gob.Register(make(map[string]interface{}))
	gob.Register(make([]map[string]interface{}, 0))
}

func cacheGet(c appengine.Context, key string, value interface{}) error {
	item, err := memcache.Get(c, key)
	if err != nil {
		return err
	}
	return gob.NewDecoder(bytes.NewBuffer(item.Value)).Decode(value)
}

func cacheSet(c appengine.Context, key string, value interface{}, expiration time.Duration) error {
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(value)
	if err != nil {
		return err
	}
	return memcache.Set(c, &memcache.Item{Key: key, Expiration: expiration, Value: buf.Bytes()})
}

var errReading = errors.New("urlReader: reading")

type urlReader struct {
	buf     bytes.Buffer
	errChan chan error
	err     error
}

func (ur *urlReader) Read(b []byte) (int, error) {
	if ur.err == errReading {
		ur.err = <-ur.errChan
	}
	if ur.err != nil {
		return 0, ur.err
	}
	return ur.buf.Read(b)
}

// newAsyncReader asynchronously reads the resource at url and returns a reader
// that will block waiting for the result.
func newAsyncReader(c appengine.Context, url string, header http.Header) io.Reader {
	ur := &urlReader{err: errReading, errChan: make(chan error, 1)}
	go func() {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			ur.errChan <- err
			return
		}
		for k, vs := range header {
			req.Header[k] = vs
		}
		resp, err := urlfetch.Client(c).Do(req)
		if err != nil {
			ur.errChan <- err
			return
		}
		if resp.StatusCode != 200 {
			ur.errChan <- fmt.Errorf("get %s -> %d", url, resp.StatusCode)
			return
		}
		defer resp.Body.Close()
		_, err = io.Copy(&ur.buf, resp.Body)
		ur.errChan <- err
	}()
	return ur
}

// httpGet gets the resource at url. If the resource is not found,
// doc.ErrPackageNotFound is returned.
func httpGet(c appengine.Context, url string) ([]byte, error) {
	resp, err := urlfetch.Client(c).Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 404:
		return nil, doc.ErrPackageNotFound
	case resp.StatusCode != 200:
		return nil, fmt.Errorf("get %s -> %d", url, resp.StatusCode)
	}
	return ioutil.ReadAll(resp.Body)
}
