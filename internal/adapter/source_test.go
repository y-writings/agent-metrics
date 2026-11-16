package adapter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSourcePrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "OPENCODE_DB"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}

	explicit := filepath.Join(t.TempDir(), "explicit")
	configured := filepath.Join(t.TempDir(), "configured")
	defaultSessions := filepath.Join(home, ".codex", "sessions")
	for _, dir := range []string{explicit, filepath.Join(configured, "sessions"), defaultSessions} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "marker.jsonl"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Distinctive file names prove which root was chosen.
	if err := os.WriteFile(filepath.Join(explicit, "from-explicit.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configured, "sessions", "from-configured.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaultSessions, "from-default.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("explicit beats configuration and default", func(t *testing.T) {
		t.Setenv("CODEX_HOME", configured)
		got, err := ResolveSource("codex", explicit)
		if err != nil || got != explicit {
			t.Fatalf("explicit root = %q, %v", got, err)
		}
	})

	t.Run("configuration beats default", func(t *testing.T) {
		t.Setenv("CODEX_HOME", configured)
		got, err := ResolveSource("codex", "")
		if err != nil || got != filepath.Join(configured, "sessions") {
			t.Fatalf("configured root = %q, %v", got, err)
		}
	})

	t.Run("default when unset", func(t *testing.T) {
		os.Unsetenv("CODEX_HOME")
		got, err := ResolveSource("codex", "")
		if err != nil || got != defaultSessions {
			t.Fatalf("default root = %q, %v", got, err)
		}
	})

	t.Run("invalid explicit does not fall through", func(t *testing.T) {
		t.Setenv("CODEX_HOME", configured)
		got, err := ResolveSource("codex", filepath.Join(t.TempDir(), "missing"))
		se, ok := err.(*SourceError)
		if got != "" || !ok || se.Code != "invalid_source" || se.Message == "" {
			t.Fatalf("invalid explicit = %q, %#v", got, err)
		}
		if se.Message == "CODEX_HOME is empty" {
			t.Fatal("invalid explicit was classified as empty configuration")
		}
	})

	t.Run("empty configuration does not fall through", func(t *testing.T) {
		t.Setenv("CODEX_HOME", "")
		got, err := ResolveSource("codex", "")
		se, ok := err.(*SourceError)
		if got != "" || !ok || se.Code != "invalid_source" || se.Message != "CODEX_HOME is empty" {
			t.Fatalf("empty CODEX_HOME = %q, %#v", got, err)
		}
	})

	t.Run("empty explicit directory is not another source", func(t *testing.T) {
		t.Setenv("CODEX_HOME", configured)
		empty := filepath.Join(t.TempDir(), "empty")
		if err := os.MkdirAll(empty, 0o700); err != nil {
			t.Fatal(err)
		}
		got, err := ResolveSource("codex", empty)
		se, ok := err.(*SourceError)
		if got != "" || !ok || se.Code != "empty_source" {
			t.Fatalf("empty explicit = %q, %#v", got, err)
		}
	})

	t.Run("missing default is source absence", func(t *testing.T) {
		os.Unsetenv("CODEX_HOME")
		t.Setenv("HOME", t.TempDir())
		got, err := ResolveSource("codex", "")
		se, ok := err.(*SourceError)
		if got != "" || !ok || se.Code != "source_not_found" {
			t.Fatalf("missing default = %q, %#v", got, err)
		}
	})

	t.Run("claude configuration and default stay distinct", func(t *testing.T) {
		os.Unsetenv("CODEX_HOME")
		t.Setenv("HOME", home)
		claudeDefault := filepath.Join(home, ".claude", "projects")
		if err := os.MkdirAll(claudeDefault, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(claudeDefault, "default.jsonl"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		claudeConfig := filepath.Join(t.TempDir(), "claude-config")
		projects := filepath.Join(claudeConfig, "projects")
		if err := os.MkdirAll(projects, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projects, "configured.jsonl"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CLAUDE_CONFIG_DIR", claudeConfig)
		got, err := ResolveSource("claude-code", "")
		if err != nil || got != projects {
			t.Fatalf("claude configured = %q, %v", got, err)
		}
		os.Unsetenv("CLAUDE_CONFIG_DIR")
		got, err = ResolveSource("claude", "")
		if err != nil || got != claudeDefault {
			t.Fatalf("claude default = %q, %v", got, err)
		}
	})

	t.Run("relative opencode database uses the data directory", func(t *testing.T) {
		data := filepath.Join(home, ".local", "share", "opencode")
		if err := os.MkdirAll(data, 0o700); err != nil {
			t.Fatal(err)
		}
		configuredDB := filepath.Join(data, "isolated.db")
		if err := os.WriteFile(configuredDB, []byte("db"), 0o600); err != nil {
			t.Fatal(err)
		}
		cwdDB := filepath.Join(t.TempDir(), "isolated.db")
		if err := os.WriteFile(cwdDB, []byte("cwd"), 0o600); err != nil {
			t.Fatal(err)
		}
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(filepath.Dir(cwdDB)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chdir(wd) })
		t.Setenv("OPENCODE_DB", "isolated.db")
		got, err := ResolveSource("opencode-v2", "")
		if err != nil || got != configuredDB {
			t.Fatalf("relative OPENCODE_DB = %q, %v", got, err)
		}
		if err := os.Chdir(wd); err != nil {
			t.Fatal(err)
		}
		os.Unsetenv("OPENCODE_DB")
		defaultDB := filepath.Join(data, "opencode.db")
		if err := os.WriteFile(defaultDB, []byte("db"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err = ResolveSource("opencode", "")
		if err != nil || got != defaultDB {
			t.Fatalf("opencode default = %q, %v", got, err)
		}
		dir := t.TempDir()
		if _, err := ResolveSource("opencode-v2", dir); err == nil || err.(*SourceError).Code != "invalid_source" {
			t.Fatalf("opencode directory must be invalid, got %v", err)
		}
	})
}
