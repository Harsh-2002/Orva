package workloadpolicy

// ManagedRequestSchema describes authorable operation DTOs. Raw decoding and
// service validation remain authoritative, including aggregate and byte limits.
func ManagedRequestSchema(op string) map[string]any {
	obj := func(properties map[string]any, required ...string) map[string]any {
		if required == nil {
			required = []string{}
		}
		return map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
	}
	text := map[string]any{"type": "string"}
	counter := map[string]any{"type": "string", "pattern": "^[1-9][0-9]{0,18}$"}
	uuid := map[string]any{"type": "string", "pattern": "^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"}
	artifact := map[string]any{"anyOf": []any{map[string]any{"type": "null"}, map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}}}
	expected := obj(map[string]any{"revision": counter, "current_artifact": artifact, "approved_artifact": artifact}, "revision", "current_artifact", "approved_artifact")
	owners := map[string]any{"type": "array", "maxItems": 25, "items": obj(map[string]any{"function_id": uuid, "expected": expected}, "function_id", "expected")}
	policy := func(inline bool) map[string]any {
		literal := map[string]any{"type": "string", "maxLength": 256}
		ownKV := map[string]any{"oneOf": []any{obj(map[string]any{"key": map[string]any{"type": "string", "minLength": 1, "maxLength": 256}}, "key"), obj(map[string]any{"prefix": literal}, "prefix")}}
		resources := []any{obj(map[string]any{"own_kv": ownKV}, "own_kv"), obj(map[string]any{"function": obj(map[string]any{"id": uuid}, "id")}, "function"), obj(map[string]any{"own_execution": obj(map[string]any{})}, "own_execution")}
		triggers := []string{"f2f", "job"}
		if inline {
			resources = append(resources, obj(map[string]any{"own_cron": map[string]any{"oneOf": []any{
				obj(map[string]any{"name": literal, "target_id": uuid}, "name", "target_id"), obj(map[string]any{"prefix": literal, "target_id": uuid}, "prefix", "target_id"),
			}}}, "own_cron"))
			triggers = append(triggers, "cron")
		}
		statement := obj(map[string]any{
			"actions":  map[string]any{"type": "array", "minItems": 1, "maxItems": 10, "uniqueItems": true, "items": map[string]any{"type": "string", "enum": []string{"kv.get", "kv.list", "kv.put", "kv.delete", "kv.incr", "kv.cas", "functions.invoke", "jobs.enqueue", "crons.upsert", "spans.emit"}}},
			"resource": map[string]any{"oneOf": resources}, "context": map[string]any{"type": "string", "enum": []string{"execution", "bootstrap"}},
		}, "actions", "resource", "context")
		return obj(map[string]any{"schema_version": map[string]any{"type": "integer", "const": 1}, "allow": map[string]any{"type": "array", "maxItems": 64, "items": statement},
			"inbound": map[string]any{"type": "array", "maxItems": 256, "items": obj(map[string]any{"caller_id": uuid, "triggers": map[string]any{"type": "array", "minItems": 1, "maxItems": 3, "uniqueItems": true, "items": map[string]any{"type": "string", "enum": triggers}}}, "caller_id", "triggers")},
		}, "schema_version", "allow", "inbound")
	}
	switch op {
	case "list":
		return obj(map[string]any{"offset": map[string]any{"type": "string", "pattern": "^(0|[1-9][0-9]{0,6})$"}})
	case "get":
		return obj(map[string]any{"version": counter, "history": map[string]any{"type": "boolean"}})
	case "get_function":
		return obj(map[string]any{})
	case "create":
		return obj(map[string]any{"name": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "policy": policy(false)}, "name", "policy")
	case "update":
		return obj(map[string]any{"definition_revision": counter, "policy": policy(false)}, "definition_revision", "policy")
	case "inline":
		return obj(map[string]any{"expected": expected, "policy": policy(true)}, "expected", "policy")
	case "review":
		return obj(map[string]any{"expected": expected}, "expected")
	case "attach":
		return obj(map[string]any{"membership_revision": counter, "version": counter, "owners": owners}, "membership_revision", "version", "owners")
	case "detach":
		return obj(map[string]any{"membership_revision": counter, "owners": owners, "confirm": map[string]any{"type": "boolean", "const": true}}, "membership_revision", "owners", "confirm")
	case "revoke":
		return obj(map[string]any{"definition_revision": counter, "membership_revision": counter, "owners": owners, "confirm": map[string]any{"type": "boolean", "const": true}}, "definition_revision", "membership_revision", "owners", "confirm")
	case "delete":
		return obj(map[string]any{"definition_revision": counter, "membership_revision": counter, "confirm": map[string]any{"type": "boolean", "const": true}}, "definition_revision", "membership_revision", "confirm")
	case "impact":
		var choices []any
		for _, action := range []string{"attach", "detach", "revoke"} {
			schema := ManagedRequestSchema(action)
			schema["properties"].(map[string]any)["operation"] = map[string]any{"type": "string", "const": action}
			schema["required"] = append(schema["required"].([]string), "operation")
			choices = append(choices, schema)
		}
		return map[string]any{"oneOf": choices}
	default:
		return obj(map[string]any{"unsupported": text}, "unsupported")
	}
}
