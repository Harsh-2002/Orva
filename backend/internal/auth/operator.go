package auth

import (
	"context"
	"regexp"
	"strings"
	"unicode/utf8"
)

// OperatorReference is token-free submission provenance, never authentication by ID.
type OperatorReference struct {
	Kind             string
	ID               string
	ExpectedResource string
}

type operatorContextKey struct{}

var operatorUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func validOperatorReference(ref OperatorReference) bool {
	if !operatorUUID.MatchString(ref.ID) {
		return false
	}
	switch ref.Kind {
	case KindAPIKey, KindSession:
		return ref.ExpectedResource == ""
	case KindOAuth:
		return len(ref.ExpectedResource) > 0 && len(ref.ExpectedResource) <= 4096 &&
			utf8.ValidString(ref.ExpectedResource) && !strings.ContainsRune(ref.ExpectedResource, 0)
	default:
		return false
	}
}

// WithOperator freezes only a trusted resolver's credential identity/audience.
// Invalid/channel principals clear an inherited reference; permissions are rechecked at commit.
func WithOperator(ctx context.Context, principal *Principal) context.Context {
	var ref OperatorReference
	if principal != nil {
		candidate := OperatorReference{Kind: principal.Kind, ID: principal.ID, ExpectedResource: principal.ExpectedResource}
		if validOperatorReference(candidate) {
			ref = candidate
		}
	}
	return context.WithValue(ctx, operatorContextKey{}, ref)
}

func OperatorFromContext(ctx context.Context) (OperatorReference, bool) {
	if ctx == nil {
		return OperatorReference{}, false
	}
	ref, ok := ctx.Value(operatorContextKey{}).(OperatorReference)
	return ref, ok && validOperatorReference(ref)
}
