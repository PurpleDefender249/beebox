// Package ring implements BeeBox's crash-safe on-disk ring buffer.
//
// Layout (all integers little-endian):
//
//	header (4096 bytes)
//	  0  magic      [8]byte "BEEBOXR1"
//	  8  version    uint32
//	 12  slotSize   uint32
//	 16  slotCount  uint32
//	 20  flags      uint32 (bit 0: clean stop)
//	 24  created    int64  Unix ms
//	 32  stopped    int64  Unix ms (0 while running)
//	 40  intervalMs uint32
//	 44  bootStart  int64  Unix seconds
//	 52  bootID     [32]byte
//	 84  kernel     [64]byte
//	148  crc32      uint32 over bytes [0,148)
//	slots (slotCount x slotSize)
//	  0  magic  uint32 "SLOT"
//	  4  length uint32
//	  8  seq    uint64 (1-based, monotonically increasing)
//	 16  crc32  uint32 over payload
//	 20  payload (JSON-encoded sample.Sample)
//
// Every slot is self-validating, so a torn write during a crash only loses
// that one slot. The file is preallocated, so the recorder never needs new
// disk blocks while the system is in trouble.
package ring

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/PurpleDefender249/beebox/internal/sample"
)

const (
	headerSize   = 4096
	slotHeader   = 20
	slotMagic    = 0x544F4C53 // "SLOT"
	version      = 1
	flagClean    = 1
	hdrCRCOffset = 148

	// DefaultSlotSize fits a full sample with headroom.
	DefaultSlotSize = 2048
)

var magic = [8]byte{'B', 'E', 'E', 'B', 'O', 'X', 'R', '1'}

// ErrNotRing is returned for files that are not BeeBox rings.
var ErrNotRing = errors.New("not a BeeBox ring file")

// Header describes a ring.
type Header struct {
	SlotSize  int
	SlotCount int
	Clean     bool
	Created   time.Time
	Stopped   time.Time
	Interval  time.Duration
	BootStart time.Time
	BootID    string
	Kernel    string
}

func (h Header) marshal() []byte {
	b := make([]byte, headerSize)
	copy(b[0:8], magic[:])
	binary.LittleEndian.PutUint32(b[8:], version)
	binary.LittleEndian.PutUint32(b[12:], uint32(h.SlotSize))  // #nosec G115 -- bounded by Create
	binary.LittleEndian.PutUint32(b[16:], uint32(h.SlotCount)) // #nosec G115 -- bounded by Create
	var flags uint32
	if h.Clean {
		flags |= flagClean
	}
	binary.LittleEndian.PutUint32(b[20:], flags)
	binary.LittleEndian.PutUint64(b[24:], uint64(h.Created.UnixMilli())) // #nosec G115
	if !h.Stopped.IsZero() {
		binary.LittleEndian.PutUint64(b[32:], uint64(h.Stopped.UnixMilli())) // #nosec G115
	}
	binary.LittleEndian.PutUint32(b[40:], uint32(h.Interval.Milliseconds())) // #nosec G115
	if !h.BootStart.IsZero() {
		binary.LittleEndian.PutUint64(b[44:], uint64(h.BootStart.Unix())) // #nosec G115
	}
	copy(b[52:84], h.BootID)
	copy(b[84:148], h.Kernel)
	binary.LittleEndian.PutUint32(b[hdrCRCOffset:], crc32.ChecksumIEEE(b[:hdrCRCOffset]))
	return b
}

