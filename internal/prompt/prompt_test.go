package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got := ExpandPath("~/foo/bar"); got != filepath.Join(home, "foo", "bar") {
		t.Fatalf("got %q", got)
	}
	if got := ExpandPath("  /abs/path  "); got != "/abs/path" {
		t.Fatalf("got %q", got)
	}
	if got := ExpandPath(""); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestStringDefault(t *testing.T) {
	in := strings.NewReader("\n")
	var out strings.Builder
	got, err := String(in, &out, "Project id", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if got != "demo" {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(out.String(), "[demo]") {
		t.Fatalf("prompt %q", out.String())
	}
}

func TestStringValue(t *testing.T) {
	in := strings.NewReader("other\n")
	var out strings.Builder
	got, err := String(in, &out, "Project id", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if got != "other" {
		t.Fatalf("got %q", got)
	}
}

func TestYesNo(t *testing.T) {
	cases := []struct {
		in   string
		def  bool
		want bool
	}{
		{"\n", false, false},
		{"\n", true, true},
		{"y\n", false, true},
		{"yes\n", false, true},
		{"n\n", true, false},
	}
	for _, tc := range cases {
		got, err := YesNo(strings.NewReader(tc.in), &strings.Builder{}, "Enable?", tc.def)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q def=%v got %v want %v", tc.in, tc.def, got, tc.want)
		}
	}
}

func TestSessionPathValidateRetry(t *testing.T) {
	dir := t.TempDir()
	// need compose file for a realistic validator; here just IsDir
	input := "nope\n" + dir + "\n"
	var out strings.Builder
	s := NewSession(strings.NewReader(input), &out)
	got, err := s.Path("Plat5 compose", "", false, func(p string) error {
		st, err := os.Stat(p)
		if err != nil {
			return err
		}
		if !st.IsDir() {
			return os.ErrNotExist
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(dir)
	gotEval, _ := filepath.EvalSymlinks(got)
	wantEval, _ := filepath.EvalSymlinks(want)
	if gotEval != wantEval {
		t.Fatalf("got %q want %q\nout=%s", got, want, out.String())
	}
	if strings.Count(out.String(), "Plat5 compose") < 2 {
		t.Fatalf("expected re-prompt, out=%q", out.String())
	}
}

func TestSessionPathEOF(t *testing.T) {
	var out strings.Builder
	s := NewSession(strings.NewReader(""), &out)
	_, err := s.Path("Plat5 compose", "", false, nil)
	if err == nil {
		t.Fatal("expected error on EOF")
	}
}

func TestSessionChoice(t *testing.T) {
	var out strings.Builder
	s := NewSession(strings.NewReader("\n"), &out)
	idx, err := s.Choice("Template", []string{"none", "bun-effect-api"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if idx != 1 {
		t.Fatalf("default idx %d", idx)
	}
	if !strings.Contains(out.String(), "1) none") || !strings.Contains(out.String(), "2) bun-effect-api") {
		t.Fatalf("out=%q", out.String())
	}

	out.Reset()
	s = NewSession(strings.NewReader("1\n"), &out)
	idx, err = s.Choice("Template", []string{"none", "bun-effect-api"}, 1)
	if err != nil || idx != 0 {
		t.Fatalf("got %d %v", idx, err)
	}

	out.Reset()
	s = NewSession(strings.NewReader("bun-effect-api\n"), &out)
	idx, err = s.Choice("Template", []string{"none", "bun-effect-api"}, 0)
	if err != nil || idx != 1 {
		t.Fatalf("got %d %v", idx, err)
	}
}

func TestSessionFlow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("name: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	input := "myapp\n" + dir + "\ny\n" + dir + "\n"
	var out strings.Builder
	s := NewSession(strings.NewReader(input), &out)

	id, err := s.String("Project id", "def")
	if err != nil || id != "myapp" {
		t.Fatalf("id %q %v", id, err)
	}
	edge, err := s.Path("Plat5", "", false, func(p string) error {
		_, err := os.Stat(filepath.Join(p, "docker-compose.yml"))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := s.YesNo("Auth?", false)
	if err != nil || !auth {
		t.Fatalf("auth %v %v", auth, err)
	}
	authPath, err := s.Path("Auth path", "", false, func(p string) error {
		_, err := os.Stat(filepath.Join(p, "docker-compose.yml"))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if edge == "" || authPath == "" {
		t.Fatal("empty paths")
	}
}
