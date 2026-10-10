// Package collect reads a machine's vitals and AI workloads.
//
// Ported from Veron's alpha control-panel collector (telemetry.js): /proc/stat deltas for CPU, /proc/meminfo,
// /proc/net/dev and nvidia-smi. Rule (node.v1): hardware facts and AI workloads only. Never a process list,
// never file contents, never addresses.
package collect

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// CPUTimes is one /proc/stat line reduced to what a usage percentage needs.
type CPUTimes struct {
	Idle  uint64 // idle + iowait
	Total uint64 // user..steal (guest is already counted in user)
}

// ParseProcStat returns the aggregate "cpu" line and one entry per "cpuN" line.
func ParseProcStat(b []byte) (agg CPUTimes, cores []CPUTimes, err error) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	seen := false
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || !strings.HasPrefix(f[0], "cpu") {
			continue
		}
		var v [8]uint64
		for i := 0; i < 8 && i+1 < len(f); i++ {
			n, perr := strconv.ParseUint(f[i+1], 10, 64)
			if perr != nil {
				return agg, nil, fmt.Errorf("proc/stat: %w", perr)
			}
			v[i] = n
		}
		t := CPUTimes{Idle: v[3] + v[4]}
		for _, x := range v {
			t.Total += x
		}
		if f[0] == "cpu" {
			agg, seen = t, true
		} else {
			cores = append(cores, t)
		}
	}
	if !seen {
		return agg, nil, fmt.Errorf("proc/stat: no aggregate cpu line")
	}
	return agg, cores, sc.Err()
}

// UsagePct is the busy share between two samples (0..100), or -1 when there is no elapsed time.
func UsagePct(prev, cur CPUTimes) float64 {
	dt := float64(cur.Total) - float64(prev.Total)
	if dt <= 0 {
		return -1
	}
	return round1(100 * (1 - (float64(cur.Idle)-float64(prev.Idle))/dt))
}

// Mem is /proc/meminfo in kB.
type Mem struct{ TotalKB, AvailKB, SwapTotalKB, SwapFreeKB uint64 }

func ParseMeminfo(b []byte) (Mem, error) {
	var m Mem
	want := map[string]*uint64{"MemTotal": &m.TotalKB, "MemAvailable": &m.AvailKB, "SwapTotal": &m.SwapTotalKB, "SwapFree": &m.SwapFreeKB}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, rest, ok := strings.Cut(sc.Text(), ":")
		p, found := want[k]
		if !ok || !found {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			return m, fmt.Errorf("meminfo %s: %w", k, err)
		}
		*p = n
	}
	if m.TotalKB == 0 {
		return m, fmt.Errorf("meminfo: no MemTotal")
	}
	return m, sc.Err()
}

// skipIface is traffic that would be counted twice or never leaves the machine.
func skipIface(name string) bool {
	return name == "lo" || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "docker") ||
		strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "virbr")
}

// ParseNetDev sums received and sent bytes over the machine's real interfaces.
func ParseNetDev(b []byte) (rx, tx uint64, err error) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		f := strings.Fields(rest)
		if len(f) < 9 || skipIface(name) {
			continue
		}
		r, e1 := strconv.ParseUint(f[0], 10, 64)
		t, e2 := strconv.ParseUint(f[8], 10, 64)
		if e1 != nil || e2 != nil {
			return 0, 0, fmt.Errorf("net/dev %s: bad counters", name)
		}
		rx += r
		tx += t
	}
	return rx, tx, sc.Err()
}

// ParseLoadavg returns the 1, 5 and 15 minute load averages.
func ParseLoadavg(b []byte) ([3]float64, error) {
	var out [3]float64
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return out, fmt.Errorf("loadavg: short")
	}
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseFloat(f[i], 64)
		if err != nil {
			return out, fmt.Errorf("loadavg: %w", err)
		}
		out[i] = v
	}
	return out, nil
}

func round1(v float64) float64 {
	return float64(int64(v*10+sign(v)*0.5)) / 10
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}
