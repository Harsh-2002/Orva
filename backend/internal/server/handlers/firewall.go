package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/firewall"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
)

// FirewallHandler exposes the sandbox egress policy as a REST resource. The
// stored rules live in the egress_blocklist table; the firewall Manager polls
// that table every 10s, compiles it into an nsjail NSTUN policy, and publishes
// it as an immutable generation that each egress sandbox loads at spawn. A new
// generation retires the warm egress pools so running workers roll forward.
// Each mutation here optionally calls Manager.ForceRefresh so the operator gets
// immediate feedback instead of waiting for the next tick.
type FirewallHandler struct {
	DB      *database.Database
	Manager *firewall.Manager
}

// wildcardUnenforceable is the single reason string behind every wildcard
// refusal. It mirrors the compiler's own `unenforced_rules` reason (see
// firewall.compile) so an operator reading the rejection and an operator
// reading the status snapshot are told the same thing.
const wildcardUnenforceable = "wildcard hostnames cannot be enforced: the egress policy matches IP/CIDR, not DNS names. Use a CIDR or an exact hostname."

func validateEnforceableRule(ruleType, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("value is required")
	}
	switch ruleType {
	case database.BlocklistTypeCIDR:
		if err := firewall.ValidateTarget(value); err != nil {
			return fmt.Errorf("value must be an enforceable IP or CIDR: %w", err)
		}
	case database.BlocklistTypeHostname:
		// A literal address intentionally typed as a hostname would still be
		// resolved as an address by net.LookupHost. Refuse the misleading type
		// instead of storing a rule that later lands in unenforced_rules.
		if net.ParseIP(value) != nil {
			return errors.New("literal IP addresses must use rule_type cidr")
		}
		if !validHostnameRe.MatchString(value) {
			return errors.New("value must be an exact hostname using letters, digits, dots, or hyphens")
		}
	}
	return nil
}

type listFirewallResponse struct {
	Rules  []*database.BlocklistRule `json:"rules"`
	Status firewall.Snapshot         `json:"status"`
}

// List handles GET /api/v1/firewall/rules.
// refreshEnforcement recompiles the egress policy after a rule or DNS change.
//
// The mutation itself has already committed, so a failure here does not make
// the response wrong about the write — but it does mean the change the
// operator just made is NOT in force. Discarding the error silently (which
// this did) left them believing a security rule had taken effect when the
// previous policy was still the one running. The compile failure is recorded
// on the status snapshot (policy_stale + last_compile_error); this makes it
// visible in the log at the moment it happens too, naming the operation.
func (h *FirewallHandler) refreshEnforcement(r *http.Request) {
	if h.Manager == nil {
		return
	}
	if err := h.Manager.ForceRefresh(); err != nil {
		slog.Warn("egress policy not refreshed after a change; the change is saved but NOT in force",
			"err", err, "method", r.Method, "path", r.URL.Path,
			"hint", "see GET /api/v1/firewall/status (last_compile_error)")
	}
}

func (h *FirewallHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	rules, err := h.DB.ListBlocklistRules()
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", err.Error(), reqID)
		return
	}
	resp := listFirewallResponse{Rules: rules}
	if h.Manager != nil {
		resp.Status = h.Manager.Snapshot()
	}
	respond.JSON(w, http.StatusOK, resp)
}

// Status handles GET /api/v1/firewall/status. Same snapshot that rides inside
// "status" on the rules/resolve responses, on its own so a monitor can poll
// whether a policy is actually in force — enforced, policy_generation,
// policy_stale, unenforced_rules — without pulling the whole rule table.
func (h *FirewallHandler) Status(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	if h.Manager == nil {
		// Reporting an empty snapshot here would read as "nothing is blocked",
		// which is indistinguishable from a healthy empty policy. Say instead
		// that enforcement state is unknown.
		respond.Error(w, http.StatusServiceUnavailable, "FIREWALL_DISABLED",
			"firewall manager not initialized", reqID)
		return
	}
	respond.JSON(w, http.StatusOK, h.Manager.Snapshot())
}

