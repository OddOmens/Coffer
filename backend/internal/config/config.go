// Package config loads Coffer's runtime configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Listen is the address of the full app (login, file manager, admin).
	Listen string
	// PublicListen optionally serves share links only. Expose this one to the
	// internet and keep Listen on the LAN / VPN.
	PublicListen string
	// PublicURL is the base URL used when building share links.
	PublicURL string

	DataDir    string
	StorageDir string
	// SecretKey overrides the generated key file (base64 or hex, >= 32 bytes).
	SecretKey string

	// TrustedProxies are the peers allowed to set X-Forwarded-For,
	// X-Forwarded-Proto and CF-Connecting-IP on the share listener
	// (PublicListen), such as a tunnel container.
	TrustedProxies []netip.Prefix
	// AppTrustedProxies are the peers allowed to set them on the app
	// listener (Listen): a reverse proxy in front of the full app.
	AppTrustedProxies []netip.Prefix
	// AppNets, when set, restricts the full app to clients in these networks.
	// Everyone else only sees share links.
	AppNets []netip.Prefix

	AdminUser     string
	AdminPassword string

	// DriveRoots are the only directories (and their sub-folders) an admin
	// may add as local drives.
	DriveRoots []string
	// UpdateRepo is the GitHub repository checked for releases.
	UpdateRepo     string
	UpdatesAllowed bool
	// MetricsToken enables /metrics (Prometheus) behind this bearer token.
	MetricsToken string

	SessionIdle time.Duration
	SessionMax  time.Duration

	LogLevel slog.Level
	LogJSON  bool
	Umask    int
}

func Load() (*Config, error) {
	c := &Config{
		Listen:         env("COFFER_LISTEN", ":8080"),
		PublicListen:   env("COFFER_PUBLIC_LISTEN", ""),
		PublicURL:      strings.TrimRight(env("COFFER_PUBLIC_URL", ""), "/"),
		DataDir:        env("COFFER_DATA_DIR", "/data"),
		StorageDir:     env("COFFER_STORAGE_DIR", "/srv/files"),
		SecretKey:      env("COFFER_SECRET_KEY", ""),
		AdminUser:      env("COFFER_ADMIN_USER", "admin"),
		AdminPassword:  env("COFFER_ADMIN_PASSWORD", ""),
		LogJSON:        env("COFFER_LOG_FORMAT", "text") == "json",
		UpdateRepo:     env("COFFER_UPDATE_REPO", "OddOmens/Coffer"),
		UpdatesAllowed: env("COFFER_UPDATE_CHECK", "true") != "false",
		MetricsToken:   env("COFFER_METRICS_TOKEN", ""),
	}
	for _, r := range strings.Split(env("COFFER_DRIVE_ROOTS", "/srv"), ",") {
		if r = strings.TrimRight(strings.TrimSpace(r), "/"); r != "" {
			c.DriveRoots = append(c.DriveRoots, r)
		}
	}
	var errs []error
	var err error

	if c.TrustedProxies, err = prefixes(env("COFFER_TRUSTED_PROXIES", "")); err != nil {
		errs = append(errs, fmt.Errorf("COFFER_TRUSTED_PROXIES: %w", err))
	}
	if c.AppTrustedProxies, err = prefixes(env("COFFER_APP_TRUSTED_PROXIES", "")); err != nil {
		errs = append(errs, fmt.Errorf("COFFER_APP_TRUSTED_PROXIES: %w", err))
	}
	if c.AppNets, err = prefixes(env("COFFER_APP_NETWORKS", "")); err != nil {
		errs = append(errs, fmt.Errorf("COFFER_APP_NETWORKS: %w", err))
	}
	if c.SessionIdle, err = duration("COFFER_SESSION_IDLE", 7*24*time.Hour); err != nil {
		errs = append(errs, err)
	}
	if c.SessionMax, err = duration("COFFER_SESSION_MAX", 30*24*time.Hour); err != nil {
		errs = append(errs, err)
	}
	if err := c.LogLevel.UnmarshalText([]byte(env("COFFER_LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("COFFER_LOG_LEVEL: %w", err))
	}
	umask, err := strconv.ParseInt(env("COFFER_UMASK", "002"), 8, 32)
	if err != nil || umask < 0 || umask > 0o777 {
		errs = append(errs, errors.New("COFFER_UMASK must be an octal value such as 002"))
	}
	c.Umask = int(umask)

	if c.PublicURL != "" {
		u, err := url.Parse(c.PublicURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			errs = append(errs, errors.New("COFFER_PUBLIC_URL must be an absolute http(s) URL"))
		}
	}
	if c.SessionIdle > c.SessionMax {
		c.SessionIdle = c.SessionMax
	}
	return c, errors.Join(errs...)
}

// AppAllowed reports whether ip may use the full app.
func (c *Config) AppAllowed(ip netip.Addr) bool {
	if len(c.AppNets) == 0 {
		return true
	}
	return contains(c.AppNets, ip)
}

// IsTrustedProxy reports whether ip may set forwarding headers on the share
// listener.
func (c *Config) IsTrustedProxy(ip netip.Addr) bool {
	return contains(c.TrustedProxies, ip)
}

// IsAppProxy reports whether ip may set forwarding headers on the app
// listener.
func (c *Config) IsAppProxy(ip netip.Addr) bool {
	return contains(c.AppTrustedProxies, ip)
}

func contains(ps []netip.Prefix, ip netip.Addr) bool {
	// Prefix.Contains is always false for a zoned address (fe80::1%eth0).
	ip = ip.Unmap().WithZone("")
	for _, p := range ps {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func duration(key string, def time.Duration) (time.Duration, error) {
	v := env(key, "")
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s: invalid duration %q (use e.g. 168h)", key, v)
	}
	return d, nil
}

// prefixes parses a comma separated list of CIDRs or bare IPs.
func prefixes(v string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }) {
		if strings.Contains(f, "/") {
			p, err := netip.ParsePrefix(f)
			if err != nil {
				return nil, err
			}
			// Client addresses are unmapped, so ::ffff:10.0.0.0/104 must
			// become 10.0.0.0/8 to ever match.
			if a := p.Addr(); a.Is4In6() {
				if p.Bits() < 96 {
					return nil, fmt.Errorf("%s: an IPv4-mapped prefix needs at least /96", f)
				}
				p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return nil, err
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}
