package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/PurpleDefender249/beebox/internal/paths"
	"github.com/PurpleDefender249/beebox/internal/report"
)

func init() {
	var wait time.Duration
	var test bool
	register(&command{
		name: "notify", group: "Service", summary: "Tell the logged-in user about a new crash report",
		long: "Shows a desktop notification when there is a report you haven't seen, with a button that opens it. It runs automatically when you log in; run it with --test to check that notifications work on your desktop.",
		flags: func(fs *flag.FlagSet) func() {
			fs.DurationVar(&wait, "wait", 2*time.Minute, "how long to wait for the recorder to finish analysing the previous boot")
			fs.BoolVar(&test, "test", false, "show a sample notification")
			return nil
		},
		run: func(ctx context.Context, a *App, args []string) error { return a.notify(ctx, wait, test) },
	})
}

func (a *App) notify(ctx context.Context, wait time.Duration, test bool) error {
	bin, err := exec.LookPath("notify-send")
	if err != nil {
		return errors.New("notify-send is not installed, so BeeBox can't show desktop notifications (install libnotify-bin or libnotify)")
	}
	if test {
		_, err := sendNotification(ctx, bin, "BeeBox notifications work", "If your computer crashes, freezes or loses power, BeeBox will tell you why the next time you log in.", false)
		return err
	}

	// Give the recorder time to analyse the previous boot after start-up.
	deadline := time.Now().Add(wait)
	for fileExists(a.Paths.AnalyzingMarker()) && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}

	r, err := a.store().Latest()
	if errors.Is(err, report.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	seenFile := filepath.Join(paths.UserStateDir(), "notified")
	seen, _ := os.ReadFile(seenFile) // #nosec G304 -- per-user state file
	last := strings.TrimSpace(string(seen))
	if last == r.ID {
		return nil
	}
	if last == "" && time.Since(r.GeneratedAt) > 24*time.Hour {
		// First run after installation: don't announce old reports.
		return markSeen(seenFile, r.ID)
	}
	if last != "" && r.ID < last {
		return nil
	}
	if err := markSeen(seenFile, r.ID); err != nil {
		fmt.Fprintf(a.Stderr, "beebox: couldn't remember the report was shown: %v\n", err)
	}

	title := "Your computer didn't shut down properly"
	if r.Clean {
		title = "Your computer shut itself down"
	}
	body := r.Verdict.Title + "."
	if r.Verdict.ID == "unknown" {
		body = "BeeBox couldn't find a clear cause."
	}
	actions := supportsActions(ctx, bin)
	if actions {
		body += " Open the report to see what happened and what to do."
	} else {
		body += " Run 'beebox last' in a terminal, or open BeeBox from your apps, to see what happened."
	}
	action, err := sendNotification(ctx, bin, title, body, actions)
	if err != nil {
		return err
	}
	if action == "open" {
		return a.view(ctx, r.ID, false)
	}
	return nil
}

func markSeen(file, id string) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(id+"\n"), 0o600)
}

func supportsActions(ctx context.Context, bin string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, bin, "--help").CombinedOutput() // #nosec G204 -- fixed binary
	return bytes.Contains(out, []byte("--action")) && bytes.Contains(out, []byte("--wait"))
}

// sendNotification shows a notification and, when actions are supported,
// waits for the user's choice and returns the action key.
func sendNotification(ctx context.Context, bin, title, body string, actions bool) (string, error) {
	icon := "dialog-warning"
	for _, p := range []string{"/usr/share/icons/hicolor/scalable/apps/beebox.svg", "/usr/local/share/icons/hicolor/scalable/apps/beebox.svg"} {
		if fileExists(p) {
			icon = "beebox"
			break
		}
	}
	args := []string{"--app-name=BeeBox", "--icon=" + icon, "--urgency=normal", "--category=system"}
	if actions {
		args = append(args, "--action=open=Open report", "--wait")
	}
	args = append(args, "--", title, body)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, args...).Output() // #nosec G204 -- fixed binary, argv only
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(out) == 0 {
			return "", nil // dismissed or timed out
		}
		return "", fmt.Errorf("notify-send: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func analysisDelay() time.Duration {
	if v := os.Getenv("BEEBOX_ANALYSIS_DELAY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 3 * time.Second
}
