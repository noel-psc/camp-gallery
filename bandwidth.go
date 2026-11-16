package main

import (
	"context"
	"io"
	"net/http"
	"time"
)

const bandwidthChunk = 32 << 10

// A budget is shared by all large transfers in one direction. A shared gate
// serializes waiting, which can be cancelled without reserving future bandwidth.
// Small pages, static assets and control requests never enter these budgets.
type byteBudget struct {
	gate chan struct{}
	rate int64
	next time.Time
}

func newByteBudget(bytesPerSecond int64) *byteBudget {
	if bytesPerSecond <= 0 {
		return nil
	}
	return &byteBudget{gate: make(chan struct{}, 1), rate: bytesPerSecond}
}

func (b *byteBudget) wait(ctx context.Context, n int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b == nil || n == 0 {
		return nil
	}
	select {
	case b.gate <- struct{}{}:
		defer func() { <-b.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	now := time.Now()
	start := b.next
	// Keep at most one chunk of credit. Credit from a slightly late timer
	// prevents 4 KiB multipart reads from being throttled by timer granularity.
	floor := now.Add(-time.Duration(int64(bandwidthChunk) * int64(time.Second) / b.rate))
	if start.Before(floor) {
		start = floor
	}
	finish := start.Add(time.Duration(int64(n) * int64(time.Second) / b.rate))
	if delay := finish.Sub(now); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	b.next = finish
	return nil
}

type pacedReader struct {
	ctx    context.Context
	reader io.Reader
	budget *byteBudget
}

func (p pacedReader) Read(buffer []byte) (int, error) {
	if len(buffer) > bandwidthChunk {
		buffer = buffer[:bandwidthChunk]
	}
	n, err := p.reader.Read(buffer)
	if waitErr := p.budget.wait(p.ctx, n); waitErr != nil {
		return 0, waitErr
	}
	return n, err
}

type pacedWriter struct {
	http.ResponseWriter
	ctx    context.Context
	budget *byteBudget
}

func (p pacedWriter) Unwrap() http.ResponseWriter { return p.ResponseWriter }
func (p pacedWriter) Write(data []byte) (int, error) {
	written := 0
	for len(data) > 0 {
		size := min(len(data), bandwidthChunk)
		if err := p.budget.wait(p.ctx, size); err != nil {
			return written, err
		}
		// A client that stops receiving cannot hold a download indefinitely.
		_ = http.NewResponseController(p.ResponseWriter).SetWriteDeadline(time.Now().Add(30 * time.Second))
		n, err := p.ResponseWriter.Write(data[:size])
		written += n
		if err != nil {
			return written, err
		}
		if n != size {
			return written, io.ErrShortWrite
		}
		data = data[size:]
	}
	return written, nil
}
