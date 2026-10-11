package profile

import (
	"fmt"
	"net/netip"
	"strings"
)

// Host, address and port rules that OpenShell v0.1.2 enforces when a sandbox
// policy is loaded or a connection is made, not in profile lint. A profile
// that breaks one of them imports cleanly but fails at sandbox start or never
// carries traffic, so sandcast reports them at lint time.
//
// Sources at the pinned tag:
//   - crates/openshell-policy/src/lib.rs: validate_sandbox_policy (TLD
//     wildcards, wildcard shape, explicit TCP host selectors)
//   - crates/openshell-core/src/net.rs: is_always_blocked_ip,
//     is_always_blocked_net, is_known_metadata_hostname
//   - crates/openshell-server/src/grpc/policy.rs: validate_host_not_always_blocked
//   - crates/openshell-supervisor-network/src/proxy.rs: BLOCKED_CONTROL_PLANE_PORTS

// blockedControlPlanePorts are rejected by the sandbox proxy for every
// declared endpoint, whatever the policy says.
var blockedControlPlanePorts = map[uint32]string{
	2379:  "etcd client",
	2380:  "etcd peer",
	6443:  "Kubernetes API server",
	10250: "kubelet API",
	10255: "kubelet read-only",
}

// hostRules checks one endpoint against the policy host, address and port
// rules.
func (l *linter) hostRules(index int, ep *Endpoint) {
	field := fmt.Sprintf("endpoints[%d]", index)
	host := ep.Host

	if isTLDWildcard(host) {
		l.add(field+".host", fmt.Sprintf(
			"TLD wildcard '%s' is not allowed; use subdomain wildcards like '*.example.com' instead", host))
	}
	if hostWildcardShapeInvalid(host) {
		l.add(field+".host", fmt.Sprintf(
			"invalid host wildcard '%s'; middle DNS label wildcards must be the entire label '*' and recursive '**' is only allowed as the entire first label", host))
	}

	trimmed := strings.TrimSpace(host)
	if addr, err := netip.ParseAddr(trimmed); err == nil && isAlwaysBlockedAddr(addr) {
		l.add(field+".host", fmt.Sprintf(
			"endpoint host '%s' is an always-blocked address (loopback/link-local/unspecified); the proxy will deny traffic to this destination regardless of policy", trimmed))
	}
	if lc := strings.ToLower(trimmed); lc == "localhost" || lc == "localhost." {
		l.add(field+".host",
			"endpoint host 'localhost' is always blocked; the proxy will deny traffic to loopback regardless of policy")
	}
	if isKnownMetadataHostname(host) {
		l.add(field+".host", fmt.Sprintf(
			"endpoint host '%s' is a known cloud metadata hostname; the proxy will deny traffic to this destination regardless of policy", trimmed))
	}

	if isExplicitTCP(ep.Protocol) && trimmed != "" {
		if _, err := netip.ParseAddr(host); err == nil {
			l.add(field+".host", fmt.Sprintf(
				"protocol tcp endpoint '%s' must use a DNS hostname, not an IP literal; direct IP connections bypass policy DNS and are blocked", host))
		} else if reason := tcpHostSelectorProblem(host); reason != "" {
			l.add(field+".host", fmt.Sprintf(
				"protocol tcp endpoint has invalid DNS host selector '%s': %s", host, reason))
		}
	}

	for _, entry := range ep.AllowedIPs {
		prefix, ok := parseAllowedIP(entry)
		switch {
		case !ok:
			l.add(field+".allowed_ips", fmt.Sprintf(
				"allowed_ips entry '%s' is not a valid IP address or CIDR", entry))
		case isAlwaysBlockedPrefix(prefix):
			l.add(field+".allowed_ips", fmt.Sprintf(
				"always-blocked allowed_ips entry '%s'; SSRF hardening prevents traffic to these destinations regardless of policy", entry))
		}
	}

	for _, port := range effectivePorts(ep) {
		if name, blocked := blockedControlPlanePorts[port]; blocked {
			l.add(field, fmt.Sprintf(
				"port %d (%s) is a blocked control-plane port; the proxy rejects connections to it regardless of policy", port, name))
		}
	}
}

// isTLDWildcard reports a wildcard that covers a whole top-level domain, such
// as *.com or **.com (policy rule TldWildcard).
func isTLDWildcard(host string) bool {
	leading := strings.HasPrefix(host, "*.") || strings.HasPrefix(host, "**.")
	return leading && len(strings.Split(host, ".")) <= 2
}

// hostWildcardShapeInvalid mirrors host_wildcard_shape_invalid: a bare * or
// **, a ** mixed into the first label, or any partial wildcard after the
// first label.
func hostWildcardShapeInvalid(host string) bool {
	if host == "*" || host == "**" {
		return true
	}
	if !strings.Contains(host, "*") {
		return false
	}
	labels := strings.Split(host, ".")
	if first := labels[0]; strings.Contains(first, "**") && first != "**" {
		return true
	}
	for _, label := range labels[1:] {
		if strings.Contains(label, "**") || (strings.Contains(label, "*") && label != "*") {
			return true
		}
	}
	return false
}

// isAlwaysBlockedAddr mirrors is_always_blocked_ip: loopback, link-local and
// unspecified addresses, including their IPv4-mapped IPv6 forms.
func isAlwaysBlockedAddr(addr netip.Addr) bool {
	a := addr.Unmap()
	return a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsUnspecified()
}

// alwaysBlockedPrefixes are the ranges is_always_blocked_net rejects in
// allowed_ips entries.
var alwaysBlockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("0.0.0.0/32"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("::ffff:127.0.0.0/104"),
	netip.MustParsePrefix("::ffff:169.254.0.0/112"),
	netip.MustParsePrefix("::ffff:0.0.0.0/128"),
}

// isAlwaysBlockedPrefix reports whether prefix overlaps an always-blocked
// range.
func isAlwaysBlockedPrefix(prefix netip.Prefix) bool {
	for _, blocked := range alwaysBlockedPrefixes {
		if prefix.Overlaps(blocked) {
			return true
		}
	}
	return false
}

// parseAllowedIP parses an allowed_ips entry, a CIDR or a single address.
func parseAllowedIP(entry string) (netip.Prefix, bool) {
	if prefix, err := netip.ParsePrefix(entry); err == nil {
		return prefix.Masked(), true
	}
	if addr, err := netip.ParseAddr(entry); err == nil {
		return netip.PrefixFrom(addr, addr.BitLen()), true
	}
	return netip.Prefix{}, false
}

// isKnownMetadataHostname mirrors is_known_metadata_hostname.
func isKnownMetadataHostname(host string) bool {
	normalized := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	return normalized == "metadata.google.internal"
}

// tcpHostSelectorProblem mirrors the cheap checks of
// validate_tcp_dns_host_selector and returns the reason, or "".
func tcpHostSelectorProblem(host string) string {
	if strings.TrimSpace(host) != host {
		return "leading or trailing whitespace is not allowed"
	}
	if strings.HasSuffix(host, ".") {
		return "omit the trailing DNS root dot"
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" {
			return "empty DNS labels are not allowed"
		}
	}
	return ""
}

// effectivePorts returns ports when set, otherwise port when non-zero.
func effectivePorts(ep *Endpoint) []uint32 {
	if len(ep.Ports) > 0 {
		return ep.Ports
	}
	if ep.Port != 0 {
		return []uint32{ep.Port}
	}
	return nil
}
