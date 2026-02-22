package georouting

import (
	"net"
	"net/netip"
	"sync"
)

var (
	defaultRouterOnce sync.Once
	defaultRouter     Router
)

// NewDefaultRouter возвращает дефолтный Router, который:
// - direct: если dst IP входит в DefaultBypassCIDRs
// - final: proxy
//
// Потокобезопасно и дешево по повторным вызовам: trie строится один раз.
func NewDefaultRouter() Router {
	defaultRouterOnce.Do(func() {
		defaultRouter = buildDefaultRouter()
	})
	return defaultRouter
}

func buildDefaultRouter() Router {
	// Собираем все CIDR в один trie.
	trie := NewIPTrie()
	for _, n := range DefaultBypassCIDRs {
		pfx, ok := prefixFromIPNet(n)
		if !ok {
			continue
		}
		trie.Add(pfx)
	}

	// Engine и compiledRule — в том же пакете, поэтому доступно без экспорта.
	// Правило direct идёт первым, final = proxy.
	return &Engine{
		rules: []compiledRule{
			{
				outbound: OutboundDirect,
				ipTrie:   trie,
			},
		},
		final: OutboundProxy,
	}
}

// prefixFromIPNet конвертирует *net.IPNet -> netip.Prefix.
// Без string-парсинга, только stdlib.
// Используем Masked(), потому что PrefixFrom НЕ маскирует host bits.
func prefixFromIPNet(n *net.IPNet) (netip.Prefix, bool) {
	if n == nil {
		return netip.Prefix{}, false
	}

	ones, bits := n.Mask.Size()
	// Если маска некорректна, Size() может вернуть 0,0
	if bits != 32 && bits != 128 {
		return netip.Prefix{}, false
	}
	if ones < 0 || ones > bits {
		return netip.Prefix{}, false
	}

	addr, ok := netip.AddrFromSlice(n.IP)
	if !ok {
		return netip.Prefix{}, false
	}
	addr = addr.Unmap()

	p := netip.PrefixFrom(addr, ones)
	if !p.IsValid() {
		return netip.Prefix{}, false
	}
	return p.Masked(), true
}
