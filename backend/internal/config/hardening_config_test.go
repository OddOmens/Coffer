package config

import (
	"net/netip"
	"testing"
)

func TestPrefixesMatchClientAddresses(t *testing.T) {
	ps, err := prefixes("::ffff:10.0.0.0/104, fe80::/10, 192.168.1.7")
	if err != nil {
		t.Fatal(err)
	}
	c := &Config{AppNets: ps}
	for _, ip := range []string{"10.1.2.3", "fe80::1%eth0", "192.168.1.7", "::ffff:192.168.1.7"} {
		if !c.AppAllowed(netip.MustParseAddr(ip)) {
			t.Errorf("%s not matched", ip)
		}
	}
	for _, ip := range []string{"11.0.0.1", "192.168.1.8", "2001:db8::1"} {
		if c.AppAllowed(netip.MustParseAddr(ip)) {
			t.Errorf("%s matched", ip)
		}
	}
	if _, err := prefixes("::ffff:0.0.0.0/80"); err == nil {
		t.Error("mapped prefix shorter than /96 accepted")
	}
}
