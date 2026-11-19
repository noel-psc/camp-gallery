package main

import (
	"net/url"
	"strings"
	"testing"
)

func TestPromptToolsInStudentSubmissionPage(t *testing.T) {
	s := newTestSite(t)
	s.seed(t, 1)
	check := func(path string) {
		page := s.get(t, path, 200)
		if strings.Count(page, `data-copy-prompt=`) != 5 {
			t.Fatal("expected five prompt copy buttons")
		}
		if !strings.Contains(page, "/static/prompts.js?v=") || !strings.Contains(page, `id="prompt-manual" hidden`) {
			t.Fatal("missing script or hidden fallback")
		}
		if strings.Contains(page, "请参考我上传的人像照片") || strings.Contains(page, "请帮我润色我上传的故事") {
			t.Fatal("full prompts exposed on page")
		}
		if strings.Contains(page, `id="upload-form"`) && strings.Index(page, `id="prompt-tools"`) > strings.Index(page, `id="upload-form"`) {
			t.Fatal("prompt tools should precede upload form")
		}
	}
	check("/s/token-0")
	s.upload(t, "token-0", randomID(), "作品", "photo.png", tinyPNG, 200)
	check("/s/token-0?edit=1")
	check("/s/token-0")
	s.login(t)
	s.post(t, "/admin/activities/1", url.Values{"action": {"archive"}}, 303)
	if strings.Contains(s.get(t, "/s/token-0?edit=1", 200), `id="prompt-tools"`) {
		t.Fatal("archived space shows creation controls")
	}
}
