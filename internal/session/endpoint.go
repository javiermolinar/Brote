package session

import (
	"crypto/subtle"
	"fmt"
	"net/url"
)

// LocalEndpoint rejects credentials, path/query fragments and non-loopback
// destinations before a stored descriptor can cause a network request.
func LocalEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("expected a local broker URL")
	}
	return nil
}

func ValidToken(expected, actual string) bool {
	return expected != "" && subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}
