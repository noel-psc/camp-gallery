package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestByteBudgetIsSharedAndCancellable(t *testing.T) {
	budget := newByteBudget(128 << 10)
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reader := pacedReader{ctx: context.Background(), reader: bytes.NewReader(make([]byte, 32<<10)), budget: budget}
			if _, err := io.Copy(io.Discard, reader); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if elapsed := time.Since(start); elapsed < 650*time.Millisecond {
		t.Fatalf("global rate was exceeded: %v", elapsed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := budget.wait(ctx, 1); err != context.Canceled {
		t.Fatalf("cancellation not propagated: %v", err)
	}
	// Cancelling a queued waiter must not postpone later transfers.
	budget.next = time.Now().Add(time.Second)
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	before := budget.next
	if err := budget.wait(ctx, bandwidthChunk); err != context.DeadlineExceeded {
		t.Fatalf("waiting did not time out: %v", err)
	}
	if !budget.next.Equal(before) {
		t.Fatal("cancelled transfer reserved future bandwidth")
	}
}

func TestFullUploadSlotsAndQueueKeepControlPagesResponsive(t *testing.T) {
	s := newTestSite(t)
	s.seed(t, 12)
	s.login(t)
	s.app.uploadBudget = newByteBudget(256 << 10)
	payload := append(append([]byte{}, tinyPNG...), make([]byte, 64<<10)...)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.upload(t, "token-"+strconv.Itoa(i), randomID(), "拥挤时上传", "作品.png", payload, 200)
		}(i)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(s.app.uploadQueue) <= cap(s.app.uploadSlots) {
		if time.Now().After(deadline) {
			t.Fatal("test did not fill the upload queue")
		}
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	for _, path := range []string{"/", "/admin", "/healthz"} {
		s.get(t, path, 200)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("control requests stalled behind uploads: %v", elapsed)
	}
	wg.Wait()
	if count := s.workCount(t); count != 12 {
		t.Fatalf("queued requests did not all complete: %d", count)
	}
	s.assertClean(t)
}

func TestMediaAndExportUseOneDownloadBudget(t *testing.T) {
	s := newTestSite(t)
	s.seed(t, 1)
	s.login(t)
	payload := append(append([]byte{}, tinyPNG...), make([]byte, 128<<10)...)
	s.upload(t, "token-0", randomID(), "测试", "图片.png", payload, 200)
	s.app.downloadBudget = newByteBudget(128 << 10)
	start := time.Now()
	var wg sync.WaitGroup
	for _, path := range []string{"/s/token-0/file/1", "/admin/export?activity=1"} {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			response, err := s.client.Get(s.server.URL + path)
			if err != nil {
				t.Error(err)
				return
			}
			defer response.Body.Close()
			_, err = io.Copy(io.Discard, response.Body)
			if err != nil || response.StatusCode != http.StatusOK {
				t.Errorf("download failed: %d %v", response.StatusCode, err)
			}
		}(path)
	}
	s.get(t, "/healthz", 200)
	wg.Wait()
	if elapsed := time.Since(start); elapsed < 1500*time.Millisecond {
		t.Fatalf("media and ZIP did not share the same cap: %v", elapsed)
	}
}
