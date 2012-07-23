# GoPkgDoc

This project is the source for http://go.pkgdoc.org/

## License

[Apache License, Version 2.0](http://www.apache.org/licenses/LICENSE-2.0.html).

## Contents

* app/ App Engine application
    * frontend/ Application frontend.
    * backend/ Applicaton backend plus code shared with frontend.
* doc/ Fetches documentation from vcs, symlinked to app/github.com/garyburd/gopkgdoc/doc
* index/ In memory index, symlinked to app/github.com/garyburd/gopkgdoc/index

## URL path structure

The top directory is a single character to avoid clashes with standard library packages.

* /-/.\* - User visible pages (about, index, and so on).
* /b/.\* - Backend handlers.
* /a/.\* - APIs.