func unmarshalHeader(b []byte) (Header, error) {
	var h Header
	if len(b) < headerSize || string(b[0:8]) != string(magic[:]) {
		return h, ErrNotRing
	}
	if binary.LittleEndian.Uint32(b[hdrCRCOffset:]) != crc32.ChecksumIEEE(b[:hdrCRCOffset]) {
		return h, fmt.Errorf("%w: header checksum mismatch", ErrNotRing)
	}
	if v := binary.LittleEndian.Uint32(b[8:]); v != version {
		return h, fmt.Errorf("unsupported ring version %d", v)
	}
	h.SlotSize = int(binary.LittleEndian.Uint32(b[12:]))
	h.SlotCount = int(binary.LittleEndian.Uint32(b[16:]))
	h.Clean = binary.LittleEndian.Uint32(b[20:])&flagClean != 0
	h.Created = time.UnixMilli(int64(binary.LittleEndian.Uint64(b[24:]))) // #nosec G115
	if ms := int64(binary.LittleEndian.Uint64(b[32:])); ms > 0 {          // #nosec G115
		h.Stopped = time.UnixMilli(ms)
	}
	h.Interval = time.Duration(binary.LittleEndian.Uint32(b[40:])) * time.Millisecond
	if s := int64(binary.LittleEndian.Uint64(b[44:])); s > 0 { // #nosec G115
		h.BootStart = time.Unix(s, 0)
	}
	h.BootID = strings.TrimRight(string(b[52:84]), "\x00")
	h.Kernel = strings.TrimRight(string(b[84:148]), "\x00")
	if h.SlotSize < slotHeader+64 || h.SlotSize > 1<<20 || h.SlotCount < 1 || h.SlotCount > 1<<20 {
		return h, fmt.Errorf("%w: implausible geometry", ErrNotRing)
	}
	return h, nil
}

// Writer appends samples to a ring.
//
// The file is memory-mapped and the mapping is locked into RAM, so appending
// a sample is a plain memory copy: it never allocates, never waits for the
// disk and cannot be stalled by memory pressure. Getting the data onto disk
// is a separate step (Sync), which the recorder runs on its own goroutine so
// that a slow disk can never stop sampling.
type Writer struct {
	mu     sync.Mutex // guards mem, hdr, seq, buf, done
	syncMu sync.Mutex // held while flushing; see Sync
	f      *os.File
	mem    []byte
	hdr    Header
	seq    uint64
	buf    []byte
	done   bool
}

// Create makes a new ring at path, replacing any existing file atomically.
func Create(path string, hdr Header) (*Writer, error) {
	if hdr.SlotSize == 0 {
		hdr.SlotSize = DefaultSlotSize
	}
	if hdr.SlotCount < 1 || hdr.SlotCount > 1<<20 {
		return nil, fmt.Errorf("invalid slot count %d", hdr.SlotCount)
	}
	if hdr.Created.IsZero() {
		hdr.Created = time.Now()
	}
	hdr.Clean = false
	hdr.Stopped = time.Time{}

	tmp := path + ".new"
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644) // #nosec G302 G304 -- world-readable by design, see PRIVACY.md
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Writer, error) {
		f.Close()
		os.Remove(tmp)
		return nil, err
	}
	size := int64(headerSize) + int64(hdr.SlotCount)*int64(hdr.SlotSize)
	// On copy-on-write filesystems (Btrfs, the default on Fedora and
	// openSUSE) every overwrite would need new blocks; turning CoW off for
	// this one file, while it is still empty, makes the preallocation real.
	disableCoW(f)
	// Reserve real blocks so later writes cannot fail with ENOSPC.
	if err := syscall.Fallocate(int(f.Fd()), 0, 0, size); err != nil { // #nosec G115
		if err := f.Truncate(size); err != nil {
			return fail(err)
		}
	}
	if _, err := f.WriteAt(hdr.marshal(), 0); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fail(err)
	}
	syncDir(path)
	return mapWriter(f, hdr, 0)
}

// Resume reopens an existing ring for appending, continuing its sequence.
// It is used when the recorder restarts within the same boot so that the
// history leading up to a crash stays in one place.
func Resume(path string) (*Writer, error) {
	c, err := Read(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0) // #nosec G304 -- recorder-owned path
	if err != nil {
		return nil, err
	}
	var maxSeq uint64
	slot := make([]byte, slotHeader)
	for i := 0; i < c.Header.SlotCount; i++ {
		off := int64(headerSize) + int64(i)*int64(c.Header.SlotSize)
		if _, err := f.ReadAt(slot, off); err != nil {
			break
		}
		if binary.LittleEndian.Uint32(slot[0:]) == slotMagic {
			if s := binary.LittleEndian.Uint64(slot[8:]); s > maxSeq {
				maxSeq = s
			}
		}
	}
	hdr := c.Header
	hdr.Clean = false
	hdr.Stopped = time.Time{}
	w, err := mapWriter(f, hdr, maxSeq)
	if err != nil {
		return nil, err
	}
	w.mu.Lock()
	copy(w.mem[:headerSize], hdr.marshal())
	w.mu.Unlock()
	return w, w.Sync()
}

