package relay

import (
	"context"
	"errors"
	"io"
	"sync"
)

// streamfinalizer.go — Stream finalization helper.
// Port of src/app/api/v1/relay/chat/completions/streamFinalizer.ts
//
// Fixed per adversarial review:
// - uses errors.Is(err, io.EOF) so wrapped EOF is clean completion
// - implements io.ReadCloser; Close delegates and finalizes (covers early
//   abandonment / client disconnect that never produces a Read error)
// - FinalizeOnce (sync.Once) guarantees the callback runs exactly once

// FinalizeOnce guards a finalize callback so it runs at most once.
type FinalizeOnce struct {
	once sync.Once
	fn   func(error)
}

// NewFinalizeOnce wraps a finalize callback.
func NewFinalizeOnce(fn func(error)) *FinalizeOnce {
	return &FinalizeOnce{fn: fn}
}

// Finalize invokes the callback exactly once. Subsequent calls are no-ops.
func (f *FinalizeOnce) Finalize(err error) {
	f.once.Do(func() {
		if f.fn != nil {
			f.fn(err)
		}
	})
}

// FinalizingReader wraps an io.Reader and invokes onFinalize exactly once on
// EOF, error, or Close. Implements io.ReadCloser so closing the wrapper
// (e.g. HTTP response body close, client disconnect) triggers finalization.
type FinalizingReader struct {
	r   io.Reader
	fin *FinalizeOnce
}

// NewFinalizingReader wraps r so onFinalize fires once on EOF/error/Close.
func NewFinalizingReader(r io.Reader, onFinalize func(error)) *FinalizingReader {
	return &FinalizingReader{r: r, fin: NewFinalizeOnce(onFinalize)}
}

func (fr *FinalizingReader) Read(p []byte) (int, error) {
	n, err := fr.r.Read(p)
	if err != nil {
		if errors.Is(err, io.EOF) {
			fr.fin.Finalize(nil)
		} else {
			fr.fin.Finalize(err)
		}
	}
	return n, err
}

// Close finalizes (with context.Canceled) and closes the underlying reader if
// it is an io.Closer. Safe to call multiple times; finalization runs once.
func (fr *FinalizingReader) Close() error {
	fr.fin.Finalize(context.Canceled)
	if c, ok := fr.r.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
