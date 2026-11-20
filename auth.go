package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (a *App) sign(kind, payload string) string {
	mac := hmac.New(sha256.New, []byte(a.cfg.SecretKey))
	mac.Write([]byte(kind + ":" + payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}
func (a *App) verify(kind, value string) (string, bool) {
	payload, signature, ok := strings.Cut(value, ".")
	if !ok {
		return "", false
	}
	expected := a.sign(kind, payload)
	_, wanted, _ := strings.Cut(expected, ".")
	return payload, hmac.Equal([]byte(signature), []byte(wanted))
}
func (a *App) cookie(w http.ResponseWriter, r *http.Request, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: strings.HasPrefix(a.cfg.PublicBaseURL, "https://") || a.secureRequest(r), MaxAge: age})
}
func (a *App) isTrustedProxy(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, network := range a.cfg.TrustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
func (a *App) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !a.isTrustedProxy(host) {
		return host
	}
	var forwarded []string
	for _, value := range r.Header.Values("X-Forwarded-For") {
		forwarded = append(forwarded, strings.Split(value, ",")...)
	}
	for i := len(forwarded) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(forwarded[i])
		if candidate == "" {
			continue
		}
		ip := net.ParseIP(candidate)
		if ip == nil {
			if host, _, splitErr := net.SplitHostPort(candidate); splitErr == nil {
				ip = net.ParseIP(host)
			}
		}
		if ip == nil || a.isTrustedProxy(ip.String()) {
			continue
		}
		return ip.String()
	}
	return host
}
func (a *App) secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !a.isTrustedProxy(host) {
		return false
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = proto[:i]
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}
func (a *App) csrfToken(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie("camp_csrf"); err == nil {
		if payload, valid := a.verify("csrf", c.Value); valid {
			parts := strings.Split(payload, "_")
			if len(parts) == 2 {
				expires, _ := strconv.ParseInt(parts[1], 10, 64)
				if expires > a.now().Unix() {
					return parts[0]
				}
			}
		}
	}
	token := randomID()
	payload := token + "_" + strconv.FormatInt(a.now().Add(24*time.Hour).Unix(), 10)
	a.cookie(w, r, "camp_csrf", a.sign("csrf", payload), 24*3600)
	return token
}
func (a *App) validCSRF(r *http.Request, token string) bool {
	c, err := r.Cookie("camp_csrf")
	if err != nil {
		return false
	}
	payload, valid := a.verify("csrf", c.Value)
	if !valid {
		return false
	}
	parts := strings.Split(payload, "_")
	if len(parts) != 2 {
		return false
	}
	expires, _ := strconv.ParseInt(parts[1], 10, 64)
	if expires <= a.now().Unix() || len(token) == 0 || subtle.ConstantTimeCompare([]byte(token), []byte(parts[0])) != 1 {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	return true
}
func (a *App) isAdmin(r *http.Request) bool {
	c, err := r.Cookie("camp_admin")
	if err != nil {
		return false
	}
	payload, valid := a.verify("admin", c.Value)
	if !valid {
		return false
	}
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return false
	}
	parts := strings.Split(string(data), "|")
	if len(parts) != 3 {
		return false
	}
	expires, _ := strconv.ParseInt(parts[0], 10, 64)
	// Password changes invalidate existing sessions without storing plaintext credentials in cookies.
	passwordHash := sha256.Sum256([]byte(a.cfg.AdminPassword))
	return expires > a.now().Unix() && hmac.Equal([]byte(parts[2]), []byte(hex.EncodeToString(passwordHash[:])))
}
func (a *App) adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.isAdmin(r) {
			if wantsJSON(r) {
				a.fail(w, r, 401, "登录已失效，请重新登录")
			} else {
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			}
			return
		}
		next(w, r)
	}
}
func (a *App) parseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Second))
	if err := r.ParseForm(); err != nil {
		a.fail(w, r, 400, "表单内容过大或格式不正确")
		return false
	}
	if !a.validCSRF(r, r.FormValue("csrf")) {
		a.fail(w, r, 403, "页面已过期，请刷新后重试")
		return false
	}
	return true
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if a.isAdmin(r) {
			http.Redirect(w, r, "/admin", 303)
			return
		}
		a.render(w, r, "login", Page{Title: "管理入口 · SWEET少年"}, 200)
		return
	}
	if !a.parseForm(w, r) {
		return
	}
	ip := a.clientIP(r)
	a.loginMu.Lock()
	attempt := a.attempts[ip]
	now := a.now()
	if attempt.Until.Before(now) {
		attempt = loginAttempt{Until: now.Add(15 * time.Minute)}
	}
	if attempt.Failures >= 8 {
		a.loginMu.Unlock()
		w.Header().Set("Retry-After", "900")
		a.render(w, r, "login", Page{Error: "尝试次数较多，请 15 分钟后再试"}, 429)
		return
	}
	expected, provided := sha256.Sum256([]byte(a.cfg.AdminPassword)), sha256.Sum256([]byte(r.FormValue("password")))
	if subtle.ConstantTimeCompare(expected[:], provided[:]) != 1 {
		attempt.Failures++
		if len(a.attempts) >= 1024 {
			for key, v := range a.attempts {
				if v.Until.Before(now) {
					delete(a.attempts, key)
				}
			}
		}
		if _, exists := a.attempts[ip]; !exists && len(a.attempts) >= 1024 {
			oldestKey, oldest := "", time.Time{}
			for key, v := range a.attempts {
				if oldestKey == "" || v.Until.Before(oldest) {
					oldestKey, oldest = key, v.Until
				}
			}
			delete(a.attempts, oldestKey)
		}
		a.attempts[ip] = attempt
		a.loginMu.Unlock()
		a.render(w, r, "login", Page{Error: "密码不正确，请检查后重试"}, 401)
		return
	}
	delete(a.attempts, ip)
	a.loginMu.Unlock()
	payload := fmt.Sprintf("%d|%s|%s", now.Add(12*time.Hour).Unix(), randomID(), hex.EncodeToString(expected[:]))
	a.cookie(w, r, "camp_admin", a.sign("admin", base64.RawURLEncoding.EncodeToString([]byte(payload))), 12*3600)
	http.Redirect(w, r, "/admin", 303)
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if !a.parseForm(w, r) {
		return
	}
	a.cookie(w, r, "camp_admin", "", -1)
	http.Redirect(w, r, "/admin/login", 303)
}
