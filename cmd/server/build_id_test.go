package main

import (
	"embed"
	"strings"
	"testing"
)

// We can't easily synthesise an embed.FS at runtime, so the determinism /
// sensitivity tests below use the live embedded `assets` from main.go. The
// "two reads in a row produce the same hash" property is enough to pin the
// invariant we care about (deterministic over identical inputs); a
// "different content → different hash" check is covered separately via an
// in-tree test fixture that walks the same FS twice and asserts the result
// matches a known-shape.

//go:embed all:build_id_fixture
var buildIDFixture embed.FS

func TestComputeBuildIDIsDeterministicOnTheRealAssets(t *testing.T) {
	id1, err := computeBuildID(assets)
	if err != nil {
		t.Fatalf("computeBuildID: %v", err)
	}
	id2, err := computeBuildID(assets)
	if err != nil {
		t.Fatalf("computeBuildID (second call): %v", err)
	}
	if id1 != id2 {
		t.Fatalf("non-deterministic build id: %q vs %q", id1, id2)
	}
	if len(id1) != 16 {
		t.Fatalf("expected 16-char build id, got %d (%q)", len(id1), id1)
	}
	if strings.ContainsAny(id1, " \t\n") {
		t.Fatalf("build id contains whitespace: %q", id1)
	}
}

func TestComputeBuildIDChangesWithContent(t *testing.T) {
	// Same FS but conceptually different: we hash a small fixture with the
	// helper, then assert it differs from the real-asset hash. They both
	// originate from the same embed mechanism but different content, so the
	// digests MUST differ. This is the property a deploy-time cache key
	// relies on.
	fixID, err := computeBuildID(buildIDFixture)
	if err != nil {
		t.Fatalf("computeBuildID fixture: %v", err)
	}
	realID, err := computeBuildID(assets)
	if err != nil {
		t.Fatalf("computeBuildID assets: %v", err)
	}
	if fixID == realID {
		t.Fatalf("fixture and real assets produced the same id %q — hash is not content-sensitive", fixID)
	}
}
