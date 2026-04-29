package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/firewall"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// FirewallHandler exposes the egress blocklist as a REST resource. The
// blocklist itself lives in the egress_blocklist table; the firewall
// Manager polls that table every 10s and re-applies nftables rules.
// Each mutation here optionally calls Manager.ForceRefresh so the
// operator gets immediate feedback instead of waiting for the next tick.
type FirewallHandler struct {
	DB      *database.Database
	Manager *firewall.Manager
}

type listFirewallResponse struct {
	Rules    []*database.BlocklistRule `json:"rules"`
	Status   firewall.Snapshot         `json:"status"`
}

// List handles GET /api/v1/firewall/rules.
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

type createFirewallRequest struct {
	RuleType string `json:"rule_type"` // 'cidr' | 'hostname' | 'wildcard'
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
		// Auto-detect: '/' → CIDR, '*.' → wildcard, else hostname.
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
			"rule_type must be one of: cidr, hostname, wildcard", reqID)
		return
	}
	if req.Value == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "value is required", reqID)
		return
	}
	// Type-specific validation so the operator can't enter
	// "192.168.1.1" with rule_type=hostname and have it silently treated
	// as a DNS name.
	switch req.RuleType {
	case database.BlocklistTypeCIDR:
		if _, _, err := net.ParseCIDR(req.Value); err != nil {
			if ip := net.ParseIP(req.Value); ip == nil {
				respond.Error(w, http.StatusBadRequest, "VALIDATION",
					"value must be an IP or CIDR (e.g. 192.168.1.0/24)", reqID)
				return
			}
		}
	case database.BlocklistTypeWildcard:
		if !strings.HasPrefix(req.Value, "*.") {
			respond.Error(w, http.StatusBadRequest, "VALIDATION",
				"wildcard rules must start with '*.' (e.g. *.corp.com)", reqID)
			return
		}
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
		_ = h.Manager.ForceRefresh()
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

	// Toggle enabled is allowed regardless of kind.
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

	// Edit value/type/label is custom-only (the DAO enforces this).
	if req.Value != nil || req.RuleType != nil || req.Label != nil {
		existing, err := h.DB.GetBlocklistRule(id)
		if err != nil {
			respond.Error(w, http.StatusNotFound, "NOT_FOUND", "rule not found", reqID)
			return
		}
		ruleType := existing.RuleType
		value := existing.Value
		label := existing.Label
		if req.RuleType != nil {
			ruleType = *req.RuleType
		}
		if req.Value != nil {
			value = strings.TrimSpace(*req.Value)
		}
		if req.Label != nil {
			label = *req.Label
		}
		if !database.ValidBlocklistRuleType(ruleType) {
			respond.Error(w, http.StatusBadRequest, "VALIDATION",
				"rule_type must be one of: cidr, hostname, wildcard", reqID)
			return
		}
		if err := h.DB.UpdateBlocklistRuleValue(id, ruleType, value, label); err != nil {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
			return
		}
	}

	if h.Manager != nil {
		_ = h.Manager.ForceRefresh()
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
		_ = h.Manager.ForceRefresh()
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
	Servers []string `json:"servers"` // explicit IPs; empty array = use defaults
	Search  string   `json:"search"`  // optional search domain
}

// PutDNS handles PUT /api/v1/firewall/dns. Validates each server as a
// literal IP (no hostnames — would be a chicken/egg). Empty servers
// list means "fall back to default resolvers"; empty search clears it.
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
		if net.ParseIP(s) == nil {
			respond.Error(w, http.StatusBadRequest, "VALIDATION",
				"invalid resolver IP: "+s+" (use a literal IPv4 or IPv6 address)", reqID)
			return
		}
		clean = append(clean, s)
	}
	if len(clean) > 8 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "max 8 resolvers", reqID)
		return
	}

	if err := h.DB.SetSystemConfig("dns_servers", strings.Join(clean, ",")); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", err.Error(), reqID)
		return
	}
	if err := h.DB.SetSystemConfig("dns_search", strings.TrimSpace(req.Search)); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", err.Error(), reqID)
		return
	}

	// ForceRefresh re-renders resolv.conf immediately. Sandboxes spawned
	// after this point pick up the new file. Existing warm workers keep
	// their old resolv.conf (mounted at spawn) — the operator can drain
	// them by toggling network_mode off and on again, or wait for them
	// to age out via idle TTL.
	if h.Manager != nil {
		_ = h.Manager.ForceRefresh()
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
