package main

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminStudentChangesPreserveSpaceAndDeleteOnlySelectedStudent(t *testing.T) {
	s := newTestSite(t)
	s.seed(t, 2)
	s.get(t, "/admin/students/token-0", 303)
	s.post(t, "/admin/students/token-0", url.Values{"action": {"delete"}, "confirm_delete": {"yes"}}, 303)
	if _, err := getStudent(context.Background(), s.app.db, "token-0"); err != nil {
		t.Fatal("unauthenticated request deleted student")
	}
	s.upload(t, "token-0", randomID(), "作品", "photo.png", tinyPNG, 200)
	s.upload(t, "token-1", randomID(), "其他作品", "photo.png", tinyPNG, 200)
	works, _ := listWorks(context.Background(), s.app.db, 1)
	s.login(t)
	if page := s.get(t, "/admin", 200); !strings.Contains(page, "/admin/students/token-0") {
		t.Fatal("missing edit link")
	}
	s.get(t, "/admin/students/token-0", 200)
	s.post(t, "/admin/students/token-0", url.Values{"action": {"save"}, "class_name": {"新班级"}, "name": {"新姓名"}, "student_number": {"NEW-001"}}, 303)
	child, err := getStudent(context.Background(), s.app.db, "token-0")
	if err != nil || child.Name != "新姓名" || child.ClassName != "新班级" || child.StudentNumber != "NEW-001" || child.ID != 1 {
		t.Fatalf("wrong updated student: %+v, %v", child, err)
	}
	s.get(t, "/s/token-0/file/1", 200)
	if page := s.get(t, "/admin/cards", 200); !strings.Contains(page, "新姓名") {
		t.Fatal("cards did not use updated name")
	}
	s.post(t, "/admin/students/token-0", url.Values{"action": {"delete"}}, 400)
	s.get(t, "/s/token-0", 200)
	s.post(t, "/admin/students/token-0", url.Values{"action": {"delete"}, "confirm_delete": {"yes"}}, 303)
	s.get(t, "/s/token-0", 404)
	s.get(t, "/admin/qr/token-0", 404)
	s.get(t, "/s/token-1/file/2", 200)
	if _, err := os.Stat(filepath.Join(s.app.cfg.DataDir, "uploads", works[0].Filename)); !os.IsNotExist(err) {
		t.Fatal("deleted student's file remains")
	}
	var batches int
	if err := s.app.db.QueryRow("SELECT count(*) FROM upload_batches WHERE student_id=1").Scan(&batches); err != nil || batches != 0 {
		t.Fatal("upload batches not removed", err)
	}
	if s.workCount(t) != 1 {
		t.Fatal("other student's work changed")
	}
}

func TestAdminStudentValidationAndCSRF(t *testing.T) {
	s := newTestSite(t)
	s.seed(t, 2)
	s.login(t)
	for _, v := range []url.Values{
		{"action": {"save"}, "class_name": {"一班"}, "name": {"重复学号"}, "student_number": {"001"}},
		{"action": {"save"}, "class_name": {"一班"}, "name": {""}, "student_number": {"002"}},
		{"action": {"save"}, "class_name": {""}, "name": {"姓名"}, "student_number": {"002"}},
		{"action": {"save"}, "class_name": {"一班"}, "name": {"姓名"}, "student_number": {"bad number"}},
	} {
		s.post(t, "/admin/students/token-0", v, 400)
	}
	response, err := s.client.PostForm(s.server.URL+"/admin/students/token-0", url.Values{"action": {"delete"}, "confirm_delete": {"yes"}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("CSRF bypass")
	}
	child, err := getStudent(context.Background(), s.app.db, "token-0")
	if err != nil || child.Name != "学生0" || child.StudentNumber != "000" {
		t.Fatal("invalid form changed student")
	}
	s.get(t, "/admin/students/missing", 404)
}
