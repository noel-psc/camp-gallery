package main

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

func (a *App) selectedPage(r *http.Request) (Page, error) {
	activities, err := listActivities(r.Context(), a.db)
	if err != nil {
		return Page{}, err
	}
	if len(activities) == 0 {
		return Page{}, fmt.Errorf("还没有活动")
	}
	selected := activities[0]
	for _, activity := range activities {
		if !activity.Archived {
			selected = activity
			break
		}
	}
	if raw := r.URL.Query().Get("activity"); raw != "" {
		id, err := parseID(raw)
		if err != nil {
			return Page{}, err
		}
		found := false
		for _, activity := range activities {
			if activity.ID == id {
				selected = activity
				found = true
				break
			}
		}
		if !found {
			return Page{}, fmt.Errorf("所选活动不存在")
		}
	}
	return Page{Title: "学生与作品 · SWEET少年", Activity: selected, Activities: activities, ActiveNav: "students"}, nil
}
func (a *App) admin(w http.ResponseWriter, r *http.Request) {
	page, err := a.selectedPage(r)
	if err != nil {
		a.fail(w, r, 400, err.Error())
		return
	}
	if r.Method == http.MethodPost {
		if !a.parseForm(w, r) {
			return
		}
		page.FormClass = strings.TrimSpace(r.FormValue("class_name"))
		page.FormNames = r.FormValue("names")
		entries, err := parseRoster(page.FormNames)
		if err == nil && (page.FormClass == "" || len([]rune(page.FormClass)) > 80) {
			err = fmt.Errorf("请填写 1–80 个字的班级名称")
		}
		if err == nil {
			err = importRoster(r.Context(), a.db, page.Activity.ID, page.FormClass, entries)
		}
		if err != nil {
			page.Error = err.Error()
		} else {
			http.Redirect(w, r, fmt.Sprintf("/admin?activity=%d&notice=%s", page.Activity.ID, url.QueryEscape("名单已导入，已有学生的二维码保持不变")), 303)
			return
		}
	}
	students, err := listStudents(r.Context(), a.db, page.Activity.ID)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	page.ClassFilter = r.URL.Query().Get("class")
	page.Search = truncate(r.URL.Query().Get("q"), 80)
	classes := map[string]bool{}
	for _, child := range students {
		classes[child.ClassName] = true
		if page.ClassFilter != "" && child.ClassName != page.ClassFilter {
			continue
		}
		if page.Search != "" && !strings.Contains(strings.ToLower(child.Name+" "+child.StudentNumber), strings.ToLower(page.Search)) {
			continue
		}
		page.Students = append(page.Students, child)
	}
	for name := range classes {
		page.Classes = append(page.Classes, name)
	}
	sort.Strings(page.Classes)
	free, err := diskFree(a.cfg.DataDir)
	if err == nil {
		page.FreeSpace = humanBytes(free)
	} else {
		page.FreeSpace = "暂不可用"
	}
	code := 200
	if page.Error != "" {
		code = 400
	}
	a.render(w, r, "admin", page, code)
}
func (a *App) activities(w http.ResponseWriter, r *http.Request) {
	page, err := a.selectedPage(r)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	page.Title = "活动管理 · SWEET少年"
	page.ActiveNav = "activities"
	if r.Method == http.MethodPost {
		if !a.parseForm(w, r) {
			return
		}
		title := strings.TrimSpace(r.FormValue("title"))
		deadline, parseErr := parseDeadlineInput(r.FormValue("deadline"))
		if title == "" || len([]rune(title)) > 80 || parseErr != nil {
			page.Error = "请填写活动名称和有效的北京时间截止日期"
		} else {
			result, err := a.db.ExecContext(r.Context(), "INSERT INTO activities (title,deadline,created_at) VALUES (?,?,?)", title, deadline.Format(time.RFC3339), a.now().In(shanghai).Format(time.RFC3339))
			if err != nil {
				a.internal(w, r, err)
				return
			}
			id, _ := result.LastInsertId()
			http.Redirect(w, r, fmt.Sprintf("/admin?activity=%d&notice=%s", id, url.QueryEscape("新活动已创建，现在可以导入学生名单")), 303)
			return
		}
	}
	code := 200
	if page.Error != "" {
		code = 400
	}
	a.render(w, r, "activities", page, code)
}
func (a *App) updateActivity(w http.ResponseWriter, r *http.Request) {
	if !a.parseForm(w, r) {
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		a.fail(w, r, 404, "活动不存在")
		return
	}
	if _, err := getActivity(r.Context(), a.db, id); err != nil {
		a.fail(w, r, 404, "活动不存在")
		return
	}
	switch r.FormValue("action") {
	case "archive":
		_, err = a.db.ExecContext(r.Context(), "UPDATE activities SET archived=1 WHERE id=?", id)
	case "restore":
		_, err = a.db.ExecContext(r.Context(), "UPDATE activities SET archived=0 WHERE id=?", id)
	case "save":
		title := strings.TrimSpace(r.FormValue("title"))
		deadline, parseErr := parseDeadlineInput(r.FormValue("deadline"))
		if title == "" || len([]rune(title)) > 80 || parseErr != nil {
			a.fail(w, r, 400, "请填写活动名称和有效的北京时间截止日期")
			return
		}
		_, err = a.db.ExecContext(r.Context(), "UPDATE activities SET title=?,deadline=? WHERE id=?", title, deadline.Format(time.RFC3339), id)
	default:
		a.fail(w, r, 400, "无法识别此操作")
		return
	}
	if err != nil {
		a.internal(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/activities?notice="+url.QueryEscape("活动设置已保存，旧二维码可继续访问"), 303)
}
func (a *App) cards(w http.ResponseWriter, r *http.Request) {
	page, err := a.selectedPage(r)
	if err != nil {
		a.fail(w, r, 400, err.Error())
		return
	}
	page.Students, err = listStudents(r.Context(), a.db, page.Activity.ID)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	page.Labels = r.URL.Query().Get("layout") == "labels"
	page.Title = "专属二维码贺卡"
	a.render(w, r, "cards", page, 200)
}
func (a *App) qr(w http.ResponseWriter, r *http.Request) {
	child, err := getStudent(r.Context(), a.db, r.PathValue("token"))
	if err != nil {
		a.fail(w, r, 404, "学生空间不存在")
		return
	}
	png, err := qrcode.Encode(a.cfg.PublicBaseURL+"/s/"+url.PathEscape(child.Token), qrcode.Medium, 360)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)
}

// Older minute-only forms mean the end of the selected minute.
func parseDeadlineInput(value string) (time.Time, error) {
	if len(value) == 16 {
		t, err := time.ParseInLocation("2006-01-02T15:04", value, shanghai)
		return t.Add(59 * time.Second), err
	}
	return time.ParseInLocation("2006-01-02T15:04:05", value, shanghai)
}
