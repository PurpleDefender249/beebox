package journal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParse(t *testing.T) {
	out := []byte(`{"MESSAGE":"Out of memory: Killed process 1 (x)","PRIORITY":"3","_TRANSPORT":"kernel","__REALTIME_TIMESTAMP":"1730730000123456","_BOOT_ID":"5c0ffee0b0a7d00d8badf00dfeedface"}
{"MESSAGE":[104,105,255],"PRIORITY":"6","SYSLOG_IDENTIFIER":"app","__REALTIME_TIMESTAMP":"1730730001000000"}
not json
{"PRIORITY":"6"}
`)
	es, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 {
		t.Fatalf("got %d entries", len(es))
	}
	if !es[0].Kernel || es[0].Ident != "kernel" || es[0].Prio != 3 || es[0].Time.UnixMicro() != 1730730000123456 || es[0].BootID == "" {
		t.Fatalf("entry 0: %+v", es[0])
	}
	if es[1].Msg != "hi?" || es[1].Ident != "app" {
		t.Fatalf("binary message not decoded: %+v", es[1])
	}
}

func TestValidSelector(t *testing.T) {
	for _, ok := range []string{"-1", "0", "-12", "5c0ffee0b0a7d00d8badf00dfeedface"} {
		if !ValidSelector(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "-1; rm -rf /", "--all", "5C0FFEE0", "-99999"} {
		if ValidSelector(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestPersistent(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "journald.conf")
	if err := os.WriteFile(conf, []byte("[Journal]\n#Storage=auto\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, _ := Persistent([]string{conf}, true); !ok {
		t.Fatal("auto + /var/log/journal should be persistent")
	}
	if ok, _ := Persistent([]string{conf}, false); ok {
		t.Fatal("auto without /var/log/journal should be volatile")
	}
	if err := os.WriteFile(conf, []byte("[Journal]\nStorage=volatile\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, s := Persistent([]string{conf}, true); ok || s != "volatile" {
		t.Fatalf("explicit volatile: %v %s", ok, s)
	}
}
