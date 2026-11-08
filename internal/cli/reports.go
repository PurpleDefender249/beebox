package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/PurpleDefender249/beebox/internal/analyze"
	"github.com/PurpleDefender249/beebox/internal/journal"
	"github.com/PurpleDefender249/beebox/internal/paths"
	"github.com/PurpleDefender249/beebox/internal/pstore"
	"github.com/PurpleDefender249/beebox/internal/render"
	"github.com/PurpleDefender249/beebox/internal/report"
	"github.com/PurpleDefender249/beebox/internal/scenario"
	"github.com/PurpleDefender249/beebox/internal/term"
	"github.com/PurpleDefender249/beebox/internal/version"
)

type outputFlags struct {
	json    bool
	verbose bool
	html    string
}

func (o *outputFlags) register(fs *flag.FlagSet) {
	fs.BoolVar(&o.json, "json", false, "print the report as JSON")
	fs.BoolVar(&o.verbose, "verbose", false, "include every evidence line and finding")
	fs.StringVar(&o.html, "html", "", "write the report as a web page to `FILE`")
}

func init() {
	var lastOut, showOut outputFlags
	register(&command{
		name: "last", banner: true, group: "Everyday", summary: "Show the most recent crash report",
		long:  "Shows why your computer last crashed, froze or lost power, and what you can do about it. Reports are written automatically when the computer starts after an unexpected shutdown.",
		flags: func(fs *flag.FlagSet) func() { lastOut.register(fs); return nil },
		run: func(ctx context.Context, a *App, args []string) error {
			if len(args) > 0 {
				return usagef("'last' takes no arguments; to pick a report use 'beebox show <id>'")
			}
			return a.showReport("", lastOut)
		},
	})
	register(&command{
		name: "show", banner: true, group: "Everyday", args: "[id]", summary: "Show one report (default: the latest)",
		long:  "Shows the report with the given ID. A unique prefix of the ID is enough; 'beebox list' shows the IDs.",
		flags: func(fs *flag.FlagSet) func() { showOut.register(fs); return nil },
		run: func(ctx context.Context, a *App, args []string) error {
			if len(args) > 1 {
				return usagef("give at most one report ID")
			}
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			return a.showReport(id, showOut)
		},
	})

	var listJSON, listIDs bool
	register(&command{
		name: "list", banner: true, group: "Everyday", summary: "List crash reports, newest first",
		flags: func(fs *flag.FlagSet) func() {
			fs.BoolVar(&listJSON, "json", false, "print as JSON")
			fs.BoolVar(&listIDs, "ids", false, "print only report IDs (for scripts)")
			return nil
		},
		run: func(ctx context.Context, a *App, args []string) error { return a.list(listJSON, listIDs) },
	})

	var viewPrint, viewSample bool
	var viewScenario string
	register(&command{
		name: "view", banner: true, group: "Everyday", args: "[id]", summary: "Open a report in your web browser",
		long: "Writes the report as a self-contained web page in your cache folder and opens it. The page works offline and loads nothing from the internet. Use --sample to see what a report looks like before anything has gone wrong.",
		flags: func(fs *flag.FlagSet) func() {
			fs.BoolVar(&viewPrint, "print-path", false, "only write the page and print its path")
			fs.BoolVar(&viewSample, "sample", false, "show a sample report (nothing is wrong with your computer)")
			fs.StringVar(&viewScenario, "scenario", "out-of-memory", "which sample to show with --sample: "+strings.Join(scenario.Kinds(), ", "))
			return nil
		},
		run: func(ctx context.Context, a *App, args []string) error {
			if viewSample {
				return a.viewSample(ctx, viewScenario, viewPrint)
			}
			id := ""
			if len(args) > 0 {
				id = args[0]
			}
			return a.view(ctx, id, viewPrint)
		},
	})

	var anBoot, anRing string
	var anSave bool
	var anOut outputFlags
	register(&command{
		name: "analyze", banner: true, group: "Investigate", summary: "Work out why a previous boot ended",
		long: "Analyses a previous boot on demand, using BeeBox's recording if one exists and the system journal. This also works for crashes that happened before BeeBox was installed, as long as the journal kept them. Run it with sudo to read the full kernel log.",
		flags: func(fs *flag.FlagSet) func() {
			fs.StringVar(&anBoot, "boot", "-1", "boot to analyse: an offset like -1 (previous boot) or a journal `BOOT_ID`")
			fs.StringVar(&anRing, "ring", "", "use this BeeBox recording `FILE` (from /var/lib/beebox/boots)")
			fs.BoolVar(&anSave, "save", false, "save the result as a report (needs root)")
			anOut.register(fs)
			return nil
		},
		run: func(ctx context.Context, a *App, args []string) error {
			return a.analyze(ctx, anBoot, anRing, anSave, anOut)
		},
	})
}

