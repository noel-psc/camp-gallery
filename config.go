package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

const version = "2.1.0-go"

var shanghai = time.FixedZone("CST", 8*60*60)

type Config struct {
	Addr, DataDir, SecretKey, AdminPassword, PublicBaseURL string
	ActivityTitle, DefaultDeadline                         string
	MaxUploadBytes                                         int64
	MaxConcurrentUploads, MaxQueuedUploads                 int
	MinFreeBytes                                           int64
	UploadTimeout                                          time.Duration
	UploadBytesPerSecond, DownloadBytesPerSecond           int64
	TrustedProxies                                         []*net.IPNet
	RequireHTTPS, AllowInsecureHTTP                        bool
	HSTSMaxAge                                             int64
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(scanner.Text()), "export "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf(".env 中存在无效配置行")
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if strings.ContainsAny(key, " \t\x00") || key == "" {
			return fmt.Errorf(".env 配置名无效")
		}
		if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
			value = value[1 : len(value)-1]
		} else if strings.HasPrefix(value, "\"") {
			value, err = strconv.Unquote(value)
			if err != nil {
				return fmt.Errorf(".env 的 %s 引号无效", key)
			}
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback, low, high int64) (int64, error) {
	value, err := strconv.ParseInt(env(key, strconv.FormatInt(fallback, 10)), 10, 64)
	if err != nil || value < low || value > high {
		return 0, fmt.Errorf("%s 必须在 %d–%d 之间", key, low, high)
	}
	return value, nil
}

func envBool(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("%s 必须为 1/0、true/false、yes/no 或 on/off", key)
}

func parseTrustedProxies(raw string) ([]*net.IPNet, error) {
	var networks []*net.IPNet
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(item); err == nil {
			networks = append(networks, network)
			continue
		}
		ip := net.ParseIP(item)
		if ip == nil {
			return nil, fmt.Errorf("TRUSTED_PROXIES 中的 %q 不是有效 IP 或 CIDR", item)
		}
		bits := 128
		if v4 := ip.To4(); v4 != nil {
			ip, bits = v4, 32
		}
		networks = append(networks, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	return networks, nil
}

func loadConfig() (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, err
	}
	c := Config{Addr: env("LISTEN_ADDR", ":8000"), DataDir: env("DATA_DIR", "data"), SecretKey: os.Getenv("SECRET_KEY"), AdminPassword: os.Getenv("ADMIN_PASSWORD"), PublicBaseURL: strings.TrimRight(env("PUBLIC_BASE_URL", "http://127.0.0.1:8000"), "/"), ActivityTitle: env("ACTIVITY_TITLE", "心纹研学营"), DefaultDeadline: env("UPLOAD_DEADLINE", "2026-11-19T23:59:59+08:00")}
	if c.AdminPassword == "" {
		return c, fmt.Errorf("请设置 ADMIN_PASSWORD，可复制 .env.example 为 .env 后填写")
	}
	if c.SecretKey == "" {
		c.SecretKey = randomID() + randomID()
	}
	allowInsecure, err := envBool("ALLOW_INSECURE_HTTP", false)
	if err != nil {
		return c, err
	}
	c.AllowInsecureHTTP = allowInsecure
	proxies, err := parseTrustedProxies(os.Getenv("TRUSTED_PROXIES"))
	if err != nil {
		return c, err
	}
	c.TrustedProxies = proxies
	base, err := url.Parse(c.PublicBaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.Path != "" || base.RawQuery != "" || base.Fragment != "" {
		return c, fmt.Errorf("PUBLIC_BASE_URL 必须是完整的网站源地址，例如 https://noel-psc.cn")
	}
	// Backward compatible: HTTP is allowed unless the operator opts into a
	// trusted TLS proxy (https + TRUSTED_PROXIES) and does not force it off.
	if base.Scheme == "https" && len(c.TrustedProxies) > 0 && !c.AllowInsecureHTTP {
		c.RequireHTTPS = true
	}
	hsts, err := envInt("HSTS_MAX_AGE", 0, 0, 63072000)
	if err != nil {
		return c, err
	}
	c.HSTSMaxAge = hsts
	if _, err := time.Parse(time.RFC3339, c.DefaultDeadline); err != nil {
		return c, fmt.Errorf("UPLOAD_DEADLINE 必须是带时区的 ISO 8601 时间")
	}
	mb, err := envInt("MAX_UPLOAD_MB", 200, 1, 2048)
	if err != nil {
		return c, err
	}
	c.MaxUploadBytes = mb << 20
	concurrent, err := envInt("MAX_CONCURRENT_UPLOADS", 8, 1, 64)
	if err != nil {
		return c, err
	}
	c.MaxConcurrentUploads = int(concurrent)
	queued, err := envInt("MAX_QUEUED_UPLOADS", 128, 0, 1000)
	if err != nil {
		return c, err
	}
	c.MaxQueuedUploads = int(queued)
	free, err := envInt("MIN_FREE_DISK_MB", 512, 0, 1<<20)
	if err != nil {
		return c, err
	}
	c.MinFreeBytes = free << 20
	seconds, err := envInt("UPLOAD_TIMEOUT_SECONDS", 7200, 30, 7200)
	if err != nil {
		return c, err
	}
	c.UploadTimeout = time.Duration(seconds) * time.Second
	for _, setting := range []struct {
		name     string
		fallback int64
		target   *int64
	}{{"UPLOAD_BANDWIDTH_MBPS", 90, &c.UploadBytesPerSecond}, {"DOWNLOAD_BANDWIDTH_MBPS", 80, &c.DownloadBytesPerSecond}} {
		mbps, err := envInt(setting.name, setting.fallback, 0, 10000)
		if err != nil {
			return c, err
		}
		*setting.target = mbps * 1000000 / 8
	}
	return c, nil
}

func randomID() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
