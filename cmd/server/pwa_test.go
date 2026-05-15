package main

import "testing"

func TestSessionLaunchPath(t *testing.T) {
	if got := sessionLaunchPath("tmpfab-abc123"); got != "/launch/tmpfab-abc123" {
		t.Fatalf("unexpected launch path: %q", got)
	}
}

func TestSessionManifestPath(t *testing.T) {
	if got := sessionManifestPath("tmpfab-abc123"); got != "/manifest/session/tmpfab-abc123.webmanifest" {
		t.Fatalf("unexpected manifest path: %q", got)
	}
}

func TestManifestShortName(t *testing.T) {
	if got := manifestShortName("short-name"); got != "short-name" {
		t.Fatalf("unexpected short short_name: %q", got)
	}
	if got := manifestShortName("tmpfab-3dEW32pMePDQbHxB"); got != "tmpfab-3dEW3" {
		t.Fatalf("unexpected truncated short_name: %q", got)
	}
}

func TestSessionManifest(t *testing.T) {
	m := sessionManifest("tmpfab-3dEW32pMePDQbHxB")
	if m.ID != "/launch/tmpfab-3dEW32pMePDQbHxB" {
		t.Fatalf("unexpected id: %q", m.ID)
	}
	if m.StartURL != "/launch/tmpfab-3dEW32pMePDQbHxB" {
		t.Fatalf("unexpected start_url: %q", m.StartURL)
	}
	if m.Scope != "/" {
		t.Fatalf("unexpected scope: %q", m.Scope)
	}
	if m.ShortName != "tmpfab-3dEW3" {
		t.Fatalf("unexpected short_name: %q", m.ShortName)
	}
	if len(m.Icons) != 4 {
		t.Fatalf("expected icons copied from default manifest, got %d", len(m.Icons))
	}
}
