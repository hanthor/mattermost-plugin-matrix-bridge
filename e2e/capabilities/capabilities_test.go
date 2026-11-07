package capabilities

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite docs/CAPABILITIES.md from the capability list")

const docPath = "../../docs/CAPABILITIES.md"

// TestCapabilitiesDoc keeps docs/CAPABILITIES.md in sync with the Go list.
func TestCapabilitiesDoc(t *testing.T) {
	want := Markdown()
	path := filepath.Clean(docPath)

	if *update {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run `make capabilities`)", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s is out of date; run `make capabilities`", path)
	}
}

func TestCapabilityIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range All {
		if c.ID == "" || c.Name == "" || c.Area == "" {
			t.Errorf("capability %+v is missing a field", c)
		}
		if seen[c.ID] {
			t.Errorf("duplicate capability ID %q", c.ID)
		}
		seen[c.ID] = true
	}
}

func TestVerifyOutcomes(t *testing.T) {
	cases := []struct {
		status   Status
		works    bool
		wantFail bool
	}{
		{Supported, true, false},
		{Supported, false, true},
		{Unsupported, false, false},
		{Unsupported, true, true},
		{Partial, true, false},
		{Partial, false, false},
	}
	for _, tc := range cases {
		All = append(All, Capability{ID: "test.probe", Area: "Test", Name: "probe", Direction: MattermostToMatrix, Status: tc.status})
		rec := &recorder{}
		Verify(rec, "test.probe", tc.works)
		All = All[:len(All)-1]
		if rec.failed != tc.wantFail {
			t.Errorf("status=%s works=%v: failed=%v, want %v", tc.status, tc.works, rec.failed, tc.wantFail)
		}
	}
}

// recorder is a minimal testing.TB that records failures.
type recorder struct {
	testing.TB
	failed bool
}

func (r *recorder) Helper()               {}
func (r *recorder) Logf(string, ...any)   {}
func (r *recorder) Errorf(string, ...any) { r.failed = true }
