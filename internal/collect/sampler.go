package collect

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Reading is one send interval's summary (node.v1 Reading, proposed 1.1.0). Hardware and AI workloads only.
type Reading struct {
	At           time.Time `json:"at"`
	IntervalS    int       `json:"interval_s"`
	CPU          CPU       `json:"cpu"`
	Mem          MemGB     `json:"mem"`
	Net          Net       `json:"net"`
	GPUs         []GPU     `json:"gpus"`
	Engines      []Engine  `json:"engines"`
	Ollama       Ollama    `json:"ollama"`
	AgentVersion string    `json:"agent_version"`
}

type CPU struct {
	UsagePct     *float64   `json:"usage_pct"`      // average over the interval
	PeakPct      *float64   `json:"peak_pct"`       // highest 2 s sample
	PerCorePct   []float64  `json:"per_core_pct"`   // last sample
	Load         [3]float64 `json:"load"`           // 1, 5, 15 min
	PackageTempC *float64   `json:"package_temp_c"` // coretemp, when the kernel exposes it
}

type MemGB struct {
	TotalGB    float64 `json:"total_gb"`
	UsedGB     float64 `json:"used_gb"`
	SwapUsedGB float64 `json:"swap_used_gb"`
}

type Net struct {
	RxBps *float64 `json:"rx_bps"` // bytes per second over the interval
	TxBps *float64 `json:"tx_bps"`
}

// Sampler keeps the previous /proc counters between ticks. Root is "/" in production, a fixture dir in tests.
type Sampler struct {
	Root      string
	OllamaURL string
	Version   string

	mu        sync.Mutex
	prevAgg   *CPUTimes
	prevCores []CPUTimes
	sum, peak float64
	n         int
	perCore   []float64
	netStart  *[2]uint64
	netAt     time.Time
}

func (s *Sampler) read(p string) ([]byte, error) { return os.ReadFile(filepath.Join(s.Root, p)) }

// Tick takes one local sample (call every 2 s).
func (s *Sampler) Tick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, err := s.read("proc/stat"); err == nil {
		if agg, cores, err := ParseProcStat(b); err == nil {
			if s.prevAgg != nil {
				if u := UsagePct(*s.prevAgg, agg); u >= 0 {
					s.sum += u
					s.n++
					if u > s.peak {
						s.peak = u
					}
				}
				if len(s.prevCores) == len(cores) {
					pc := make([]float64, len(cores))
					for i := range cores {
						pc[i] = max(0, UsagePct(s.prevCores[i], cores[i]))
					}
					s.perCore = pc
				}
			}
			s.prevAgg, s.prevCores = &agg, cores
		}
	}
	if s.netStart == nil {
		if b, err := s.read("proc/net/dev"); err == nil {
			if rx, tx, err := ParseNetDev(b); err == nil {
				s.netStart, s.netAt = &[2]uint64{rx, tx}, time.Now()
			}
		}
	}
}

// Reading closes the interval: averages since the last Reading, plus a fresh GPU / Ollama look.
func (s *Sampler) Reading(ctx context.Context, interval time.Duration) Reading {
	r := Reading{At: time.Now().UTC().Truncate(time.Second), IntervalS: int(interval.Seconds()), AgentVersion: s.Version,
		GPUs: []GPU{}, Engines: []Engine{}}
	s.mu.Lock()
	if s.n > 0 {
		avg, pk := round1(s.sum/float64(s.n)), round1(s.peak)
		r.CPU.UsagePct, r.CPU.PeakPct = &avg, &pk
	}
	r.CPU.PerCorePct = s.perCore
	s.sum, s.peak, s.n = 0, 0, 0
	if b, err := s.read("proc/net/dev"); err == nil && s.netStart != nil {
		if rx, tx, err := ParseNetDev(b); err == nil {
			if dt := time.Since(s.netAt).Seconds(); dt > 0 && rx >= s.netStart[0] && tx >= s.netStart[1] {
				rb, tb := round1(float64(rx-s.netStart[0])/dt), round1(float64(tx-s.netStart[1])/dt)
				r.Net = Net{RxBps: &rb, TxBps: &tb}
			}
			s.netStart, s.netAt = &[2]uint64{rx, tx}, time.Now()
		}
	}
	s.mu.Unlock()
	if b, err := s.read("proc/loadavg"); err == nil {
		r.CPU.Load, _ = ParseLoadavg(b)
	}
	if b, err := s.read("proc/meminfo"); err == nil {
		if m, err := ParseMeminfo(b); err == nil {
			r.Mem = MemGB{TotalGB: kbToGB(m.TotalKB), UsedGB: kbToGB(m.TotalKB - m.AvailKB), SwapUsedGB: kbToGB(m.SwapTotalKB - m.SwapFreeKB)}
		}
	}
	r.CPU.PackageTempC = s.packageTemp()
	if gpus, engines := ReadGPUs(ctx); gpus != nil {
		r.GPUs = gpus
		if engines != nil {
			r.Engines = engines
		}
	}
	if s.OllamaURL != "" {
		r.Ollama = ReadOllama(ctx, s.OllamaURL)
	}
	if r.Ollama.Models == nil {
		r.Ollama.Models = []OllamaModel{}
	}
	return r
}

