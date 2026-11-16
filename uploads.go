package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

func extension(name string) string { return strings.ToLower(filepath.Ext(name)) }
func humanBytes(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}
func diskFree(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}
func (a *App) reserveDisk(need int64) (func(), error) {
	a.diskMu.Lock()
	defer a.diskMu.Unlock()
	free, err := diskFree(a.cfg.DataDir)
	if err != nil {
		return nil, err
	}
	if free-a.reserved-need < a.cfg.MinFreeBytes {
		return nil, errors.New("存储空间不足，请联系老师清理或扩容后再试")
	}
	a.reserved += need
	return func() { a.diskMu.Lock(); a.reserved -= need; a.diskMu.Unlock() }, nil
}
func (a *App) cleanStaging() {
	root := filepath.Join(a.cfg.DataDir, "uploads", ".staging")
	entries, err := os.ReadDir(root)
	if err != nil {
		a.logger.Warn("staging cleanup unavailable", "error", err)
		return
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "upload-") || !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(a.now().Add(-24*time.Hour)) {
			continue
		}
		folder := filepath.Join(root, entry.Name())
		var names []string
		if data, err := os.ReadFile(filepath.Join(folder, "manifest.json")); err == nil && json.Unmarshal(data, &names) == nil {
			for _, name := range names {
				if filepath.Base(name) != name || len(strings.TrimSuffix(name, extension(name))) != 48 || (!allowedExtension(extension(name)) && extension(name) != ".txt") {
					continue
				}
				var count int
				if err := a.db.QueryRow("SELECT count(*) FROM works WHERE filename=?", name).Scan(&count); err != nil {
					a.logger.Warn("staging reference check failed", "error", err)
					continue
				}
				if count == 0 {
					if err := a.files.Remove(name); err != nil && !os.IsNotExist(err) {
						a.logger.Warn("orphan cleanup failed", "error", err)
					}
				}
			}
		}
		if err := os.RemoveAll(folder); err != nil {
			a.logger.Warn("staging cleanup failed", "error", err)
		}
	}
}

type stagedWork struct {
	Work
	Path, Hash string
}

