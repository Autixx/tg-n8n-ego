package auth

import (
	"crypto/subtle"
	"net/http"
)

const HeaderName = "X-Codex-Agent-Token"

func TokenMatches(expected, actual string) bool {
	if expected == "" || actual == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

func CheckRequest(r *http.Request, expected string) bool {
	return TokenMatches(expected, r.Header.Get(HeaderName))
}
