package server

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestIdleTimeoutReaderResetsAfterData(t *testing.T) {
	reader, writer := io.Pipe()
	wrapped := withIdleTimeout(reader, 40*time.Millisecond)
	defer wrapped.Close()

	go func() {
		_, _ = io.WriteString(writer, "first")
		time.Sleep(10 * time.Millisecond)
		_, _ = io.WriteString(writer, "second")
		_ = writer.Close()
	}()

	data, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if string(data) != "firstsecond" {
		t.Fatalf("data=%q, want firstsecond", data)
	}
}

func TestIdleTimeoutReaderClosesBlockedStream(t *testing.T) {
	reader, writer := io.Pipe()
	wrapped := withIdleTimeout(reader, 20*time.Millisecond)
	defer wrapped.Close()

	buf := make([]byte, 8)
	start := time.Now()
	_, err := wrapped.Read(buf)
	if err == nil || !strings.Contains(err.Error(), "idle timeout") {
		t.Fatalf("Read error=%v, want idle timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("idle timeout took %s", elapsed)
	}
	_ = writer.Close()
}
