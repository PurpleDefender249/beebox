package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/PurpleDefender249/beebox/internal/doctor"
)

func TestAsRoot(t *testing.T) {
	for _, tc := range []struct {
		fix, script string
		restart, ok bool
	}{
		{"sudo systemctl enable --now beebox", "systemctl enable --now beebox", false, true},
		{"sudo env DEBIAN_FRONTEND=noninteractive apt install -y kdump-tools && sudo sed -i 's/^USE_KDUMP=.*/USE_KDUMP=1/' /etc/default/kdump-tools && sudo update-grub   # then restart",
			"env DEBIAN_FRONTEND=noninteractive apt-get install -y kdump-tools && sed -i 's/^USE_KDUMP=.*/USE_KDUMP=1/' /etc/default/kdump-tools && update-grub", true, true},
		{"echo kernel.sysrq=176 | sudo tee /etc/sysctl.d/90-sysrq.conf && sudo sysctl --system",
			"echo kernel.sysrq=176 | tee /etc/sysctl.d/90-sysrq.conf && sysctl --system", false, true},
		{"printf '[zram0]\\n' | sudo tee /etc/systemd/zram-generator.conf", "printf '[zram0]\\n' | tee /etc/systemd/zram-generator.conf", false, true},
		{"Reinstall the beebox package, or copy packaging/xdg/beebox-notify.desktop to /etc/xdg/autostart/", "", false, false},
		{"", "", false, false},
	} {
		script, restart, ok := asRoot(tc.fix)
		if script != tc.script || restart != tc.restart || ok != tc.ok {
			t.Errorf("asRoot(%q) = %q, %v, %v; want %q, %v, %v", tc.fix, script, restart, ok, tc.script, tc.restart, tc.ok)
		}
	}
}

func TestPlanSetup(t *testing.T) {
	checks := []doctor.Check{
		{ID: "recorder", Status: doctor.OK, Result: "Recording"},
		{ID: "oom", Status: doctor.Warn, Fix: "sudo systemctl enable --now systemd-oomd"},
		{ID: "pstore", Status: doctor.Info, Fix: "sudo update-grub   # then restart"},
		{ID: "swap", Status: doctor.Warn, Fix: "sudo apt install -y zram-tools"},
		{ID: "notify", Status: doctor.Info, Fix: "Reinstall the beebox package"},
		{ID: "sensors", Status: doctor.Info, Result: "None: virtual machines have no temperature sensors"},
	}
	steps, manual := planSetup(checks, map[string]bool{"swap": true})
	if len(steps) != 2 || steps[0].check.ID != "oom" || steps[1].check.ID != "pstore" || !steps[1].restart {
		t.Fatalf("steps = %+v", steps)
	}
	if len(manual) != 1 || manual[0].ID != "notify" {
		t.Fatalf("manual = %+v", manual)
	}
}

// Without root, setup shows its plan with --dry-run and refuses otherwise.
func TestSetupDryRunAndRoot(t *testing.T) {
	a, out, errb := testApp(t)
	if code := a.Run(context.Background(), []string{"setup", "--dry-run"}); code != ExitOK {
		t.Fatalf("dry run: exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "BeeBox setup") {
		t.Fatalf("dry run output: %s", out.String())
	}
}
