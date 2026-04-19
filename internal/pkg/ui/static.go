package ui

import (
	"io/fs"
	"net/http"
	"strings"
)

// StaticHandler serves files from fsys, stripping prefix, and returns 404 for
// directory paths so embedded asset trees don't leak via index listings.
func StaticHandler(prefix string, fsys fs.FS) http.Handler {
	fileSrv := http.StripPrefix(prefix, http.FileServer(http.FS(noDirFS{fsys})))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		fileSrv.ServeHTTP(w, r)
	})
}

// noDirFS wraps fs.FS and hides directories from http.FileServer. Reads on a
// directory return os.ErrNotExist so the default listing page is never rendered.
type noDirFS struct{ fsys fs.FS }

func (n noDirFS) Open(name string) (fs.File, error) {
	f, err := n.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.IsDir() {
		f.Close()
		return nil, fs.ErrNotExist
	}
	return f, nil
}
