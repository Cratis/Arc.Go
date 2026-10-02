// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package tenancy

import (
	"net"
	"strconv"
	"strings"
)

// normalizeHost returns empty for invalid ASCII hosts and address literals. Unicode
// is explicitly unsupported rather than silently selecting the fallback header.
func normalizeHost(text string) (string, error) {
	for _, c := range text {
		if c > 127 {
			return "", ErrUnsupportedHost
		}
	}
	host := strings.TrimSpace(text)
	if strings.Contains(host, ":") {
		name, port, err := net.SplitHostPort(host)
		if err != nil {
			return "", nil
		}
		if port == "" {
			return "", nil
		}
		for _, c := range port {
			if c < '0' || c > '9' {
				return "", nil
			}
		}
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return "", nil
		}
		host = name
	}
	host = strings.ToLower(strings.Trim(host, "."))
	if net.ParseIP(host) != nil || len(host) > 253 {
		return "", nil
	}
	for _, label := range strings.Split(host, ".") {
		if !dnsLabel(label) {
			return "", nil
		}
	}
	return host, nil
}
func dnsLabel(label string) bool {
	if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, c := range label {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' {
			continue
		}
		return false
	}
	return true
}
