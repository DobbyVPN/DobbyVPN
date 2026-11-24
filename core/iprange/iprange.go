// Package iprange stores a set of IP addresses as sorted, merged ranges, so a
// large ExcludeIPs list is held once and matched with a binary search.
package iprange

import (
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"net/netip"
	"slices"
	"sort"
)

// Range is an inclusive address range of one family.
type Range struct{ First, Last netip.Addr }

// PrefixRange returns the addresses of a prefix. IPv4-mapped IPv6 prefixes
// are returned as IPv4.
func PrefixRange(prefix netip.Prefix) Range {
	prefix = prefix.Masked()
	first := prefix.Addr()
	if first.Is4() {
		host := uint32(1)<<(32-prefix.Bits()) - 1
		return Range{first, addr4(value4(first) | host)}
	}
	value := value16(first)
	hostBits := 128 - prefix.Bits()
	if hostBits >= 64 {
		value.lo = ^uint64(0)
		value.hi |= 1<<(hostBits-64) - 1
	} else {
		value.lo |= 1<<hostBits - 1
	}
	last := value.addr()
	if first.Is4In6() {
		return Range{first.Unmap(), last.Unmap()}
	}
	return Range{first, last}
}

// Set is immutable after New and safe for concurrent use. A nil Set is empty.
type Set struct {
	v4   []range4
	v6   []range6
	hash string
}

var emptyHash = (&Set{}).computeHash()

type range4 struct{ first, last uint32 }

type u128 struct{ hi, lo uint64 }

type range6 struct{ first, last u128 }

// New merges overlapping, nested and adjacent ranges. Invalid ranges and
// ranges whose ends are of different families are ignored.
func New(ranges []Range) *Set {
	ipv4 := 0
	for _, r := range ranges {
		if r.First.Unmap().Is4() {
			ipv4++
		}
	}
	s := &Set{v4: make([]range4, 0, ipv4), v6: make([]range6, 0, len(ranges)-ipv4)}
	for _, r := range ranges {
		first, last := r.First.Unmap(), r.Last.Unmap()
		if !first.IsValid() || !last.IsValid() || first.Is4() != last.Is4() || last.Less(first) {
			continue
		}
		if first.Is4() {
			s.v4 = append(s.v4, range4{value4(first), value4(last)})
		} else {
			s.v6 = append(s.v6, range6{value16(first), value16(last)})
		}
	}
	slices.SortFunc(s.v4, func(a, b range4) int { return cmp.Compare(a.first, b.first) })
	merged4 := s.v4[:0]
	for _, r := range s.v4 {
		if n := len(merged4); n > 0 && (merged4[n-1].last == ^uint32(0) || r.first <= merged4[n-1].last+1) {
			merged4[n-1].last = max(merged4[n-1].last, r.last)
			continue
		}
		merged4 = append(merged4, r)
	}
	s.v4 = exact(merged4)
	slices.SortFunc(s.v6, func(a, b range6) int { return a.first.compare(b.first) })
	merged6 := s.v6[:0]
	for _, r := range s.v6 {
		n := len(merged6)
		if n == 0 {
			merged6 = append(merged6, r)
			continue
		}
		if next, overflow := merged6[n-1].last.next(); !overflow && r.first.compare(next) > 0 {
			merged6 = append(merged6, r)
			continue
		}
		if merged6[n-1].last.compare(r.last) < 0 {
			merged6[n-1].last = r.last
		}
	}
	s.v6 = exact(merged6)
	s.hash = s.computeHash()
	return s
}

// exact copies merged ranges into an array of their own length, so the
// array sized for the unmerged input is freed.
func exact[T any](merged []T) []T {
	if len(merged) == cap(merged) {
		return merged
	}
	return append(make([]T, 0, len(merged)), merged...)
}

