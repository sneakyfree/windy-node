package collect

import (
	"context"
	"encoding/csv"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// GPU is one card as nvidia-smi reports it. Nullable numbers are pointers (a field the card does not expose).
type GPU struct {
	Index       int      `json:"index"`
	Name        string   `json:"name"`
	UtilPct     *float64 `json:"util_pct"`
	VRAMUsedGB  *float64 `json:"vram_used_gb"`
	VRAMTotalGB *float64 `json:"vram_total_gb"`
	TempC       *float64 `json:"temp_c"`
	PowerW      *float64 `json:"power_w"`
	PowerLimitW *float64 `json:"power_limit_w"`
}

// GPUQuery is the nvidia-smi field list ParseGPUCSV expects, in order.
const GPUQuery = "index,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw,power.limit"

// AppsQuery lists GPU compute processes; only the binary's base name and its memory are read.
const AppsQuery = "process_name,used_memory"

func num(s string) *float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return nil // "[N/A]", "[Not Supported]", empty
	}
	return &v
}

func mibToGB(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := round1(*p / 1024)
	return &v
}

// ParseGPUCSV reads `nvidia-smi --query-gpu=GPUQuery --format=csv,noheader,nounits`.
func ParseGPUCSV(out string) ([]GPU, error) {
	r := csv.NewReader(strings.NewReader(out))
	r.TrimLeadingSpace = true
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	var gpus []GPU
	for _, p := range rows {
		if len(p) < 8 {
			continue
		}
		idx, _ := strconv.Atoi(strings.TrimSpace(p[0]))
		gpus = append(gpus, GPU{
			Index: idx, Name: strings.TrimSpace(p[1]), UtilPct: num(p[2]),
			VRAMUsedGB: mibToGB(num(p[3])), VRAMTotalGB: mibToGB(num(p[4])),
			TempC: num(p[5]), PowerW: num(p[6]), PowerLimitW: num(p[7]),
		})
	}
	return gpus, nil
}

// Engine is one class of AI engine with the GPU memory its processes hold. Never a pid or a raw process name.
type Engine struct {
	Engine    string  `json:"engine"` // ollama | llama.cpp | vllm | other
	Processes int     `json:"processes"`
	VRAMGB    float64 `json:"vram_gb"`
}

// EngineClass maps a process's binary to an engine class. Unknown GPU processes are counted as "other".
func EngineClass(processName string) string {
	b := strings.ToLower(filepath.Base(strings.TrimSpace(processName)))
	switch {
	case strings.HasPrefix(b, "ollama"):
		return "ollama"
	case strings.HasPrefix(b, "llama-") || b == "llama.cpp" || strings.HasPrefix(b, "llamafile"):
		return "llama.cpp"
	case strings.Contains(b, "vllm"):
		return "vllm"
	default:
		return "other"
	}
}

// ParseAppsCSV reads `nvidia-smi --query-compute-apps=AppsQuery --format=csv,noheader,nounits` into engine classes.
func ParseAppsCSV(out string) []Engine {
	r := csv.NewReader(strings.NewReader(out))
	r.TrimLeadingSpace = true
	rows, err := r.ReadAll()
	if err != nil {
		return nil
	}
	byClass := map[string]*Engine{}
	var order []string
	for _, p := range rows {
		if len(p) < 2 {
			continue
		}
		c := EngineClass(p[0])
		e, ok := byClass[c]
		if !ok {
			e = &Engine{Engine: c}
			byClass[c] = e
			order = append(order, c)
		}
		e.Processes++
		if m := num(p[1]); m != nil {
			e.VRAMGB = round1(e.VRAMGB + *m/1024)
		}
	}
	engines := make([]Engine, 0, len(order))
	for _, c := range order {
		engines = append(engines, *byClass[c])
	}
	return engines
}

// nvidiaSMI runs nvidia-smi with a hard timeout. A machine without it simply has no NVIDIA readings.
func nvidiaSMI(ctx context.Context, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nvidia-smi", args...).Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// ReadGPUs returns the cards and the engine classes using them (both empty without nvidia-smi).
func ReadGPUs(ctx context.Context) ([]GPU, []Engine) {
	var gpus []GPU
	if out, ok := nvidiaSMI(ctx, "--query-gpu="+GPUQuery, "--format=csv,noheader,nounits"); ok {
		gpus, _ = ParseGPUCSV(out)
	}
	var engines []Engine
	if out, ok := nvidiaSMI(ctx, "--query-compute-apps="+AppsQuery, "--format=csv,noheader,nounits"); ok {
		engines = ParseAppsCSV(out)
	}
	return gpus, engines
}
