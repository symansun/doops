package server

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

type edgeBackendAllow struct {
	exact map[string]struct{}
	cidrs []edgeCIDRPort
}

type edgeCIDRPort struct {
	prefix netip.Prefix
	port   uint16
}

func parseEdgeBackendAllow(raw string) (*edgeBackendAllow, error) {
	allow := &edgeBackendAllow{exact: make(map[string]struct{})}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		host, port, err := splitAllowHostPort(entry)
		if err != nil {
			return nil, err
		}
		number, err := strconv.ParseUint(port, 10, 16)
		if err != nil || number == 0 {
			return nil, fmt.Errorf("edge backend allow %q has an invalid port", entry)
		}
		if prefix, cidrErr := netip.ParsePrefix(host); cidrErr == nil {
			allow.cidrs = append(allow.cidrs, edgeCIDRPort{prefix: prefix, port: uint16(number)})
			continue
		}
		normalized, err := normalizeEdgeBackend(net.JoinHostPort(host, port))
		if err != nil {
			return nil, fmt.Errorf("edge backend allow %q: %w", entry, err)
		}
		allow.exact[normalized] = struct{}{}
	}
	if len(allow.exact) == 0 && len(allow.cidrs) == 0 {
		return nil, fmt.Errorf("edge backend allow list is empty")
	}
	return allow, nil
}

func (a *edgeBackendAllow) permits(backend string) bool {
	if a == nil {
		return false
	}
	normalized, err := normalizeEdgeBackend(backend)
	if err != nil {
		return false
	}
	if _, ok := a.exact[normalized]; ok {
		return true
	}
	host, port, err := net.SplitHostPort(normalized)
	if err != nil {
		return false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return false
	}
	for _, rule := range a.cidrs {
		if rule.port == uint16(number) && rule.prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func splitAllowHostPort(entry string) (string, string, error) {
	if host, port, err := net.SplitHostPort(entry); err == nil {
		return host, port, nil
	}
	// 192.168.150.0/24:443 — net.SplitHostPort 不认 CIDR。
	idx := strings.LastIndex(entry, ":")
	if idx <= 0 || idx == len(entry)-1 {
		return "", "", fmt.Errorf("backend allow %q must be host:port or cidr:port", entry)
	}
	return entry[:idx], entry[idx+1:], nil
}
