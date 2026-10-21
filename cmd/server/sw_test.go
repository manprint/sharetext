package main

import (
	"io/fs"
	"strings"
	"testing"
)

// The offline shell loads ES modules from the service-worker cache. A module
// missing from PRECACHE_URLS makes every import of it fail offline, which
// takes the whole page down with it.
func TestServiceWorkerPrecachesEveryModule(t *testing.T) {
	// admin.js is never cached: /admin bypasses the service worker.
	skip := map[string]bool{"admin.js": true}
	entries, err := fs.ReadDir(assets, "static")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || skip[name] || !(strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".css")) {
			continue
		}
		checked++
		if !strings.Contains(swSource, `"/static/`+name+`"`) {
			t.Errorf("sw.js.tmpl PRECACHE_URLS is missing /static/%s", name)
		}
	}
	if checked == 0 {
		t.Fatal("no static modules found; did the embed path change?")
	}
}
