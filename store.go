package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func openStore(c Config) (*sql.DB, string, error) {
	if err := os.MkdirAll(filepath.Join(c.DataDir, "uploads", ".staging"), 0700); err != nil {
		return nil, "", err
	}
	abs, err := filepath.Abs(filepath.Join(c.DataDir, "camp.sqlite3"))
	if err != nil {
		return nil, "", err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, "", err
	}
	// Short database operations are serialized; network and disk uploads never hold a connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	success := false
	defer func() {
		if !success {
			db.Close()
		}
	}()
	fail := func(err error) (*sql.DB, string, error) { return nil, "", err }
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;"); err != nil {
		return fail(err)
	}
	var legacy, migrated int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='students'").Scan(&legacy); err != nil {
		return fail(err)
	}
	if err := db.QueryRow("PRAGMA user_version").Scan(&migrated); err != nil {
		return fail(err)
	}
	backup := ""
	if legacy > 0 && migrated < 2 {
		folder := filepath.Join(c.DataDir, "backups")
		if err := os.MkdirAll(folder, 0700); err != nil {
			return fail(err)
		}
		backup = filepath.Join(folder, "camp-before-go-"+time.Now().UTC().Format("20060102T150405.000000000")+".sqlite3")
		if _, err := db.Exec("VACUUM INTO ?", backup); err != nil {
			return fail(fmt.Errorf("迁移前数据库备份失败: %w", err))
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	schema := `CREATE TABLE IF NOT EXISTS activities (id INTEGER PRIMARY KEY, title TEXT NOT NULL, deadline TEXT NOT NULL, archived INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS students (id INTEGER PRIMARY KEY, class_name TEXT NOT NULL, name TEXT NOT NULL, token TEXT UNIQUE NOT NULL, student_number TEXT NOT NULL DEFAULT '', activity_id INTEGER NOT NULL DEFAULT 1 REFERENCES activities(id));
CREATE TABLE IF NOT EXISTS works (id INTEGER PRIMARY KEY, student_id INTEGER NOT NULL REFERENCES students(id), filename TEXT NOT NULL, original_name TEXT NOT NULL, title TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS upload_batches (student_id INTEGER NOT NULL REFERENCES students(id), request_id TEXT NOT NULL, fingerprint TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(student_id,request_id));`
	if _, err := tx.Exec(schema); err != nil {
		return fail(err)
	}
	if _, err := tx.Exec("INSERT OR IGNORE INTO activities (id,title,deadline,created_at) VALUES (1,?,?,?)", c.ActivityTitle, c.DefaultDeadline, time.Now().In(shanghai).Format(time.RFC3339)); err != nil {
		return fail(err)
	}
	columns, err := tx.Query("PRAGMA table_info(students)")
	if err != nil {
		return fail(err)
	}
	found := map[string]bool{}
	for columns.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def sql.NullString
		if err := columns.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			columns.Close()
			return fail(err)
		}
		found[name] = true
	}
	if err := columns.Err(); err != nil {
		columns.Close()
		return fail(err)
	}
	columns.Close()
	for _, column := range []struct{ name, definition string }{{"student_number", "TEXT NOT NULL DEFAULT ''"}, {"activity_id", "INTEGER NOT NULL DEFAULT 1"}} {
		if !found[column.name] {
			if _, err := tx.Exec("ALTER TABLE students ADD COLUMN " + column.name + " " + column.definition); err != nil {
				return fail(err)
			}
		}
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS works_student_idx ON works(student_id,id DESC);
CREATE INDEX IF NOT EXISTS students_activity_idx ON students(activity_id,class_name,id);
CREATE UNIQUE INDEX IF NOT EXISTS students_number_idx ON students(activity_id,class_name,student_number) WHERE student_number<>'';
PRAGMA user_version=2;`); err != nil {
		return fail(fmt.Errorf("数据库迁移失败，请核对旧名单是否有重复学号: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	success = true
	return db, backup, nil
}

func getStudent(ctx context.Context, db *sql.DB, token string) (Student, error) {
	var s Student
	err := db.QueryRowContext(ctx, "SELECT id,activity_id,class_name,name,student_number,token FROM students WHERE token=?", token).Scan(&s.ID, &s.ActivityID, &s.ClassName, &s.Name, &s.StudentNumber, &s.Token)
	return s, err
}
func getActivity(ctx context.Context, db *sql.DB, id int64) (Activity, error) {
	var a Activity
	err := db.QueryRowContext(ctx, "SELECT id,title,deadline,archived,created_at FROM activities WHERE id=?", id).Scan(&a.ID, &a.Title, &a.Deadline, &a.Archived, &a.CreatedAt)
	return a, err
}
func listActivities(ctx context.Context, db *sql.DB) ([]Activity, error) {
	rows, err := db.QueryContext(ctx, `SELECT a.id,a.title,a.deadline,a.archived,a.created_at,
(SELECT count(*) FROM students WHERE activity_id=a.id),
(SELECT count(*) FROM students s WHERE s.activity_id=a.id AND EXISTS (SELECT 1 FROM works w WHERE w.student_id=s.id)),
(SELECT count(*) FROM works w JOIN students s ON s.id=w.student_id WHERE s.activity_id=a.id)
FROM activities a ORDER BY a.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var activities []Activity
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.Title, &a.Deadline, &a.Archived, &a.CreatedAt, &a.Students, &a.Submitted, &a.Works); err != nil {
			return nil, err
		}
		activities = append(activities, a)
	}
	return activities, rows.Err()
}
func listStudents(ctx context.Context, db *sql.DB, activityID int64) ([]Student, error) {
	rows, err := db.QueryContext(ctx, `SELECT s.id,s.activity_id,s.class_name,s.name,s.student_number,s.token,count(w.id) FROM students s LEFT JOIN works w ON w.student_id=s.id WHERE s.activity_id=? GROUP BY s.id ORDER BY s.class_name,s.student_number,s.id`, activityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var students []Student
	for rows.Next() {
		var s Student
		if err := rows.Scan(&s.ID, &s.ActivityID, &s.ClassName, &s.Name, &s.StudentNumber, &s.Token, &s.Count); err != nil {
			return nil, err
		}
		students = append(students, s)
	}
	return students, rows.Err()
}
func listWorks(ctx context.Context, db *sql.DB, studentID int64) ([]Work, error) {
	rows, err := db.QueryContext(ctx, "SELECT id,student_id,filename,original_name,title,created_at FROM works WHERE student_id=? ORDER BY id DESC", studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var works []Work
	for rows.Next() {
		var w Work
		if err := rows.Scan(&w.ID, &w.StudentID, &w.Filename, &w.OriginalName, &w.Title, &w.CreatedAt); err != nil {
			return nil, err
		}
		works = append(works, w)
	}
	return works, rows.Err()
}

type rosterEntry struct{ Number, Name string }

func parseRoster(text string) ([]rosterEntry, error) {
	var entries []rosterEntry
	seen := map[string]bool{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == '\t' || r == ',' || r == '，' })
		if len(parts) < 2 || !numberPattern.MatchString(parts[0]) {
			return nil, fmt.Errorf("第 %d 行请按“学号 姓名”填写", i+1)
		}
		name := strings.TrimSpace(strings.Join(parts[1:], " "))
		if len([]rune(name)) > 80 {
			return nil, fmt.Errorf("第 %d 行姓名过长", i+1)
		}
		if seen[parts[0]] {
			return nil, fmt.Errorf("第 %d 行学号重复，请核对名单", i+1)
		}
		seen[parts[0]] = true
		entries = append(entries, rosterEntry{parts[0], name})
	}
	if len(entries) == 0 || len(entries) > 200 {
		return nil, errors.New("请填写 1–200 行学号和姓名")
	}
	return entries, nil
}

