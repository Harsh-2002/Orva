package workloadiam

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

func Validate(p Policy) error {
	if p.SchemaVersion != SchemaVersion {
		return errors.New("unsupported policy schema version")
	}
	if p.Allow == nil || p.Inbound == nil {
		return errors.New("allow and inbound arrays are required")
	}
	if len(p.Allow) > MaxStatements || len(p.Inbound) > MaxTargets {
		return errors.New("policy count limit exceeded")
	}
	targets := len(p.Inbound)
	for i, s := range p.Allow {
		if err := validateStatement(s); err != nil {
			return fmt.Errorf("allow[%d]: %w", i, err)
		}
		if s.Resource.Function != nil {
			targets++
		}
	}
	if targets > MaxTargets {
		return errors.New("total target/caller limit exceeded")
	}
	for _, a := range p.Inbound {
		if !validUUID(a.CallerID) || len(a.Triggers) == 0 || len(a.Triggers) > 3 {
			return errors.New("invalid inbound acceptance")
		}
		seen := map[Trigger]bool{}
		for _, t := range a.Triggers {
			if !validTrigger(t) || seen[t] {
				return errors.New("invalid or duplicate trigger")
			}
			seen[t] = true
		}
	}
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("policy encode: %w", err)
	}
	if len(data) > MaxPolicyBytes {
		return errors.New("policy exceeds byte limit")
	}
	return nil
}

func validateStatement(s Statement) error {
	if s.Context != Execution && s.Context != Bootstrap {
		return errors.New("invalid context")
	}
	if len(s.Actions) == 0 || len(s.Actions) > 10 || !validResource(s.Resource) {
		return errors.New("invalid actions or resource")
	}
	seen := map[Action]bool{}
	for _, a := range s.Actions {
		if seen[a] || !actionResource(a, s.Resource) {
			return errors.New("invalid action/resource combination")
		}
		seen[a] = true
		if s.Context == Bootstrap && a != KVGet && a != KVList {
			return errors.New("bootstrap permits only KV reads")
		}
	}
	return nil
}

func validResource(r Resource) bool {
	n := 0
	if r.OwnKV != nil {
		n++
		if !validSelector(r.OwnKV.Key, r.OwnKV.Prefix) {
			return false
		}
	}
	if r.Function != nil {
		n++
		if !validUUID(r.Function.ID) {
			return false
		}
	}
	if r.OwnCron != nil {
		n++
		if !validSelector(r.OwnCron.Name, r.OwnCron.Prefix) || !validUUID(r.OwnCron.TargetID) {
			return false
		}
	}
	if r.OwnExecution != nil {
		n++
	}
	return n == 1
}

func actionResource(a Action, r Resource) bool {
	switch a {
	case KVGet, KVPut, KVDelete, KVIncr, KVCAS:
		return r.OwnKV != nil
	case KVList:
		return r.OwnKV != nil && r.OwnKV.Prefix != nil
	case FunctionsInvoke, JobsEnqueue:
		return r.Function != nil
	case CronsUpsert:
		return r.OwnCron != nil
	case SpansEmit:
		return r.OwnExecution != nil
	default:
		return false
	}
}

func validSelector(exact, prefix *string) bool {
	if (exact == nil) == (prefix == nil) {
		return false
	}
	if exact != nil {
		return validLiteral(*exact, false)
	}
	return validLiteral(*prefix, true)
}

func validLiteral(s string, empty bool) bool {
	return (empty || s != "") && utf8.ValidString(s) && utf8.RuneCountInString(s) <= MaxKeyRunes
}

// UUIDs use lowercase canonical RFC 9562 text, valid version and RFC variant.
func validUUID(s string) bool {
	if len(s) != 36 || s == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i := range len(s) {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if s[i] != '-' {
				return false
			}
			continue
		}
		if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return s[14] >= '1' && s[14] <= '8' && (s[19] == '8' || s[19] == '9' || s[19] == 'a' || s[19] == 'b')
}

func validTrigger(t Trigger) bool { return t == F2F || t == Job || t == Cron }
