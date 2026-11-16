package main

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func wordFixture(t *testing.T, document string) []byte {
	t.Helper()
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	for _, entry := range []struct{ name, body string }{
		{"[Content_Types].xml", `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`},
		{"_rels/.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
		{"word/document.xml", document},
	} {
		file, err := archive.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(file, entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func (s *testSite) uploadArticle(t *testing.T, id, article string, files map[string][]byte, status int) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, field := range []struct{ name, value string }{{"csrf", s.csrf}, {"request_id", id}, {"title", "我的研学文章"}, {"article", article}} {
		if err := writer.WriteField(field.name, field.value); err != nil {
			t.Fatal(err)
		}
	}
	if len(files) == 0 {
		// Browsers include an empty file part when only text was submitted.
		if _, err := writer.CreateFormFile("files", ""); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range files {
		file, err := writer.CreateFormFile("files", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest("POST", s.server.URL+"/s/token-0", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	output, _ := io.ReadAll(response.Body)
	if response.StatusCode != status {
		t.Fatalf("article upload: %d wanted %d: %s", response.StatusCode, status, output)
	}
	s.assertClean(t)
}

func TestPastedArticleViewIsolationRetryAndExport(t *testing.T) {
	site := newTestSite(t)
	site.seed(t, 2)
	article := "今天我用 AI 创作。\n第二段 <script>alert(1)</script>"
	site.uploadArticle(t, "article-request-1", article, nil, 200)
	site.uploadArticle(t, "article-request-1", article, nil, 200)
	if site.workCount(t) != 1 {
		t.Fatal("retry duplicated article")
	}
	page := site.get(t, "/s/token-0", 200)
	if !strings.Contains(page, "今天我用 AI 创作。") || !strings.Contains(page, "&lt;script&gt;") || strings.Contains(page, "<script>alert") {
		t.Fatal("article missing or unescaped")
	}
	site.get(t, "/s/token-1/file/1", 404)
	if body := site.get(t, "/s/token-0/file/1", 200); body != article {
		t.Fatalf("article bytes changed: %q", body)
	}
	site.login(t)
	data := site.get(t, "/admin/export?activity=1", 200)
	archive, err := zip.NewReader(strings.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, file := range archive.File {
		if strings.HasSuffix(file.Name, ".txt") {
			if !strings.HasPrefix(file.Name, "一班/000_学生0/") {
				t.Fatal(file.Name)
			}
			reader, _ := file.Open()
			text, _ := io.ReadAll(reader)
			reader.Close()
			if string(text) != article {
				t.Fatal("export lost article content")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("article not exported")
	}
	if _, err := site.app.db.Exec("UPDATE activities SET deadline='2000-01-01T00:00:00+08:00' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	site.uploadArticle(t, "article-after-deadline", "截止后", nil, 403)
	site.get(t, "/s/token-0/file/1", 200)
}

func TestDOCXAndPhotoBatchPreservesOriginals(t *testing.T) {
	site := newTestSite(t)
	site.seed(t, 1)
	doc := wordFixture(t, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>我的文章</w:t></w:r></w:p></w:body></w:document>`)
	originals := map[string][]byte{"研学文章.docx": doc, "照片.png": tinyPNG}
	site.uploadArticle(t, "docx-photo-batch", "", originals, 200)
	if site.workCount(t) != 2 {
		t.Fatal("mixed batch not saved")
	}
	works, err := listWorks(context.Background(), site.app.db, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, work := range works {
		response, err := site.client.Get(site.server.URL + "/s/token-0/file/" + strconv.FormatInt(work.ID, 10))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if !bytes.Equal(data, originals[work.OriginalName]) {
			t.Fatal("original file changed")
		}
		if work.IsDocument() && !strings.HasPrefix(response.Header.Get("Content-Disposition"), "attachment;") {
			t.Fatal("DOCX must download as attachment")
		}
	}
	if page := site.get(t, "/s/token-0", 200); !strings.Contains(page, "下载 DOCX") {
		t.Fatal("document download not shown")
	}
	site.login(t)
	data := site.get(t, "/admin/export?activity=1", 200)
	archive, err := zip.NewReader(strings.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, file := range archive.File {
		for name, original := range originals {
			if strings.HasSuffix(file.Name, "_"+name) {
				reader, _ := file.Open()
				data, _ := io.ReadAll(reader)
				reader.Close()
				if !bytes.Equal(data, original) {
					t.Fatal("export changed original")
				}
				found++
			}
		}
	}
	if found != 2 {
		t.Fatal("mixed batch export missing files")
	}
}

func TestInvalidArticlesRollbackEntireBatch(t *testing.T) {
	for _, scenario := range []struct {
		name, text string
		files      map[string][]byte
	}{
		{name: "empty"},
		{name: "too-long", text: strings.Repeat("字", maxArticleChars+1)},
		{name: "invalid-utf8", text: string([]byte{0xff})},
		{name: "renamed-file", files: map[string][]byte{"文章.docx": tinyPNG, "照片.png": tinyPNG}},
		{name: "invalid-xml", files: map[string][]byte{"文章.docx": wordFixture(t, `<document/>`), "照片.png": tinyPNG}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			site := newTestSite(t)
			site.seed(t, 1)
			site.uploadArticle(t, "invalid-article-batch", scenario.text, scenario.files, 400)
			if site.workCount(t) != 0 {
				t.Fatal("invalid batch partially committed")
			}
		})
	}
}
