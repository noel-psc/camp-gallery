package main

import "time"

type Activity struct {
	ID                         int64
	Title, Deadline, CreatedAt string
	Archived                   bool
	Students, Submitted, Works int
}

func (a Activity) Editable(now time.Time) bool {
	deadline, err := time.Parse(time.RFC3339, a.Deadline)
	return err == nil && !a.Archived && !now.After(deadline)
}
func (a Activity) DeadlineLabel() string {
	deadline, err := time.Parse(time.RFC3339, a.Deadline)
	if err != nil {
		return a.Deadline
	}
	return deadline.In(shanghai).Format("2006年01月02日 15:04")
}
func (a Activity) DeadlineInput() string {
	deadline, _ := time.Parse(time.RFC3339, a.Deadline)
	return deadline.In(shanghai).Format("2006-01-02T15:04")
}
func (a Activity) Completion() int {
	if a.Students == 0 {
		return 0
	}
	return a.Submitted * 100 / a.Students
}

type Student struct {
	ID, ActivityID                        int64
	ClassName, Name, StudentNumber, Token string
	Count                                 int
}

type Work struct {
	ID, StudentID                            int64
	Filename, OriginalName, Title, CreatedAt string
}

func (w Work) IsVideo() bool {
	switch extension(w.Filename) {
	case ".mp4", ".webm", ".mov":
		return true
	}
	return false
}

type Page struct {
	Title, ActiveNav, CSRF, Notice, Error     string
	Activity                                  Activity
	Activities                                []Activity
	Student                                   Student
	Students                                  []Student
	Works                                     []Work
	Classes                                   []string
	ClassFilter, Search, FormClass, FormNames string
	Editing, Editable, Labels, IsAdmin        bool
	RequestID, FreeSpace                      string
	MaxUploadMB                               int64
	UploadTimeoutMS                           int64
}
