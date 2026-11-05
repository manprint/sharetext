package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestRunHealthcheck(t *testing.T) {
	code := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	if runHealthcheck(u.Port()) != 0 {
		t.Fatal("want healthy")
	}
	code = http.StatusServiceUnavailable
	if runHealthcheck(u.Port()) == 0 {
		t.Fatal("want unhealthy")
	}
}
