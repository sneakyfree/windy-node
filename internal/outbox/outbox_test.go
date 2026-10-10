package outbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCapPeekAckAndTornLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "outbox.jsonl")
	o := &Outbox{Path: p, Cap: 3}
	for i := 0; i < 5; i++ {
		if err := o.Add(map[string]int{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	if o.Len() != 3 {
		t.Fatalf("len %d", o.Len())
	}
	got, _ := o.Peek(2)
	if string(got[0]) != `{"i":2}` || string(got[1]) != `{"i":3}` {
		t.Fatalf("oldest first after cap: %s %s", got[0], got[1])
	}
	if err := o.Ack(2); err != nil || o.Len() != 1 {
		t.Fatalf("ack: %v %d", err, o.Len())
	}
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(`{"i":9`) // a crash mid-write
	_ = f.Close()
	if o.Len() != 1 {
		t.Fatalf("torn line must be skipped, len %d", o.Len())
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
}
