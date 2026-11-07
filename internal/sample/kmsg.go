package sample

import (
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// notableKernel matches informational kernel messages worth keeping even
// though their priority is below "warning".
var notableKernel = regexp.MustCompile(`(?i)` + strings.Join([]string{
	`out of memory`, `oom-kill`, `oom_reaper`, `killed process`,
	`PM: (suspend|hibernation) (entry|exit)`, `PM: suspend`, `ACPI: PM:`, `Freezing`, `resume`,
	`thermal`, `critical temperature`, `throttl`,
	`hung_task`, `blocked for more than`, `soft lockup`, `hard lockup`, `rcu.*stall`,
	`NVRM: Xid`, `fallen off the bus`, `amdgpu.*(timeout|reset|fault|hang)`, `i915.*(hang|reset)`, `GPU HANG`, `nouveau.*(fault|error|TRAP)`,
	`I/O error`, `nvme.*(timeout|reset|down)`, `EXT4-fs error`, `BTRFS (error|critical)`, `XFS.*(error|shutdown)`, `read-only`,
	`sysrq`, `watchdog`, `panic`, `Oops`, `BUG:`,
	`Power key pressed`, `battery`,
}, "|"))

// kernelNoise matches the bulk of multi-line kernel reports (stack frames,
// register dumps, the OOM killer's memory and task tables). They are logged
// at warning level but say nothing a crash report needs, and a single OOM
// kill prints dozens of them, which would push the one line that matters
// ("Killed process …") out of the sample. The lines the analyser does use
// are kept: "RIP:"/"pc :" (the faulting function) and "CPU: … Comm:" (the
// program). The cgroup path in "oom-kill:constraint=" is dropped for
// privacy as well as noise.
var kernelNoise = regexp.MustCompile(`^(?:` + strings.Join([]string{
	`(?:\? )?[\w.$]+\+0x[0-9a-f]+/0x[0-9a-f]+(?: \[[\w]+\])?$`, // stack frame
	`(?:RSP|RAX|RBX|RCX|RDX|RSI|RDI|RBP|R\d{1,2}|FS|GS|CS|DS|ES|CR\d|DR\d|PKRU|EFLAGS|ORIG_RAX):`,
	`(?:x\d{1,2}|sp|lr|pstate)\s*:`, // arm64 registers (pc : is kept)
	`</?(?:TASK|IRQ|NMI|SOFTIRQ)>`, `Call [Tt]race:`, `Code: `, `Hardware name:`, `Modules linked in:`, `Workqueue:`,
	`---\[ end trace`, `Mem-Info:`, `Tasks state`, `\[\s*\d+\]`, `oom-kill:constraint=`,
	`Node \d`, `(?:DMA|DMA32|Normal|Movable|HighMem)[ :]`, `lowmem_reserve`, `active_anon:`,
	`\d+ (?:total pagecache|pages)`, `Free swap`, `Total swap`, `Swap cache`,
}, "|") + `)`)

// kmsgReader tails /dev/kmsg and buffers notable messages until the next
// sample drains them. This keeps kernel evidence inside the ring even on
// systems where the journal is not persistent.
type kmsgReader struct {
	mu    sync.Mutex
	queue []string
	f     *os.File
}

const (
	kmsgQueueMax = 64
	kmsgLineMax  = 220
)

// startKmsg opens path (normally /dev/kmsg), skips existing history and
// starts a background reader. It returns nil if the device is unavailable.
func startKmsg(ctx context.Context, path string) *kmsgReader {
	f, err := os.Open(path) // #nosec G304 -- fixed device path
	if err != nil {
		return nil
	}
	// SEEK_END on /dev/kmsg moves to the next record that will be written.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil
	}
	k := &kmsgReader{f: f}
	go func() {
		<-ctx.Done()
		f.Close()
	}()
	go k.loop()
	return k
}

func (k *kmsgReader) loop() {
	buf := make([]byte, 8192)
	for {
		n, err := k.f.Read(buf)
		if err != nil {
			// EPIPE means records were overwritten before we read them;
			// the next read continues with the oldest available record.
			if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.EINTR) {
				continue
			}
			return
		}
		if msg, ok := ParseKmsgRecord(buf[:n]); ok {
			k.push(msg)
		}
	}
}

// ParseKmsgRecord parses one /dev/kmsg record ("prio,seq,usec,flags;text")
// and reports whether it is notable.
func ParseKmsgRecord(rec []byte) (string, bool) {
	s := string(rec)
	header, text, ok := strings.Cut(s, ";")
	if !ok {
		return "", false
	}
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i] // drop continuation (dictionary) lines
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	prioStr, _, _ := strings.Cut(header, ",")
	prio, err := strconv.Atoi(prioStr)
	if err != nil {
		return "", false
	}
	if kernelNoise.MatchString(text) {
		return "", false
	}
	level := prio & 7
	if level > 4 && !notableKernel.MatchString(text) {
		return "", false
	}
	if len(text) > kmsgLineMax {
		text = text[:kmsgLineMax]
	}
	return text, true
}

func (k *kmsgReader) push(msg string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.queue) >= kmsgQueueMax {
		k.queue = k.queue[1:]
	}
	k.queue = append(k.queue, msg)
}

// drain returns up to max queued messages.
func (k *kmsgReader) drain(max int) []string {
	if k == nil {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.queue) == 0 {
		return nil
	}
	n := len(k.queue)
	if n > max {
		n = max
	}
	out := make([]string, n)
	copy(out, k.queue[:n])
	k.queue = k.queue[n:]
	return out
}
