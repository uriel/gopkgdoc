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
	"bytes"
	"crypto/md5"
	"doc"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"text/template"
	"time"
)

// relativeTime formats the time t in nanoseconds as a human readable relative
// time.
func relativeTime(t time.Time) string {
	d := time.Now().Sub(t)
	switch {
	case d < time.Second:
		return "just now"
	case d < 2*time.Second:
		return "one second ago"
	case d < time.Minute:
		return fmt.Sprintf("%d seconds ago", d/time.Second)
	case d < 2*time.Minute:
		return "one minute ago"
	}
	return fmt.Sprintf("%d minutes ago", d/time.Minute)
}

// commentFmt formats a source code control comment as HTML.
func commentFmt(v string) string {
	var buf bytes.Buffer
	doc.ToHTML(&buf, v)
	return buf.String()
}

// declFrmt formats a Decl as HTML.
func declFmt(decl doc.Decl) string {
	var buf bytes.Buffer
	last := 0
	t := []byte(decl.Text)
	for _, a := range decl.Annotations {
		p := a.ImportPath
		var link bool
		switch {
		case standardPackages[p]:
			p = standardPackagePath + p
			link = true
		case p == "":
			link = true
		default:
			if m := oldGooglePattern.FindStringSubmatch(p); m != nil {
				p = newGooglePath(m)
			}
			for _, s := range services {
				if strings.HasPrefix(p, s.prefix) {
					link = true
					break
				}
			}
			p = "/pkg/" + p
		}
		if link {
			template.HTMLEscape(&buf, t[last:a.Pos])
			buf.WriteString(`<a href="`)
			template.HTMLEscape(&buf, []byte(p))
			buf.WriteByte('#')
			template.HTMLEscape(&buf, []byte(a.Name))
			buf.WriteString(`">`)
			template.HTMLEscape(&buf, t[a.Pos:a.End])
			buf.WriteString(`</a>`)
			last = a.End
		}
	}
	template.HTMLEscape(&buf, t[last:])
	return buf.String()
}

var (
	staticMutex sync.RWMutex
	staticHash  = make(map[string]string)
)

func staticURL(path string) string {
	staticMutex.RLock()
	h, ok := staticHash[path]
	staticMutex.RUnlock()

	if !ok {
		p, err := ioutil.ReadFile(path[1:])
		if err != nil {
			return path
		}

		m := md5.New()
		m.Write(p)
		h = hex.EncodeToString(m.Sum(nil))

		staticMutex.Lock()
		staticHash[path] = h
		staticMutex.Unlock()
	}
	return path + "?v=" + h
}

func executeTemplate(w http.ResponseWriter, name string, status int, data interface{}) error {
	s := templateSet
	if appengine.IsDevAppServer() {
		var err error
		s, err = parseTemplates()
		if err != nil {
			return err
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	return s.ExecuteTemplate(w, name, data)
}

var templateSet *template.Template

func parseTemplates() (*template.Template, error) {
	// TODO: Is there a better way to call ParseGlob with application specified
	// funcs? The dummy template thing is gross.
	set, err := template.New("__dummy__").Parse(`{{define "__dummy__"}}{{end}}`)
	if err != nil {
		return nil, err
	}
	set.Funcs(template.FuncMap{
		"comment":      commentFmt,
		"decl":         declFmt,
		"relativeTime": relativeTime,
		"staticURL":    staticURL,
		"equal":        reflect.DeepEqual,
	})
	return set.ParseGlob("template/*.html")
}

func init() {
	templateSet = template.Must(parseTemplates())
}
