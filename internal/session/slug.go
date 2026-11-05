package session

import (
	"crypto/rand"
	"errors"
	"math/big"
	"regexp"
)

const Alphabet = "ABCDEFGHIJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

const (
	NameMinLen = 1
	NameMaxLen = 32
)

// The random part of a slug is the only secret guarding a plaintext session,
// so its length is kept within bounds whatever SLUG_LEN says: 12 characters of
// this alphabet are ~70 bits, enough to make guessing impractical.
const (
	DefaultRandomLen = 16
	MinRandomLen     = 12
	MaxRandomLen     = 64
)

// ClampRandomLen maps a configured slug length onto the allowed range; zero or
// negative means the default.
func ClampRandomLen(n int) int {
	switch {
	case n <= 0:
		return DefaultRandomLen
	case n < MinRandomLen:
		return MinRandomLen
	case n > MaxRandomLen:
		return MaxRandomLen
	}
	return n
}

var (
	nameRe         = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	ErrInvalidName = errors.New("invalid name")
)

func NewSlug(n int) (string, error) {
	if n <= 0 {
		n = 16
	}
	max := big.NewInt(int64(len(Alphabet)))
	buf := make([]byte, n)
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = Alphabet[idx.Int64()]
	}
	return string(buf), nil
}

// ValidName reports whether name matches the persistent-session name rules:
// non-empty, length 1..NameMaxLen, [A-Za-z0-9_-]+.
func ValidName(name string) bool {
	if len(name) < NameMinLen || len(name) > NameMaxLen {
		return false
	}
	return nameRe.MatchString(name)
}

// Compose builds a slug of the form "{name}-{random}" for named persistent
// sessions, or just "{random}" when name is empty.
func Compose(name string, randomLen int) (string, error) {
	rnd, err := NewSlug(ClampRandomLen(randomLen))
	if err != nil {
		return "", err
	}
	if name == "" {
		return rnd, nil
	}
	if !ValidName(name) {
		return "", ErrInvalidName
	}
	return name + "-" + rnd, nil
}
