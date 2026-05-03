package oauth

import (
	_ "embed"
	"html/template"
)

//go:embed consent.tmpl
var consentTemplateSrc string

// consentTemplate is parsed once at package init. Re-parsing per request
// would be wasteful for a static file.
var consentTemplate = template.Must(template.New("consent").Parse(consentTemplateSrc))

// consentData is the view-model passed to consent.tmpl. Keep field
// names aligned with the {{...}} placeholders in the template.
type consentData struct {
	ClientName               string
	ClientID                 string
	RedirectURI              string
	Scope                    string
	State                    string
	CodeChallenge            string
	CodeChallengeMethod      string
	Resource                 string
	Username                 string
	ScopeBullets             []string
	AccessTokenLifetimeHuman string
}
