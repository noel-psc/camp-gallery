package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed templates/*.html static/*
var assets embed.FS

var numberPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,32}$`)
var requestPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,100}$`)

type loginAttempt struct {
	Failures int
	Until    time.Time
}
type App struct {
	cfg                                   Config
	db                                    *sql.DB
	files                                 *os.Root
	mux                                   *http.ServeMux
	views                                 map[string]*template.Template
	logger                                *slog.Logger
	now                                   func() time.Time
	uploadSlots, uploadQueue, exportSlots chan struct{}
	activeUploads                         atomic.Int64
	uploadBudget, downloadBudget          *byteBudget
	diskMu                                sync.Mutex
	reserved                              int64
	loginMu                               sync.Mutex
	attempts                              map[string]loginAttempt
}

func NewApp(c Config, logger *slog.Logger) (*App, error) {
	db, backup, err := openStore(c)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Join(c.DataDir, "uploads"))
	if err != nil {
		db.Close()
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	a := &App{cfg: c, db: db, files: root, logger: logger, now: time.Now, views: map[string]*template.Template{}, mux: http.NewServeMux(), uploadSlots: make(chan struct{}, c.MaxConcurrentUploads), uploadQueue: make(chan struct{}, c.MaxConcurrentUploads+c.MaxQueuedUploads), attempts: map[string]loginAttempt{}, exportSlots: make(chan struct{}, 1)}
	a.uploadBudget = newByteBudget(c.UploadBytesPerSecond)
	a.downloadBudget = newByteBudget(c.DownloadBytesPerSecond)
	funcs := template.FuncMap{
		"asset": func(name string) string {
			data, _ := assets.ReadFile("static/" + name)
			return fmt.Sprintf("/static/%s?v=%x", name, sha256.Sum256(data))
		},
		"spaceURL": func(s Student) string { return "/s/" + url.PathEscape(s.Token) },
		"mediaURL": func(token string, work Work) string {
			return fmt.Sprintf("/s/%s/file/%d?v=%s", url.PathEscape(token), work.ID, url.QueryEscape(work.Filename))
		},
		"qrURL":       func(token string) string { return "/admin/qr/" + url.PathEscape(token) },
		"activityURL": func(path string, id int64) string { return fmt.Sprintf("%s?activity=%d", path, id) },
		"statusLabel": func(activity Activity) string {
			if activity.Archived {
				return "已归档"
			}
			if activity.Editable(a.now()) {
				return "开放上传"
			}
			return "已截止"
		},
		"dateLabel": func(value string) string {
			t, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return value
			}
			return t.In(shanghai).Format("01月02日 15:04")
		},
	}
	// Each page owns its content definition while sharing the same shell.
	funcs["isOpen"] = func(activity Activity) bool { return activity.Editable(a.now()) }
	for _, name := range []string{"home", "space", "admin", "activities", "student", "login", "error"} {
		t, err := template.New("base.html").Funcs(funcs).ParseFS(assets, "templates/base.html", "templates/"+name+".html")
		if err != nil {
			a.Close()
			return nil, err
		}
		a.views[name] = t
	}
	cards, err := template.New("cards.html").Funcs(funcs).ParseFS(assets, "templates/cards.html")
	if err != nil {
		a.Close()
		return nil, err
	}
	a.views["cards"] = cards
	static, err := fs.Sub(assets, "static")
	if err != nil {
		a.Close()
		return nil, err
	}
	a.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	a.mux.HandleFunc("GET /{$}", a.home)
	a.mux.HandleFunc("GET /healthz", a.health)
	a.mux.HandleFunc("GET /s/{token}", a.space)
	a.mux.HandleFunc("POST /s/{token}", a.upload)
	a.mux.HandleFunc("GET /s/{token}/file/{id}", a.media)
	a.mux.HandleFunc("POST /s/{token}/work/{id}", a.editWork)
	a.mux.HandleFunc("GET /admin/login", a.login)
	a.mux.HandleFunc("POST /admin/login", a.login)
	a.mux.HandleFunc("GET /admin", a.adminOnly(a.admin))
	a.mux.HandleFunc("POST /admin", a.adminOnly(a.admin))
	a.mux.HandleFunc("POST /admin/logout", a.adminOnly(a.logout))
	a.mux.HandleFunc("GET /admin/activities", a.adminOnly(a.activities))
	a.mux.HandleFunc("POST /admin/activities", a.adminOnly(a.activities))
	a.mux.HandleFunc("POST /admin/activities/{id}", a.adminOnly(a.updateActivity))
	a.mux.HandleFunc("GET /admin/students/{token}", a.adminOnly(a.manageStudent))
	a.mux.HandleFunc("POST /admin/students/{token}", a.adminOnly(a.manageStudent))
	a.mux.HandleFunc("GET /admin/cards", a.adminOnly(a.cards))
	a.mux.HandleFunc("GET /admin/qr/{token}", a.adminOnly(a.qr))
	a.mux.HandleFunc("GET /admin/export", a.adminOnly(a.exportWorks))
	if backup != "" {
		a.logger.Info("legacy database migrated", "backup", backup)
	}
	a.cleanStaging()
	return a, nil
}

func (a *App) Close() error { a.files.Close(); return a.db.Close() }
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' blob: data:; media-src 'self' blob:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	if strings.HasPrefix(r.URL.Path, "/static/") {
		w.Header().Set("Cache-Control", "public, max-age=86400")
	} else {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	}
	defer func() {
		if problem := recover(); problem != nil {
			a.logger.Error("request panic", "method", r.Method, "panic", problem)
			a.fail(w, r, 500, "服务暂时不可用，请稍后重试")
		}
	}()
	if a.cfg.RequireHTTPS {
		if a.secureRequest(r) {
			if a.cfg.HSTSMaxAge > 0 {
				w.Header().Set("Strict-Transport-Security", fmt.Sprintf("max-age=%d", a.cfg.HSTSMaxAge))
			}
		} else if r.URL.Path != "/healthz" {
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				http.Redirect(w, r, a.cfg.PublicBaseURL+r.URL.RequestURI(), http.StatusPermanentRedirect)
			} else {
				a.fail(w, r, http.StatusBadRequest, "请通过 HTTPS 访问")
			}
			return
		}
	}
	a.mux.ServeHTTP(w, r)
}
func (a *App) render(w http.ResponseWriter, r *http.Request, name string, page Page, code int) {
	page.CSRF = a.csrfToken(w, r)
	page.MaxUploadMB = a.cfg.MaxUploadBytes >> 20
	page.UploadTimeoutMS = (a.cfg.UploadTimeout + time.Minute).Milliseconds()
	page.IsAdmin = a.isAdmin(r)
	if page.Title == "" {
		page.Title = "SWEET少年的创想空间"
	}
	if page.Notice == "" {
		page.Notice = truncate(r.URL.Query().Get("notice"), 200)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	entry := "base.html"
	if name == "cards" {
		entry = "cards.html"
	}
	if err := a.views[name].ExecuteTemplate(w, entry, page); err != nil {
		a.logger.Error("render failed", "page", name, "error", err)
	}
}
func (a *App) fail(w http.ResponseWriter, r *http.Request, code int, message string) {
	if wantsJSON(r) {
		writeJSON(w, code, map[string]any{"error": message})
		return
	}
	a.render(w, r, "error", Page{Title: "请稍后再试 · SWEET少年", Error: message}, code)
}
func (a *App) internal(w http.ResponseWriter, r *http.Request, err error) {
	a.logger.Error("operation failed", "method", r.Method, "error", err)
	a.fail(w, r, 500, "服务暂时未能完成操作，请稍后重试")
}
func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n])
	}
	return string(r)
}
func parseID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("无效编号")
	}
	return id, nil
}
func (a *App) home(w http.ResponseWriter, r *http.Request) { a.render(w, r, "home", Page{}, 200) }
func (a *App) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := a.db.PingContext(ctx); err != nil {
		writeJSON(w, 503, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok", "version": version})
}
