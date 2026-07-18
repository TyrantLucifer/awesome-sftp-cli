package redaction

const Placeholder = "<redacted>"

type Sensitivity string

const (
	Public         Sensitivity = "public"
	SystemMetadata Sensitivity = "system_metadata"
	Pseudonymous   Sensitivity = "pseudonymous"
	Secret         Sensitivity = "secret"
	Content        Sensitivity = "content"
)

func ExportString(class Sensitivity, value string) (string, bool) {
	switch class {
	case Public:
		if SafeToken(value) {
			return value, true
		}
		return Placeholder, true
	case SystemMetadata:
		if SafeSystemMetadata(value) {
			return value, true
		}
		return Placeholder, true
	case Pseudonymous:
		return Placeholder, true
	case Secret, Content:
		return "", false
	default:
		return "", false
	}
}

func SafeToken(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func SafeSystemMetadata(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') &&
			(char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') &&
			char != '_' && char != '-' && char != '.' && char != '+' && char != ':' {
			return false
		}
	}
	return true
}