func (a *App) store() report.Store { return report.Store{Dir: a.Paths.ReportsDir()} }

func (a *App) showReport(id string, o outputFlags) error {
	st := a.store()
	var r *report.Report
	var err error
	if id == "" {
		r, err = st.Latest()
	} else {
		r, err = st.Load(id)
	}
	if errors.Is(err, report.ErrNotFound) {
		if id != "" {
			return fmt.Errorf("no report %q. Run 'beebox list' to see the reports there are", id)
		}
		if o.json {
			fmt.Fprintln(a.Stdout, "null")
			return nil
		}
		a.noReports()
		return nil
	}
	if err != nil {
		return err
	}
	return a.output(r, o)
}

func (a *App) output(r *report.Report, o outputFlags) error {
	if o.html != "" {
		if err := writeHTML(o.html, r); err != nil {
			return err
		}
		fmt.Fprintf(a.Stderr, "Wrote %s\n", o.html)
		if !o.json {
			return nil
		}
	}
	if o.json {
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	render.Text(a.Stdout, r, render.TextOptions{Style: a.Style, Width: a.Width, Verbose: o.verbose})
	return nil
}

func (a *App) noReports() {
	st := a.Style
	fmt.Fprintln(a.Stdout, st.Bold("No crash reports yet."))
	fmt.Fprintln(a.Stdout)
	fmt.Fprintln(a.Stdout, term.Wrap("BeeBox writes a report when your computer starts after a crash, freeze or power loss. To check that it's recording, run 'beebox status'. To look into a crash from before BeeBox was installed, run 'sudo beebox analyze'.", min(a.Width, 90), ""))
}

func writeHTML(path string, r *report.Report) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 -- user-chosen output path
	if err != nil {
		return err
	}
	if err := render.HTML(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (a *App) list(asJSON, idsOnly bool) error {
	all, err := a.store().All()
	if err != nil {
		return err
	}
	// Newest first.
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	if idsOnly {
		for _, r := range all {
			fmt.Fprintln(a.Stdout, r.ID)
		}
		return nil
	}
	if asJSON {
		type item struct {
			ID         string    `json:"id"`
			Ended      time.Time `json:"ended"`
			Verdict    string    `json:"verdict"`
			Title      string    `json:"title"`
			Confidence int       `json:"confidence"`
		}
		items := make([]item, 0, len(all))
		for _, r := range all {
			items = append(items, item{r.ID, r.LastSeen, r.Verdict.ID, r.Verdict.Title, r.Verdict.Confidence})
		}
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(items)
	}
	if len(all) == 0 {
		a.noReports()
		return nil
	}
	st := a.Style
	fmt.Fprintf(a.Stdout, "%s\n", st.Bold(term.Pad("Ended", 20)+term.Pad("What happened", 50)+"Report ID"))
	for _, r := range all {
		title := r.Verdict.Title
		if w := analyze.ConfidenceWord(r.Verdict.Confidence); r.Verdict.ID != "unknown" {
			title = fmt.Sprintf("%s (%s)", title, w)
		}
		fmt.Fprintf(a.Stdout, "%s%s%s\n", term.Pad(r.LastSeen.Local().Format("Mon 2 Jan 15:04"), 20), term.Pad(truncRunes(title, 48), 50), st.Dim(r.ID))
	}
	fmt.Fprintf(a.Stdout, "\n%s\n", st.Dim("Show one with: beebox show <id>   Open in a browser: beebox view <id>"))
	return nil
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func (a *App) view(ctx context.Context, id string, printOnly bool) error {
	dir := paths.UserCacheDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	st := a.store()
	var r *report.Report
	var err error
	if id == "" {
		r, err = st.Latest()
	} else {
		r, err = st.Load(id)
	}
	var file string
	switch {
	case errors.Is(err, report.ErrNotFound) && id == "":
		file = filepath.Join(dir, "no-reports.html")
		f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304
		if err != nil {
			return err
		}
		rec := readRecordingState(a.Paths)
		msg := "BeeBox is not recording right now, so it can't catch the next crash. Run 'beebox doctor' in a terminal to fix that."
		if rec.Active {
			msg = fmt.Sprintf("BeeBox has been recording since %s. Your computer hasn't crashed, frozen or lost power since then.", rec.Since.Local().Format("Monday 2 January at 15:04"))
		}
		if err := render.HTMLEmpty(f, render.EmptyState{Recording: rec.Active, Since: rec.Since, Message: msg}); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	case errors.Is(err, report.ErrNotFound):
		return fmt.Errorf("no report %q. Run 'beebox list' to see the reports there are", id)
	case err != nil:
		return err
	default:
		file = filepath.Join(dir, "report-"+r.ID+".html")
		if err := writeHTML(file, r); err != nil {
			return err
		}
	}
	if printOnly {
		fmt.Fprintln(a.Stdout, file)
		return nil
	}
	if err := openInBrowser(ctx, file); err != nil {
		fmt.Fprintf(a.Stdout, "The report is at %s\n", file)
		fmt.Fprintf(a.Stderr, "Couldn't open a browser (%v). Open the file above in any web browser.\n", err)
		return nil
	}
	fmt.Fprintf(a.Stderr, "Opened %s\n", file)
	return nil
}

func (a *App) viewSample(ctx context.Context, kind string, printOnly bool) error {
	in, err := scenario.Build(kind, time.Now().Add(-5*time.Minute).Truncate(time.Second))
	if err != nil {
		return usagef("%v (choose from: %s)", err, strings.Join(scenario.Kinds(), ", "))
	}
	in.Generator = "beebox " + version.Short()
	r := analyze.Analyze(in)
	if r == nil {
		return usagef("the %q sample ends with a normal shutdown, so there is no report to show", kind)
	}
	r.Sample = true
	r.Notes = append([]string{"This is a sample made by 'beebox view --sample'. Nothing happened to your computer."}, r.Notes...)
	dir := paths.UserCacheDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file := filepath.Join(dir, "sample-"+kind+".html")
	if err := writeHTML(file, r); err != nil {
		return err
	}
	if printOnly {
		fmt.Fprintln(a.Stdout, file)
		return nil
	}
	if err := openInBrowser(ctx, file); err != nil {
		fmt.Fprintf(a.Stdout, "The sample report is at %s\n", file)
		return nil
	}
	fmt.Fprintf(a.Stderr, "Opened %s\n", file)
	return nil
}

func openInBrowser(ctx context.Context, file string) error {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return errors.New("no graphical session")
	}
	opener := ""
	for _, c := range []string{"xdg-open", "gio"} {
		if p, err := exec.LookPath(c); err == nil {
			opener = p
			break
		}
	}
	if opener == "" {
		return errors.New("xdg-open is not installed")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := []string{file}
	if filepath.Base(opener) == "gio" {
		args = []string{"open", file}
	}
	cmd := exec.CommandContext(ctx, opener, args...) // #nosec G204 -- fixed opener, our own file path
	cmd.Stdout, cmd.Stderr = nil, nil
	return cmd.Run()
}

func (a *App) analyze(ctx context.Context, boot, ringPath string, save bool, o outputFlags) error {
	if !journal.ValidSelector(boot) {
		return usagef("--boot must be an offset like -1 or a 32-character boot ID")
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(a.Stderr, a.Style.Dim("Tip: run with sudo so BeeBox can read the full kernel log."))
	}
	if save && os.Geteuid() != 0 {
		return errors.New("--save needs root: sudo beebox analyze --save")
	}
	opt := analyze.GatherOptions{
		RingPath:   ringPath,
		Journal:    journal.Journalctl{},
		PstoreDirs: pstore.DefaultDirs,
		Previous:   boot == "-1",
		Generator:  "beebox " + version.Short(),
	}
	if ringPath == "" || boot != "-1" {
		opt.Boot = boot
	}
	in, err := analyze.Gather(ctx, opt)
	if err != nil {
		if errors.Is(err, journal.ErrUnavailable) {
			return errors.New("journalctl was not found; BeeBox needs the systemd journal to analyse boots it didn't record")
		}
		return err
	}
	r := analyze.Analyze(in)
	if r == nil {
		if o.json {
			fmt.Fprintln(a.Stdout, "null")
			return nil
		}
		fmt.Fprintln(a.Stdout, a.Style.Green("✓")+" That boot ended with a normal shutdown or restart. Nothing went wrong.")
		return nil
	}
	if save {
		file, err := a.store().Save(r)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Stderr, "Saved %s\n", file)
	}
	return a.output(r, o)
}