func importRoster(ctx context.Context, db *sql.DB, activityID int64, className string, entries []rosterEntry) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var archived bool
	if err := tx.QueryRowContext(ctx, "SELECT archived FROM activities WHERE id=?", activityID).Scan(&archived); err != nil {
		return err
	}
	if archived {
		return errors.New("此活动已归档，请先恢复活动再导入名单")
	}
	for _, entry := range entries {
		var existingName string
		err := tx.QueryRowContext(ctx, "SELECT name FROM students WHERE activity_id=? AND class_name=? AND student_number=?", activityID, className, entry.Number).Scan(&existingName)
		if err == nil {
			if existingName != entry.Name {
				return fmt.Errorf("学号 %s 已对应 %s，请核对名单", entry.Number, existingName)
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT id FROM students WHERE activity_id=? AND class_name=? AND name=? AND student_number=''", activityID, className, entry.Name)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(ids) > 1 {
			return fmt.Errorf("%s 有多个同班同名旧空间，请先核对原二维码", entry.Name)
		}
		if len(ids) == 1 {
			_, err = tx.ExecContext(ctx, "UPDATE students SET student_number=? WHERE id=?", entry.Number, ids[0])
		} else {
			_, err = tx.ExecContext(ctx, "INSERT INTO students (activity_id,class_name,name,student_number,token) VALUES (?,?,?,?,?)", activityID, className, entry.Name, entry.Number, randomID())
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
