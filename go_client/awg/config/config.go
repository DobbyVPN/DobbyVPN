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

func (conf *Config) DeduplicateNetworkEntries() {
	m := make(map[string]bool, len(conf.Interface.Addresses))
	i := 0
	for _, addr := range conf.Interface.Addresses {
		s := addr.String()
		if m[s] {
			continue
		}
		m[s] = true
		conf.Interface.Addresses[i] = addr
		i++
	}
	conf.Interface.Addresses = conf.Interface.Addresses[:i]

	m = make(map[string]bool, len(conf.Interface.DNS))
	i = 0
	for _, addr := range conf.Interface.DNS {
		s := addr.String()
		if m[s] {
			continue
		}
		m[s] = true
		conf.Interface.DNS[i] = addr
		i++
	}
	conf.Interface.DNS = conf.Interface.DNS[:i]

	for _, peer := range conf.Peers {
		m = make(map[string]bool, len(peer.AllowedIPs))
		i = 0
		for _, addr := range peer.AllowedIPs {
			s := addr.String()
			if m[s] {
				continue
			}
			m[s] = true
			peer.AllowedIPs[i] = addr
			i++
		}
		peer.AllowedIPs = peer.AllowedIPs[:i]
	}
}

func (conf *Config) Redact() {
	conf.Interface.PrivateKey = Key{}
	for i := range conf.Peers {
		conf.Peers[i].PublicKey = Key{}
		conf.Peers[i].PresharedKey = Key{}
	}
}
