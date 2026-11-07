package analyze

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/PurpleDefender249/beebox/internal/journal"
	"github.com/PurpleDefender249/beebox/internal/pstore"
	"github.com/PurpleDefender249/beebox/internal/ring"
	"github.com/PurpleDefender249/beebox/internal/sample"
)

// GatherOptions selects the boot to analyse and where evidence comes from.
type GatherOptions struct {
	// Boot is a journal boot ID or a relative offset ("-1" = previous
	// boot). Ignored when RingPath is set and Boot is empty.
	Boot string
	// RingPath is an archived ring from the boot being analysed (optional).
	RingPath string
	// Previous is true when the boot being analysed is the one immediately
	// before the current boot; only then are pstore crash logs attributed.
	Previous bool

	Journal    journal.Source
	PstoreDirs []string
	ProcDir    string
	Generator  string
	Env        *Env
}

// Gather collects evidence for one boot. Missing sources are not errors:
// whatever is available is analysed, and the report notes what was missing.
func Gather(ctx context.Context, opt GatherOptions) (Input, error) {
	in := Input{Generator: opt.Generator, Now: time.Now()}
	if opt.Env != nil {
		in.Env = *opt.Env
	} else {
		in.Env = DetectEnv(opt.ProcDir)
	}

	if opt.RingPath != "" {
		rc, err := ring.Read(opt.RingPath)
		if err != nil {
			return in, fmt.Errorf("read recording %s: %w", opt.RingPath, err)
		}
		in.Ring = &rc
		in.BootID = rc.Header.BootID
		in.BootStart = rc.Header.BootStart
		in.Kernel = rc.Header.Kernel
		in.Clean = rc.Header.Clean
		if opt.Boot == "" {
			opt.Boot = rc.Header.BootID
		}
	}
	if opt.Boot == "" {
		opt.Boot = "-1"
	}
	if !journal.ValidSelector(opt.Boot) {
		return in, fmt.Errorf("%q is not a boot ID or offset like -1", opt.Boot)
	}

	if opt.Journal != nil {
		all, err := opt.Journal.Entries(ctx, opt.Boot, false, 1500)
		switch {
		case err == nil:
			in.Journal = all
			if in.BootID == "" && len(all) > 0 {
				in.BootID = sample.NormalizeBootID(all[len(all)-1].BootID)
			}
			if k, err := opt.Journal.Entries(ctx, opt.Boot, true, 3000); err == nil {
				in.KernelLog = k
			}
			if w, err := opt.Journal.Warnings(ctx, opt.Boot, 500); err == nil {
				in.Warnings = w
			}
			if in.BootStart.IsZero() {
				if fs, ok := opt.Journal.(interface {
					FirstEntry(context.Context, string) (time.Time, error)
				}); ok {
					if t, err := fs.FirstEntry(ctx, opt.Boot); err == nil {
						in.BootStart = t
					}
				}
			}
		case errors.Is(err, journal.ErrNoBoot), errors.Is(err, journal.ErrUnavailable):
			// Analyse with whatever else we have.
		default:
			if in.Ring == nil {
				return in, err
			}
		}
	}
	if in.Ring == nil && len(in.Journal) == 0 {
		return in, fmt.Errorf("no evidence for boot %s: BeeBox has no recording and the journal has no entries for it", opt.Boot)
	}

	if opt.Previous {
		since := in.BootStart
		if since.IsZero() && in.Ring != nil && len(in.Ring.Samples) > 0 {
			since = time.UnixMilli(in.Ring.Samples[0].T)
		}
		if !since.IsZero() {
			in.Pstore = pstore.Find(opt.PstoreDirs, since)
		}
	}
	return in, nil
}
