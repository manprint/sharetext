package handlers

import (
	"testing"
	"time"
)

func TestDownloadContentType(t *testing.T) {
	for in, want := range map[string]string{
		"text/html":                "application/octet-stream",
		"Text/HTML; charset=utf-8": "application/octet-stream",
		"image/svg+xml":            "application/octet-stream",
		"bad;;;":                   "application/octet-stream",
		"image/png":                "image/png",
		"text/plain":               "text/plain",
	} {
		if got := downloadContentType(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestAuthLockout(t *testing.T) {
	l := newAuthLockout(3, time.Minute)
	for i := 0; i < 2; i++ {
		l.fail("1.1.1.1")
	}
	if l.blocked("1.1.1.1") != 0 {
		t.Fatal("locked too early")
	}
	l.fail("1.1.1.1")
	if l.blocked("1.1.1.1") == 0 {
		t.Fatal("not locked")
	}
	if l.blocked("2.2.2.2") != 0 {
		t.Fatal("other ip locked")
	}
	l.reset("1.1.1.1")
	if l.blocked("1.1.1.1") != 0 {
		t.Fatal("reset failed")
	}
	if newAuthLockout(0, 0).blocked("x") != 0 {
		t.Fatal("disabled lockout blocks")
	}
}
