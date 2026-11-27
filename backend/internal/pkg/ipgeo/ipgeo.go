// Package ipgeo 使用随程序提供的 DB-IP 国家数据离线识别网络地址归属。
package ipgeo

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strings"
	"sync"
)

const (
	// DatabaseSource 是数据提供者的正式名称，发布时保留来源署名。
	DatabaseSource = "DB-IP IP to Country Lite"
	// DatabaseSourceURL 提供数据库介绍、下载和许可信息。
	DatabaseSourceURL = "https://db-ip.com/db/download/ip-to-country-lite"
	// DatabaseVersion 标识内置数据版本，方便管理员核对与更新。
	DatabaseVersion = "2026-11"
	// AttributionURL 应与使用归属结果的页面中的数据署名一同保留。
	AttributionURL  = "https://db-ip.com/"
	databaseSHA256  = "e5c8459d638a272ece7f4d7cfa49dce18d91b4f403f6940ba9249bf0db3781fd"
	maxDecodedBytes = 64 << 20
	maxRecords      = 2_000_000
)

var (
	ErrInvalidIP           = errors.New("invalid IP address")
	ErrNonPublicIP         = errors.New("IP address is not publicly routable")
	ErrUnknownCountry      = errors.New("IP country is unknown")
	ErrDatabaseUnavailable = errors.New("IP country database is unavailable")
)

//go:embed data/dbip-country-lite-2026-11.csv.gz
var compressedDatabase []byte

type ipv4Range struct {
	first, last uint32
	country     [2]byte
}

type ipv6Range struct {
	first, last [16]byte
	country     [2]byte
}

// 加载后只读，所有查询均可并发调用。
type database struct {
	v4 []ipv4Range
	v6 []ipv6Range
}

var loadDefault = sync.OnceValues(func() (*database, error) {
	if digest := fmt.Sprintf("%x", sha256.Sum256(compressedDatabase)); digest != databaseSHA256 {
		return nil, fmt.Errorf("%w: checksum mismatch", ErrDatabaseUnavailable)
	}
	db, err := readDatabase(compressedDatabase)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDatabaseUnavailable, err)
	}
	return db, nil
})

// Ready 验证并预加载内置数据。失败时调用方不能把“无法识别”当成境外地址。
func Ready() error {
	_, err := loadDefault()
	return err
}

// Lookup 返回 DB-IP 的两位国家或地区代码，例如 CN、HK、MO、TW。
// 地址会先规范化 IPv4 映射表示；无效、非公网、未覆盖地址均返回明确错误。
// 查询不会向外部服务发送访客地址，也不会读取未经验证的转发头。
func Lookup(address string) (string, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(address))
	if err != nil || addr.Zone() != "" {
		return "", ErrInvalidIP
	}
	addr = addr.Unmap()
	if !isPublic(addr) {
		return "", ErrNonPublicIP
	}
	db, err := loadDefault()
	if err != nil {
		return "", err
	}
	return db.lookup(addr)
}

// IsPublic reports whether the address can have a public geographic location.
// 调用方仍需使用 Lookup 的结果决定地区策略，不能把 false 当成境外。
func IsPublic(address string) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(address))
	return err == nil && addr.Zone() == "" && isPublic(addr.Unmap())
}

var specialUsePrefixes = [...]netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("3fff::/20"),
}

var publicIPv6Prefix = netip.MustParsePrefix("2000::/3")

func isPublic(addr netip.Addr) bool {
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() {
		return false
	}
	// 当前公网单播分配位于 2000::/3；其它地址不能因数据库兜底范围获得国家代码。
	if addr.Is6() && !publicIPv6Prefix.Contains(addr) {
		return false
	}
	for _, prefix := range specialUsePrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func (db *database) lookup(addr netip.Addr) (string, error) {
	var code [2]byte
	if addr.Is4() {
		raw := addr.As4()
		value := binary.BigEndian.Uint32(raw[:])
		index := sort.Search(len(db.v4), func(i int) bool { return db.v4[i].last >= value })
		if index == len(db.v4) || db.v4[index].first > value {
			return "", ErrUnknownCountry
		}
		code = db.v4[index].country
	} else {
		value := addr.As16()
		index := sort.Search(len(db.v6), func(i int) bool { return bytes.Compare(db.v6[i].last[:], value[:]) >= 0 })
		if index == len(db.v6) || bytes.Compare(db.v6[index].first[:], value[:]) > 0 {
			return "", ErrUnknownCountry
		}
		code = db.v6[index].country
	}
	if code == [2]byte{'Z', 'Z'} {
		return "", ErrUnknownCountry
	}
	return string(code[:]), nil
}

func readDatabase(compressed []byte) (*database, error) {
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	limited := &io.LimitedReader{R: zr, N: maxDecodedBytes + 1}
	reader := csv.NewReader(limited)
	reader.FieldsPerRecord = 3
	reader.ReuseRecord = true
	db := &database{}
	for count := 0; ; count++ {
		row, err := reader.Read()
		if limited.N <= 0 {
			return nil, errors.New("decoded database exceeds size limit")
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("record %d: %w", count+1, err)
		}
		if count >= maxRecords {
			return nil, errors.New("database exceeds record limit")
		}
		first, firstErr := netip.ParseAddr(row[0])
		last, lastErr := netip.ParseAddr(row[1])
		if firstErr != nil || lastErr != nil || first.Zone() != "" || last.Zone() != "" || first.BitLen() != last.BitLen() || first.Compare(last) > 0 {
			return nil, fmt.Errorf("record %d: invalid address range", count+1)
		}
		if len(row[2]) != 2 || row[2][0] < 'A' || row[2][0] > 'Z' || row[2][1] < 'A' || row[2][1] > 'Z' {
			return nil, fmt.Errorf("record %d: invalid country code", count+1)
		}
		code := [2]byte{row[2][0], row[2][1]}
		if first.Is4() {
			start, end := first.As4(), last.As4()
			rangeEntry := ipv4Range{binary.BigEndian.Uint32(start[:]), binary.BigEndian.Uint32(end[:]), code}
			if n := len(db.v4); n > 0 && db.v4[n-1].last >= rangeEntry.first {
				return nil, fmt.Errorf("record %d: overlapping or unsorted IPv4 range", count+1)
			}
			db.v4 = append(db.v4, rangeEntry)
		} else {
			rangeEntry := ipv6Range{first.As16(), last.As16(), code}
			if n := len(db.v6); n > 0 && bytes.Compare(db.v6[n-1].last[:], rangeEntry.first[:]) >= 0 {
				return nil, fmt.Errorf("record %d: overlapping or unsorted IPv6 range", count+1)
			}
			db.v6 = append(db.v6, rangeEntry)
		}
	}
	if len(db.v4) == 0 || len(db.v6) == 0 {
		return nil, errors.New("database must contain both IPv4 and IPv6 records")
	}
	return db, nil
}
