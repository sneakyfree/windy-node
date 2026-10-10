package collect

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// OllamaModel is one loaded model. CPUGB > 0 means part of it spilled out of the GPU onto the CPU
// (Veron 10-05: a 30B model loaded on the CPU ran every core at 100% for hours).
type OllamaModel struct {
	Name      string  `json:"name"`
	SizeGB    float64 `json:"size_gb"`
	VRAMGB    float64 `json:"vram_gb"`
	CPUGB     float64 `json:"cpu_gb"`
	ExpiresAt string  `json:"expires_at,omitempty"`
}

// Ollama is what the local Ollama server says is loaded. Reachable=false means no Ollama answered.
type Ollama struct {
	Reachable bool          `json:"reachable"`
	Models    []OllamaModel `json:"models"`
}

type psBody struct {
	Models []struct {
		Name      string `json:"name"`
		Size      int64  `json:"size"`
		SizeVRAM  int64  `json:"size_vram"`
		ExpiresAt string `json:"expires_at"`
	} `json:"models"`
}

// OllamaURL is the local Ollama base URL: OLLAMA_HOST if set, else 127.0.0.1:11434.
func OllamaURL() string {
	h := strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
	if h == "" || strings.HasPrefix(h, "0.0.0.0") {
		h = "127.0.0.1:11434"
	}
	if !strings.HasPrefix(h, "http://") && !strings.HasPrefix(h, "https://") {
		h = "http://" + h
	}
	return strings.TrimRight(h, "/")
}

func gb(b int64) float64 { return round1(float64(b) / (1 << 30)) }

// ParseOllamaPS reads GET /api/ps.
func ParseOllamaPS(b []byte) ([]OllamaModel, error) {
	var ps psBody
	if err := json.Unmarshal(b, &ps); err != nil {
		return nil, err
	}
	out := make([]OllamaModel, 0, len(ps.Models))
	for _, m := range ps.Models {
		cpu := m.Size - m.SizeVRAM
		if cpu < 0 {
			cpu = 0
		}
		out = append(out, OllamaModel{Name: m.Name, SizeGB: gb(m.Size), VRAMGB: gb(m.SizeVRAM), CPUGB: gb(cpu), ExpiresAt: m.ExpiresAt})
	}
	return out, nil
}

// ReadOllama asks the local Ollama what it has loaded (2 s timeout; never anything but /api/ps).
func ReadOllama(ctx context.Context, base string) Ollama {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/ps", nil)
	if err != nil {
		return Ollama{}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Ollama{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Ollama{}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Ollama{Reachable: true}
	}
	models, err := ParseOllamaPS(b)
	if err != nil {
		return Ollama{Reachable: true}
	}
	return Ollama{Reachable: true, Models: models}
}
