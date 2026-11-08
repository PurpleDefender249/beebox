package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PurpleDefender249/beebox/internal/pstore"
)

func TestPstoreCheck(t *testing.T) {
	write := func(root, rel, content string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name   string
		files  map[string]string
		tools  []string // commands that exist
		units  []string // enabled systemd units
		pm     string
		status Status
		result string
		fix    string
	}{
		{"working", map[string]string{"module/pstore/parameters/backend": "efi_pstore\n"}, nil, nil, "apt", OK, "backend: efi_pstore", ""},
		{"kdump ready", map[string]string{"kernel/kexec_crash_loaded": "1\n"}, []string{"kdump-config"}, []string{"kdump-tools.service"}, "apt", OK, "kdump", ""},
		// Kali: loaded by hand with kdump-config, but the service is off,
		// so the next restart switches it off again.
		{"kdump loaded, service off", map[string]string{"kernel/kexec_crash_loaded": "1\n"}, []string{"kdump-config"}, nil, "apt", Warn, "next restart", "systemctl enable kdump-tools"},
		{"bios, apt", map[string]string{"module/pstore/parameters/backend": "(null)\n"}, []string{"update-grub"}, nil, "apt", Info, "legacy BIOS", "crashkernel=" + kdumpMemory},
		{"bios, dnf", nil, nil, nil, "dnf", Info, "legacy BIOS", "dnf install -y kexec-tools"},
		{"bios, other", nil, nil, nil, "pacman", Info, "legacy BIOS", ""},
		{"kdump, no memory", map[string]string{"kernel/kexec_crash_loaded": "0\n", "kernel/kexec_crash_size": "0\n"}, []string{"kdump-config", "update-grub"}, nil, "apt", Info, "no memory reserved", "systemctl enable kdump-tools && sudo mkdir -p /etc/default/grub.d"},
		{"kdump, not loaded", map[string]string{"kernel/kexec_crash_loaded": "0\n", "kernel/kexec_crash_size": "201326592\n"}, []string{"kdump-config"}, nil, "apt", Info, "not switched on", "systemctl enable kdump-tools && sudo systemctl restart kdump-tools"},
		{"kdumpctl, not loaded", map[string]string{"kernel/kexec_crash_size": "201326592\n"}, []string{"kdumpctl"}, nil, "dnf", Info, "not switched on", "systemctl enable kdump && sudo systemctl restart kdump"},
		{"disabled", map[string]string{"firmware/efi/systab": "x", "module/efi_pstore/parameters/pstore_disable": "Y\n"}, []string{"update-grub"}, nil, "apt", Info, "switched off", "update-grub"},
		{"not loaded", map[string]string{"firmware/efi/systab": "x"}, nil, nil, "apt", Info, "isn't loaded", "modprobe efi_pstore"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			for rel, content := range c.files {
				write(root, rel, content)
			}
			lookPath := func(name string) (string, error) {
				for _, tool := range c.tools {
					if tool == name {
						return "/usr/sbin/" + name, nil
					}
				}
				return "", errors.New("not found")
			}
			enabled := func(unit string) bool {
				for _, u := range c.units {
					if u == unit {
						return true
					}
				}
				return false
			}
			got := pstoreCheck(crashEnv{sys: root, lookPath: lookPath, enabled: enabled, pm: c.pm,
				suggested: func() int { return 0 },
				folders:   func() []pstore.Attempt { return nil },
				handled:   func() time.Time { return time.Time{} },
			})
			if got.Status != c.status || !strings.Contains(got.Result, c.result) || !strings.Contains(got.Fix, c.fix) {
				t.Fatalf("got %+v", got)
			}
			if c.fix == "" && got.Fix != "" {
				t.Fatalf("no command expected: %+v", got)
			}
			if got.Status == Info && !strings.Contains(got.Why, "power cut") {
				t.Fatalf("Why should explain the consequence: %q", got.Why)
			}
			if strings.HasPrefix(c.name, "bios") && !strings.Contains(got.Why, "kdump") {
				t.Fatalf("BIOS advice should explain kdump: %+v", got)
			}
		})
	}
}

func TestSensorsCheck(t *testing.T) {
	if c := checkSensors(Recording{}, true, "apt"); c.Fix != "" || !strings.Contains(c.Result, "virtual machines") {
		t.Fatalf("VM: %+v", c)
	}
	if c := checkSensors(Recording{}, false, "apt"); !strings.Contains(c.Fix, "lm-sensors") || c.Status != Info {
		t.Fatalf("real hardware: %+v", c)
	}
}

