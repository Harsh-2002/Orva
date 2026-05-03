package oauth

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
)

// Token / code lifetimes. Tuned for browser-based MCP connectors:
// - 1h access token: short enough that revocation propagates fast,
//   long enough that a chat session doesn't refresh constantly.
// - 30d refresh: matches how long claude.ai/ChatGPT keep connectors
//   "warm" between user sessions before re-prompting consent.
// - 10m code TTL: standard OAuth 2.1 §1.3.1 — plenty for a redirect.
const (
	accessTokenLifetime  = 1 * time.Hour
	refreshTokenLifetime = 30 * 24 * time.Hour
	authCodeLifetime     = 10 * time.Minute
)

// Handler hosts the OAuth 2.1 authorization-server endpoints. It owns
// the DB handle but no HTTP-routing logic — the router wires the six
// methods (DCR / authorize GET+POST / token / revoke) into the mux.
type Handler struct {
	DB            *database.Database
	SecureCookies bool // mirrors AuthHandler.SecureCookies; set when behind HTTPS
}

// ── DCR (RFC 7591) ──────────────────────────────────────────────────

// Register handles POST /register. claude.ai and ChatGPT both call this
// during their first connection to mint a fresh client_id (they don't
// reuse client_ids across operators). We accept the RFC 7591 metadata
// shape, validate, persist, and echo back the credentials.
//
// Rate-limited per-IP (10/hr) so a script can't fill the table.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	if !defaultDCRLimiter.allow(dcrClientIP(r)) {
		w.Header().Set("Retry-After", "3600")
		writeOAuthError(w, http.StatusTooManyRequests, "temporarily_unavailable",
			"too many client registrations from this IP — try again later")
		return
	}
	if !contentTypeIsJSON(r) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata",
			"Content-Type must be application/json")
		return
	}
	var req struct {
		ClientName              string   `json:"client_name"`
		ClientURI               string   `json:"client_uri"`
		RedirectURIs            []string `json:"redirect_uris"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		Scope                   string   `json:"scope"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata",
			"failed to parse JSON body")
		return
	}

	if err := ValidateRedirectURIs(req.RedirectURIs); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
		return
	}
	if err := ValidateGrantTypes(req.GrantTypes); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", err.Error())
		return
	}
	if err := ValidateResponseTypes(req.ResponseTypes); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", err.Error())
		return
	}
	if err := ValidateAuthMethod(req.TokenEndpointAuthMethod); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", err.Error())
		return
	}

	authMethod := req.TokenEndpointAuthMethod
	if authMethod == "" {
		// OAuth 2.1: public clients (those that can't keep a secret —
		// browser-based MCP clients ALL fall here) use "none" + PKCE.
		authMethod = "none"
	}
	clientName := strings.TrimSpace(req.ClientName)
	if clientName == "" {
		clientName = "Unnamed MCP client"
	}
	scope := req.Scope
	if scope == "" {
		scope = DefaultGrantedScope
	}
	if !IsValidScope(scope) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata",
			"requested scope contains unknown values")
		return
	}
	grantTypes := req.GrantTypes
	if len(grantTypes) == 0 {
		grantTypes = []string{"authorization_code", "refresh_token"}
	}
	responseTypes := req.ResponseTypes
	if len(responseTypes) == 0 {
		responseTypes = []string{"code"}
	}

	clientID := NewClientID()
	var (
		clientSecret     string
		clientSecretHash string
	)
	if authMethod != "none" {
		clientSecret = NewClientSecret()
		clientSecretHash = HashToken(clientSecret)
	}

	rec := &database.OAuthClient{
		ID:                      NewClientStorageID(),
		ClientID:                clientID,
		ClientSecretHash:        clientSecretHash,
		ClientName:              clientName,
		ClientURI:               req.ClientURI,
		RedirectURIs:            database.EncodeStringsAsJSON(req.RedirectURIs),
		GrantTypes:              database.EncodeStringsAsJSON(grantTypes),
		ResponseTypes:           database.EncodeStringsAsJSON(responseTypes),
		TokenEndpointAuthMethod: authMethod,
		Scope:                   scope,
	}
	if err := h.DB.InsertOAuthClient(rec); err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error",
			"failed to persist client registration")
		return
	}

	resp := map[string]any{
		"client_id":                  clientID,
		"client_id_issued_at":        time.Now().Unix(),
		"client_name":                clientName,
		"redirect_uris":              req.RedirectURIs,
		"grant_types":                grantTypes,
		"response_types":             responseTypes,
		"token_endpoint_auth_method": authMethod,
		"scope":                      scope,
	}
	if clientSecret != "" {
		resp["client_secret"] = clientSecret
		// Per RFC 7591 §3.2.1, "0" means the secret never expires from
		// the AS perspective. We don't auto-rotate; revocation is the
		// only off-switch. Operators can re-register if they need a
		// fresh secret.
		resp["client_secret_expires_at"] = 0
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// ── /oauth/authorize (consent) ───────────────────────────────────────

// authorizeRequest is the validated, normalised form of a query-string
// (GET) or form-body (POST) authorize request. Both flows go through
// the same parser so the validation is identical.
type authorizeRequest struct {
	ClientID            string
	RedirectURI         string
	ResponseType        string
	Scope               string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string
}

func parseAuthorizeRequest(r *http.Request) (*authorizeRequest, error) {
	q := r.URL.Query()
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			return nil, errors.New("malformed form body")
		}
		q = r.Form
	}
	req := &authorizeRequest{
		ClientID:            q.Get("client_id"),
		RedirectURI:         q.Get("redirect_uri"),
		ResponseType:        q.Get("response_type"),
		Scope:               q.Get("scope"),
		State:               q.Get("state"),
		CodeChallenge:       q.Get("code_challenge"),
		CodeChallengeMethod: q.Get("code_challenge_method"),
		Resource:            q.Get("resource"),
	}
	if req.CodeChallengeMethod == "" {
		req.CodeChallengeMethod = "S256"
	}
	return req, nil
}

