package workloadtrigger

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func HTTPError(w http.ResponseWriter, err error) {
	status, code, msg := 503, "IAM_UNAVAILABLE", "Exposure mutation unavailable"
	switch {
	case errors.Is(err, database.ErrOperatorDenied):
		status, code, msg = 403, "FORBIDDEN", "Current operator authority required"
	case errors.Is(err, database.ErrExposureConflict):
		status, code, msg = 409, "IAM_CONFLICT", "Exposure or authority changed; inspect again"
	case errors.Is(err, database.ErrExposureInvalid):
		status, code, msg = 400, "VALIDATION", "Invalid exposure request"
	}
	respond.Error(w, status, code, msg, "")
}

// HTTP receives trusted URL selectors from the optional handler dependency.
func (s *Service) HTTP(w http.ResponseWriter, r *http.Request, op, owner, key string) {
	w.Header().Set("Cache-Control", "no-store")
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
	if err != nil {
		HTTPError(w, database.ErrExposureInvalid)
		return
	}
	m, err := object(raw)
	if err != nil {
		HTTPError(w, err)
		return
	}
	inject := map[string]string{}
	if strings.HasPrefix(op, "inbound_") {
		inject["function_id"] = owner
		if op != "inbound_create" {
			inject["id"] = key
		}
	}
	if op == "route_delete" {
		inject["path"] = key
	}
	for k, v := range inject {
		if m[k] != nil {
			HTTPError(w, database.ErrExposureInvalid)
			return
		}
		m[k], _ = json.Marshal(v)
	}
	body, _ := json.Marshal(m)
	out, err := s.Execute(r.Context(), op, body)
	if err != nil {
		HTTPError(w, err)
		return
	}
	status := http.StatusOK
	if op == "route_set" || op == "inbound_create" {
		status = http.StatusCreated
	}
	if op == "inbound_update" {
		respond.JSON(w, status, out["inbound_webhook"])
		return
	}
	respond.JSON(w, status, out)
}

// HTTPAdapter installs inspection only when explicitly registered by a fixture.
type HTTPAdapter struct {
	Service      *Service
	Authenticate func(*http.Request) (*auth.Principal, error)
}

func (h *HTTPAdapter) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/exposures", h)
}
func (h *HTTPAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h == nil || h.Service == nil || h.Authenticate == nil {
		HTTPError(w, database.ErrExposureUnavailable)
		return
	}
	p, err := h.Authenticate(r)
	if err != nil || p == nil || p.Kind == auth.KindChannel {
		HTTPError(w, database.ErrOperatorDenied)
		return
	}
	raw, _ := json.Marshal(map[string]string{"kind": r.URL.Query().Get("kind"), "key": r.URL.Query().Get("key"), "function_id": r.URL.Query().Get("function_id")})
	out, err := h.Service.Execute(auth.WithOperator(r.Context(), p), "inspect", raw)
	if err != nil {
		HTTPError(w, err)
		return
	}
	respond.JSON(w, 200, out)
}

// RegisterManagedRoutes exposes only custom routes, with no legacy write handler.
func (s *Service) RegisterManagedRoutes(mux *http.ServeMux, authenticate func(*http.Request) (*auth.Principal, error)) {
	register := func(pattern string, next http.HandlerFunc) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if s == nil || authenticate == nil {
				HTTPError(w, database.ErrExposureUnavailable)
				return
			}
			p, err := authenticate(r)
			if err != nil || p == nil || p.Kind == auth.KindChannel {
				HTTPError(w, database.ErrOperatorDenied)
				return
			}
			next(w, r.WithContext(auth.WithOperator(r.Context(), p)))
		})
	}
	register("GET /api/v1/exposures", func(w http.ResponseWriter, r *http.Request) {
		q, err := managedRouteQuery(r, "kind", "key", "function_id")
		owner := q.Get("function_id")
		if err != nil || q.Get("kind") != "route" || q.Get("key") == "" || (owner != "" && !ids.IsUUID(owner)) {
			HTTPError(w, database.ErrExposureInvalid)
			return
		}
		ref, ok := auth.OperatorFromContext(r.Context())
		if !ok {
			HTTPError(w, database.ErrOperatorDenied)
			return
		}
		out, err := s.db.InspectExposure(r.Context(), ref, "route", q.Get("key"), owner)
		if err != nil {
			HTTPError(w, err)
			return
		}
		respond.JSON(w, http.StatusOK, map[string]any{
			"version": strconv.FormatUint(out.Version, 10), "expected": out.Expected, "route": out.Route, "route_published": out.RoutePublished,
		})
	})
	register("POST /api/v1/routes", func(w http.ResponseWriter, r *http.Request) {
		if _, err := managedRouteQuery(r); err != nil {
			HTTPError(w, err)
			return
		}
		s.HTTP(w, r, "route_set", "", "")
	})
	register("DELETE /api/v1/routes", func(w http.ResponseWriter, r *http.Request) {
		q, err := managedRouteQuery(r, "path")
		if err != nil || q.Get("path") == "" {
			HTTPError(w, database.ErrExposureInvalid)
			return
		}
		s.HTTP(w, r, "route_delete", "", q.Get("path"))
	})
}

func managedRouteQuery(r *http.Request, allowed ...string) (url.Values, error) {
	if len(r.URL.RawQuery) > MaxRequestBytes {
		return nil, database.ErrExposureInvalid
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, database.ErrExposureInvalid
	}
	for _, key := range allowed {
		if values, present := q[key]; present {
			if len(values) != 1 || !utf8.ValidString(values[0]) {
				return nil, database.ErrExposureInvalid
			}
		}
	}
	for key := range q {
		if !slices.Contains(allowed, key) {
			return nil, database.ErrExposureInvalid
		}
	}
	return q, nil
}
