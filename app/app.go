package app

import (
	"appengine"
	"appengine/datastore"
	"bytes"
	"go/doc"
	"http"
	"io"
	"json"
	"os"
	"template"
	"time"
)

func commentFmt(w io.Writer, format string, x ...interface{}) {
	doc.ToHTML(w, []byte(x[0].(string)), nil)
}

var homeTemplate = template.MustParseFile("template/home.html", template.FormatterMap{
	"": template.HTMLFormatter,
})

var pkgTemplate = template.MustParseFile("template/pkg.html", template.FormatterMap{
	"":        template.HTMLFormatter,
	"comment": commentFmt,
})

func internalError(w http.ResponseWriter, c appengine.Context, err os.Error) {
	c.Logf("Error %s", err.String())
	http.Error(w, "Internal Error", http.StatusInternalServerError)
}

func servePkg(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	key := datastore.NewKey("PackageDoc", r.URL.Path[len("/key/"):], 0, nil)
	var doc PackageDoc
	err := datastore.Get(appengine.NewContext(r), key, &doc)
	if err == datastore.ErrNoSuchEntity {
		http.NotFound(w, r)
		return
	} else if err != nil {
		internalError(w, c, err)
		return
	}

	if r.FormValue("format") == "json" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		var buf bytes.Buffer
		json.Indent(&buf, doc.Data, "", "  ")
		w.Write(buf.Bytes())
		return
	}

	var m map[string]interface{}
	if err := json.Unmarshal(doc.Data, &m); err != nil {
		c.Logf("error unmarshalling json", err)
	}

	m["importPath"] = doc.ImportPath
	m["packageName"] = doc.PackageName
	m["projectURL"] = doc.ProjectURL
	m["projectName"] = doc.ProjectName
	m["updated"] = time.SecondsToLocalTime(int64(doc.Updated) / 1e6).String()
	if err := pkgTemplate.Execute(w, m); err != nil {
		c.Logf("error rendering pkg template:", err)
	}
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	c := appengine.NewContext(r)
	q := datastore.NewQuery("PackageDoc").KeysOnly()
	keys, err := q.GetAll(c, nil)
	if err != nil {
		internalError(w, c, err)
		return
	}
	if err := homeTemplate.Execute(w, keys); err != nil {
		c.Logf("error rendering home template:", err)
	}
}

func init() {
	http.HandleFunc("/", serveHome)
	http.HandleFunc("/pkg/", servePkg)
	http.HandleFunc("/hook/github", githubHook)
	http.HandleFunc("/admin/task/github", githubTask)
}