// Contains reports whether the address is in the set. IPv4-mapped IPv6
// addresses match IPv4 ranges.
func (s *Set) Contains(address netip.Addr) bool {
	if s == nil || !address.IsValid() {
		return false
	}
	address = address.Unmap()
	if address.Is4() {
		value := value4(address)
		i := sort.Search(len(s.v4), func(i int) bool { return s.v4[i].last >= value })
		return i < len(s.v4) && s.v4[i].first <= value
	}
	value := value16(address)
	i := sort.Search(len(s.v6), func(i int) bool { return s.v6[i].last.compare(value) >= 0 })
	return i < len(s.v6) && s.v6[i].first.compare(value) <= 0
}

// Len returns the number of merged ranges.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.v4) + len(s.v6)
}

// Ranges returns the merged ranges, IPv4 first, in ascending order.
func (s *Set) Ranges() []Range {
	if s == nil {
		return nil
	}
	ranges := make([]Range, 0, s.Len())
	for _, r := range s.v4 {
		ranges = append(ranges, Range{addr4(r.first), addr4(r.last)})
	}
	for _, r := range s.v6 {
		ranges = append(ranges, Range{r.first.addr(), r.last.addr()})
	}
	return ranges
}

// Addresses counts the addresses of the set inside the prefix.
func (s *Set) Addresses(within netip.Prefix) *big.Int {
	total := new(big.Int)
	if s == nil {
		return total
	}
	bounds := PrefixRange(within)
	if bounds.First.Is4() {
		low, high := value4(bounds.First), value4(bounds.Last)
		for _, r := range s.v4 {
			if first, last := max(r.first, low), min(r.last, high); first <= last {
				total.Add(total, new(big.Int).SetUint64(uint64(last-first)+1))
			}
		}
		return total
	}
	low, high := value16(bounds.First), value16(bounds.Last)
	for _, r := range s.v6 {
		first, last := r.first, r.last
		if first.compare(low) < 0 {
			first = low
		}
		if last.compare(high) > 0 {
			last = high
		}
		if first.compare(last) <= 0 {
			count := new(big.Int).Sub(last.big(), first.big())
			total.Add(total, count.Add(count, big.NewInt(1)))
		}
	}
	return total
}

// Hash identifies the set in logs and firewall updates without listing its
// ranges. It is computed once by New.
func (s *Set) Hash() string {
	if s == nil {
		return emptyHash
	}
	return s.hash
}

func (s *Set) computeHash() string {
	hash := sha256.New()
	// Hash each family marker and its network-order endpoints, IPv4 first.
	writeRange := func(family byte, first, last netip.Addr) {
		hash.Write([]byte{family})
		hash.Write(first.AsSlice())
		hash.Write(last.AsSlice())
	}
	for _, r := range s.v4 {
		writeRange(4, addr4(r.first), addr4(r.last))
	}
	for _, r := range s.v6 {
		writeRange(6, r.first.addr(), r.last.addr())
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func value4(address netip.Addr) uint32 {
	bytes := address.As4()
	return binary.BigEndian.Uint32(bytes[:])
}

func addr4(value uint32) netip.Addr {
	var bytes [4]byte
	binary.BigEndian.PutUint32(bytes[:], value)
	return netip.AddrFrom4(bytes)
}

func value16(address netip.Addr) u128 {
	bytes := address.As16()
	return u128{binary.BigEndian.Uint64(bytes[:8]), binary.BigEndian.Uint64(bytes[8:])}
}

func (u u128) addr() netip.Addr {
	var bytes [16]byte
	binary.BigEndian.PutUint64(bytes[:8], u.hi)
	binary.BigEndian.PutUint64(bytes[8:], u.lo)
	return netip.AddrFrom16(bytes)
}

func (u u128) compare(v u128) int {
	if u.hi != v.hi {
		return cmp.Compare(u.hi, v.hi)
	}
	return cmp.Compare(u.lo, v.lo)
}

func (u u128) next() (u128, bool) {
	if u.lo != ^uint64(0) {
		return u128{u.hi, u.lo + 1}, false
	}
	return u128{u.hi + 1, 0}, u.hi == ^uint64(0)
}

func (u u128) big() *big.Int {
	value := new(big.Int).SetUint64(u.hi)
	return value.Lsh(value, 64).Or(value, new(big.Int).SetUint64(u.lo))
}