func mapWriter(f *os.File, hdr Header, seq uint64) (*Writer, error) {
	size := headerSize + hdr.SlotCount*hdr.SlotSize
	mem, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED) // #nosec G115
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("map ring: %w", err)
	}
	// Pin the pages so writing a sample never has to wait for memory. This
	// needs CAP_IPC_LOCK; without it the ring still works, just unpinned.
	_ = syscall.Mlock(mem)
	return &Writer{f: f, mem: mem, hdr: hdr, seq: seq, buf: make([]byte, hdr.SlotSize)}, nil
}

// Header returns the ring's header.
func (w *Writer) Header() Header { return w.hdr }

// Seq returns the number of samples written so far.
func (w *Writer) Seq() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.seq
}

// Append copies s into the next slot, trimming optional fields if the
// encoded sample would not fit. It does not wait for the disk.
func (w *Writer) Append(s sample.Sample) error {
	payload, err := encodeFit(s, w.hdr.SlotSize-slotHeader)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return errors.New("ring is closed")
	}
	w.seq++
	b := w.buf
	clear(b)
	binary.LittleEndian.PutUint32(b[0:], slotMagic)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(payload))) // #nosec G115 -- bounded by slot size
	binary.LittleEndian.PutUint64(b[8:], w.seq)
	binary.LittleEndian.PutUint32(b[16:], crc32.ChecksumIEEE(payload))
	copy(b[slotHeader:], payload)
	idx := int((w.seq - 1) % uint64(w.hdr.SlotCount)) // #nosec G115
	off := headerSize + idx*w.hdr.SlotSize
	copy(w.mem[off:off+w.hdr.SlotSize], b)
	return nil
}

// Sync writes changed pages to disk and waits for the disk to confirm. It
// is safe to call from another goroutine while Append runs.
func (w *Writer) Sync() error {
	// syncMu keeps Close from unmapping while a flush is running; mu is
	// not held during msync, so Append never waits for the disk.
	w.syncMu.Lock()
	defer w.syncMu.Unlock()
	w.mu.Lock()
	if w.done {
		w.mu.Unlock()
		return nil
	}
	mem := w.mem
	w.mu.Unlock()
	return msync(mem)
}

// Close marks the ring as cleanly stopped (if clean), flushes it and
// releases the mapping.
func (w *Writer) Close(clean bool) error {
	w.mu.Lock()
	if w.done {
		w.mu.Unlock()
		return nil
	}
	if clean {
		w.hdr.Clean = true
		w.hdr.Stopped = time.Now()
		copy(w.mem[:headerSize], w.hdr.marshal())
	}
	w.done = true
	mem := w.mem
	w.mu.Unlock()

	w.syncMu.Lock()
	defer w.syncMu.Unlock()
	errSync := msync(mem)
	errUnmap := syscall.Munmap(mem)
	errFsync := w.f.Sync()
	errClose := w.f.Close()
	return errors.Join(errSync, errUnmap, errFsync, errClose)
}

func msync(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	// #nosec G103 -- msync on our own mapping; the stdlib has no wrapper
	_, _, errno := syscall.Syscall(syscall.SYS_MSYNC, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), syscall.MS_SYNC)
	if errno != 0 {
		return errno
	}
	return nil
}

// encodeFit marshals s, dropping lower-value fields until it fits in max.
func encodeFit(s sample.Sample, max int) ([]byte, error) {
	for step := 0; ; step++ {
		b, err := json.Marshal(s)
		if err != nil {
			return nil, err
		}
		if len(b) <= max {
			return b, nil
		}
		switch step {
		case 0:
			if len(s.Kmsg) > 2 {
				s.Kmsg = s.Kmsg[:2]
			}
		case 1:
			if len(s.TopCPU) > 3 {
				s.TopCPU = s.TopCPU[:3]
			}
			if len(s.TopMem) > 3 {
				s.TopMem = s.TopMem[:3]
			}
		case 2:
			for i := range s.Kmsg {
				if len(s.Kmsg[i]) > 80 {
					s.Kmsg[i] = s.Kmsg[i][:80]
				}
			}
			s.TopCPU = nil
		case 3:
			s.Kmsg, s.TopMem, s.Temps = nil, nil, nil
		default:
			return nil, fmt.Errorf("sample of %d bytes does not fit slot payload %d", len(b), max)
		}
	}
}

