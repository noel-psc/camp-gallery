package main

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

func exportComponent(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\<>:"|?*`, r) {
			return '_'
		}
		return r
	}, value)
	value = strings.Trim(value, " .")
	value = truncate(value, 100)
	for len(value) > 200 {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	if value == "" {
		return "未命名"
	}
	return value
}
func csvCell(value string) string {
	trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}
func (a *App) exportWorks(w http.ResponseWriter, r *http.Request) {
	select {
	case a.exportSlots <- struct{}{}:
		defer func() { <-a.exportSlots }()
	default:
		a.fail(w, r, 429, "已有作品正在导出，请稍后再试")
		return
	}
	page, err := a.selectedPage(r)
	if err != nil {
		a.fail(w, r, 400, err.Error())
		return
	}
	children, err := listStudents(r.Context(), a.db, page.Activity.ID)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	type studentWorks struct {
		Student Student
		Works   []Work
	}
	data := make([]studentWorks, 0, len(children))
	for _, child := range children {
		works, err := listWorks(r.Context(), a.db, child.ID)
		if err != nil {
			a.internal(w, r, err)
			return
		}
		data = append(data, studentWorks{child, works})
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": exportComponent(page.Activity.Title) + "_作品_" + a.now().In(shanghai).Format("20060102_150405") + ".zip"}))
	archive := zip.NewWriter(pacedWriter{ResponseWriter: w, ctx: r.Context(), budget: a.downloadBudget})
	var manifest bytes.Buffer
	manifest.WriteString("\xef\xbb\xbf")
	writer := csv.NewWriter(&manifest)
	_ = writer.Write([]string{"班级", "学号", "姓名", "作品名称", "原文件名", "上传时间", "导出路径", "状态", "活动"})
	used := map[string]bool{}
	for _, entry := range data {
		child := entry.Student
		number := child.StudentNumber
		if number == "" {
			number = "待补学号"
		}
		base := exportComponent(child.ClassName) + "/" + exportComponent(number+"_"+child.Name)
		folder := base
		for n := 0; used[folder]; n++ {
			folder = fmt.Sprintf("%s_空间%d_%d", base, child.ID, n)
		}
		used[folder] = true
		if _, err := archive.Create(folder + "/"); err != nil {
			a.logger.Warn("export interrupted", "error", err)
			return
		}
		if len(entry.Works) == 0 {
			_ = writer.Write([]string{csvCell(child.ClassName), csvCell(child.StudentNumber), csvCell(child.Name), "", "", "", "", "尚未上传", csvCell(page.Activity.Title)})
		}
		for _, work := range entry.Works {
			path := fmt.Sprintf("%s/%d_%s", folder, work.ID, exportComponent(work.OriginalName))
			status := "文件缺失"
			if filepath.Base(work.Filename) == work.Filename && !strings.HasPrefix(work.Filename, ".") {
				file, openErr := a.files.Open(work.Filename)
				if openErr == nil {
					info, statErr := file.Stat()
					if statErr == nil && info.Mode().IsRegular() {
						header := &zip.FileHeader{Name: path, Method: zip.Store}
						header.SetModTime(info.ModTime())
						destination, err := archive.CreateHeader(header)
						if err != nil {
							file.Close()
							a.logger.Warn("export interrupted", "error", err)
							return
						}
						if _, err := io.Copy(destination, file); err == nil {
							status = "已导出"
						} else {
							status = "读取失败"
							a.logger.Warn("export read failed", "work_id", work.ID, "error", err)
						}
					}
					file.Close()
				}
			}
			_ = writer.Write([]string{csvCell(child.ClassName), csvCell(child.StudentNumber), csvCell(child.Name), csvCell(work.Title), csvCell(work.OriginalName), work.CreatedAt, csvCell(path), status, csvCell(page.Activity.Title)})
		}
	}
	writer.Flush()
	entry, err := archive.Create("作品清单.csv")
	if err != nil {
		a.logger.Warn("export manifest failed", "error", err)
		return
	}
	if _, err := io.Copy(entry, &manifest); err != nil {
		a.logger.Warn("export interrupted", "error", err)
		return
	}
	if err := archive.Close(); err != nil {
		a.logger.Warn("export close failed", "error", err)
	}
}
