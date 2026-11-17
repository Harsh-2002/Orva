package workloadhistory

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
)

// HTTPAdapter is unregistered on normal servers. Authenticate must be a trusted
// live resolver, never an author-supplied principal or credential UUID.
type HTTPAdapter struct {
	Service      *Service
	Authenticate func(*http.Request) (*auth.Principal, error)
}

func (a *HTTPAdapter) Register(mux *http.ServeMux) {
	for _, op := range []string{"list", "get", "purge", "reissue"} {
		op := op
		mux.HandleFunc("POST /api/v1/work-history/"+op, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if a.Service == nil || a.Authenticate == nil {
				respond.Error(w, 503, "UNAVAILABLE", "work history unavailable", "")
				return
			}
			p, err := a.Authenticate(r)
			if err != nil || p == nil {
				respond.Error(w, 401, "UNAUTHORIZED", "authentication required", "")
				return
			}
			ctx := auth.WithOperator(r.Context(), p)
			raw, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+8193))
			if err != nil || len(raw) > (1<<20)+8192 || !utf8.Valid(raw) || !strictJSON(raw) {
				respond.Error(w, 400, "VALIDATION", "invalid history request", "")
				return
			}
			decode := func(v any) error {
				d := json.NewDecoder(bytes.NewReader(raw))
				d.DisallowUnknownFields()
				return d.Decode(v)
			}
			var out any
			switch op {
			case "list":
				var in struct {
					TargetID string                   `json:"target_id"`
					After    database.HistorySelector `json:"after"`
					Limit    int                      `json:"limit"`
				}
				err = decode(&in)
				if err == nil {
					out, err = a.Service.List(ctx, in.TargetID, in.After, in.Limit)
				}
			case "get":
				var in database.HistorySelector
				err = decode(&in)
				if err == nil {
					out, err = a.Service.Get(ctx, in)
				}
			case "purge":
				var in struct {
					Selected []database.HistorySelector `json:"selected"`
					Confirm  bool                       `json:"confirm"`
				}
				err = decode(&in)
				if err == nil {
					err = a.Service.Purge(ctx, in.Selected, in.Confirm)
					out = map[string]bool{"purged": err == nil}
				}
			case "reissue":
				var in database.HistoryReissue
				err = decode(&in)
				if err == nil {
					var id string
					id, err = a.Service.Reissue(ctx, in)
					out = map[string]string{"new_id": id, "origin": "operator"}
				}
			}
			if err != nil {
				status := 400
				code := "VALIDATION"
				if errors.Is(err, database.ErrOperatorDenied) {
					status = 403
					code = "FORBIDDEN"
				}
				if errors.Is(err, database.ErrHistoryCapacity) {
					status = 409
					code = "CONFLICT"
				}
				respond.Error(w, status, code, "history request refused", "")
				return
			}
			respond.JSON(w, 200, out)
		})
	}
}

func strictJSON(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 32 {
			return false
		}
		t, err := d.Token()
		if err != nil {
			return false
		}
		start, container := t.(json.Delim)
		if !container {
			return true
		}
		switch start {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, err := d.Token()
				name, ok := k.(string)
				if err != nil || !ok || seen[name] {
					return false
				}
				seen[name] = true
				if !value(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for d.More() {
				if !value(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	if !value(0) {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}
