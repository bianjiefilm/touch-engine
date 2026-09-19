package notifytask

import (
	"errors"
	"testing"
)

func TestScaffoldDefaultsFailExplicitly(t *testing.T) {
	// default: everything off -> ErrFeatureDisabled, never a fake success
	n := New(SideNotify, false, "", "", "touch-engine", "X-Notify-App-ID")
	if err := n.Check(); !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("notify off: want ErrFeatureDisabled, got %v", err)
	}
	k := New(SideTask, false, "http://127.0.0.1:18103", "", "touch-engine", "")
	if err := k.Check(); !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("task off: want ErrFeatureDisabled, got %v", err)
	}
}

func TestScaffoldEnabledButUnconfigured(t *testing.T) {
	n := New(SideNotify, true, "", "", "touch-engine", "X-Notify-App-ID")
	if err := n.Check(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
	n2 := New(SideNotify, true, "http://127.0.0.1:18105", "tok", "", "X-Notify-App-ID")
	if err := n2.Check(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("missing app id: want ErrNotConfigured, got %v", err)
	}
}

func TestScaffoldFullyConfiguredPassesCheck(t *testing.T) {
	n := New(SideNotify, true, "http://127.0.0.1:18105", "tok", "touch-engine", "X-Notify-App-ID")
	if err := n.Check(); err != nil {
		t.Fatalf("fully configured notify: %v", err)
	}
}
