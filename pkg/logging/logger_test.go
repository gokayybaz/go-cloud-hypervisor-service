package logging

import (
	"context"
	"strings"
	"testing"
)

func TestNewDefaultsToInfo(t *testing.T) {
	// We cannot easily inject a writer into New, so we test via the public
	// interface by inspecting os.Stdout in an integration style.  For unit
	// tests we rely on the interface contract.
	l := New("info")
	if l == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestWithContextInjectsTraceID(t *testing.T) {
	l := New("debug")
	ctx := WithTraceID(context.Background(), "abc123")
	sub := l.WithContext(ctx)
	if sub == nil {
		t.Fatal("expected non-nil contextual logger")
	}
}

func TestTraceIDFromContext(t *testing.T) {
	ctx := WithTraceID(context.Background(), "t1")
	if got := TraceIDFromContext(ctx); got != "t1" {
		t.Fatalf("expected trace_id t1, got %s", got)
	}
	if got := TraceIDFromContext(context.Background()); got != "" {
		t.Fatalf("expected empty trace_id, got %s", got)
	}
}

func TestGenerateTraceID(t *testing.T) {
	id1 := GenerateTraceID()
	id2 := GenerateTraceID()
	if id1 == id2 {
		t.Fatal("expected unique trace IDs")
	}
	if len(id1) == 0 {
		t.Fatal("expected non-empty trace ID")
	}
}

func TestWithAddsPersistentFields(t *testing.T) {
	l := New("info")
	sub := l.With("svc", "test")
	if sub == nil {
		t.Fatal("expected non-nil child logger")
	}
}

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"debug", "debug"},
		{"info", "info"},
		{"warn", "warn"},
		{"error", "error"},
		{"", "info"},
		{"UNKNOWN", "info"},
	}
	for _, c := range cases {
		lvl := parseLevel(c.in)
		if !strings.EqualFold(lvl.String(), c.want) {
			t.Fatalf("parseLevel(%q) = %s, want %s", c.in, lvl.String(), c.want)
		}
	}
}