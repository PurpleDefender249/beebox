package sample

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// sensorGroup is one hwmon chip; its reading is the hottest input.
type sensorGroup struct {
	name   string
	inputs []string
	crit   float64
}

// classifyHwmon maps hwmon driver names to stable, readable group names.
func classifyHwmon(driver string) string {
	switch driver {
	case "k10temp", "coretemp", "zenpower", "cpu_thermal", "cpu", "soc_thermal", "x86_pkg_temp":
		return "cpu"
	case "amdgpu", "radeon", "nouveau", "gpu_thermal":
		return "gpu"
	case "nvme":
		return "nvme"
	case "drivetemp":
		return "disk"
	case "acpitz", "pch_cannonlake", "pch_skylake", "pch_cometlake":
		return "board"
	default:
		return ""
	}
}

// discoverSensors finds temperature sensors under sysDir.
func discoverSensors(sysDir string) []sensorGroup {
	var groups []sensorGroup
	hw, _ := filepath.Glob(filepath.Join(sysDir, "class/hwmon/hwmon*"))
	sort.Strings(hw)
	seen := map[string]int{}
	for _, dir := range hw {
		driver, err := readTrim(filepath.Join(dir, "name"))
		if err != nil {
			continue
		}
		group := classifyHwmon(driver)
		if group == "" {
			continue
		}
		inputs, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		if len(inputs) == 0 {
			continue
		}
		sort.Strings(inputs)
		g := sensorGroup{inputs: inputs}
		for _, in := range inputs {
			base := strings.TrimSuffix(in, "_input")
			for _, suffix := range []string{"_crit", "_emergency"} {
				if v, err := readInt(base + suffix); err == nil && v > 0 {
					c := float64(v) / 1000
					if g.crit == 0 || c < g.crit {
						g.crit = c
					}
					break
				}
			}
		}
		seen[group]++
		g.name = group
		if seen[group] > 1 {
			g.name = group + strconv.Itoa(seen[group])
		}
		groups = append(groups, g)
	}
	if !hasGroup(groups, "cpu") {
		// Fallback: thermal zones (common on ARM boards and some laptops).
		zones, _ := filepath.Glob(filepath.Join(sysDir, "class/thermal/thermal_zone*"))
		for _, z := range zones {
			t, err := readTrim(filepath.Join(z, "type"))
			if err != nil || classifyHwmon(t) != "cpu" {
				continue
			}
			g := sensorGroup{name: "cpu", inputs: []string{filepath.Join(z, "temp")}}
			trips, _ := filepath.Glob(filepath.Join(z, "trip_point_*_type"))
			for _, tp := range trips {
				if kind, _ := readTrim(tp); kind == "critical" {
					if v, err := readInt(strings.TrimSuffix(tp, "_type") + "_temp"); err == nil && v > 0 {
						g.crit = float64(v) / 1000
					}
				}
			}
			groups = append(groups, g)
			break
		}
	}
	return groups
}

func hasGroup(gs []sensorGroup, name string) bool {
	for _, g := range gs {
		if g.name == name {
			return true
		}
	}
	return false
}

func readSensors(groups []sensorGroup) []Temp {
	out := make([]Temp, 0, len(groups))
	for _, g := range groups {
		best := -1000.0
		for _, in := range g.inputs {
			v, err := readInt(in)
			if err != nil {
				continue
			}
			c := float64(v) / 1000
			if c > best && c < 200 {
				best = c
			}
		}
		if best > -1000 {
			out = append(out, Temp{Name: g.name, C: round1(best), Crit: g.crit})
		}
		if len(out) == 6 {
			break
		}
	}
	return out
}

// powerSupplies locates the system battery and mains adapter.
type powerSupplies struct {
	battery string
	mains   string
}

func discoverPower(sysDir string) powerSupplies {
	var ps powerSupplies
	dirs, _ := filepath.Glob(filepath.Join(sysDir, "class/power_supply/*"))
	sort.Strings(dirs)
	for _, d := range dirs {
		typ, err := readTrim(filepath.Join(d, "type"))
		if err != nil {
			continue
		}
		// Peripheral batteries (mice, headsets) report scope=Device.
		if scope, err := readTrim(filepath.Join(d, "scope")); err == nil && scope == "Device" {
			continue
		}
		switch typ {
		case "Battery":
			if ps.battery == "" {
				ps.battery = d
			}
		case "Mains", "USB":
			if ps.mains == "" || typ == "Mains" {
				ps.mains = d
			}
		}
	}
	return ps
}

func readPower(ps powerSupplies) (*Battery, *bool) {
	var bat *Battery
	var ac *bool
	if ps.battery != "" {
		if pct, err := readInt(filepath.Join(ps.battery, "capacity")); err == nil {
			b := &Battery{Pct: int(pct), Status: "Unknown"}
			if s, err := readTrim(filepath.Join(ps.battery, "status")); err == nil {
				b.Status = s
			}
			if uw, err := readInt(filepath.Join(ps.battery, "power_now")); err == nil && uw > 0 {
				b.PowerW = round1(float64(uw) / 1e6)
			} else if ua, err := readInt(filepath.Join(ps.battery, "current_now")); err == nil && ua > 0 {
				if uv, err := readInt(filepath.Join(ps.battery, "voltage_now")); err == nil && uv > 0 {
					b.PowerW = round1(float64(ua) * float64(uv) / 1e12)
				}
			}
			bat = b
		}
	}
	if ps.mains != "" {
		if v, err := readInt(filepath.Join(ps.mains, "online")); err == nil {
			on := v == 1
			ac = &on
		}
	}
	return bat, ac
}

func discoverGPUBusy(sysDir string) string {
	m, _ := filepath.Glob(filepath.Join(sysDir, "class/drm/card*/device/gpu_busy_percent"))
	sort.Strings(m)
	if len(m) > 0 {
		return m[0]
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
