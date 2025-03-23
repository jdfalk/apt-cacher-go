package security

import (
	"net"
	"net/http"
	"strings"
	"sync"
)

// ACL handles access control lists for the server
type ACL struct {
	allowedNetworks []*net.IPNet
	mutex           sync.RWMutex
}

// New creates a new ACL
func New(allowedCIDRs []string) (*ACL, error) {
	acl := &ACL{
		allowedNetworks: make([]*net.IPNet, 0, len(allowedCIDRs)),
	}

	// If no CIDRs provided, default to allowing all
	if len(allowedCIDRs) == 0 {
		_, allIPv4, _ := net.ParseCIDR("0.0.0.0/0")
		_, allIPv6, _ := net.ParseCIDR("::/0")
		acl.allowedNetworks = append(acl.allowedNetworks, allIPv4, allIPv6)
		return acl, nil
	}

	for _, cidr := range allowedCIDRs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, err
		}
		acl.allowedNetworks = append(acl.allowedNetworks, network)
	}

	return acl, nil
}

// AddAllowedCIDR adds a new CIDR to the allowed list
func (a *ACL) AddAllowedCIDR(cidr string) error {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return err
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	a.allowedNetworks = append(a.allowedNetworks, network)
	return nil
}

// IsAllowed checks if an IP is allowed by the ACL
func (a *ACL) IsAllowed(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	a.mutex.RLock()
	defer a.mutex.RUnlock()

	// If no networks configured, default to allowing all
	if len(a.allowedNetworks) == 0 {
		return true
	}

	for _, network := range a.allowedNetworks {
		if network.Contains(ip) {
			return true
		}
	}

	return false
}

// Middleware returns an HTTP middleware that enforces the ACL
func (a *ACL) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract client IP from request
		ip := extractClientIP(r)

		// Check if allowed
		if !a.IsAllowed(ip) {
			http.Error(w, "Access denied", http.StatusForbidden)
			return
		}

		// Continue to next handler
		next.ServeHTTP(w, r)
	})
}

// extractClientIP extracts the client IP from a request
func extractClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first (for proxies)
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		// X-Forwarded-For can contain multiple IPs, use the first one
		ips := strings.Split(forwarded, ",")
		return strings.TrimSpace(ips[0])
	}

	// Otherwise use RemoteAddr
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// If no port in the address, use as is
		return r.RemoteAddr
	}

	return ip
}
