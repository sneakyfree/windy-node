package collect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestProcStatUsage(t *testing.T) {
	a, ca, err := ParseProcStat(fixture(t, "stat_a"))
	if err != nil {
		t.Fatal(err)
	}
	b, cb, _ := ParseProcStat(fixture(t, "stat_b"))
	if len(ca) != 2 || len(cb) != 2 {
		t.Fatalf("cores: %d %d", len(ca), len(cb))
	}
	// busy +900 of total +1400 → 64.3 %; core0 all busy (100), core1 all idle (0)
	if got := UsagePct(a, b); got != 64.3 {
		t.Fatalf("usage %v", got)
	}
	if UsagePct(ca[0], cb[0]) != 100 || UsagePct(ca[1], cb[1]) != 0 {
		t.Fatalf("per core %v %v", UsagePct(ca[0], cb[0]), UsagePct(ca[1], cb[1]))
	}
	if UsagePct(b, b) != -1 {
		t.Fatal("no elapsed time must be -1")
	}
	if _, _, err := ParseProcStat([]byte("intr 1\n")); err == nil {
		t.Fatal("missing aggregate line must error")
	}
}

func TestMeminfoNetLoad(t *testing.T) {
	m, err := ParseMeminfo(fixture(t, "meminfo"))
	if err != nil || m.TotalKB != 65536000 || m.AvailKB != 32768000 || m.SwapFreeKB != 4194304 {
		t.Fatalf("%+v %v", m, err)
	}
	rx, tx, err := ParseNetDev(fixture(t, "netdev"))
	if err != nil || rx != 1300 || tx != 2400 { // lo and docker0 skipped
		t.Fatalf("rx %d tx %d %v", rx, tx, err)
	}
	l, err := ParseLoadavg(fixture(t, "loadavg"))
	if err != nil || l != [3]float64{0.52, 0.61, 0.70} {
		t.Fatalf("%v %v", l, err)
	}
}

func TestGPUCSV(t *testing.T) {
	g, err := ParseGPUCSV(string(fixture(t, "gpu.csv")))
	if err != nil || len(g) != 2 {
		t.Fatalf("%v %v", g, err)
	}
	if g[0].Name != "NVIDIA GeForce RTX 5090" || *g[0].VRAMUsedGB != 28.2 || *g[0].VRAMTotalGB != 31.8 || *g[0].PowerW != 112.4 {
		t.Fatalf("%+v", g[0])
	}
	if g[1].UtilPct != nil || g[1].PowerW != nil { // [N/A] / [Not Supported] → null, never 0
		t.Fatalf("unsupported fields must be null: %+v", g[1])
	}
}

func TestEngineClassesNeverLeakNames(t *testing.T) {
	e := ParseAppsCSV(string(fixture(t, "apps.csv")))
	want := map[string][2]float64{"ollama": {2, 15.6}, "llama.cpp": {1, 4}, "other": {1, 1.2}, "vllm": {1, 2}}
	if len(e) != len(want) {
		t.Fatalf("%+v", e)
	}
	for _, x := range e {
		w := want[x.Engine]
		if float64(x.Processes) != w[0] || x.VRAMGB != w[1] {
			t.Fatalf("%s: %+v want %v", x.Engine, x, w)
		}
	}
	b, _ := json.Marshal(e)
	for _, leak := range []string{"/usr", "python", "llama-server"} {
		if strings.Contains(string(b), leak) {
			t.Fatalf("raw process name leaked: %s", b)
		}
	}
}

func TestOllamaPSShowsCPUSpill(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(fixture(t, "ps.json"))
	}))
	defer srv.Close()
	o := ReadOllama(context.Background(), srv.URL)
	if !o.Reachable || len(o.Models) != 2 {
		t.Fatalf("%+v", o)
	}
	if o.Models[0].CPUGB != 0 || o.Models[1].SizeGB != 20 || o.Models[1].VRAMGB != 5 || o.Models[1].CPUGB != 15 {
		t.Fatalf("%+v", o.Models)
	}
	if got := ReadOllama(context.Background(), "http://127.0.0.1:1"); got.Reachable {
		t.Fatal("no server must read as unreachable")
	}
}

func TestSamplerReadsAFakeRoot(t *testing.T) {
	root := t.TempDir()
	write := func(p string, b []byte) {
		full := filepath.Join(root, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("proc/stat", fixture(t, "stat_a"))
	write("proc/meminfo", fixture(t, "meminfo"))
	write("proc/net/dev", fixture(t, "netdev"))
	write("proc/loadavg", fixture(t, "loadavg"))
	write("sys/class/hwmon/hwmon3/name", []byte("coretemp\n"))
	write("sys/class/hwmon/hwmon3/temp1_label", []byte("Package id 0\n"))
	write("sys/class/hwmon/hwmon3/temp1_input", []byte("61000\n"))
	s := &Sampler{Root: root, Version: "test"}
	s.Tick()
	write("proc/stat", fixture(t, "stat_b"))
	s.Tick()
	r := s.Reading(context.Background(), 15*time.Second)
	if r.CPU.UsagePct == nil || *r.CPU.UsagePct != 64.3 || len(r.CPU.PerCorePct) != 2 {
		t.Fatalf("cpu %+v", r.CPU)
	}
	if r.Mem.TotalGB != 62.5 || r.Mem.UsedGB != 31.3 || r.Mem.SwapUsedGB != 4 {
		t.Fatalf("mem %+v", r.Mem)
	}
	if r.CPU.PackageTempC == nil || *r.CPU.PackageTempC != 61 {
		t.Fatalf("temp %v", r.CPU.PackageTempC)
	}
	if r.Ollama.Models == nil || r.GPUs == nil || r.Engines == nil {
		t.Fatal("lists must be [] not null")
	}
}

func TestFactsNames(t *testing.T) {
	if ArchName("amd64") != "x86_64" || ArchName("arm64") != "arm64" || ArchName("386") != "" {
		t.Fatal("arch")
	}
	if OSName("darwin") != "macos" || OSName("linux") != "linux" {
		t.Fatal("os")
	}
}
