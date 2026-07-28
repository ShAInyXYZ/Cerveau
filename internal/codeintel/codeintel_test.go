package codeintel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExtractGo(t *testing.T) {
	src := `package sample

type Config struct{ Addr string }

type Storer interface { Store(v string) error }

func NewConfig(addr string) *Config { return &Config{Addr: addr} }

func (c *Config) Start() error {
	helper()
	return nil
}

func helper() {}
`
	symbols, calls, err := extractGo("sample.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, s := range symbols {
		names[s.Name] = s.Kind
	}
	if names["Config"] != "struct" || names["Storer"] != "interface" ||
		names["NewConfig"] != "func" || names["Start"] != "method" || names["helper"] != "func" {
		t.Fatalf("symbols = %v", names)
	}
	foundHelper := false
	for _, c := range calls {
		if c.CallerSymbol == "Start" && c.CalleeName == "helper" {
			foundHelper = true
		}
	}
	if !foundHelper {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestExtractPython(t *testing.T) {
	src := "class App:\n    def run(self):\n        helper()\n\ndef helper():\n    pass\n"
	symbols, calls, err := extractRegex("app.py", "python", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) != 3 {
		t.Fatalf("symbols = %+v", symbols)
	}
	found := false
	for _, c := range calls {
		if c.CalleeName == "helper" {
			found = true
		}
	}
	if !found {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	err = store.ReplaceFile(ctx, "a.go", "go", 100, 50, []Symbol{
		{File: "a.go", Name: "Foo", Kind: "func", Signature: "func Foo()", Line: 3},
	}, []Call{{CallerFile: "a.go", CallerSymbol: "Foo", CalleeName: "Bar", Line: 5}})
	if err != nil {
		t.Fatal(err)
	}
	syms, err := store.FindSymbols(ctx, "Foo", 10)
	if err != nil || len(syms) != 1 || syms[0].Line != 3 {
		t.Fatalf("syms = %+v, %v", syms, err)
	}
	refs, err := store.References(ctx, "Bar", 10)
	if err != nil || len(refs) != 1 || refs[0].CallerSymbol != "Foo" {
		t.Fatalf("refs = %+v, %v", refs, err)
	}
	fm, err := store.FileMap(ctx)
	if err != nil || len(fm) != 1 || fm[0].Count != 1 {
		t.Fatalf("filemap = %+v, %v", fm, err)
	}
}

func TestIncrementalIndex(t *testing.T) {
	tmp := t.TempDir()
	goFile := filepath.Join(tmp, "a.go")
	os.WriteFile(goFile, []byte("package a\n\nfunc A() {}\n"), 0o644)
	os.WriteFile(filepath.Join(tmp, "skip.txt"), []byte("func Nope() {}"), 0o644)

	store, _ := OpenStore(filepath.Join(t.TempDir(), "g.db"))
	defer store.Close()
	ix := NewIndexer(store, tmp)
	ctx := context.Background()

	rep, err := ix.Index(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Parsed != 1 {
		t.Fatalf("rep = %+v", rep)
	}
	rep2, _ := ix.Index(ctx)
	if rep2.Parsed != 0 || rep2.Skipped != 1 {
		t.Fatalf("second index = %+v", rep2)
	}
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(goFile, []byte("package a\n\nfunc A() {}\n\nfunc B() { A() }\n"), 0o644)
	os.Chtimes(goFile, time.Now(), time.Now())
	rep3, _ := ix.Index(ctx)
	if rep3.Parsed != 1 {
		t.Fatalf("third index = %+v", rep3)
	}
	syms, _ := store.FindSymbols(ctx, "B", 10)
	if len(syms) != 1 {
		t.Fatalf("B not indexed: %+v", syms)
	}
	os.Remove(goFile)
	rep4, _ := ix.Index(ctx)
	if rep4.Removed != 1 {
		t.Fatalf("fourth index = %+v", rep4)
	}
}