// AuthorizeGET renders the consent screen. If the user is not signed in,
// we 302 to /web/login?redirect=<original URL>; the existing login UI
// already preserves the redirect parameter end-to-end.
func (h *Handler) AuthorizeGET(w http.ResponseWriter, r *http.Request) {
	req, err := parseAuthorizeRequest(r)
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	// Pre-validation: any failure here means we CAN'T trust the
	// redirect_uri yet, so the browser sees an HTML error rather than
	// a redirect to a possibly-attacker-controlled URI (OAuth 2.1
	// §4.1.2.1 — errors before redirect_uri validation must not
	// redirect).
	if req.ResponseType != "code" {
		http.Error(w, "unsupported response_type — only 'code' is allowed", http.StatusBadRequest)
		return
	}
	if req.CodeChallenge == "" || req.CodeChallengeMethod != "S256" {
		http.Error(w, "PKCE required: code_challenge + S256", http.StatusBadRequest)
		return
	}
	client, err := h.DB.GetOAuthClientByID(req.ClientID)
	if err != nil || client == nil || client.RevokedAt != nil {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}
	if !MatchRedirectURI(client.RedirectURIList(), req.RedirectURI) {
		http.Error(w, "redirect_uri does not match any registered URI", http.StatusBadRequest)
		return
	}

	// From here on, errors that originate from the user's choices CAN
	// safely redirect to the validated redirect_uri.

	user, err := h.userFromSession(r)
	if err != nil {
		// Bounce through the SPA login. The login page reads ?redirect
		// from the query and POSTs it back to itself on submit.
		original := r.URL.RequestURI()
		http.Redirect(w, r, "/web/login?redirect="+url.QueryEscape(original), http.StatusFound)
		return
	}

	scope := req.Scope
	if scope == "" {
		scope = client.Scope
	}
	if !IsValidScope(scope) {
		redirectWithError(w, r, req.RedirectURI, "invalid_scope", req.State)
		return
	}

	data := consentData{
		ClientName:               client.ClientName,
		ClientID:                 client.ClientID,
		RedirectURI:              req.RedirectURI,
		Scope:                    scope,
		State:                    req.State,
		CodeChallenge:            req.CodeChallenge,
		CodeChallengeMethod:      req.CodeChallengeMethod,
		Resource:                 req.Resource,
		Username:                 user.Username,
		ScopeBullets:             HumanScopeBullets(scope),
		AccessTokenLifetimeHuman: humaniseLifetime(accessTokenLifetime),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if err := consentTemplate.Execute(w, data); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
}

// AuthorizePOST processes the consent form submission. Either mints a
// code and 302s back to redirect_uri, or 302s back with an OAuth error.
func (h *Handler) AuthorizePOST(w http.ResponseWriter, r *http.Request) {
	req, err := parseAuthorizeRequest(r)
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.ResponseType != "code" || req.CodeChallenge == "" || req.CodeChallengeMethod != "S256" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	client, err := h.DB.GetOAuthClientByID(req.ClientID)
	if err != nil || client == nil || client.RevokedAt != nil {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}
	if !MatchRedirectURI(client.RedirectURIList(), req.RedirectURI) {
		http.Error(w, "redirect_uri does not match any registered URI", http.StatusBadRequest)
		return
	}

	user, err := h.userFromSession(r)
	if err != nil {
		http.Error(w, "session expired", http.StatusUnauthorized)
		return
	}

	decision := r.FormValue("decision")
	if decision != "allow" {
		redirectWithError(w, r, req.RedirectURI, "access_denied", req.State)
		return
	}

	scope := req.Scope
	if scope == "" {
		scope = client.Scope
	}
	if !IsValidScope(scope) {
		redirectWithError(w, r, req.RedirectURI, "invalid_scope", req.State)
		return
	}

	plaintext := NewAuthCode()
	codeHash := HashToken(plaintext)
	row := &database.OAuthAuthorizationCode{
		CodeHash:            codeHash,
		ClientID:            client.ClientID,
		UserID:              user.ID,
		RedirectURI:         req.RedirectURI,
		Scope:               NormaliseScope(ParseScope(scope)),
		Resource:            req.Resource,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
		ExpiresAt:           time.Now().Add(authCodeLifetime),
	}
	if err := h.DB.InsertOAuthAuthorizationCode(row); err != nil {
		redirectWithError(w, r, req.RedirectURI, "server_error", req.State)
		return
	}

	target, err := url.Parse(req.RedirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	q := target.Query()
	q.Set("code", plaintext)
	if req.State != "" {
		q.Set("state", req.State)
	}
	target.RawQuery = q.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// ── /oauth/token ─────────────────────────────────────────────────────

// Token branches on grant_type. authorization_code redeems a code +
// PKCE; refresh_token rotates the refresh hash. Both paths emit the
// same response shape so callers don't need a special case.
func (h *Handler) Token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
		return
	}

	clientID, clientSecret := extractClientCreds(r)
	client, err := h.DB.GetOAuthClientByID(clientID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "unknown client_id")
			return
		}
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "client lookup failed")
		return
	}
	if client.RevokedAt != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "client revoked")
		return
	}
	if !client.IsPublic() {
		// Confidential client: require + verify the secret.
		if clientSecret == "" || HashToken(clientSecret) != client.ClientSecretHash {
			writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
			return
		}
	}

	switch r.PostFormValue("grant_type") {
	case "authorization_code":
		h.handleAuthorizationCodeGrant(w, r, client)
	case "refresh_token":
		h.handleRefreshTokenGrant(w, r, client)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type",
			"only authorization_code and refresh_token grants are supported")
	}
}