type createFirewallRequest struct {
	RuleType string `json:"rule_type"` // 'cidr' | 'hostname' ('wildcard' exists in the table but is refused here)
	Value    string `json:"value"`
	Label    string `json:"label"`
	Enabled  *bool  `json:"enabled"` // optional, default true
}

// Create handles POST /api/v1/firewall/rules. Always inserts as kind='custom'.
func (h *FirewallHandler) Create(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	var req createFirewallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	req.RuleType = strings.TrimSpace(req.RuleType)
	req.Value = strings.TrimSpace(req.Value)
	if req.RuleType == "" {
		// Auto-detect: '/' → CIDR, '*.' → wildcard (refused just below, with
		// the reason, rather than silently downgraded to a hostname), else
		// hostname.
		switch {
		case strings.Contains(req.Value, "/"):
			req.RuleType = database.BlocklistTypeCIDR
		case strings.HasPrefix(req.Value, "*."):
			req.RuleType = database.BlocklistTypeWildcard
		default:
			// Bare IP without /N is also CIDR-able as /32 / /128.
			if ip := net.ParseIP(req.Value); ip != nil {
				if ip.To4() != nil {
					req.Value += "/32"
				} else {
					req.Value += "/128"
				}
				req.RuleType = database.BlocklistTypeCIDR
			} else {
				req.RuleType = database.BlocklistTypeHostname
			}
		}
	}
	if !database.ValidBlocklistRuleType(req.RuleType) {
		respond.Error(w, http.StatusBadRequest, "VALIDATION",
			"rule_type must be one of: cidr, hostname", reqID)
		return
	}
	// A wildcard is stored-but-never-enforced (the compiler drops it into
	// status.unenforced_rules), so accepting a new one would hand the operator a
	// rule that looks armed and blocks nothing. Refuse at the door. Legacy rows
	// stay readable and deletable — only creating and enabling are closed off.
	if req.RuleType == database.BlocklistTypeWildcard {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", wildcardUnenforceable, reqID)
		return
	}
	if err := validateEnforceableRule(req.RuleType, req.Value); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	rule, err := h.DB.InsertCustomBlocklistRule(req.RuleType, req.Value, req.Label, enabled)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			respond.Error(w, http.StatusConflict, "CONFLICT", "rule with this value already exists", reqID)
			return
		}
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", err.Error(), reqID)
		return
	}
	if h.Manager != nil {
		h.refreshEnforcement(r)
	}
	respond.JSON(w, http.StatusCreated, rule)
}

type updateFirewallRequest struct {
	RuleType *string `json:"rule_type"`
	Value    *string `json:"value"`
	Label    *string `json:"label"`
	Enabled  *bool   `json:"enabled"`
}

// Update handles PUT /api/v1/firewall/rules/{id}. Toggles enabled
// flag for any kind, and edits value/type/label only for kind='custom'.
func (h *FirewallHandler) Update(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id, ok := parseRuleID(r.URL.Path)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing or invalid rule id", reqID)
		return
	}

	var req updateFirewallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}

	editing := req.Value != nil || req.RuleType != nil || req.Label != nil
	if req.Enabled != nil || editing {
		existing, err := h.DB.GetBlocklistRule(id)
		if err != nil {
			respond.Error(w, http.StatusNotFound, "NOT_FOUND", "rule not found", reqID)
			return
		}
		ruleType := existing.RuleType
		value := existing.Value
		label := existing.Label
		if req.RuleType != nil {
			ruleType = strings.TrimSpace(*req.RuleType)
		}
		if req.Value != nil {
			value = strings.TrimSpace(*req.Value)
		}
		if req.Label != nil {
			label = *req.Label
		}
		if editing && !database.ValidBlocklistRuleType(ruleType) {
			respond.Error(w, http.StatusBadRequest, "VALIDATION",
				"rule_type must be one of: cidr, hostname", reqID)
			return
		}
		// Catches both "switch this rule to wildcard" and "edit the value of an
		// existing wildcard row" (ruleType carries over from the stored row).
		// A legacy wildcard is therefore disable-or-delete only; there is no
		// edit that leaves it enforceable.
		if (editing || (req.Enabled != nil && *req.Enabled)) && ruleType == database.BlocklistTypeWildcard {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", wildcardUnenforceable, reqID)
			return
		}
		// Validate before either write so a combined {enabled:true,value:bad}
		// request cannot partially arm a legacy row and then fail its edit.
		if editing || (req.Enabled != nil && *req.Enabled) {
			if err := validateEnforceableRule(ruleType, value); err != nil {
				respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
				return
			}
		}
		if req.Enabled != nil {
			if err := h.DB.SetBlocklistRuleEnabled(id, *req.Enabled); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					respond.Error(w, http.StatusNotFound, "NOT_FOUND", "rule not found", reqID)
					return
				}
				respond.Error(w, http.StatusInternalServerError, "INTERNAL", err.Error(), reqID)
				return
			}
		}
		if editing {
			if err := h.DB.UpdateBlocklistRuleValue(id, ruleType, value, label); err != nil {
				respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
				return
			}
		}
	}

	if h.Manager != nil {
		h.refreshEnforcement(r)
	}

	rule, err := h.DB.GetBlocklistRule(id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, rule)
}

