package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PurpleDefender249/beebox/internal/ring"
	"github.com/PurpleDefender249/beebox/internal/sample"
)

// dump is a hidden diagnostics command: it prints the raw samples of a
// recording so that a wrong verdict can be investigated (and attached to a
// bug report) without a hex editor.
func init() {
	var dumpLast int
	var dumpJSON bool
	register(&command{
		name: "dump", group: "Investigate", args: "[file]", hidden: true,
		summary: "Print the raw samples of a recording (for bug reports)",
		long:    "Prints the last samples of a BeeBox recording. With no file, uses the most recent archived recording in /var/lib/beebox/boots (the boot that ended last). Use 'current' for this boot's recording.",
		flags: func(fs *flag.FlagSet) func() {
			fs.IntVar(&dumpLast, "last", 120, "how many of the final samples to print (0 = all)")
			fs.BoolVar(&dumpJSON, "json", false, "print the header and samples as JSON")
			return nil
		},
		run: func(ctx context.Context, a *App, args []string) error {
			if len(args) > 1 {
				return usagef("give at most one recording file")
			}
			path := ""
			if len(args) == 1 {
				path = args[0]
			}
			return a.dump(path, dumpLast, dumpJSON)
		},
	})
}

func (a *App) dump(path string, last int, asJSON bool) error {
	switch path {
	case "current":
		path = a.Paths.RingFile()
	case "":
		m, _ := filepath.Glob(filepath.Join(a.Paths.BootsDir(), "*.ring"))
		if len(m) == 0 {
			return fmt.Errorf("no archived recordings in %s yet (use 'beebox dump current' for this boot)", a.Paths.BootsDir())
		}
		sort.Strings(m)
		path = m[len(m)-1]
	}
	c, err := ring.Read(path)
	if err != nil {
		return err
	}
	ss := c.Samples
	if last > 0 && len(ss) > last {
		ss = ss[len(ss)-last:]
	}
	if asJSON {
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			File    string          `json:"file"`
			Header  ring.Header     `json:"header"`
			Corrupt int             `json:"corrupt_slots"`
			Samples []sample.Sample `json:"samples"`
		}{path, c.Header, c.Corrupt, ss})
	}

	h := c.Header
	fmt.Fprintf(a.Stdout, "file      %s\n", path)
	fmt.Fprintf(a.Stdout, "boot      %s (kernel %s, started %s)\n", h.BootID, h.Kernel, h.BootStart.Local().Format(time.DateTime))
	fmt.Fprintf(a.Stdout, "recording %d slots × %s, created %s, clean stop: %v\n", h.SlotCount, h.Interval, h.Created.Local().Format(time.DateTime), h.Clean)
	fmt.Fprintf(a.Stdout, "samples   %d readable, %d corrupt; showing %d\n\n", len(c.Samples), c.Corrupt, len(ss))
	fmt.Fprintln(a.Stdout, "time      cpu%  avail%  mem-some mem-full io-full  lag-ms  flags   biggest app")
	for _, s := range ss {
		top := "-"
		if len(s.TopMem) > 0 {
			top = fmt.Sprintf("%s %s", s.TopMem[0].Name, mib(s.TopMem[0].MemKB))
		}
		fmt.Fprintf(a.Stdout, "%s %5.1f %6.1f %8.1f %8.1f %7.1f %7d  %-7s %s\n",
			time.UnixMilli(s.T).Local().Format("15:04:05"), s.CPU, s.MemAvailPct(),
			s.PSI.MemSome, s.PSI.MemFull, s.PSI.IOFull, s.LagMs, flagString(s), top)
		if s.OOMKills > 0 {
			fmt.Fprintf(a.Stdout, "          ↳ %d process(es) killed for memory\n", s.OOMKills)
		}
		for _, k := range s.Kmsg {
			fmt.Fprintf(a.Stdout, "          ↳ kernel: %s\n", k)
		}
	}
	return nil
}

func flagString(s sample.Sample) string {
	var f []string
	for _, x := range []struct {
		bit  uint32
		name string
	}{{sample.FlagStart, "S"}, {sample.FlagStop, "E"}, {sample.FlagResume, "Z"}, {sample.FlagLate, "L"}} {
		if s.Flags&x.bit != 0 {
			f = append(f, x.name)
		}
	}
	if len(f) == 0 {
		return "-"
	}
	return strings.Join(f, "")
}

func mib(kb uint64) string {
	if kb >= 1<<20 {
		return fmt.Sprintf("%.1f GiB", float64(kb)/(1<<20))
	}
	return fmt.Sprintf("%d MiB", kb>>10)
}
