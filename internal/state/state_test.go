package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJSONRoundTripAndMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "doc.json")
	type doc struct{ A int }

	d := doc{A: 5}
	if err := LoadJSON(p, &d); err != nil || d.A != 5 {
		t.Fatalf("missing file must leave the value untouched: %+v %v", d, err)
	}
	if err := SaveJSON(p, doc{A: 7}); err != nil {
		t.Fatal(err)
	}
	var got doc
	if err := LoadJSON(p, &got); err != nil || got.A != 7 {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("state files must be private, mode = %v", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

func TestCorruptJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(p, []byte("{nope"), 0o600)
	var v map[string]any
	if err := LoadJSON(p, &v); err == nil {
		t.Fatal("corrupt JSON must be an error, not silently ignored")
	}
}

func TestInt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "offset")
	if n, err := LoadInt(p); err != nil || n != 0 {
		t.Fatalf("missing: %d %v", n, err)
	}
	if err := SaveInt(p, 12345); err != nil {
		t.Fatal(err)
	}
	if n, err := LoadInt(p); err != nil || n != 12345 {
		t.Fatalf("got %d %v", n, err)
	}
	_ = os.WriteFile(p, []byte("x"), 0o600)
	if _, err := LoadInt(p); err == nil {
		t.Fatal("garbage must be an error")
	}
}
