package store

import (
	"errors"
	"os"
	"testing"
)

func TestAIAccessValidation(t *testing.T) {
	c := Connection{Name: "x", Kind: "postgres", Host: "h"}
	c.Normalize()
	if c.AIAccess != AIOff {
		t.Fatalf("default = %q", c.AIAccess)
	}
	for _, a := range AIAccessLevels {
		c.AIAccess = a
		if err := c.Validate(); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
	}
	c.AIAccess = "admin"
	var ve *ValidationError
	if err := c.Validate(); !errors.As(err, &ve) || ve.Field != "aiAccess" {
		t.Fatalf("err = %v", err)
	}
}

func TestEffectiveAIAccess(t *testing.T) {
	cases := []struct {
		name string
		c    Connection
		want AIAccess
	}{
		{"missing means off", Connection{Kind: "postgres"}, AIOff},
		{"unknown means off", Connection{Kind: "postgres", AIAccess: "admin"}, AIOff},
		{"off", Connection{Kind: "postgres", AIAccess: AIOff}, AIOff},
		{"read", Connection{Kind: "postgres", AIAccess: AIRead}, AIRead},
		{"write", Connection{Kind: "postgres", AIAccess: AIWrite, SafeMode: SafeConfirm}, AIWrite},
		{"write capped by readonly safe mode", Connection{Kind: "mysql", AIAccess: AIWrite, SafeMode: SafeReadOnly}, AIRead},
		{"write capped by read-only sqlite file", Connection{Kind: "sqlite", AIAccess: AIWrite, ReadOnly: true}, AIRead},
		{"read stays read in readonly mode", Connection{Kind: "sqlite", AIAccess: AIRead, SafeMode: SafeReadOnly}, AIRead},
	}
	for _, tc := range cases {
		if got := tc.c.EffectiveAIAccess(); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestAIAccessMissingInFileIsOff(t *testing.T) {
	s, _ := newTestStore(t)
	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	v3 := `{"version":3,"connections":[{"id":"a","name":"A","kind":"postgres","host":"h","tag":"none","safeMode":"off"}]}`
	if err := os.WriteFile(s.Path(), []byte(v3), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := s.Get("a")
	if err != nil {
		t.Fatal(err)
	}
	if a.EffectiveAIAccess() != AIOff {
		t.Fatalf("access = %q", a.EffectiveAIAccess())
	}
	a.AIAccess = AIRead
	saved, err := s.Save(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get("a"); got.AIAccess != AIRead || saved.AIAccess != AIRead {
		t.Fatalf("saved access = %q / %q", got.AIAccess, saved.AIAccess)
	}
}
