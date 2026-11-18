package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

func (a *App) manageStudent(w http.ResponseWriter, r *http.Request) {
	child, err := getStudent(r.Context(), a.db, r.PathValue("token"))
	if errors.Is(err, sql.ErrNoRows) {
		a.fail(w, r, 404, "学生不存在")
		return
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	activity, err := getActivity(r.Context(), a.db, child.ActivityID)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	page := Page{Title: "修改学生信息", ActiveNav: "students", Student: child, Activity: activity}
	if r.Method == http.MethodGet {
		a.render(w, r, "student", page, 200)
		return
	}
	if !a.parseForm(w, r) {
		return
	}
	showError := func(message string) { page.Error = message; a.render(w, r, "student", page, 400) }
	notice := "学生信息已保存，原二维码和作品保持不变"
	action := r.FormValue("action")
	if action == "delete" {
		if r.FormValue("confirm_delete") != "yes" {
			showError("请确认删除学生及其全部作品")
			return
		}
		tx, err := a.db.BeginTx(r.Context(), nil)
		if err != nil {
			a.internal(w, r, err)
			return
		}
		defer tx.Rollback()
		var currentToken string
		if err = tx.QueryRowContext(r.Context(), "SELECT token FROM students WHERE id=?", child.ID).Scan(&currentToken); err != nil || currentToken != child.Token {
			a.fail(w, r, 404, "学生不存在")
			return
		}
		rows, err := tx.QueryContext(r.Context(), "SELECT filename FROM works WHERE student_id=?", child.ID)
		if err != nil {
			a.internal(w, r, err)
			return
		}
		var files []string
		for rows.Next() {
			var file string
			if err = rows.Scan(&file); err != nil {
				rows.Close()
				a.internal(w, r, err)
				return
			}
			files = append(files, file)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			a.internal(w, r, err)
			return
		}
		for _, query := range []string{"DELETE FROM upload_batches WHERE student_id=?", "DELETE FROM works WHERE student_id=?", "DELETE FROM students WHERE id=?"} {
			if _, err = tx.ExecContext(r.Context(), query, child.ID); err != nil {
				a.internal(w, r, err)
				return
			}
		}
		if err = tx.Commit(); err != nil {
			a.internal(w, r, err)
			return
		}
		for _, file := range files {
			if filepath.Base(file) == file && !strings.HasPrefix(file, ".") {
				if err = a.files.Remove(file); err != nil {
					a.logger.Warn("deleted student file could not be removed", "student_id", child.ID, "error", err)
				}
			}
		}
		notice = "学生及其作品已删除，原二维码已失效"
	} else if action == "save" {
		page.Student.ClassName = strings.TrimSpace(r.FormValue("class_name"))
		page.Student.Name = strings.Join(strings.Fields(r.FormValue("name")), " ")
		page.Student.StudentNumber = strings.TrimSpace(r.FormValue("student_number"))
		if n := len([]rune(page.Student.ClassName)); n < 1 || n > 80 {
			showError("班级须为 1–80 个字")
			return
		}
		if n := len([]rune(page.Student.Name)); n < 1 || n > 80 {
			showError("姓名须为 1–80 个字")
			return
		}
		if !numberPattern.MatchString(page.Student.StudentNumber) {
			showError("学号须为 1–32 位字母、数字或连字符")
			return
		}
		_, err = a.db.ExecContext(r.Context(), "UPDATE students SET class_name=?,name=?,student_number=? WHERE id=? AND token=?", page.Student.ClassName, page.Student.Name, page.Student.StudentNumber, child.ID, child.Token)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				showError("本期该班已有相同学号，请核对后保存")
				return
			}
			a.internal(w, r, err)
			return
		}
	} else {
		showError("无效操作")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin?activity=%d&notice=%s", child.ActivityID, url.QueryEscape(notice)), 303)
}
