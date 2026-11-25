package ipgeo

import (
	"bytes"
	"compress/gzip"
	"errors"
	"net/netip"
	"sync"
	"testing"
)

func TestLookupEmbeddedDatabase(t *testing.T) {
	// 固定样本来自所附 2026-11 官方数据，用于检测大陆与港澳台混淆、地址族遗漏。
	tests := []struct{ address, country string }{
		{"223.5.5.5", "CN"},
		{"114.114.114.114", "CN"},
		{"1.32.192.1", "HK"},
		{"17.91.136.1", "MO"},
		{"1.32.199.1", "TW"},
		{"8.8.8.8", "US"},
		{"1.1.1.1", "AU"},
		{"2400:3200::1", "CN"},
		{"2001:4860:4860::8888", "CA"},
		{"::ffff:223.5.5.5", "CN"},
		{"::ffff:8.8.8.8", "US"},
		{" 223.5.5.5 ", "CN"},
	}
	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			t.Parallel()
			got, err := Lookup(tt.address)
			if err != nil || got != tt.country {
				t.Fatalf("Lookup(%q) = %q, %v; want %q", tt.address, got, err, tt.country)
			}
		})
	}
}

func TestLookupRejectsInvalidAndNonPublicAddresses(t *testing.T) {
	for _, address := range []string{"", "example.com", "8.8.8.8:53", "not-an-ip", "1.2.3.4,8.8.8.8", "fe80::1%eth0", "2001:4860::1%eth0"} {
		if code, err := Lookup(address); code != "" || !errors.Is(err, ErrInvalidIP) {
			t.Errorf("Lookup(%q) = %q, %v; want invalid address", address, code, err)
		}
	}
	for _, address := range []string{"127.0.0.1", "10.2.3.4", "172.16.0.1", "192.168.1.1", "169.254.1.1", "100.64.0.1", "192.0.2.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "::", "::1", "fe80::1", "fd00::1", "ff02::1", "2001:db8::1", "3fff::1", "4000::1", "64:ff9b::808:808", "::ffff:127.0.0.1"} {
		if code, err := Lookup(address); code != "" || !errors.Is(err, ErrNonPublicIP) {
			t.Errorf("Lookup(%q) = %q, %v; want non-public address", address, code, err)
		}
	}
}

func TestRangeBoundariesAndUnknownCountries(t *testing.T) {
	db, err := readDatabase(testGzip(t, "1.0.1.0,1.0.1.255,CN\n1.0.2.0,1.0.2.255,HK\n1.0.4.0,1.0.4.255,ZZ\n2001:4860::,2001:4860::ffff,US\n2001:4861::,2001:4861::ffff,CN\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ address, country string }{
		{"1.0.1.0", "CN"}, {"1.0.1.255", "CN"}, {"1.0.2.0", "HK"}, {"1.0.2.255", "HK"},
		{"2001:4860::", "US"}, {"2001:4860::ffff", "US"}, {"2001:4861::", "CN"},
	} {
		got, err := db.lookup(netip.MustParseAddr(tt.address))
		if err != nil || got != tt.country {
			t.Errorf("boundary %s: got %q, %v; want %q", tt.address, got, err, tt.country)
		}
	}
	for _, address := range []string{"1.0.0.255", "1.0.3.0", "1.0.4.1", "1.0.5.0", "2001:4859::1", "2001:4860::1:0", "2001:4862::"} {
		got, err := db.lookup(netip.MustParseAddr(address))
		if got != "" || !errors.Is(err, ErrUnknownCountry) {
			t.Errorf("gap %s: got %q, %v; want unknown country", address, got, err)
		}
	}
}

func TestRejectCorruptDatabase(t *testing.T) {
	valid := "1.0.1.0,1.0.1.255,CN\n2001:4860::,2001:4860::ffff,US\n"
	for name, source := range map[string]string{
		"missing_ipv6":     "1.0.1.0,1.0.1.255,CN\n",
		"bad_country":      "1.0.1.0,1.0.1.255,cn\n",
		"reversed":         "1.0.1.255,1.0.1.0,CN\n",
		"mixed_family":     "1.0.1.0,2001:4860::,CN\n",
		"invalid_ip":       "bad,1.0.1.255,CN\n",
		"invalid_csv":      "1.0.1.0,1.0.1.255,CN,extra\n",
		"overlapping":      "1.0.1.0,1.0.1.255,CN\n1.0.1.255,1.0.2.255,HK\n",
		"unsorted":         "1.0.2.0,1.0.2.255,HK\n1.0.1.0,1.0.1.255,CN\n",
		"overlapping_ipv6": "2001:4860::,2001:4860::ffff,CN\n2001:4860::ffff,2001:4861::,HK\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readDatabase(testGzip(t, source)); err == nil {
				t.Fatal("accepted corrupt database")
			}
		})
	}
	compressed := testGzip(t, valid)
	compressed[len(compressed)-8] ^= 0xff
	if _, err := readDatabase(compressed); err == nil {
		t.Fatal("accepted invalid gzip checksum")
	}
}

func TestConcurrentLookup(t *testing.T) {
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				code, err := Lookup("2400:3200::1")
				if err != nil || code != "CN" {
					t.Errorf("concurrent lookup = %q, %v", code, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func testGzip(t *testing.T, source string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write([]byte(source)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