func (h *Handler) handleAuthorizationCodeGrant(w http.ResponseWriter, r *http.Request, client *database.OAuthClient) {
	code := r.PostFormValue("code")
	verifier := r.PostFormValue("code_verifier")
	redirectURI := r.PostFormValue("redirect_uri")
	if code == "" || verifier == "" || redirectURI == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "missing required parameter")
		return
	}

	row, err := h.DB.RedeemOAuthAuthorizationCode(HashToken(code))
	if err != nil {
		// Either already-used (ErrAuthCodeAlreadyUsed) or expired/unknown
		// (sql.ErrNoRows). Same OAuth error in both cases — don't help
		// attackers distinguish.
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code is invalid or expired")
		return
	}
	if row.ClientID != client.ClientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code was issued to a different client")
		return
	}
	if row.RedirectURI != redirectURI {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
		return
	}
	if !VerifyPKCE(verifier, row.CodeChallenge, row.CodeChallengeMethod) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}
	// RFC 8707 §2.2: when resource is included on the token request,
	// it must match what was on the authorization request. Some clients
	// re-send it; others omit it. Empty on token is fine — we already
	// have the canonical resource from /authorize.
	if reqResource := r.PostFormValue("resource"); reqResource != "" && reqResource != row.Resource {
		writeOAuthError(w, http.StatusBadRequest, "invalid_target",
			"resource parameter does not match the authorization request")
		return
	}

	at, rt, expiresIn, err := h.mintTokenPair(client.ClientID, row.UserID, row.Scope, row.Resource)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to mint token")
		return
	}
	writeTokenResponse(w, at, rt, expiresIn, row.Scope)
}

