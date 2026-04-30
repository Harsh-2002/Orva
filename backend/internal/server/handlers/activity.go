package handlers

import (
	"net/http"
	"strconv"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// ActivityHandler exposes the unified activity feed at GET /api/v1/activity.
// It's the historical companion to the live SSE stream — when the dashboard
// loads or the user hits Refresh, this endpoint hands back the last N rows
// matching the current filters; the SSE subscription on /api/v1/events
// (event type "activity") then prepends new ones live.
type ActivityHandler struct {
	DB *database.Database
}

// List handles GET /api/v1/activity.
//
// Query params (all optional):
//   - source       exact match (web|api|mcp|sdk|webhook|cron|internal)
//   - actor_id     exact match
//   - since        unix millis lower bound (inclusive)
//   - until        unix millis upper bound (exclusive)
//   - status_min   integer; rows with status >= n (use 400 for "errors")
//   - q            free-text LIKE on path/summary/actor_label
//   - limit        default 200, max 1000
//   - cursor       ts millis from a previous response's next_cursor
func (h *ActivityHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	q := r.URL.Query()

	filter := database.ActivityFilter{
		Source:  q.Get("source"),
		ActorID: q.Get("actor_id"),
		Search:  q.Get("q"),
	}
	if v := q.Get("since"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			filter.SinceMS = n
		}
	}
	if v := q.Get("until"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			filter.UntilMS = n
		}
	}
	if v := q.Get("status_min"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.StatusMin = n
		}
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Limit = n
		}
	}
	if v := q.Get("cursor"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			filter.Cursor = n
		}
	}

	rows, next, err := h.DB.ListActivity(filter)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to list activity", reqID)
		return
	}
	if rows == nil {
		rows = []database.ActivityRow{}
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"rows":        rows,
		"next_cursor": next,
		"count":       len(rows),
	})
}
