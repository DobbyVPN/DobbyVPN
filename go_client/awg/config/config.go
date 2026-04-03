package config

type Config struct {
	Name      string
	Interface Interface
	Peers     []Peer
}

func (conf *Config) IntersectsWith(other *Config) bool {
	type hashableIPCidr struct {
		ip   string
		cidr byte
	}
	allRoutes := make(map[hashableIPCidr]bool, len(conf.Interface.Addresses)*2+len(conf.Peers)*3)
	for _, a := range conf.Interface.Addresses {
		allRoutes[hashableIPCidr{string(a.IP), byte(len(a.IP) * 8)}] = true
		a.MaskSelf()
		allRoutes[hashableIPCidr{string(a.IP), a.Cidr}] = true
	}
	for i := range conf.Peers {
		for _, a := range conf.Peers[i].AllowedIPs {
			a.MaskSelf()
			allRoutes[hashableIPCidr{string(a.IP), a.Cidr}] = true
		}
	}
	for _, a := range other.Interface.Addresses {
		if allRoutes[hashableIPCidr{string(a.IP), byte(len(a.IP) * 8)}] {
			return true
		}
		a.MaskSelf()
		if allRoutes[hashableIPCidr{string(a.IP), a.Cidr}] {
			return true
		}
	}
	for i := range other.Peers {
		for _, a := range other.Peers[i].AllowedIPs {
			a.MaskSelf()
			if allRoutes[hashableIPCidr{string(a.IP), a.Cidr}] {
				return true
			}
		}
	}
	return false
}

func (c *Config) MaybeAddPeer(p *Peer) {
	if p != nil {
		c.Peers = append(c.Peers, *p)
	}
}