func (h *Handler) handleRefreshTokenGrant(w http.ResponseWriter, r *http.Request, client *database.OAuthClient) {
	refresh := r.PostFormValue("refresh_token")
	if refresh == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "refresh_token required")
		return
	}
	current, err := h.DB.GetOAuthAccessTokenByRefreshHash(HashToken(refresh))
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid")
		return
	}
	if current.ClientID != client.ClientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token issued to a different client")
		return
	}
	if current.RevokedAt != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token revoked")
		return
	}
	if current.RefreshExpiresAt != nil && time.Now().After(*current.RefreshExpiresAt) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token expired")
		return
	}

	// OAuth 2.1 §4.3.1 mandates rotation for public clients. We rotate
	// for everyone — there's no downside, and the atomic UPDATE-then-
	// INSERT guarantees only one rotation wins under contention.
	newAccess := NewAccessToken()
	newRefresh := NewRefreshToken()
	now := time.Now()
	refreshExpires := now.Add(refreshTokenLifetime)
	replacement := &database.OAuthAccessToken{
		ID:               NewTokenStorageID(),
		AccessTokenHash:  HashToken(newAccess),
		RefreshTokenHash: HashToken(newRefresh),
		ClientID:         current.ClientID,
		UserID:           current.UserID,
		Scope:            current.Scope,
		Resource:         current.Resource,
		AccessExpiresAt:  now.Add(accessTokenLifetime),
		RefreshExpiresAt: &refreshExpires,
	}
	if err := h.DB.RotateOAuthRefreshToken(HashToken(refresh), replacement); err != nil {
		if errors.Is(err, database.ErrRefreshTokenAlreadyRotated) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token already used")
			return
		}
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "rotation failed")
		return
	}
	writeTokenResponse(w, newAccess, newRefresh, int(accessTokenLifetime.Seconds()), current.Scope)
}

// mintTokenPair creates and inserts a fresh access+refresh row. Used by
// the authorization_code grant; refresh-token grant uses RotateOAuthRefreshToken
// instead so the old hash is atomically nulled in the same transaction.
func (h *Handler) mintTokenPair(clientID string, userID int64, scope, resource string) (string, string, int, error) {
	access := NewAccessToken()
	refresh := NewRefreshToken()
	now := time.Now()
	refreshExpires := now.Add(refreshTokenLifetime)
	row := &database.OAuthAccessToken{
		ID:               NewTokenStorageID(),
		AccessTokenHash:  HashToken(access),
		RefreshTokenHash: HashToken(refresh),
		ClientID:         clientID,
		UserID:           userID,
		Scope:            scope,
		Resource:         resource,
		AccessExpiresAt:  now.Add(accessTokenLifetime),
		RefreshExpiresAt: &refreshExpires,
	}
	if err := h.DB.InsertOAuthAccessToken(row); err != nil {
		return "", "", 0, err
	}
	return access, refresh, int(accessTokenLifetime.Seconds()), nil
}

// ── /oauth/revoke (RFC 7009) ─────────────────────────────────────────

// Revoke flips revoked_at on the matching row regardless of whether the
// caller passed an access or a refresh token. RFC 7009 §2.2: always
// return 200 (even on unknown tokens) so the client doesn't infer
// existence by status code.
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
		return
	}
	token := r.PostFormValue("token")
	if token == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "token parameter required")
		return
	}
	_ = h.DB.RevokeOAuthAccessToken(HashToken(token))
	w.WriteHeader(http.StatusOK)
}

// ── helpers ──────────────────────────────────────────────────────────

func (h *Handler) userFromSession(r *http.Request) (*database.User, error) {
	cookie, err := r.Cookie("session_token")
	if err != nil {
		return nil, err
	}
	return h.DB.GetSessionUser(cookie.Value)
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": description,
	})
}

func writeTokenResponse(w http.ResponseWriter, accessToken, refreshToken string, expiresIn int, scope string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  accessToken,
		"token_type":    "Bearer",
		"expires_in":    expiresIn,
		"refresh_token": refreshToken,
		"scope":         scope,
	})
}

func redirectWithError(w http.ResponseWriter, r *http.Request, redirectURI, code, state string) {
	target, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	q := target.Query()
	q.Set("error", code)
	if state != "" {
		q.Set("state", state)
	}
	target.RawQuery = q.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// extractClientCreds pulls (client_id, client_secret) from either
// Authorization: Basic (RFC 6749 §2.3.1) or the form body (§2.3.1
// allows client_secret_post too). Returns ("", "") if neither path
// produces a client_id.
func extractClientCreds(r *http.Request) (clientID, clientSecret string) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Basic ") {
		if dec, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(h, "Basic ")); err == nil {
			if id, sec, ok := strings.Cut(string(dec), ":"); ok {
				// HTTP Basic uses URL-encoding for credentials per
				// RFC 6749 §2.3.1.
				if du, err := url.QueryUnescape(id); err == nil {
					id = du
				}
				if ds, err := url.QueryUnescape(sec); err == nil {
					sec = ds
				}
				return id, sec
			}
		}
	}
	return r.PostFormValue("client_id"), r.PostFormValue("client_secret")
}

func contentTypeIsJSON(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return false
	}
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	return strings.EqualFold(strings.TrimSpace(ct), "application/json")
}

func humaniseLifetime(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "short-lived"
	case d < time.Hour:
		return d.Round(time.Minute).String()
	case d == time.Hour:
		return "1-hour"
	default:
		return d.Round(time.Hour).String()
	}
}
