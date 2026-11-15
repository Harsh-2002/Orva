package pool

import "strings"

func sanitizeSDKEnv(env map[string]string) {
	for key := range env {
		// nsjail interprets '=' in an env argument as a literal assignment.
		name, _, _ := strings.Cut(key, "=")
		switch strings.ToUpper(name) {
		case "ORVA_INTERNAL_TOKEN", "ORVA_API_BASE", "ORVA_SDK_FD", "ORVA_SDK_PROTO":
			delete(env, key)
		}
	}
}
