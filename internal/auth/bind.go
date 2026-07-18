package auth

import (
	"fmt"
	"net"
	"strings"
)

// IsLoopbackListen reports whether addr is a loopback-only listen address.
// Empty host, 0.0.0.0, and :: bind all interfaces and are not loopback.
func IsLoopbackListen(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "0.0.0.0" || host == "::" || host == "*" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Unresolvable host names are treated as non-loopback (safer default).
		return false
	}
	return ip.IsLoopback()
}

// CheckBind enforces SPEC bind safety: non-loopback listen without a shared
// token requires an explicit override (IUnderstand).
func CheckBind(listen, token string, iUnderstand bool) error {
	if IsLoopbackListen(listen) {
		return nil
	}
	if strings.TrimSpace(token) != "" {
		return nil
	}
	if iUnderstand {
		return nil
	}
	return fmt.Errorf(
		"refusing non-loopback listen %q without --token (kernel RCE as this OS user); set GADERNO_TOKEN/--token or pass --i-understand",
		listen,
	)
}