func acceptedHeader(ext string, header []byte) bool {
	typ := http.DetectContentType(header)
	switch ext {
	case ".jpg", ".jpeg":
		return typ == "image/jpeg"
	case ".png":
		return typ == "image/png"
	case ".gif":
		return typ == "image/gif"
	case ".webp":
		return typ == "image/webp"
	case ".mp4":
		return len(header) >= 12 && string(header[4:8]) == "ftyp"
	case ".mov":
		if len(header) < 8 {
			return false
		}
		switch string(header[4:8]) {
		case "ftyp", "moov", "mdat", "wide", "free":
			return true
		}
	case ".webm":
		return len(header) > 4 && bytes.Equal(header[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}) && bytes.Contains(header, []byte("webm"))
	case ".docx":
		return len(header) >= 4 && bytes.Equal(header[:4], []byte{'P', 'K', 3, 4})
	}
	return false
}
func allowedExtension(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".mp4", ".mov", ".webm", ".docx":
		return true
	}
	return false
}
func (a *App) upload(w http.ResponseWriter, r *http.Request) {
	started := a.now()
	child, activity, ok := a.studentActivity(w, r)
	if !ok {
		return
	}
	if !activity.Editable(started) {
		a.fail(w, r, 403, "上传时间已结束，作品可以继续查看")
		return
	}
	if r.ContentLength > a.cfg.MaxUploadBytes {
		a.fail(w, r, 413, "本次文件总大小超过上限，请压缩文件或分批上传")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), a.cfg.UploadTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(a.cfg.UploadTimeout))
	select {
	case a.uploadQueue <- struct{}{}:
		defer func() { <-a.uploadQueue }()
	default:
		w.Header().Set("Retry-After", "5")
		a.fail(w, r, 429, "当前上传人数较多，请稍候再试，所选文件会保留")
		return
	}
	select {
	case a.uploadSlots <- struct{}{}:
		defer func() { <-a.uploadSlots }()
	case <-ctx.Done():
		a.fail(w, r, 408, "等待上传超时，请稍后重试")
		return
	}
	a.activeUploads.Add(1)
	defer a.activeUploads.Add(-1)
	reserve := r.ContentLength
	if reserve <= 0 {
		reserve = a.cfg.MaxUploadBytes
	}
	release, err := a.reserveDisk(reserve)
	if err != nil {
		a.fail(w, r, 507, err.Error())
		return
	}
	defer release()
	r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MaxUploadBytes)
	r.Body = struct {
		io.Reader
		io.Closer
	}{pacedReader{ctx: ctx, reader: r.Body, budget: a.uploadBudget}, r.Body}
	reader, err := r.MultipartReader()
	if err != nil {
		a.fail(w, r, 400, "请选择作品或粘贴文章后上传")
		return
	}
	stage, err := os.MkdirTemp(filepath.Join(a.cfg.DataDir, "uploads", ".staging"), "upload-")
	if err != nil {
		a.internal(w, r, err)
		return
	}
	defer os.RemoveAll(stage)
	fields := map[string]string{}
	var works []stagedWork
	var totalFields int64
	failRead := func(err error) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			a.fail(w, r, 413, "本次文件总大小超过上限，请分批上传")
		} else if ctx.Err() != nil {
			a.fail(w, r, 408, "上传等待超时，请检查网络后重试")
		} else {
			a.fail(w, r, 400, "文件传输未完成，请检查网络后重试")
		}
	}
	for parts := 0; ; parts++ {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			failRead(err)
			return
		}
		if parts >= 110 {
			part.Close()
			a.fail(w, r, 400, "一次最多上传 100 个文件")
			return
		}
		if part.FileName() == "" {
			limit := int64(8192)
			if part.FormName() == "article" {
				limit = maxArticleBytes
			}
			value, err := io.ReadAll(io.LimitReader(part, limit+1))
			if err != nil {
				failRead(err)
				return
			}
			totalFields += int64(len(value))
			if int64(len(value)) > limit || totalFields > maxArticleBytes+32768 {
				a.fail(w, r, 400, "表单文字过长")
				return
			}
			fields[part.FormName()] = string(value)
			part.Close()
			continue
		}
		if !a.validCSRF(r, fields["csrf"]) {
			part.Close()
			a.fail(w, r, 403, "页面已过期，请刷新后重新选择作品")
			return
		}
		if part.FormName() != "files" || len(works) >= 100 {
			part.Close()
			a.fail(w, r, 400, "一次最多上传 100 个作品文件")
			return
		}
		name := truncate(filepath.Base(strings.ReplaceAll(part.FileName(), "\\", "/")), 255)
		ext := extension(name)
		if !allowedExtension(ext) {
			part.Close()
			a.fail(w, r, 400, "支持 JPG、PNG、GIF、WebP 图片、MP4、MOV、WebM 视频和 DOCX 文章")
			return
		}
		filename := randomID() + ext
		path := filepath.Join(stage, filename)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			a.internal(w, r, err)
			return
		}
		hash := sha256.New()
		var first bytes.Buffer
		header := make([]byte, 512)
		n, readErr := io.ReadFull(part, header)
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			file.Close()
			failRead(readErr)
			return
		}
		if n == 0 || !acceptedHeader(ext, header[:n]) {
			file.Close()
			part.Close()
			a.fail(w, r, 400, "文件内容与格式不匹配，请选择原始图片、视频或 DOCX 文档")
			return
		}
		first.Write(header[:n])
		writer := io.MultiWriter(file, hash)
		_, err = io.CopyBuffer(writer, io.MultiReader(&first, part), make([]byte, 64<<10))
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			if errors.Is(err, syscall.ENOSPC) {
				a.fail(w, r, 507, "存储空间已满，请联系老师")
			} else {
				failRead(err)
			}
			return
		}
		part.Close()
		if ext == ".docx" {
			if err := validateDOCX(path); err != nil {
				a.fail(w, r, 400, err.Error())
				return
			}
		}
		works = append(works, stagedWork{Work: Work{StudentID: child.ID, Filename: filename, OriginalName: name}, Path: path, Hash: hex.EncodeToString(hash.Sum(nil))})
	}
	if !a.validCSRF(r, fields["csrf"]) {
		a.fail(w, r, 403, "页面已过期，请刷新后重试")
		return
	}
	title := truncate(fields["title"], 100)
	if text := strings.TrimSpace(fields["article"]); text != "" {
		if !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxArticleChars {
			a.fail(w, r, 400, "文章正文最多 20000 字，请精简后提交或上传 .docx")
			return
		}
		article, err := stageArticle(stage, text, title, child.ID)
		if err != nil {
			a.internal(w, r, err)
			return
		}
		works = append(works, article)
	}
	if len(works) == 0 {
		a.fail(w, r, 400, "请至少选择一件作品")
		return
	}
	requestID := fields["request_id"]
	if !requestPattern.MatchString(requestID) {
		a.fail(w, r, 400, "上传标识已失效，请刷新页面后重试")
		return
	}
	if header := r.Header.Get("X-Upload-ID"); header != "" && header != requestID {
		a.fail(w, r, 400, "上传标识不一致，请刷新页面")
		return
	}
	number := strings.TrimSpace(fields["student_number"])
	if child.StudentNumber != "" {
		if number != "" && number != child.StudentNumber {
			a.fail(w, r, 400, "学号与当前学生空间不一致")
			return
		}
		number = child.StudentNumber
	}
	if !numberPattern.MatchString(number) {
		a.fail(w, r, 400, "请填写 1–32 位数字、字母或连字符组成的学号")
		return
	}
	if name := strings.TrimSpace(fields["name"]); name != "" && name != child.Name {
		a.fail(w, r, 400, "学生姓名与此空间不一致")
		return
	}
	fingerprint := sha256.New()
	fmt.Fprintf(fingerprint, "%q\x00%q", number, title)
	for i := range works {
		works[i].Title = title
		if title == "" {
			works[i].Title = truncate(works[i].OriginalName, 100)
		}
		fmt.Fprintf(fingerprint, "\x00%q:%s", works[i].OriginalName, works[i].Hash)
	}
	err = a.commitUpload(ctx, child, requestID, hex.EncodeToString(fingerprint.Sum(nil)), number, works, started)
	if err != nil {
		var userErr *uploadError
		if errors.As(err, &userErr) {
			a.fail(w, r, userErr.Code, userErr.Message)
		} else {
			a.internal(w, r, err)
		}
		return
	}
	if wantsJSON(r) {
		writeJSON(w, 200, map[string]any{"ok": true, "redirect": "/s/" + child.Token, "count": len(works)})
		return
	}
	http.Redirect(w, r, "/s/"+child.Token, 303)
}

