package journal

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ConfFiles lists journald configuration files in the order systemd applies
// them (main file first, then drop-ins sorted by name).
func ConfFiles() []string {
	files := []string{"/etc/systemd/journald.conf"}
	var dropins []string
	for _, dir := range []string{"/usr/lib/systemd/journald.conf.d", "/etc/systemd/journald.conf.d", "/run/systemd/journald.conf.d"} {
		m, _ := filepath.Glob(filepath.Join(dir, "*.conf"))
		dropins = append(dropins, m...)
	}
	sort.Slice(dropins, func(i, j int) bool { return filepath.Base(dropins[i]) < filepath.Base(dropins[j]) })
	return append(files, dropins...)
}

func readStorage(path string) string {
	f, err := os.Open(path) // #nosec G304 -- fixed system config paths
	if err != nil {
		return ""
	}
	defer f.Close()
	val := ""
	inJournal := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inJournal = strings.EqualFold(line, "[Journal]")
			continue
		}
		if !inJournal {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == "Storage" {
			val = strings.ToLower(strings.TrimSpace(v))
		}
	}
	return val
}
