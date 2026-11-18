package main

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var tinyPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg==")
var csrfPattern = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

type testSite struct {
	app    *App
	server *httptest.Server
	client *http.Client
	csrf   string
}

func testConfig(dir string) Config {
	return Config{DataDir: dir, SecretKey: "test-secret", AdminPassword: "test-password", PublicBaseURL: "http://camp.example.test:8000", ActivityTitle: "旧研学营", DefaultDeadline: "2099-11-19T23:59:59+08:00", MaxUploadBytes: 8 << 20, MaxConcurrentUploads: 4, MaxQueuedUploads: 128, UploadTimeout: 30 * time.Second}
}
func newTestSite(t *testing.T) *testSite {
	t.Helper()
	app, err := NewApp(testConfig(t.TempDir()), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 15 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	site := &testSite{app: app, server: server, client: client}
	body := site.get(t, "/admin/login", 200)
	match := csrfPattern.FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatal("CSRF token not rendered")
	}
	site.csrf = match[1]
	return site
}
func (s *testSite) get(t *testing.T, path string, status int) string {
	t.Helper()
	response, err := s.client.Get(s.server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != status {
		t.Fatalf("GET %s: %d, wanted %d: %s", path, response.StatusCode, status, body)
	}
	return string(body)
}
func (s *testSite) post(t *testing.T, path string, values url.Values, status int) string {
	t.Helper()
	if values == nil {
		values = url.Values{}
	}
	values.Set("csrf", s.csrf)
	response, err := s.client.PostForm(s.server.URL+path, values)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != status {
		t.Fatalf("POST %s: %d, wanted %d: %s", path, response.StatusCode, status, body)
	}
	return string(body)
}
func (s *testSite) login(t *testing.T) {
	t.Helper()
	s.post(t, "/admin/login", url.Values{"password": {"test-password"}}, 303)
}
func (s *testSite) seed(t *testing.T, n int) {
	t.Helper()
	tx, err := s.app.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < n; i++ {
		if _, err := tx.Exec("INSERT INTO students (activity_id,class_name,name,student_number,token) VALUES (1,'一班',?,?,?)", fmt.Sprintf("学生%d", i), fmt.Sprintf("%03d", i), fmt.Sprintf("token-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func multipartBody(csrf, id, title, filename string, data []byte) ([]byte, string) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	writer.WriteField("csrf", csrf)
	writer.WriteField("request_id", id)
	writer.WriteField("title", title)
	part, _ := writer.CreateFormFile("files", filename)
	part.Write(data)
	writer.Close()
	return buffer.Bytes(), writer.FormDataContentType()
}
func (s *testSite) upload(t *testing.T, token, id, title, filename string, data []byte, status int) string {
	t.Helper()
	body, typ := multipartBody(s.csrf, id, title, filename, data)
	request, _ := http.NewRequest("POST", s.server.URL+"/s/"+token, bytes.NewReader(body))
	request.Header.Set("Content-Type", typ)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Upload-ID", id)
	response, err := s.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	output, _ := io.ReadAll(response.Body)
	if response.StatusCode != status {
		t.Fatalf("upload returned %d, wanted %d: %s", response.StatusCode, status, output)
	}
	return string(output)
}
func (s *testSite) workCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := s.app.db.QueryRow("SELECT count(*) FROM works").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func (s *testSite) assertClean(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(s.app.cfg.DataDir, "uploads", ".staging"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary files leaked: %v", entries)
	}
}

func TestLegacyMigrationKeepsTokensFilesAndCreatesBackup(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "uploads"), 0700)
	db, err := sql.Open("sqlite", filepath.Join(dir, "camp.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE students (id INTEGER PRIMARY KEY,class_name TEXT NOT NULL,name TEXT NOT NULL,token TEXT UNIQUE NOT NULL);CREATE TABLE works(id INTEGER PRIMARY KEY,student_id INTEGER NOT NULL,filename TEXT NOT NULL,original_name TEXT NOT NULL,title TEXT NOT NULL,created_at TEXT NOT NULL);INSERT INTO students VALUES(7,'旧一班','旧学生','original-printed-token');INSERT INTO works VALUES(9,7,'original.png','原图.png','旧作品','2026-11-13T10:00:00+08:00');`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	os.WriteFile(filepath.Join(dir, "uploads", "original.png"), tinyPNG, 0600)
	cfg := testConfig(dir)
	app, err := NewApp(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	child, err := getStudent(context.Background(), app.db, "original-printed-token")
	if err != nil || child.ID != 7 || child.ActivityID != 1 || child.StudentNumber != "" {
		t.Fatalf("legacy student changed: %+v %v", child, err)
	}
	server := httptest.NewServer(app)
	response, err := http.Get(server.URL + "/s/original-printed-token/file/9")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !bytes.Equal(data, tinyPNG) {
		t.Fatal("original work changed")
	}
	server.Close()
	app.Close()
	backups, _ := filepath.Glob(filepath.Join(dir, "backups", "*.sqlite3"))
	if len(backups) != 1 {
		t.Fatalf("expected migration backup, got %v", backups)
	}
	backup, _ := sql.Open("sqlite", backups[0])
	var tables int
	backup.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='activities'").Scan(&tables)
	backup.Close()
	if tables != 0 {
		t.Fatal("backup should contain pre-migration schema")
	}
	app, err = NewApp(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	app.Close()
	backups, _ = filepath.Glob(filepath.Join(dir, "backups", "*.sqlite3"))
	if len(backups) != 1 {
		t.Fatal("migration is not idempotent")
	}
}
func TestPagesAndSecurity(t *testing.T) {
	s := newTestSite(t)
	s.seed(t, 1)
	s.get(t, "/", 200)
	s.get(t, "/healthz", 200)
	s.get(t, "/static/upload.js", 200)
	s.get(t, "/admin", 303)
	body := s.get(t, "/s/token-0", 200)
	if !strings.Contains(body, "上传第一件作品") || !strings.Contains(body, "学生0") {
		t.Fatal("missing upload UI or identity")
	}
	request, _ := http.NewRequest("POST", s.server.URL+"/admin", strings.NewReader("class_name=1&names=001+A"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, _ := s.client.Do(request)
	response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatal("admin not protected")
	}
	s.login(t)
	s.get(t, "/admin", 200)
	s.get(t, "/admin/activities", 200)
	request, _ = http.NewRequest("POST", s.server.URL+"/admin", strings.NewReader("class_name=1&names=001+A"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, _ = s.client.Do(request)
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("missing CSRF was accepted")
	}
	request, _ = http.NewRequest("POST", s.server.URL+"/admin", strings.NewReader(url.Values{"csrf": {s.csrf}, "class_name": {"一班"}, "names": {"001 A"}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://evil.example")
	response, _ = s.client.Do(request)
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("cross-origin mutation was accepted")
	}
	s.get(t, "/admin/cards", 200)
	s.get(t, "/admin/cards?layout=labels", 200)
	s.get(t, "/admin/qr/token-0", 200)
}
func TestRosterIsolationAndStableTokens(t *testing.T) {
	s := newTestSite(t)
	s.login(t)
	values := url.Values{"class_name": {"一班"}, "names": {"001 小明\n002,小红"}}
	s.post(t, "/admin?activity=1", values, 303)
	children, _ := listStudents(context.Background(), s.app.db, 1)
	token := children[0].Token
	s.post(t, "/admin?activity=1", values, 303)
	children, _ = listStudents(context.Background(), s.app.db, 1)
	if len(children) != 2 || children[0].Token != token {
		t.Fatal("reimport changed printed QR")
	}
	s.post(t, "/admin?activity=1", url.Values{"class_name": {"一班"}, "names": {"003 新同学\n001 其他人"}}, 400)
	children, _ = listStudents(context.Background(), s.app.db, 1)
	if len(children) != 2 {
		t.Fatal("failed import partially persisted")
	}
	s.post(t, "/admin/activities", url.Values{"title": {"下一期活动"}, "deadline": {"2099-12-01T12:00"}}, 303)
	s.post(t, "/admin?activity=2", url.Values{"class_name": {"一班"}, "names": {"001 新学生"}}, 303)
	newChildren, _ := listStudents(context.Background(), s.app.db, 2)
	if len(newChildren) != 1 || newChildren[0].Token == token {
		t.Fatal("activities share spaces")
	}
	s.get(t, "/s/"+token, 200)
}
func TestUploadIdempotencyAndEditing(t *testing.T) {
	s := newTestSite(t)
	s.seed(t, 2)
	id := randomID()
	s.upload(t, "token-0", id, "我的创作", "图片.png", tinyPNG, 200)
	s.upload(t, "token-0", id, "我的创作", "图片.png", tinyPNG, 200)
	if s.workCount(t) != 1 {
		t.Fatal("retry duplicated work")
	}
	s.upload(t, "token-0", id, "改了名称", "图片.png", tinyPNG, 409)
	if s.workCount(t) != 1 {
		t.Fatal("conflicting retry mutated work")
	}
	s.assertClean(t)
	view := s.get(t, "/s/token-0", 200)
	if strings.Contains(view, "id=\"upload-form\"") || !strings.Contains(view, "编辑作品") {
		t.Fatal("default must show gallery")
	}
	s.get(t, "/s/token-0?edit=1", 200)
	works, _ := listWorks(context.Background(), s.app.db, 1)
	idPath := fmt.Sprintf("/s/token-0/work/%d", works[0].ID)
	s.post(t, fmt.Sprintf("/s/token-1/work/%d", works[0].ID), url.Values{"action": {"delete"}}, 404)
	s.post(t, idPath, url.Values{"action": {"save"}, "title": {"新名称"}, "work_version": {works[0].Filename}}, 303)
	s.get(t, "/s/token-0", 200)
	request, _ := http.NewRequest("GET", s.server.URL+fmt.Sprintf("/s/token-0/file/%d", works[0].ID), nil)
	request.Header.Set("Range", "bytes=0-7")
	response, err := s.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 206 || !bytes.Equal(data, tinyPNG[:8]) {
		t.Fatal("video/image range requests unsupported")
	}
	s.post(t, idPath, url.Values{"action": {"delete"}, "work_version": {works[0].Filename}}, 303)
	if s.workCount(t) != 0 {
		t.Fatal("delete failed")
	}
	if _, err := os.Stat(filepath.Join(s.app.cfg.DataDir, "uploads", works[0].Filename)); !os.IsNotExist(err) {
		t.Fatal("deleted file leaked")
	}
}
func TestUploadValidationCleanupAndLimits(t *testing.T) {
	s := newTestSite(t)
	s.seed(t, 1)
	s.upload(t, "token-0", randomID(), "", "假图片.png", []byte("<script>alert(1)</script>"), 400)
	s.upload(t, "token-0", randomID(), "", "程序.txt", tinyPNG, 400)
	csrf := s.csrf
	s.csrf = "invalid"
	s.upload(t, "token-0", randomID(), "", "图片.png", tinyPNG, 403)
	s.csrf = csrf
	big := append(append([]byte{}, tinyPNG...), make([]byte, 8<<20)...)
	s.upload(t, "token-0", randomID(), "", "大图.png", big, 413)
	if s.workCount(t) != 0 {
		t.Fatal("invalid uploads persisted")
	}
	s.assertClean(t)
	body, typ := multipartBody(s.csrf, randomID(), "", "图.png", tinyPNG)
	body = body[:len(body)-20]
	request, _ := http.NewRequest("POST", s.server.URL+"/s/token-0", bytes.NewReader(body))
	request.Header.Set("Content-Type", typ)
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("truncated multipart accepted")
	}
	s.assertClean(t)
	s.app.cfg.MinFreeBytes = 1 << 62
	s.upload(t, "token-0", randomID(), "", "图.png", tinyPNG, 507)
	s.assertClean(t)
}
func TestDeadlineAndArchive(t *testing.T) {
	s := newTestSite(t)
	s.seed(t, 1)
	s.upload(t, "token-0", randomID(), "", "图.png", tinyPNG, 200)
	s.login(t)
	s.post(t, "/admin/activities/1", url.Values{"action": {"archive"}}, 303)
	s.upload(t, "token-0", randomID(), "", "图.png", tinyPNG, 403)
	page := s.get(t, "/s/token-0?edit=1", 200)
	if strings.Contains(page, "upload-form") {
		t.Fatal("archived activity permits editing")
	}
	s.post(t, "/admin/activities/1", url.Values{"action": {"restore"}}, 303)
	s.post(t, "/admin/activities/1", url.Values{"action": {"save"}, "title": {"旧活动"}, "deadline": {"2000-01-01T00:00"}}, 303)
	s.upload(t, "token-0", randomID(), "", "图.png", tinyPNG, 403)
	s.get(t, "/s/token-0", 200)
}
func TestExportOriginalBytesAndSafePaths(t *testing.T) {
	s := newTestSite(t)
	s.seed(t, 2)
	s.upload(t, "token-0", randomID(), "原始作品", "原图.png", tinyPNG, 200)
	s.login(t)
	s.app.db.Exec("UPDATE students SET class_name='../一班',name='../../名字' WHERE id=1")
	response, err := s.client.Get(s.server.URL + "/admin/export?activity=1")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode, string(data))
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	photos := 0
	manifest := ""
	for _, entry := range archive.File {
		if strings.HasPrefix(entry.Name, "/") || strings.Contains(entry.Name, "/../") {
			t.Fatal("unsafe archive path")
		}
		file, _ := entry.Open()
		contents, _ := io.ReadAll(file)
		file.Close()
		if strings.HasSuffix(entry.Name, ".png") {
			photos++
			if !bytes.Equal(contents, tinyPNG) {
				t.Fatal("original bytes changed")
			}
		}
		if entry.Name == "作品清单.csv" {
			manifest = string(contents)
		}
	}
	if photos != 1 || !strings.Contains(manifest, "尚未上传") || !strings.Contains(manifest, "旧研学营") {
		t.Fatalf("incomplete export: %d %s", photos, manifest)
	}
}
func TestTwoSlowUploadsDoNotBlockPages(t *testing.T) {
	s := newTestSite(t)
	s.seed(t, 3)
	release := make(chan struct{})
	var wg sync.WaitGroup
	var statuses [2]int
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body, typ := multipartBody(s.csrf, randomID(), "", "图.png", append(append([]byte{}, tinyPNG...), make([]byte, 128<<10)...))
			reader, writer := io.Pipe()
			request, _ := http.NewRequest("POST", fmt.Sprintf("%s/s/token-%d", s.server.URL, i), reader)
			request.ContentLength = int64(len(body))
			request.Header.Set("Content-Type", typ)
			request.Header.Set("Accept", "application/json")
			go func() { defer writer.Close(); writer.Write(body[:4096]); <-release; writer.Write(body[4096:]) }()
			response, err := s.client.Do(request)
			if err == nil {
				statuses[i] = response.StatusCode
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
			}
		}(i)
	}
	deadline := time.Now().Add(3 * time.Second)
	for s.app.activeUploads.Load() != 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	active := s.app.activeUploads.Load()
	start := time.Now()
	s.get(t, "/healthz", 200)
	s.get(t, "/", 200)
	elapsed := time.Since(start)
	close(release)
	wg.Wait()
	if active != 2 || elapsed > time.Second || statuses[0] != 200 || statuses[1] != 200 {
		t.Fatalf("pages blocked by uploads: active=%d elapsed=%v statuses=%v", active, elapsed, statuses)
	}
	t.Logf("two slow uploads: homepage and health combined %v", elapsed)
}
func TestEightyOneConcurrentUploads(t *testing.T) {
	if testing.Short() {
		t.Skip("81-client integration check")
	}
	s := newTestSite(t)
	s.seed(t, 81)
	payload := append(append([]byte{}, tinyPNG...), make([]byte, 1<<20)...)
	start := make(chan struct{})
	var wg sync.WaitGroup
	var failures atomic.Int64
	var high atomic.Int64
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				active := s.app.activeUploads.Load()
				for old := high.Load(); active > old; old = high.Load() {
					if high.CompareAndSwap(old, active) {
						break
					}
				}
				time.Sleep(time.Millisecond)
			}
		}
	}()
	started := time.Now()
	for i := 0; i < 81; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body, typ := multipartBody(s.csrf, randomID(), "81人测试", "图.png", payload)
			<-start
			request, _ := http.NewRequest("POST", fmt.Sprintf("%s/s/token-%d", s.server.URL, i), bytes.NewReader(body))
			request.Header.Set("Content-Type", typ)
			request.Header.Set("Accept", "application/json")
			response, err := s.client.Do(request)
			if err != nil {
				failures.Add(1)
				return
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != 200 {
				failures.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(done)
	if failures.Load() != 0 || s.workCount(t) != 81 {
		t.Fatalf("failed=%d works=%d", failures.Load(), s.workCount(t))
	}
	if high.Load() > int64(s.app.cfg.MaxConcurrentUploads) {
		t.Fatalf("concurrency exceeded: %d", high.Load())
	}
	s.assertClean(t)
	rows, _ := s.app.db.Query("SELECT filename FROM works")
	defer rows.Close()
	for rows.Next() {
		var name string
		rows.Scan(&name)
		info, err := os.Stat(filepath.Join(s.app.cfg.DataDir, "uploads", name))
		if err != nil || info.Size() != int64(len(payload)) {
			t.Fatal("incomplete file", name, err)
		}
	}
	t.Logf("81 local 1MiB uploads: all successful in %v, active upload high-water=%d (not ECS bandwidth test)", time.Since(started), high.Load())
}
func TestCrashRecoveryPreservesCommittedFiles(t *testing.T) {
	s := newTestSite(t)
	s.seed(t, 1)
	s.upload(t, "token-0", randomID(), "", "图.png", tinyPNG, 200)
	works, _ := listWorks(context.Background(), s.app.db, 1)
	orphan := randomID() + ".png"
	os.WriteFile(filepath.Join(s.app.cfg.DataDir, "uploads", orphan), tinyPNG, 0600)
	stage := filepath.Join(s.app.cfg.DataDir, "uploads", ".staging", "upload-abandoned")
	os.Mkdir(stage, 0700)
	data, _ := json.Marshal([]string{works[0].Filename, orphan, "../camp.sqlite3"})
	os.WriteFile(filepath.Join(stage, "manifest.json"), data, 0600)
	old := time.Now().Add(-25 * time.Hour)
	os.Chtimes(stage, old, old)
	s.app.cleanStaging()
	if _, err := os.Stat(filepath.Join(s.app.cfg.DataDir, "uploads", orphan)); !os.IsNotExist(err) {
		t.Fatal("orphan not removed")
	}
	if _, err := os.Stat(filepath.Join(s.app.cfg.DataDir, "uploads", works[0].Filename)); err != nil {
		t.Fatal("committed file deleted")
	}
	s.assertClean(t)
}

func TestExportNamesFitFilesystemsAndCSVFormulaProtection(t *testing.T) {
	if name := exportComponent(strings.Repeat("创", 120)); len(name) > 200 {
		t.Fatal("export directory or filename is too long in UTF-8 bytes")
	}
	for _, value := range []string{" =1+1", "\t+1", "\n-1", "\u3000@SUM(1)"} {
		if !strings.HasPrefix(csvCell(value), "'") {
			t.Fatalf("unsafe CSV cell: %q", value)
		}
	}
}
