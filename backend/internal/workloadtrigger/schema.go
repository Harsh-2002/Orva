package workloadtrigger

func RequestSchema(op string) map[string]any {
	text := func() map[string]any { return map[string]any{"type": "string"} }
	properties := map[string]any{}
	required := []string{}
	if op == "inspect" {
		properties["kind"] = map[string]any{"type": "string", "enum": []string{"route", "inbound"}}
		properties["key"], properties["function_id"] = text(), text()
		required = []string{"kind"}
	} else {
		revision := map[string]any{"type": "string", "pattern": "^[1-9][0-9]*$"}
		properties["version"] = map[string]any{"type": "string", "pattern": "^(0|[1-9][0-9]*)$"}
		properties["expected"] = map[string]any{"type": "object", "minProperties": 1, "maxProperties": 2, "additionalProperties": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"policy_revision", "authority_epoch"},
			"properties": map[string]any{"policy_revision": revision, "authority_epoch": revision},
		}}
		required = []string{"version", "expected"}
		if op == "route_set" || op == "route_delete" {
			properties["path"] = text()
			required = append(required, "path")
		}
		if op == "route_set" || op == "inbound_create" || op == "inbound_update" || op == "inbound_delete" {
			properties["function_id"] = text()
			required = append(required, "function_id")
		}
		if op == "route_set" {
			properties["methods"] = text()
		}
		if op == "inbound_update" || op == "inbound_delete" {
			properties["id"] = text()
			required = append(required, "id")
		}
		if op == "inbound_create" || op == "inbound_update" {
			for _, k := range []string{"name", "signature_header", "signature_format"} {
				properties[k] = text()
			}
			properties["active"] = map[string]any{"type": "boolean"}
			if op == "inbound_create" {
				required = append(required, "name")
			}
		}
		if op == "inbound_delete" || op == "route_delete" {
			properties["confirm"] = map[string]any{"type": "boolean"}
			required = append(required, "confirm")
		}
	}
	return map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": properties}
}
