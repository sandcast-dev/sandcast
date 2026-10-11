package profile

import (
	"net/netip"
	"regexp"
	"sort"
)

// hostPattern is the character set of a DNS name or host wildcard pattern.
// Hosts end up in generated files (allowed-domains.txt, adapted profiles),
// so anything else, such as whitespace, quotes or line breaks, is refused.
var hostPattern = regexp.MustCompile(`^[A-Za-z0-9_*.-]+$`)

// ValidHost reports whether host is a DNS name or wildcard pattern made of
// letters, digits, '_', '-', '.' and '*', or a parsable IP address without
// an IPv6 zone (a zone may hold any text). Wildcard shape and blocked
// destinations are checked separately by lint.
func ValidHost(host string) bool {
	if hostPattern.MatchString(host) {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Zone() == ""
}

// Domains returns the sorted, unique endpoint hosts of profiles, the content
// of allowed-domains.txt for the container target (FR-040). Hosts are kept
// verbatim, wildcards included; empty hosts and nil profiles are skipped.
// Callers that write the result must check each host with ValidHost; lint
// already refuses profiles with other hosts.
func Domains(profiles []*Profile) []string {
	seen := map[string]bool{}
	var hosts []string
	for _, p := range profiles {
		if p == nil {
			continue
		}
		for _, ep := range p.Endpoints {
			if ep.Host == "" || seen[ep.Host] {
				continue
			}
			seen[ep.Host] = true
			hosts = append(hosts, ep.Host)
		}
	}
	sort.Strings(hosts)
	return hosts
}
