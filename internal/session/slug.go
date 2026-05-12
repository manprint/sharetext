package session

import (
	"crypto/rand"
	"errors"
	"math/big"
	"regexp"
)

const alphabet = "ABCDEFGHIJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

const (
	NameMinLen = 1
	NameMaxLen = 32
)

var (
	nameRe        = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	ErrInvalidName = errors.New("invalid name")
)

func NewSlug(n int) (string, error) {
	if n <= 0 {
		n = 16
	}
	max := big.NewInt(int64(len(alphabet)))
	buf := make([]byte, n)
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = alphabet[idx.Int64()]
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
	rnd, err := NewSlug(randomLen)
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
