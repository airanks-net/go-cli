package main

import (
	"net"
	"net/url"
	"regexp"
	"strings"
)

// Mirrors node-cli's normalizeHostname (which mirrors the API's HostnameNormalizer).
var schemeRe = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://`)

// normalizeHostname returns "" for anything that isn't a supported bare hostname:
// non-http(s) schemes, localhost, IP literals (v4 or v6), missing dot, or trailing dot.
// A multi-word input (e.g. "best crm software") fails to parse as a URL host and
// correctly falls through to "" — the caller treats that as a search query.
func normalizeHostname(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	candidate := raw
	if !schemeRe.MatchString(raw) {
		candidate = "https://" + raw
	}
	u, err := url.Parse(candidate)
	if err != nil {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	hostname := strings.ToLower(u.Hostname())
	if hostname == "" || hostname == "localhost" {
		return ""
	}
	if net.ParseIP(hostname) != nil { // rejects IPv4 and (bracketed-in-source) IPv6 alike
		return ""
	}
	hostname = strings.TrimPrefix(hostname, "www.")
	if !strings.Contains(hostname, ".") || strings.HasSuffix(hostname, ".") {
		return ""
	}
	return hostname
}
