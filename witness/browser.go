package main

import "errors"

// browserAppleScript accepts only characters that need no AppleScript string escaping.
func browserAppleScript(url string) (string, error) {
	if url == "" {
		return "", errors.New("empty browser URL")
	}
	for _, c := range url {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == ':' || c == '/' || c == '.' || c == '-') {
			return "", errors.New("unsafe character in browser URL")
		}
	}
	return "open location \"" + url + "\"\n", nil
}
