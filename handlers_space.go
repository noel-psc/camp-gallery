package main

import (
	"database/sql"
	"errors"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

func (a *App) studentActivity(w http.ResponseWriter, r *http.Request) (Student, Activity, bool) {
	child, err := getStudent(r.Context(), a.db, r.PathValue("token"))
	if errors.Is(err, sql.ErrNoRows) {
		a.fail(w, r, 404, "没有找到这个作品空间，请核对二维码")
		return Student{}, Activity{}, false
	}
	if err != nil {
		a.internal(w, r, err)
		return Student{}, Activity{}, false
	}
	activity, err := getActivity(r.Context(), a.db, child.ActivityID)
	if err != nil {
		a.internal(w, r, err)
		return child, activity, false
	}
	return child, activity, true
}
func (a *App) space(w http.ResponseWriter, r *http.Request) {
	child, activity, ok := a.studentActivity(w, r)
	if !ok {
		return
	}
	works, err := listWorks(r.Context(), a.db, child.ID)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	canEdit := activity.Editable(a.now())
	for i := range works {
		if works[i].IsArticle() {
			works[i].ArticleText = a.articleText(works[i])
		}
	}
	editing := canEdit && (len(works) == 0 || r.URL.Query().Get("edit") == "1")
	a.render(w, r, "space", Page{Student: child, Activity: activity, Works: works, Editable: canEdit, Editing: editing, RequestID: randomID()}, 200)
}
func (a *App) media(w http.ResponseWriter, r *http.Request) {
	child, _, ok := a.studentActivity(w, r)
	if !ok {
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		a.fail(w, r, 404, "没有找到这件作品")
		return
	}
	var work Work
	err = a.db.QueryRowContext(r.Context(), "SELECT filename,original_name FROM works WHERE id=? AND student_id=?", id, child.ID).Scan(&work.Filename, &work.OriginalName)
	if errors.Is(err, sql.ErrNoRows) {
		a.fail(w, r, 404, "没有找到这件作品")
		return
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	if version := r.URL.Query().Get("v"); version != "" && version != work.Filename {
		a.fail(w, r, 404, "这件作品已删除或替换")
		return
	}
	if filepath.Base(work.Filename) != work.Filename || strings.HasPrefix(work.Filename, ".") {
		a.fail(w, r, 404, "作品文件不存在")
		return
	}
	file, err := a.files.Open(work.Filename)
	if err != nil {
		a.fail(w, r, 404, "作品文件暂时不可用，请联系老师")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		a.fail(w, r, 404, "作品文件不存在")
		return
	}
	contentType := mime.TypeByExtension(extension(work.Filename))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	disposition := "inline"
	if work.IsDocument() || r.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	if work.IsDocument() {
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": work.OriginalName}))
	// SQLite can reuse a deleted work ID. Cache only URLs bound to this file.
	w.Header().Set("ETag", `"`+work.Filename+`"`)
	if r.URL.Query().Get("v") == work.Filename {
		w.Header().Set("Cache-Control", "private, max-age=3600")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	http.ServeContent(pacedWriter{ResponseWriter: w, ctx: r.Context(), budget: a.downloadBudget}, r, work.OriginalName, info.ModTime(), file)
}
func (a *App) editWork(w http.ResponseWriter, r *http.Request) {
	child, activity, ok := a.studentActivity(w, r)
	if !ok {
		return
	}
	if !activity.Editable(a.now()) {
		a.fail(w, r, 403, "上传时间已结束，作品可以继续查看")
		return
	}
	if !a.parseForm(w, r) {
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		a.fail(w, r, 404, "作品不存在")
		return
	}
	var exists int
	if err := a.db.QueryRowContext(r.Context(), "SELECT count(*) FROM works WHERE id=? AND student_id=?", id, child.ID).Scan(&exists); err != nil {
		a.internal(w, r, err)
		return
	}
	if exists == 0 {
		a.fail(w, r, 404, "作品不存在")
		return
	}
	filename := r.FormValue("work_version")
	if filename == "" {
		a.fail(w, r, 409, "作品页面已更新，请刷新后重试")
		return
	}
	action := r.FormValue("action")
	var result sql.Result
	// Match both immutable file identity and current space token in the mutation.
	predicate := " WHERE id=? AND student_id=? AND filename=? AND EXISTS (SELECT 1 FROM students WHERE id=? AND token=?)"
	if action == "delete" {
		result, err = a.db.ExecContext(r.Context(), "DELETE FROM works"+predicate, id, child.ID, filename, child.ID, child.Token)
	} else if action == "save" {
		title := truncate(r.FormValue("title"), 100)
		if title == "" {
			a.fail(w, r, 400, "作品名称不能为空")
			return
		}
		result, err = a.db.ExecContext(r.Context(), "UPDATE works SET title=?"+predicate, title, id, child.ID, filename, child.ID, child.Token)
	} else {
		a.fail(w, r, 400, "无效操作")
		return
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		a.internal(w, r, err)
		return
	}
	if count == 0 {
		a.fail(w, r, 409, "作品已删除或替换，请刷新页面")
		return
	}
	if action == "delete" && filepath.Base(filename) == filename && !strings.HasPrefix(filename, ".") {
		if err := a.files.Remove(filename); err != nil {
			a.logger.Warn("deleted work file could not be removed", "work_id", id, "error", err)
		}
	}
	http.Redirect(w, r, "/s/"+child.Token+"?edit=1&notice="+"作品已更新", 303)
}
