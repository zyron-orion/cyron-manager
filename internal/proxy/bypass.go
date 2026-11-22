package proxy

import (
	"net"
	"strings"
)

type bypassKind int

const (
	bypassDomain bypassKind = iota
	bypassIP
	bypassCIDR
)

type bypassEntry struct {
	kind   bypassKind
	domain string
	ip     net.IP
	ipnet  *net.IPNet
}

type Bypass struct {
	entries []bypassEntry
}

func ParseBypass(raw string) *Bypass {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ';'
	})
	out := &Bypass{}
	for _, field := range fields {
		value := strings.ToLower(strings.TrimSpace(field))
		if value == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(value); err == nil {
			out.entries = append(out.entries, bypassEntry{kind: bypassCIDR, ipnet: network})
			continue
		}
		if ip := net.ParseIP(value); ip != nil {
			out.entries = append(out.entries, bypassEntry{kind: bypassIP, ip: ip})
			continue
		}
		value = strings.TrimPrefix(value, "*.")
		value = strings.TrimPrefix(value, ".")
		if value == "" {
			continue
		}
		out.entries = append(out.entries, bypassEntry{kind: bypassDomain, domain: value})
	}
	return out
}

func (b *Bypass) Empty() bool {
	return b == nil || len(b.entries) == 0
}

func (b *Bypass) Match(host string) bool {
	if b == nil {
		return false
	}
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	for _, entry := range b.entries {
		switch entry.kind {
		case bypassIP:
			if ip != nil && entry.ip.Equal(ip) {
				return true
			}
		case bypassCIDR:
			if ip != nil && entry.ipnet.Contains(ip) {
				return true
			}
		case bypassDomain:
			if ip == nil && (host == entry.domain || strings.HasSuffix(host, "."+entry.domain)) {
				return true
			}
		}
	}
	return false
}

func (b *Bypass) String() string {
	if b == nil {
		return ""
	}
	parts := make([]string, 0, len(b.entries))
	for _, entry := range b.entries {
		switch entry.kind {
		case bypassDomain:
			parts = append(parts, entry.domain)
		case bypassIP:
			parts = append(parts, entry.ip.String())
		case bypassCIDR:
			parts = append(parts, entry.ipnet.String())
		}
	}
	return strings.Join(parts, ", ")
}
