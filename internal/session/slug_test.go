package session

import (
	"errors"
	"strings"
	"testing"
)

func TestNewSlugLength(t *testing.T) {
	s, err := NewSlug(16)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 16 {
		t.Fatalf("want len 16, got %d", len(s))
	}
}

func TestNewSlugDefault(t *testing.T) {
	s, err := NewSlug(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 16 {
		t.Fatalf("default len want 16, got %d", len(s))
	}
}

func TestNewSlugAlphabet(t *testing.T) {
	s, err := NewSlug(64)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range s {
		if !strings.ContainsRune(alphabet, r) {
			t.Fatalf("char %q not in alphabet", r)
		}
	}
}

func TestNewSlugUnique(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 1000; i++ {
		s, err := NewSlug(16)
		if err != nil {
			t.Fatal(err)
		}
		if _, dup := seen[s]; dup {
			t.Fatalf("dup slug %s", s)
		}
		seen[s] = struct{}{}
	}
}

func TestValidName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"alpha", true},
		{"alpha_beta", true},
		{"alpha-beta", true},
		{"team-42", true},
		{"_x", true},
		{"-x", true},
		{"1abc", true},
		{"a", true},
		{strings.Repeat("a", NameMaxLen), true},

		{"", false},
		{strings.Repeat("a", NameMaxLen+1), false},
		{"with space", false},
		{"with.dot", false},
		{"with/slash", false},
		{"with:colon", false},
		{"èaccent", false},
		{"emoji😀", false},
	}
	for _, c := range cases {
		got := ValidName(c.in)
		if got != c.want {
			t.Errorf("ValidName(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestComposeAnonymous(t *testing.T) {
	s, err := Compose("", 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 16 {
		t.Fatalf("anon want 16 chars, got %d", len(s))
	}
}

func TestComposeNamed(t *testing.T) {
	s, err := Compose("myteam", 16)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s, "myteam-") {
		t.Fatalf("want myteam- prefix, got %s", s)
	}
	if len(s) != len("myteam-")+16 {
		t.Fatalf("unexpected len for %s", s)
	}
}

func TestComposeInvalidName(t *testing.T) {
	_, err := Compose("bad name", 16)
	if !errors.Is(err, ErrInvalidName) {
		t.Fatalf("want ErrInvalidName, got %v", err)
	}
}
