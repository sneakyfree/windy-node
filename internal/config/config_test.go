package config

import (
	"os"
	"testing"
)

func TestSaveLoadAndOptOut(t *testing.T) {
	d := t.TempDir()
	t.Setenv("WINDY_NODE_DIR", d)
	p := Default()
	if _, _, err := p.Load(); err != ErrNotEnrolled {
		t.Fatalf("want not enrolled, got %v", err)
	}
	if err := p.Save(Node{NodeID: "nd_0123456789abcdef", MindURL: "https://api.windymind.ai"}, "wnt_secret"); err != nil {
		t.Fatal(err)
	}
	n, tok, err := p.Load()
	if err != nil || n.NodeID != "nd_0123456789abcdef" || tok != "wnt_secret" {
		t.Fatalf("%+v %q %v", n, tok, err)
	}
	st, _ := os.Stat(p.TokenFile())
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("token mode %v", st.Mode())
	}
	if !p.CommandsAllowed() {
		t.Fatal("commands allowed by default")
	}
	_ = os.WriteFile(p.OptOutFile(), nil, 0o600)
	if p.CommandsAllowed() {
		t.Fatal("no-commands file must refuse commands")
	}
}
