package adapter

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveSource selects the record location for product.
// Precedence is the explicit path, then the product's configuration, then the
// default location. An invalid or empty higher-priority choice is returned as
// an error and is never replaced by a lower-priority location.
func ResolveSource(product, explicit string) (string, error) {
	name, err := canonicalProduct(product)
	if err != nil {
		return "", err
	}
	home, homeErr := os.UserHomeDir()
	if explicit != "" {
		return validateSelected(name, explicit, "explicit")
	}
	if path, ok, err := configuredPath(name, home); err != nil || ok {
		if err != nil {
			return "", err
		}
		return validateSelected(name, path, "configured")
	}
	if homeErr != nil {
		return "", &SourceError{Code: "invalid_source", Message: "home directory is unavailable"}
	}
	path := defaultPath(name, home)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &SourceError{Code: "source_not_found", Message: "default record location does not exist"}
		}
		return "", &SourceError{Code: "invalid_source", Message: "default record location is not readable"}
	}
	if info.IsDir() {
		empty, err := directoryEmpty(path)
		if err != nil {
			return "", &SourceError{Code: "invalid_source", Message: "default record location is not readable"}
		}
		if empty {
			return "", &SourceError{Code: "empty_source", Message: "default record location contains no records"}
		}
	}
	return path, nil
}

func canonicalProduct(product string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(product)) {
	case "claude", "claude-code":
		return "claude-code", nil
	case "codex":
		return "codex", nil
	case "opencode", "opencode-v2":
		return "opencode-v2", nil
	default:
		return "", &SourceError{Code: "unsupported_product", Message: "unsupported product"}
	}
}

func configuredPath(product, home string) (string, bool, error) {
	switch product {
	case "claude-code":
		value, ok := os.LookupEnv("CLAUDE_CONFIG_DIR")
		if !ok {
			return "", false, nil
		}
		if value == "" {
			return "", true, &SourceError{Code: "invalid_source", Message: "CLAUDE_CONFIG_DIR is empty"}
		}
		return filepath.Join(value, "projects"), true, nil
	case "codex":
		value, ok := os.LookupEnv("CODEX_HOME")
		if !ok {
			return "", false, nil
		}
		if value == "" {
			return "", true, &SourceError{Code: "invalid_source", Message: "CODEX_HOME is empty"}
		}
		return filepath.Join(value, "sessions"), true, nil
	case "opencode-v2":
		value, ok := os.LookupEnv("OPENCODE_DB")
		if !ok {
			return "", false, nil
		}
		if value == "" {
			return "", true, &SourceError{Code: "invalid_source", Message: "OPENCODE_DB is empty"}
		}
		// OpenCode resolves a relative database path from its data directory.
		// It is not a working-directory path.
		if !filepath.IsAbs(value) {
			if home == "" {
				return "", true, &SourceError{Code: "invalid_source", Message: "home directory is unavailable"}
			}
			value = filepath.Join(home, ".local", "share", "opencode", value)
		}
		return value, true, nil
	default:
		return "", false, &SourceError{Code: "unsupported_product", Message: "unsupported product"}
	}
}

func defaultPath(product, home string) string {
	switch product {
	case "claude-code":
		return filepath.Join(home, ".claude", "projects")
	case "codex":
		return filepath.Join(home, ".codex", "sessions")
	case "opencode-v2":
		return filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	default:
		return ""
	}
}

func validateSelected(product, path, origin string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &SourceError{Code: "invalid_source", Message: origin + " record location does not exist"}
		}
		return "", &SourceError{Code: "invalid_source", Message: origin + " record location is not readable"}
	}
	if product == "opencode-v2" && info.IsDir() {
		return "", &SourceError{Code: "invalid_source", Message: "OpenCode records must be a database file"}
	}
	if info.IsDir() {
		empty, err := directoryEmpty(path)
		if err != nil {
			return "", &SourceError{Code: "invalid_source", Message: origin + " record location is not readable"}
		}
		if empty {
			return "", &SourceError{Code: "empty_source", Message: origin + " record location contains no records"}
		}
	}
	return path, nil
}

func directoryEmpty(root string) (bool, error) {
	empty := true
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".json", ".jsonl", ".db":
			empty = false
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return empty, nil
}
