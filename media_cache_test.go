package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"testing"
)

func TestDeletedPhotoReplacementHasDistinctCacheIdentity(t *testing.T) {
	s := newTestSite(t)
	s.seed(t, 1)
	picture := func(c color.RGBA) []byte {
		img := image.NewRGBA(image.Rect(0, 0, 1, 1))
		img.SetRGBA(0, 0, c)
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	mediaPath := func() string {
		page := s.get(t, "/s/token-0", 200)
		match := regexp.MustCompile(`<img src="([^"]+)"`).FindStringSubmatch(page)
		if len(match) != 2 {
			t.Fatal("missing photo URL")
		}
		return match[1]
	}
	oldPhoto := picture(color.RGBA{R: 255, A: 255})
	newPhoto := picture(color.RGBA{B: 255, A: 255})
	s.upload(t, "token-0", randomID(), "照片", "photo.png", oldPhoto, 200)
	oldURL := mediaPath()
	response, err := s.client.Get(s.server.URL + oldURL)
	if err != nil {
		t.Fatal(err)
	}
	etag := response.Header.Get("ETag")
	response.Body.Close()
	works, _ := listWorks(context.Background(), s.app.db, 1)
	s.post(t, "/s/token-0/work/1", url.Values{"action": {"delete"}, "work_version": {works[0].Filename}}, 303)
	s.upload(t, "token-0", randomID(), "照片", "photo.png", newPhoto, 200)
	newURL := mediaPath()
	if oldURL == newURL {
		t.Fatal("replacement reused cached photo URL")
	}
	if etag == "" {
		t.Fatal("missing file-specific ETag")
	}
	s.get(t, oldURL, 404)
	request, _ := http.NewRequest("GET", s.server.URL+newURL, nil)
	request.Header.Set("If-None-Match", etag)
	response, err = s.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !bytes.Equal(data, newPhoto) {
		t.Fatal("replacement did not return new photo")
	}
	request, _ = http.NewRequest("GET", s.server.URL+"/s/token-0/file/1", nil)
	request.Header.Set("If-None-Match", etag)
	response, err = s.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "private, no-cache" {
		t.Fatal("legacy URL must revalidate replacement")
	}
	if got := response.Header.Get("ETag"); got == etag {
		t.Fatal(fmt.Sprintf("replacement reused ETag %s", got))
	}
}
