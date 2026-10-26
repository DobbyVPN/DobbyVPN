//go:build android

package dnscache

import "errors"

// Android resolver ownership uses an android.net.Network, not an interface
// index. The mobile bridge installs DnsResolver through SetResolverSource.
func (c *Cache) UseInterface(int) error {
	return errors.New("Android resolution requires a captured Network")
}
