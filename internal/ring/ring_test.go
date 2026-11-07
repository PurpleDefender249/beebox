package ring

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PurpleDefender249/beebox/internal/sample"
)

func newRing(t *testing.T, slots int) (string, *Writer) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "current.ring")
	w, err := Create(path, Header{SlotCount: slots, Interval: time.Second, BootID: strings.Repeat("ab", 16), Kernel: "6.11.0-test", BootStart: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatal(err)
	}
	return path, w
}

func TestWriteReadAndWrap(t *testing.T) {
	path, w := newRing(t, 10)
	for i := 1; i <= 25; i++ {
		if err := w.Append(sample.Sample{T: int64(i), CPU: float64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	c, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Samples) != 10 {
		t.Fatalf("got %d samples, want 10 (ring wraps)", len(c.Samples))
	}
	for i, s := range c.Samples {
		if want := int64(16 + i); s.T != want {
			t.Fatalf("sample %d has T=%d, want %d (oldest first)", i, s.T, want)
		}
	}
	if c.Header.Clean {
		t.Fatal("ring should not be clean before Close(true)")
	}
	if err := w.Close(true); err != nil {
		t.Fatal(err)
	}
	h, err := ReadHeader(path)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Clean || h.Stopped.IsZero() || h.BootID != strings.Repeat("ab", 16) || h.Kernel != "6.11.0-test" {
		t.Fatalf("unexpected header after clean close: %+v", h)
	}
}

func TestTornSlotIsSkipped(t *testing.T) {
	path, w := newRing(t, 5)
	for i := 1; i <= 3; i++ {
		if err := w.Append(sample.Sample{T: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	_ = w.Sync()
	// Corrupt the payload of slot 1 as if power failed mid-write.
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("XXXX"), int64(headerSize+DefaultSlotSize+slotHeader+2)); err != nil {
		t.Fatal(err)
	}
	f.Close()
	c, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Samples) != 2 || c.Corrupt != 1 {
		t.Fatalf("got %d samples, %d corrupt; want 2 and 1", len(c.Samples), c.Corrupt)
	}
}

func TestResumeContinuesSequence(t *testing.T) {
	path, w := newRing(t, 20)
	for i := 1; i <= 4; i++ {
		_ = w.Append(sample.Sample{T: int64(i)})
	}
	_ = w.Close(true)
	w2, err := Resume(path)
	if err != nil {
		t.Fatal(err)
	}
	if w2.Seq() != 4 {
		t.Fatalf("resumed at seq %d, want 4", w2.Seq())
	}
	if w2.Header().Clean {
		t.Fatal("resumed ring must be marked unclean")
	}
	_ = w2.Append(sample.Sample{T: 5})
	_ = w2.Close(false)
	c, _ := Read(path)
	if len(c.Samples) != 5 || c.Samples[4].T != 5 || c.Header.Clean {
		t.Fatalf("after resume: %d samples, clean=%v", len(c.Samples), c.Header.Clean)
	}
}

func TestOversizedSampleIsTrimmed(t *testing.T) {
	path, w := newRing(t, 3)
	s := sample.Sample{T: 1}
	for i := 0; i < 10; i++ {
		s.Kmsg = append(s.Kmsg, strings.Repeat("k", 220))
		s.TopMem = append(s.TopMem, sample.App{Name: strings.Repeat("n", 32), Count: 9, MemKB: 123456})
		s.TopCPU = append(s.TopCPU, sample.App{Name: strings.Repeat("c", 32), Count: 9, CPU: 12.5})
	}
	if err := w.Append(s); err != nil {
		t.Fatalf("append oversized: %v", err)
	}
	c, err := Read(path)
	if err != nil || len(c.Samples) != 1 {
		t.Fatalf("read back: %v, %d samples", err, len(c.Samples))
	}
}

// The recorder appends from its sampling loop while a separate goroutine
// flushes; this must be race-free (run with -race) and every sample must
// be readable straight after Append, before any flush.
func TestConcurrentAppendAndSync(t *testing.T) {
	path, w := newRing(t, 50)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			if err := w.Sync(); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for i := 1; i <= 120; i++ {
		if err := w.Append(sample.Sample{T: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	<-done
	c, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Samples) != 50 || c.Samples[49].T != 120 {
		t.Fatalf("got %d samples, last T=%d; want 50 ending at 120", len(c.Samples), c.Samples[len(c.Samples)-1].T)
	}
	if err := w.Close(false); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(false); err == nil {
		t.Log("second Close is tolerated")
	}
}

func TestRejectsForeignFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(path, make([]byte, 8192), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("expected error for a non-ring file")
	}
}

func FuzzRead(f *testing.F) {
	f.Add(make([]byte, headerSize+100))
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "fuzz.ring")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _ = Read(path) // must never panic
	})
}
