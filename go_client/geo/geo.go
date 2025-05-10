package geo

import (
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"strings"

	"github.com/oschwald/maxminddb-golang/v2"
)

func CIDRList(countryCodes []string) ([]*net.IPNet, error) {
	dbPath := filepath.Join("go_client", "geo", "geoip.db") // TODO: correct path
	db, err := maxminddb.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer db.Close()

	want := make(map[string]struct{}, len(countryCodes))
	for _, c := range countryCodes {
		want[strings.ToLower(strings.TrimSpace(c))] = struct{}{}
	}

	var (
		out     []*net.IPNet
		iterErr error
	)

	db.Networks()(func(res maxminddb.Result) bool {
		if iterErr != nil {
			return false
		}

		var cc string
		if err := res.Decode(&cc); err != nil {
			iterErr = err
			return false
		}

		if _, ok := want[cc]; ok {
			pfx := res.Prefix()
			out = append(out, toIPNet(pfx))
		}
		return true
	})

	if iterErr != nil {
		return nil, iterErr
	}
	return out, nil
}

func toIPNet(p netip.Prefix) *net.IPNet {
	addr := p.Addr()
	bits := p.Bits()

	if addr.Is4() {
		b4 := addr.As4()
		return &net.IPNet{
			IP:   b4[:],
			Mask: net.CIDRMask(bits, 32),
		}
	}

	b16 := addr.As16()
	return &net.IPNet{
		IP:   b16[:],
		Mask: net.CIDRMask(bits, 128),
	}
}