type uploadError struct {
	Code    int
	Message string
}

func (e *uploadError) Error() string { return e.Message }
func (a *App) commitUpload(ctx context.Context, child Student, requestID, fingerprint, number string, works []stagedWork, started time.Time) (err error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous string
	err = tx.QueryRowContext(ctx, "SELECT fingerprint FROM upload_batches WHERE student_id=? AND request_id=?", child.ID, requestID).Scan(&previous)
	if err == nil {
		if previous == fingerprint {
			return nil
		}
		return &uploadError{409, "这次上传内容已变更，请重新选择文件后提交"}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var registered string
	var archived bool
	if err := tx.QueryRowContext(ctx, "SELECT s.student_number,a.archived FROM students s JOIN activities a ON a.id=s.activity_id WHERE s.id=?", child.ID).Scan(&registered, &archived); err != nil {
		return err
	}
	if archived {
		return &uploadError{403, "活动已归档，作品可以继续查看"}
	}
	if registered != "" && registered != number {
		return &uploadError{400, "学号与当前空间已登记的学号不一致"}
	}
	var duplicates int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM students WHERE activity_id=? AND class_name=? AND student_number=? AND id<>?", child.ActivityID, child.ClassName, number, child.ID).Scan(&duplicates); err != nil {
		return err
	}
	if duplicates > 0 {
		return &uploadError{400, "本班已有其他空间登记此学号，请联系老师核对"}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE students SET student_number=? WHERE id=?", number, child.ID); err != nil {
		return err
	}
	// This journal makes a crash between file publication and SQL commit recoverable.
	journal := make([]string, 0, len(works))
	for _, work := range works {
		journal = append(journal, work.Filename)
	}
	encoded, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	manifest, err := os.OpenFile(filepath.Join(filepath.Dir(works[0].Path), "manifest.json"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, writeErr := manifest.Write(encoded)
	if writeErr == nil {
		writeErr = manifest.Sync()
	}
	closeErr := manifest.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	var published []string
	committed := false
	defer func() {
		if !committed {
			for _, name := range published {
				if removeErr := a.files.Remove(name); removeErr != nil {
					a.logger.Error("upload rollback cleanup failed", "file", name, "error", removeErr)
				}
			}
		}
	}()
	created := started.In(shanghai).Format(time.RFC3339Nano)
	for _, work := range works {
		if err := os.Rename(work.Path, filepath.Join(a.cfg.DataDir, "uploads", work.Filename)); err != nil {
			return err
		}
		published = append(published, work.Filename)
		if _, err := tx.ExecContext(ctx, "INSERT INTO works (student_id,filename,original_name,title,created_at) VALUES (?,?,?,?,?)", child.ID, work.Filename, work.OriginalName, work.Title, created); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO upload_batches (student_id,request_id,fingerprint,created_at) VALUES (?,?,?,?)", child.ID, requestID, fingerprint, created); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
