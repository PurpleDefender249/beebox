package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		pm     string
		status Status
		result string
		fix    string
	}{
		{"working", map[string]string{"module/pstore/parameters/backend": "efi_pstore\n"}, nil, "apt", OK, "backend: efi_pstore", ""},
		{"kdump ready", map[string]string{"kernel/kexec_crash_loaded": "1\n"}, []string{"kdump-config"}, "apt", OK, "kdump", ""},
		{"bios, apt", map[string]string{"module/pstore/parameters/backend": "(null)\n"}, []string{"update-grub"}, "apt", Info, "legacy BIOS", "apt install -y kdump-tools"},
		{"bios, dnf", nil, nil, "dnf", Info, "legacy BIOS", "dnf install -y kexec-tools"},
		{"bios, other", nil, nil, "pacman", Info, "legacy BIOS", ""},
		{"kdump, no memory", map[string]string{"kernel/kexec_crash_loaded": "0\n", "kernel/kexec_crash_size": "0\n"}, []string{"kdump-config"}, "apt", Info, "no memory reserved", "update-grub"},
		{"kdump, not loaded", map[string]string{"kernel/kexec_crash_loaded": "0\n", "kernel/kexec_crash_size": "201326592\n"}, []string{"kdump-config"}, "apt", Info, "not switched on", "kdump-config load"},
		{"kdumpctl, not loaded", map[string]string{"kernel/kexec_crash_size": "201326592\n"}, []string{"kdumpctl"}, "dnf", Info, "not switched on", "systemctl enable --now kdump"},
		{"disabled", map[string]string{"firmware/efi/systab": "x", "module/efi_pstore/parameters/pstore_disable": "Y\n"}, []string{"update-grub"}, "apt", Info, "switched off", "update-grub"},
		{"not loaded", map[string]string{"firmware/efi/systab": "x"}, nil, "apt", Info, "isn't loaded", "modprobe efi_pstore"},
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
			got := pstoreCheck(root, lookPath, c.pm)
			if got.Status != c.status || !strings.Contains(got.Result, c.result) || !strings.Contains(got.Fix, c.fix) {
				t.Fatalf("got %+v", got)
			}
			if c.fix == "" && got.Fix != "" {
				t.Fatalf("no command expected: %+v", got)
			}
			if got.Status != OK && !strings.Contains(got.Why, "power cut") {
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
