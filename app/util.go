package app

import (
	"appengine"
	"appengine/memcache"
	"appengine/urlfetch"
	"bytes"
	"fmt"
	"gob"
	"http"
	"io"
	"io/ioutil"
	"os"
)

func init() {
	gob.Register(make(map[string]interface{}))
	gob.Register(make([]map[string]interface{}, 0))
}

func cacheGet(c appengine.Context, key string, value interface{}) os.Error {
	item, err := memcache.Get(c, key)
	if err != nil {
		return err
	}
	return gob.NewDecoder(bytes.NewBuffer(item.Value)).Decode(value)
}

func cacheSet(c appengine.Context, key string, value interface{}, expiration int32) os.Error {
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(value)
	if err != nil {
		return err
	}
	return memcache.Set(c, &memcache.Item{Key: key, Expiration: expiration, Value: buf.Bytes()})
}

var errReading = os.NewError("urlReader: reading")

type urlReader struct {
	buf     bytes.Buffer
	errChan chan os.Error
	err     os.Error
}

func (ur *urlReader) Read(b []byte) (int, os.Error) {
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
	ur := &urlReader{err: errReading, errChan: make(chan os.Error, 1)}
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
// errPackageNotFound is returned.
func httpGet(c appengine.Context, url string) ([]byte, os.Error) {
	resp, err := urlfetch.Client(c).Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 404:
		return nil, errPackageNotFound
	case resp.StatusCode != 200:
		return nil, fmt.Errorf("get %s -> %d", url, resp.StatusCode)
	}
	return ioutil.ReadAll(resp.Body)
}
