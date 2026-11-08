package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/PurpleDefender249/beebox/internal/config"
	"github.com/PurpleDefender249/beebox/internal/doctor"
	"github.com/PurpleDefender249/beebox/internal/recorder"
	"github.com/PurpleDefender249/beebox/internal/sample"
	"github.com/PurpleDefender249/beebox/internal/term"
	"github.com/PurpleDefender249/beebox/internal/version"
)

func init() {
	var drJSON, drStrict bool
	register(&command{
		name: "doctor", banner: true, group: "Everyday", summary: "Check this computer is ready to catch the next crash",
		long: "Checks the recorder, system logs, memory protection, swap, kernel crash logs and notifications, and prints the exact command to fix anything missing. Nothing is changed for you.",
		flags: func(fs *flag.FlagSet) func() {
			fs.BoolVar(&drJSON, "json", false, "print results as JSON")
			fs.BoolVar(&drStrict, "strict", false, "exit with status 1 if anything needs fixing")
			return nil
		},
		run: func(ctx context.Context, a *App, args []string) error { return a.doctor(drJSON, drStrict) },
	})

	var recConfig string
	register(&command{
		name: "record", group: "Service", summary: "Run the recorder (started for you by systemd)",
		long: "Runs the BeeBox recorder in the foreground. You normally never run this yourself: the beebox service starts it at boot. Settings are read from /etc/beebox/beebox.conf.",
		flags: func(fs *flag.FlagSet) func() {
			fs.StringVar(&recConfig, "config", "", "read settings from `FILE` instead of /etc/beebox/beebox.conf")
			return nil
		},
		run: func(ctx context.Context, a *App, args []string) error { return a.record(ctx, recConfig) },
	})

	var verJSON, verVerbose bool
	register(&command{
		name: "version", group: "Other", summary: "Print the version",
		long: "Prints the version, for example \"beebox 0.2.0\". Use --verbose for the commit, build date, Go version and platform (useful in bug reports), or --json for scripts.",
		flags: func(fs *flag.FlagSet) func() {
			fs.BoolVar(&verJSON, "json", false, "print the version and build details as JSON")
			fs.BoolVar(&verVerbose, "verbose", false, "also print the commit, build date, Go version and platform")
			return nil
		},
		run: func(ctx context.Context, a *App, args []string) error {
			switch {
			case verJSON:
				return json.NewEncoder(a.Stdout).Encode(version.Details())
			case verVerbose:
				fmt.Fprintln(a.Stdout, version.Long())
			default:
				fmt.Fprintln(a.Stdout, "beebox "+version.Short())
			}
			return nil
		},
	})

	register(&command{
		name: "help", group: "Other", args: "[command]", summary: "Show help for beebox or a command",
		run: func(ctx context.Context, a *App, args []string) error {
			if len(args) == 0 {
				a.printHelp()
				return nil
			}
			c := lookup(args[0])
			if c == nil {
				return usagef("%q is not a beebox command", args[0])
			}
			return ExitCodeError{a.Run(ctx, []string{c.name, "--help"})}
		},
	})
}

func (a *App) doctor(asJSON, strict bool) error {
	checks := doctor.Run(a.Paths)
	problems := 0
	for _, c := range checks {
		if c.Status == doctor.Fail || c.Status == doctor.Warn {
			problems++
		}
	}
	if asJSON {
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(checks); err != nil {
			return err
		}
	} else {
		st := a.Style
		w := a.Stdout
		fmt.Fprintf(w, "%s\n\n", st.Bold("BeeBox doctor"))
		for _, c := range checks {
			var mark string
			switch c.Status {
			case doctor.OK:
				mark = st.Green("✓") + " "
			case doctor.Warn:
				mark = st.Yellow("!") + " "
			case doctor.Fail:
				mark = st.Red("✗") + " "
			default:
				mark = st.Cyan("i") + " "
			}
			label := map[doctor.Status]string{doctor.OK: "ok", doctor.Warn: "fix", doctor.Fail: "fix now", doctor.Info: "optional"}[c.Status]
			fmt.Fprintf(w, "  %s%s %s %s\n", mark, term.Pad(c.Name, 26), c.Result, st.Dim("("+label+")"))
			if c.Status != doctor.OK {
				if c.Why != "" {
					fmt.Fprintf(w, "%s\n", st.Dim(term.Wrap(c.Why, min(a.Width, 96), strings.Repeat(" ", 31))))
				}
				if c.Fix != "" {
					fmt.Fprintf(w, "%s%s\n", strings.Repeat(" ", 31), st.Cyan(c.Fix))
				}
			}
		}
		fmt.Fprintln(w)
		switch problems {
		case 0:
			fmt.Fprintln(w, st.Green("✓")+" Everything BeeBox needs is in place.")
		case 1:
			fmt.Fprintln(w, st.Yellow("!")+" 1 thing to fix. The command to run is shown above.")
		default:
			fmt.Fprintf(w, "%s %d things to fix. The commands to run are shown above.\n", st.Yellow("!"), problems)
		}
	}
	if strict && problems > 0 {
		return ExitCodeError{ExitError}
	}
	return nil
}

func (a *App) record(ctx context.Context, cfgPath string) error {
	if os.Geteuid() != 0 && os.Getenv("BEEBOX_STATE_DIR") == "" {
		return errors.New("the recorder needs root. It is normally started by systemd: sudo systemctl enable --now beebox")
	}
	if cfgPath == "" {
		cfgPath = a.Paths.ConfigFile
	}
	cfg, warns, err := config.Load(cfgPath)
	log := newLogger(a)
	for _, w := range warns {
		log.Warn("config: " + w)
	}
	if err != nil {
		log.Warn("using default settings", "err", err)
	}
	return recorder.Run(ctx, recorder.Options{
		Config: cfg, Paths: a.Paths, Log: log,
		AnalysisDelay: analysisDelay(),
		Collector:     sample.Options{},
	})
}

func newLogger(a *App) *slog.Logger {
	level := slog.LevelInfo
	if strings.EqualFold(os.Getenv("BEEBOX_LOG"), "debug") {
		level = slog.LevelDebug
	}
	underJournal := os.Getenv("JOURNAL_STREAM") != ""
	h := slog.NewTextHandler(a.Stderr, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, at slog.Attr) slog.Attr {
			if underJournal && at.Key == slog.TimeKey && len(groups) == 0 {
				return slog.Attr{} // journald adds its own timestamps
			}
			return at
		},
	})
	return slog.New(h)
}