func TestVirtualMachine(t *testing.T) {
	dir := t.TempDir()
	cpu := filepath.Join(dir, "cpuinfo")
	dmi := filepath.Join(dir, "dmi")
	if err := os.MkdirAll(dmi, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cpu, []byte("processor\t: 0\nflags\t\t: fpu vme sse2 hypervisor lahf_lm\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !virtualMachine(cpu, dmi) {
		t.Fatal("hypervisor flag means a VM")
	}
	if err := os.WriteFile(cpu, []byte("flags\t\t: fpu vme sse2 lahf_lm\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if virtualMachine(cpu, dmi) {
		t.Fatal("no flag and no DMI vendor means real hardware")
	}
	if err := os.WriteFile(filepath.Join(dmi, "sys_vendor"), []byte("VMware, Inc.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !virtualMachine(cpu, dmi) {
		t.Fatal("VMware DMI vendor means a VM")
	}
}

func TestLookPathSbin(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	if _, err := lookPathSbin("sh"); err == nil {
		t.Fatal("sh is not in an sbin directory, so it should not be found with an empty PATH")
	}
	for _, name := range []string{"ldconfig", "sysctl", "update-grub", "modprobe"} {
		for _, dir := range []string{"/usr/sbin", "/sbin"} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				if _, err := lookPathSbin(name); err != nil {
					t.Fatalf("%s is in %s but was not found", name, dir)
				}
				return
			}
		}
	}
}

func TestKdumpHealth(t *testing.T) {
	root := t.TempDir()
	for rel, v := range map[string]string{"kernel/kexec_crash_loaded": "1\n", "kernel/kexec_crash_size": "201326592\n"} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	env := func(suggested int, folders []pstore.Attempt, handled time.Time) crashEnv {
		return crashEnv{
			sys:       root,
			lookPath:  func(n string) (string, error) { return "/usr/sbin/" + n, nil },
			enabled:   func(string) bool { return true },
			pm:        "apt",
			suggested: func() int { return suggested },
			folders:   func() []pstore.Attempt { return folders },
			handled:   func() time.Time { return handled },
		}
	}
	if c := pstoreCheck(env(180, nil, time.Time{})); c.Status != OK {
		t.Fatalf("enough memory: %+v", c)
	}
	// No estimate yet (kdump-tools makes one at its first start-up): 192 MB
	// is too little for current kernels.
	if c := pstoreCheck(env(0, nil, time.Time{})); c.Status != Warn || !strings.Contains(c.Fix, "crashkernel="+kdumpMemory) {
		t.Fatalf("no estimate, 192 MB: %+v", c)
	}
	// Kali's big kernel: kdump-tools' estimate is above the 192 MB kept.
	c := pstoreCheck(env(260, nil, time.Time{}))
	if c.Status != Warn || !strings.Contains(c.Result, "192 MB kept, about 260 MB needed") || !strings.Contains(c.Fix, "crashkernel=512M-:384M") {
		t.Fatalf("too little memory: %+v", c)
	}
	unsaved := []pstore.Attempt{{Path: "/var/crash/202611081123", Time: now.Add(-2 * time.Hour), HasLog: true}, {Path: "/var/crash/202611081300", Time: now.Add(-time.Hour)}}
	c = pstoreCheck(env(150, unsaved, time.Time{}))
	if c.Status != Warn || !strings.Contains(c.Result, "was not saved") || !strings.Contains(c.Fix, "crashkernel=512M-:320M") {
		t.Fatalf("unsaved crash: %+v", c)
	}
	// Already acted on (BeeBox changed the memory after that crash).
	if c := pstoreCheck(env(150, unsaved, now)); c.Status != OK {
		t.Fatalf("handled crash should not warn again: %+v", c)
	}
	// A later crash was saved: kdump works.
	saved := append(unsaved, pstore.Attempt{Path: "/var/crash/202611081400", Time: now.Add(-time.Minute), HasLog: true})
	if c := pstoreCheck(env(150, saved, time.Time{})); c.Status != OK {
		t.Fatalf("a later saved crash means kdump works: %+v", c)
	}
}

func TestParseSuggestedMB(t *testing.T) {
	out := "current state:    ready to kdump\n\ncrashkernel suggested size: 204M\n\nkexec command:\n"
	if n := parseSuggestedMB(out); n != 204 {
		t.Fatalf("got %d", n)
	}
	if n := parseSuggestedMB("crashkernel suggested size: Unknown (can't find early boot memory info)\n"); n != 0 {
		t.Fatalf("unknown should be 0, got %d", n)
	}
}
