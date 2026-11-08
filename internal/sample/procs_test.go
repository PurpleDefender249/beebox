package sample

import "testing"

func TestMergeLauncher(t *testing.T) {
	apps := map[string]*appAcc{
		"x-www-browser": {App: App{Name: "x-www-browser", Count: 1, MemKB: 400 << 10, CPU: 3}, shmemMax: 50 << 10},
		"firefox-esr":   {App: App{Name: "firefox-esr", Count: 10, MemKB: 420 << 10, CPU: 5}, shmemMax: 80 << 10},
		"Xorg":          {App: App{Name: "Xorg", Count: 1, MemKB: 150 << 10}},
	}
	mergeLauncher(apps)
	ff := apps["firefox-esr"]
	if _, still := apps["x-www-browser"]; still || ff.Count != 11 || ff.MemKB != 820<<10 || ff.CPU != 8 || ff.shmemMax != 80<<10 {
		t.Fatalf("launcher not merged into the browser: %+v", apps)
	}

	// Two browsers: the launcher could be either, so leave it alone.
	apps = map[string]*appAcc{
		"x-www-browser": {App: App{Name: "x-www-browser", Count: 1}},
		"firefox":       {App: App{Name: "firefox", Count: 3}},
		"chromium":      {App: App{Name: "chromium", Count: 5}},
	}
	mergeLauncher(apps)
	if _, ok := apps["x-www-browser"]; !ok {
		t.Fatal("ambiguous launcher should be kept")
	}
}
