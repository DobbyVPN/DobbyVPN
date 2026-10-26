package common

// VPNDNSServers returns the resolver addresses installed for the VPN tunnel.
func VPNDNSServers() []string {
	return []string{
		"9.9.9.9",
		"1.1.1.1",
		"2620:fe::fe",
		"2606:4700:4700::1111",
	}
}
