package externalprocess

import "strings"

// ScrubEnvironment keeps only non-secret process context needed by terminal
// editors and desktop openers. In particular, command configuration, askpass,
// dynamic-loader, language-runtime injection, and application-private values
// are not inherited.
func ScrubEnvironment(base []string) []string {
	result := make([]string, 0, len(base))
	seen := make(map[string]struct{}, len(base))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" || !allowedEnvironmentKey(key) || containsEnvironmentControl(value) {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, entry)
	}
	return result
}

func containsEnvironmentControl(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] == 0 || value[i] == '\r' || value[i] == '\n' {
			return true
		}
	}
	return false
}

func allowedEnvironmentKey(key string) bool {
	if strings.HasPrefix(key, "LC_") && len(key) > len("LC_") {
		return true
	}
	switch key {
	case "HOME", "USER", "LOGNAME", "PATH", "LANG", "TERM", "COLORTERM", "TMPDIR", "SHELL",
		"DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME",
		"DBUS_SESSION_BUS_ADDRESS", "__CF_USER_TEXT_ENCODING":
		return true
	default:
		return false
	}
}
