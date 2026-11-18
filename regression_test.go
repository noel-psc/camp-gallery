package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestRegressionStaleDeleteForm(t *testing.T) {
	s := newTestSite(t)
	s.seed(t, 1)
	s.upload(t, "token-0", randomID(), "旧照片", "old.png", tinyPNG, 200)
	// Two tabs have both loaded the form for work 1.
	page := s.get(t, "/s/token-0?edit=1", 200)
	works, _ := listWorks(context.Background(), s.app.db, 1)
	if !strings.Contains(page, `name="work_version" value="`+works[0].Filename+`"`) {
		t.Fatal("edit form missing file identity")
	}
	form := url.Values{"action": {"delete"}, "work_version": {works[0].Filename}}
	s.post(t, "/s/token-0/work/1", form, 303)
	s.upload(t, "token-0", randomID(), "新照片", "new.png", tinyPNG, 200)
	// The second tab now submits its stale form.
	s.post(t, "/s/token-0/work/1", form, 409)
	s.post(t, "/s/token-0/work/1", url.Values{"action": {"save"}, "title": {"错误改名"}, "work_version": {works[0].Filename}}, 409)
	s.post(t, "/s/token-0/work/1", url.Values{"action": {"delete"}}, 409)
	if s.workCount(t) != 1 {
		t.Fatal("BUG: submitting stale delete form deleted replacement photo")
	}
}

func TestRegressionDeletedStudentInflightUpload(t *testing.T) {
	s := newTestSite(t)
	s.seed(t, 1)
	// Send only multipart fields and the beginning of a file, pausing its completion.
	var prefix bytes.Buffer
	mw := multipart.NewWriter(&prefix)
	mw.WriteField("csrf", s.csrf)
	mw.WriteField("request_id", randomID())
	part, _ := mw.CreateFormFile("files", "slow.png")
	part.Write(tinyPNG[:8])
	pr, pw := io.Pipe()
	req, _ := http.NewRequest("POST", s.server.URL+"/s/token-0", pr)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	done := make(chan *http.Response, 1)
	errs := make(chan error, 1)
	go func() {
		resp, err := s.client.Do(req)
		if err != nil {
			errs <- err
		} else {
			done <- resp
		}
	}()
	go func() { pw.Write(prefix.Bytes()) }()
	defer pw.Close()
	deadline := time.Now().Add(3 * time.Second)
	for s.app.activeUploads.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.app.activeUploads.Load() == 0 {
		t.Fatal("upload did not start")
	}
	s.login(t)
	s.post(t, "/admin/students/token-0", url.Values{"action": {"delete"}, "confirm_delete": {"yes"}}, 303)
	// Import a new person with the same number, a normal correction workflow.
	if err := importRoster(context.Background(), s.app.db, 1, "一班", []rosterEntry{{"000", "新学生"}}); err != nil {
		t.Fatal(err)
	}
	child, err := getStudent(context.Background(), s.app.db, "token-0")
	if err == nil {
		t.Fatal("deleted token survived", child)
	}
	suffix := append([]byte(nil), tinyPNG[8:]...)
	suffix = append(suffix, []byte("\r\n--"+mw.Boundary()+"--\r\n")...)
	go func() { pw.Write(suffix); pw.Close() }()
	select {
	case err := <-errs:
		t.Fatal(err)
	case resp := <-done:
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatalf("BUG: deleted student's pending upload succeeded for replacement student's row: %s", strings.TrimSpace(string(data)))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("upload stalled")
	}
	if s.workCount(t) != 0 {
		t.Fatal("replacement student received a stale upload")
	}
	s.assertClean(t)
}

func TestRegressionSavingActivityPreservesEndOfMinute(t *testing.T) {
	s := newTestSite(t)
	s.login(t)
	_, err := s.app.db.Exec("UPDATE activities SET deadline='2099-11-19T23:59:59+08:00' WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	activity, err := getActivity(context.Background(), s.app.db, 1)
	if err != nil {
		t.Fatal(err)
	}
	s.post(t, "/admin/activities/1", url.Values{"action": {"save"}, "title": {"仅修改活动名称"}, "deadline": {activity.DeadlineInput()}}, 303)
	changed, err := getActivity(context.Background(), s.app.db, 1)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Deadline != activity.Deadline {
		t.Fatalf("BUG: saving only activity name changed deadline from %s to %s", activity.Deadline, changed.Deadline)
	}
}

func TestRegressionStaticAssetsUseContentIdentity(t *testing.T) {
	s := newTestSite(t)
	s.seed(t, 1)
	page := s.get(t, "/s/token-0", 200)
	data, err := assets.ReadFile("static/upload.js")
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("/static/upload.js?v=%x", sha256.Sum256(data))
	if !strings.Contains(page, want) {
		t.Fatal("script URL not bound to embedded file content")
	}
}

func TestRegressionMinuteDeadlineAndExplicitSeconds(t *testing.T) {
	for value, want := range map[string]string{"2099-11-19T23:59": "23:59:59", "2099-11-19T23:59:13": "23:59:13"} {
		got, err := parseDeadlineInput(value)
		if err != nil || got.Format("15:04:05") != want {
			t.Fatalf("%s: %s %v", value, got, err)
		}
	}
}
