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
		status Status
		result string
		fix    string
	}{
		{"working", map[string]string{"module/pstore/parameters/backend": "efi_pstore\n"}, OK, "backend: efi_pstore", ""},
		{"bios", map[string]string{"module/pstore/parameters/backend": "(null)\n"}, Info, "legacy BIOS", ""},
		{"disabled", map[string]string{"firmware/efi/systab": "x", "module/efi_pstore/parameters/pstore_disable": "Y\n"}, Info, "switched off", "update-grub"},
		{"not loaded", map[string]string{"firmware/efi/systab": "x"}, Info, "isn't loaded", "modprobe efi_pstore"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			for rel, content := range c.files {
				write(root, rel, content)
			}
			lookPath := func(name string) (string, error) {
				if name == "update-grub" {
					return "/usr/sbin/update-grub", nil
				}
				return "", errors.New("not found")
			}
			got := pstoreCheck(root, lookPath)
			if got.Status != c.status || !strings.Contains(got.Result, c.result) || !strings.Contains(got.Fix, c.fix) {
				t.Fatalf("got %+v", got)
			}
			if got.Status != OK && !strings.Contains(got.Why, "power cut") {
				t.Fatalf("Why should explain the consequence: %q", got.Why)
			}
			if c.name == "bios" && (got.Fix != "" || !strings.Contains(got.Why, "new install")) {
				t.Fatalf("BIOS advice belongs in Why, not as a command: %+v", got)
			}
		})
	}
}
