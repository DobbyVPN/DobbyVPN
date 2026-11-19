package routing

import (
    "fmt"
    "net/netip"
    "strconv"
    "strings"
)

// Parse only stock ifconfig's native per-interface NAT64 inventory.
func parseMacOSNAT64Prefixes(output string) ([]netip.Prefix, error) {
    var prefixes []netip.Prefix
    for _, line := range strings.Split(output, "\n") {
        fields := strings.Fields(line)
        if len(fields) < 2 || fields[0] != "nat64" || fields[1] != "prefix" { continue }
        if len(fields) != 5 || fields[3] != "prefixlen" {
            return nil, fmt.Errorf("invalid native NAT64 inventory: %s", line)
        }
        bits, err := strconv.Atoi(fields[4])
        if err != nil {return nil, fmt.Errorf("invalid native NAT64 length: %s",line)}
        switch bits {case 32,40,48,56,64,96:default:return nil,fmt.Errorf("unsupported native NAT64 length: %d",bits)}
        address, err := netip.ParseAddr(fields[2])
        if err != nil || !address.Is6() || address.Is4In6() || address.Zone() != "" {
            return nil, fmt.Errorf("invalid native NAT64 prefix: %s",line)
        }
        prefixes = append(prefixes, netip.PrefixFrom(address,bits).Masked())
    }
    return prefixes,nil
}
