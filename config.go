package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

const version = "2.0.0-go"

var shanghai = time.FixedZone("CST", 8*60*60)

type Config struct {
	Addr, DataDir, SecretKey, AdminPassword, PublicBaseURL string
	ActivityTitle, DefaultDeadline                         string
	MaxUploadBytes                                         int64
	MaxConcurrentUploads, MaxQueuedUploads                 int
	MinFreeBytes                                           int64
	UploadTimeout                                          time.Duration
	UploadBytesPerSecond, DownloadBytesPerSecond           int64
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
	base, err := url.Parse(c.PublicBaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.Path != "" || base.RawQuery != "" || base.Fragment != "" {
		return c, fmt.Errorf("PUBLIC_BASE_URL 必须是完整的网站源地址，例如 http://noel-psc.cn:8000")
	}
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
