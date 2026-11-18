//go:build linux

package workloadconfig

// RequestSchema describes only authorable mutations/CAS, never host identity.
func RequestSchema(operation string) map[string]any {
	if operation == "inspect" {
		return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}}
	}
	expectedFields := map[string]any{}
	requiredExpected := []string{"function_version", "artifact_revision", "artifact_epoch", "generation", "policy_revision", "authority_epoch", "approved_artifact"}
	for _, name := range []string{"function_version", "artifact_revision", "artifact_epoch", "policy_revision", "authority_epoch"} {
		expectedFields[name] = map[string]any{"type": "string", "pattern": "^[1-9][0-9]*$", "description": "Exact inspected revision, never a JSON number or automatic retry."}
	}
	expectedFields["generation"] = map[string]any{"type": "string", "description": "Exact durable generation UUID; not proof of current execution."}
	expectedFields["approved_artifact"] = map[string]any{"type": "string", "description": "Exact stored approval CAS token (digest or iam:unbound); never authorizes the new configuration."}
	properties := map[string]any{"expected": map[string]any{"type": "object", "additionalProperties": false, "properties": expectedFields, "required": requiredExpected}}
	required := []string{"expected"}
	if operation != "install" {
		properties["current_artifact"] = map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$", "description": "Exact actual installed Factory digest observed before this mutation."}
		required = append(required, "current_artifact")
	}
	switch operation {
	case "environment", "settings":
		properties["environment"] = map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "maxProperties": 1024, "description": "Complete environment replacement, not a patch. Never include secret values here."}
		required = append(required, "environment")
		if operation == "settings" {
			fields := map[string]any{
				"timeout_ms":         map[string]any{"type": "integer", "minimum": 1, "maximum": 86400000},
				"memory_mb":          map[string]any{"type": "integer", "minimum": 16, "maximum": 1048576},
				"cpus":               map[string]any{"type": "number", "minimum": 0, "maximum": 1024},
				"network_mode":       map[string]any{"type": "string", "enum": []string{"none", "egress"}},
				"sdk":                map[string]any{"type": "boolean"},
				"max_concurrency":    map[string]any{"type": "integer", "minimum": 0},
				"concurrency_policy": map[string]any{"type": "string", "enum": []string{"queue", "reject"}},
				"auth_mode":          map[string]any{"type": "string", "enum": []string{"none", "platform_key", "signed"}},
				"rate_limit_per_min": map[string]any{"type": "integer", "minimum": 0},
			}
			properties["settings"] = map[string]any{"type": "object", "additionalProperties": false, "properties": fields,
				"required": []string{"timeout_ms", "memory_mb", "cpus", "network_mode", "sdk", "max_concurrency", "concurrency_policy", "auth_mode", "rate_limit_per_min"}}
			required = append(required, "settings")
		}
	case "secret_upsert", "secret_delete":
		fields := map[string]any{"key": map[string]any{"type": "string", "minLength": 1, "maxLength": 4096}}
		secretRequired := []string{"key"}
		if operation == "secret_upsert" {
			fields["value"] = map[string]any{"type": "string", "maxLength": MaxRequestBytes}
			secretRequired = append(secretRequired, "value")
		}
		properties["secret"] = map[string]any{"type": "object", "additionalProperties": false, "properties": fields, "required": secretRequired}
		required = append(required, "secret")
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
}