func kbToGB(kb uint64) float64 { return round1(float64(kb) / (1 << 20)) }

// packageTemp reads the coretemp "Package id 0" sensor, if present.
func (s *Sampler) packageTemp() *float64 {
	dirs, _ := filepath.Glob(filepath.Join(s.Root, "sys/class/hwmon/hwmon*"))
	for _, d := range dirs {
		name, _ := os.ReadFile(filepath.Join(d, "name"))
		if strings.TrimSpace(string(name)) != "coretemp" {
			continue
		}
		labels, _ := filepath.Glob(filepath.Join(d, "temp*_label"))
		for _, l := range labels {
			lb, _ := os.ReadFile(l)
			if !strings.HasPrefix(strings.TrimSpace(string(lb)), "Package") {
				continue
			}
			if v := num(readTrim(strings.TrimSuffix(l, "_label") + "_input")); v != nil {
				t := round1(*v / 1000)
				return &t
			}
		}
	}
	return nil
}

func readTrim(p string) string {
	b, _ := os.ReadFile(p)
	return strings.TrimSpace(string(b))
}

// Facts are the hardware facts sent at enrolment (node.v1 NodeFacts).
type Facts struct {
	Label        string    `json:"label,omitempty"`
	OS           string    `json:"os"`
	Arch         string    `json:"arch"`
	CPUCores     int       `json:"cpu_cores"`
	MemGB        float64   `json:"mem_gb,omitempty"`
	GPUs         []GPUFact `json:"gpus,omitempty"`
	AgentVersion string    `json:"agent_version"`
}

type GPUFact struct {
	Vendor string   `json:"vendor"`
	Model  string   `json:"model,omitempty"`
	VRAMGB *float64 `json:"vram_gb,omitempty"`
}

// ArchName maps Go's GOARCH to node.v1's arch enum ("" when unsupported).
func ArchName(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "arm64"
	}
	return ""
}

// OSName maps Go's GOOS to node.v1's os enum.
func OSName(goos string) string {
	if goos == "darwin" {
		return "macos"
	}
	return goos
}

// ReadFacts gathers the enrolment facts. The label defaults to the hostname (cut to 64).
func (s *Sampler) ReadFacts(ctx context.Context, label string) Facts {
	if label == "" {
		label, _ = os.Hostname()
	}
	if len(label) > 64 {
		label = label[:64]
	}
	f := Facts{Label: label, OS: OSName(runtime.GOOS), Arch: ArchName(runtime.GOARCH), CPUCores: runtime.NumCPU(), AgentVersion: s.Version}
	if b, err := s.read("proc/meminfo"); err == nil {
		if m, err := ParseMeminfo(b); err == nil {
			f.MemGB = kbToGB(m.TotalKB)
		}
	}
	gpus, _ := ReadGPUs(ctx)
	for _, g := range gpus {
		model := g.Name
		if len(model) > 64 {
			model = model[:64]
		}
		f.GPUs = append(f.GPUs, GPUFact{Vendor: "nvidia", Model: model, VRAMGB: g.VRAMTotalGB})
	}
	return f
}