// Contents is everything read back from a ring.
type Contents struct {
	Header  Header
	Samples []sample.Sample // oldest first
	Corrupt int             // slots that failed validation (torn writes)
}

// ReadHeader reads only the header of the ring at path.
func ReadHeader(path string) (Header, error) {
	f, err := os.Open(path) // #nosec G304 -- caller-chosen ring path
	if err != nil {
		return Header{}, err
	}
	defer f.Close()
	b := make([]byte, headerSize)
	if _, err := io.ReadFull(f, b); err != nil {
		return Header{}, fmt.Errorf("%w: %w", ErrNotRing, err)
	}
	return unmarshalHeader(b)
}

// Read loads and validates every slot of the ring at path. It is safe to
// call while a Writer is appending: slots caught mid-write fail their
// checksum and are skipped.
func Read(path string) (Contents, error) {
	var c Contents
	f, err := os.Open(path) // #nosec G304 -- caller-chosen ring path
	if err != nil {
		return c, err
	}
	defer f.Close()
	hb := make([]byte, headerSize)
	if _, err := io.ReadFull(f, hb); err != nil {
		return c, fmt.Errorf("%w: %w", ErrNotRing, err)
	}
	if c.Header, err = unmarshalHeader(hb); err != nil {
		return c, err
	}
	type entry struct {
		seq uint64
		s   sample.Sample
	}
	entries := make([]entry, 0, c.Header.SlotCount)
	slot := make([]byte, c.Header.SlotSize)
	for i := 0; i < c.Header.SlotCount; i++ {
		off := int64(headerSize) + int64(i)*int64(c.Header.SlotSize)
		n, err := f.ReadAt(slot, off)
		if n < slotHeader {
			if err != nil && !errors.Is(err, io.EOF) {
				return c, err
			}
			break
		}
		if binary.LittleEndian.Uint32(slot[0:]) != slotMagic {
			continue // never written
		}
		ln := int(binary.LittleEndian.Uint32(slot[4:]))
		seq := binary.LittleEndian.Uint64(slot[8:])
		if ln <= 0 || slotHeader+ln > n {
			c.Corrupt++
			continue
		}
		payload := slot[slotHeader : slotHeader+ln]
		if crc32.ChecksumIEEE(payload) != binary.LittleEndian.Uint32(slot[16:]) {
			c.Corrupt++
			continue
		}
		var s sample.Sample
		if err := json.Unmarshal(payload, &s); err != nil {
			c.Corrupt++
			continue
		}
		entries = append(entries, entry{seq: seq, s: s})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].seq < entries[j].seq })
	c.Samples = make([]sample.Sample, len(entries))
	for i, e := range entries {
		c.Samples[i] = e.s
	}
	return c, nil
}

func syncDir(path string) {
	if d, err := os.Open(filepath.Dir(path)); err == nil { // #nosec G304
		_ = d.Sync()
		d.Close()
	}
}

const (
	btrfsMagic    = 0x9123683e
	fsIocGetFlags = 0x80086601 // FS_IOC_GETFLAGS (_IOR('f', 1, long))
	fsIocSetFlags = 0x40086602 // FS_IOC_SETFLAGS (_IOW('f', 2, long))
	fsNoCowFlag   = 0x00800000 // FS_NOCOW_FL
)

// disableCoW sets the No_COW attribute (chattr +C) on an empty file on
// Btrfs. It is best-effort: on other filesystems, or if it fails, the ring
// still works with ordinary copy-on-write.
func disableCoW(f *os.File) {
	var st syscall.Statfs_t
	if err := syscall.Fstatfs(int(f.Fd()), &st); err != nil || uint32(st.Type) != btrfsMagic { // #nosec G115 -- fd and magic fit
		return
	}
	var flags int
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), fsIocGetFlags, uintptr(unsafe.Pointer(&flags))); e != 0 { // #nosec G103 -- ioctl needs a pointer to an int
		return
	}
	flags |= fsNoCowFlag
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), fsIocSetFlags, uintptr(unsafe.Pointer(&flags))) // #nosec G103 -- as above
}