// Delete handles DELETE /api/v1/firewall/rules/{id}. Custom only.
func (h *FirewallHandler) Delete(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id, ok := parseRuleID(r.URL.Path)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing or invalid rule id", reqID)
		return
	}
	if err := h.DB.DeleteCustomBlocklistRule(id); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}
	if h.Manager != nil {
		h.refreshEnforcement(r)
	}
	respond.JSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
}

// GetDNS handles GET /api/v1/firewall/dns. Returns the operator's
// configured resolvers + search domain + the shipped defaults so the
// UI can render a "reset to default" affordance.
func (h *FirewallHandler) GetDNS(w http.ResponseWriter, r *http.Request) {
	cfg := firewall.LoadDNSConfig(h.DB)
	respond.JSON(w, http.StatusOK, cfg)
}

type updateDNSRequest struct {
	Servers []string             `json:"servers"` // explicit IPs; empty array = use defaults
	Search  string               `json:"search"`  // optional search domain
	Records []firewall.DNSRecord `json:"records"` // operator host→IP overrides (nil = leave unchanged is wrong; we always overwrite)
}

// validHostname enforces RFC 1123-ish names for the host side of an
// override. We intentionally allow the wildcard-free single-label case
// (e.g. "db", "api") because operators commonly map short internal names
// alongside short DNS search domains.
var validHostnameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

// PutDNS handles PUT /api/v1/firewall/dns. Validates each server as a
// literal IP (no hostnames — would be a chicken/egg). Empty servers
// list means "fall back to default resolvers"; empty search clears it.
// Records are operator-defined host→IP overrides written into the
// generated /etc/hosts; they take precedence over upstream DNS.
func (h *FirewallHandler) PutDNS(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	var req updateDNSRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	clean := []string{}
	for _, s := range req.Servers {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if net.ParseIP(s) == nil || firewall.ValidateTarget(s) != nil {
			respond.Error(w, http.StatusBadRequest, "VALIDATION",
				"invalid resolver IP: "+s+" (use a specific IPv4 or IPv6 address)", reqID)
			return
		}
		clean = append(clean, s)
	}
	if len(clean) > 8 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "max 8 resolvers", reqID)
		return
	}

	cleanRecords := []firewall.DNSRecord{}
	seenHosts := map[string]bool{}
	for _, rec := range req.Records {
		host := strings.TrimSpace(rec.Host)
		ip := strings.TrimSpace(rec.IP)
		if host == "" && ip == "" {
			continue
		}
		if !validHostnameRe.MatchString(host) {
			respond.Error(w, http.StatusBadRequest, "VALIDATION",
				"invalid hostname: "+host+" (use letters, digits, dots, hyphens)", reqID)
			return
		}
		if net.ParseIP(ip) == nil {
			respond.Error(w, http.StatusBadRequest, "VALIDATION",
				"invalid IP for "+host+": "+ip+" (must be a literal IPv4 or IPv6 address)", reqID)
			return
		}
		if seenHosts[host] {
			respond.Error(w, http.StatusBadRequest, "VALIDATION",
				"duplicate host in records: "+host, reqID)
			return
		}
		seenHosts[host] = true
		cleanRecords = append(cleanRecords, firewall.DNSRecord{Host: host, IP: ip})
	}
	if len(cleanRecords) > 64 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "max 64 host records", reqID)
		return
	}

	if err := h.DB.SetSystemConfig("dns_servers", strings.Join(clean, ",")); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", err.Error(), reqID)
		return
	}
	// resolv.conf is rendered as "search %s\n" BEFORE the nameserver lines,
	// so a newline here injects a line that wins: "corp.local\nnameserver
	// 203.0.113.9" makes the attacker's resolver primary for every egress
	// sandbox, persisted in system_config. Servers and record hosts were
	// already validated; this field only had TrimSpace.
	search, err := sanitizeDNSSearch(req.Search)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION",
			"invalid dns search: "+err.Error(), reqID)
		return
	}
	if err := h.DB.SetSystemConfig("dns_search", search); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", err.Error(), reqID)
		return
	}
	if err := h.DB.SetSystemConfig("dns_records", firewall.SerializeDNSRecords(cleanRecords)); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", err.Error(), reqID)
		return
	}

	// ForceRefresh re-renders resolv.conf and hosts immediately; sandboxes
	// spawned after this point mount the new files. A resolver change also
	// shifts the compiled policy (resolvers get an explicit allow), so it
	// publishes a new generation and the warm egress pools are retired for
	// us. A search-domain- or records-only edit does not move the policy, so
	// existing warm workers keep the files they were spawned with until they
	// age out via idle TTL.
	if h.Manager != nil {
		h.refreshEnforcement(r)
	}

	respond.JSON(w, http.StatusOK, firewall.LoadDNSConfig(h.DB))
}

