package config

import "testing"

func TestDefaults(t *testing.T) {
	cfg := Load(func(string) string { return "" })
	if cfg.HTTPAddr != "127.0.0.1:18240" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.DBPath != "data/touch.db" {
		t.Fatalf("DBPath = %q", cfg.DBPath)
	}
	if cfg.AppID != "touch-engine" {
		t.Fatalf("AppID = %q", cfg.AppID)
	}
	if cfg.SessionCookie != "touch_session" {
		t.Fatalf("SessionCookie = %q", cfg.SessionCookie)
	}
	if cfg.FeatureUpload || cfg.FeatureNotify || cfg.FeatureTask {
		t.Fatal("feature flags must default to off")
	}
}

func TestGate(t *testing.T) {
	// empty config: three required keys missing
	cfg := Load(func(string) string { return "" })
	problems := cfg.Gate()
	if len(problems) != 3 {
		t.Fatalf("gate problems = %v, want 3 entries", problems)
	}

	// fully configured base: no problems
	full := map[string]string{
		"TOUCH_INTERNAL_TOKEN":       "s",
		"PLATFORM_IDENTITY_BASE_URL": "http://127.0.0.1:18101",
		"PLATFORM_IDENTITY_TOKEN":    "t",
	}
	cfg = Load(func(k string) string { return full[k] })
	if problems := cfg.Gate(); len(problems) != 0 {
		t.Fatalf("gate problems = %v, want none", problems)
	}

	// feature upload on without upload config: explicit gate entries
	full["FEATURE_UPLOAD"] = "on"
	cfg = Load(func(k string) string { return full[k] })
	problems = cfg.Gate()
	if len(problems) != 2 {
		t.Fatalf("gate problems = %v, want 2 upload entries", problems)
	}
	full["PLATFORM_UPLOAD_BASE_URL"] = "http://127.0.0.1:18104"
	full["PLATFORM_UPLOAD_TOKEN"] = "ut"
	cfg = Load(func(k string) string { return full[k] })
	if problems := cfg.Gate(); len(problems) != 0 {
		t.Fatalf("gate problems = %v, want none", problems)
	}
}

func TestGateNotifyTaskScaffolding(t *testing.T) {
	env := map[string]string{
		"TOUCH_INTERNAL_TOKEN":       "s",
		"PLATFORM_IDENTITY_BASE_URL": "http://127.0.0.1:18101",
		"PLATFORM_IDENTITY_TOKEN":    "t",
		"FEATURE_NOTIFY":             "1",
		"FEATURE_TASK":               "true",
	}
	cfg := Load(func(k string) string { return env[k] })
	if len(cfg.Gate()) != 4 {
		t.Fatalf("gate problems = %v, want 4 (2 notify + 2 task)", cfg.Gate())
	}
	env["PLATFORM_NOTIFY_BASE_URL"] = "http://127.0.0.1:18105"
	env["PLATFORM_NOTIFY_TOKEN"] = "n"
	env["PLATFORM_TASK_BASE_URL"] = "http://127.0.0.1:18103"
	env["PLATFORM_TASK_TOKEN"] = "k"
	cfg = Load(func(k string) string { return env[k] })
	if len(cfg.Gate()) != 0 {
		t.Fatalf("gate problems = %v, want none", cfg.Gate())
	}
}

func TestProductionFlag(t *testing.T) {
	cfg := Load(func(k string) string {
		if k == "TOUCH_ENV" {
			return "production"
		}
		return ""
	})
	if !cfg.Production() {
		t.Fatal("Production() = false for TOUCH_ENV=production")
	}
}