// Resolve handles POST /api/v1/firewall/resolve. Force re-resolve and
// return the updated snapshot so the UI can refresh inline.
func (h *FirewallHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	if h.Manager == nil {
		respond.Error(w, http.StatusServiceUnavailable, "FIREWALL_DISABLED",
			"firewall manager not initialized", reqID)
		return
	}
	if err := h.Manager.ForceRefresh(); err != nil {
		respond.JSON(w, http.StatusOK, map[string]any{
			"refreshed": false,
			"error":     err.Error(),
			"status":    h.Manager.Snapshot(),
		})
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"refreshed": true,
		"status":    h.Manager.Snapshot(),
	})
}

// parseRuleID pulls the trailing /<number> off the URL path.
func parseRuleID(path string) (int64, bool) {
	idx := strings.LastIndex(path, "/")
	if idx < 0 || idx == len(path)-1 {
		return 0, false
	}
	id, err := strconv.ParseInt(path[idx+1:], 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// sanitizeDNSSearch validates the resolv.conf "search" field.
//
// firewall/dns.go renders it as "search %s\n" BEFORE the nameserver lines,
// and resolv.conf uses the first nameserver it sees, so a newline here
// installs a resolver of the caller's choosing for every egress-enabled
// sandbox — persisted in system_config. Servers and record hosts were
// already validated; this field only had TrimSpace.
//
// Control characters are rejected outright rather than being absorbed by
// the field split. strings.Fields would happily turn
// "corp.local\nnameserver 1.2.3.4" into three legal-looking labels and
// rejoin them harmlessly, which is safe but silently rewrites the
// operator's input instead of telling them it was wrong.
func sanitizeDNSSearch(raw string) (string, error) {
	for _, r := range raw {
		if r == 0x7f || (r < 0x20 && r != ' ' && r != '\t') {
			return "", fmt.Errorf("control character %q is not allowed", r)
		}
		if r == '\n' || r == '\r' {
			return "", errors.New("line breaks are not allowed")
		}
	}
	fields := strings.Fields(raw)
	// resolv.conf historically caps the search list at 6 entries.
	if len(fields) > 6 {
		return "", errors.New("at most 6 search domains")
	}
	for _, d := range fields {
		if !validHostnameRe.MatchString(d) {
			return "", fmt.Errorf("%q is not a valid domain: use letters, digits, dots or hyphens", d)
		}
	}
	return strings.Join(fields, " "), nil
}
