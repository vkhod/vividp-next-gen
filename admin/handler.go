package admin

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"vividp/job"
)

const artifactPresignTTL = 15 * time.Minute

// Handler wires the admin HTTP routes.
type Handler struct {
	store  *Store
	svc    *job.Service
	minio  *minio.Client
	bucket string
	auth   *Auth
	log    *slog.Logger
}

func NewHandler(store *Store, svc *job.Service, cfg Config, log *slog.Logger) (*Handler, error) {
	mc, err := minio.New(cfg.StorageEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.StorageAccessKey, cfg.StorageSecretKey, ""),
		Secure: cfg.StorageSecure,
	})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}
	auth, err := NewAuth(cfg)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	if auth == nil {
		log.Warn("ADMIN_PASSWORD not set — admin UI is unprotected")
	}
	return &Handler{store: store, svc: svc, minio: mc, bucket: cfg.JobsBucket, auth: auth, log: log}, nil
}

// RegisterRoutes registers all admin routes on mux using Go 1.22 method+path patterns.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Auth endpoints — always public.
	mux.HandleFunc("POST /api/auth/login", h.auth.HandleLogin)
	mux.HandleFunc("POST /api/auth/logout", h.auth.HandleLogout)
	mux.HandleFunc("GET /api/auth/me", h.auth.HandleMe)

	// Admin endpoints — protected by session cookie.
	protect := h.auth.Require
	mux.HandleFunc("GET /api/admin/jobs", protect(h.listJobs))
	mux.HandleFunc("GET /api/admin/jobs/{id}", protect(h.getJob))
	mux.HandleFunc("GET /api/admin/jobs/{id}/transitions", protect(h.getTransitions))
	mux.HandleFunc("GET /api/admin/jobs/{id}/artifacts", protect(h.getArtifacts))
	mux.HandleFunc("GET /api/admin/jobs/{id}/fields/summary", protect(h.getFieldsSummary))
	mux.HandleFunc("POST /api/admin/jobs/{id}/hold", protect(h.holdJob))
	mux.HandleFunc("POST /api/admin/jobs/{id}/release", protect(h.releaseJob))
	mux.HandleFunc("DELETE /api/admin/jobs/{id}", protect(h.deleteJob))

	// Internal endpoints — called by Lambda functions to write results back.
	// Not proxied by nginx; reachable only from within the server network.
	mux.HandleFunc("POST /api/internal/jobs/{id}/transition", h.internalTransition)
	mux.HandleFunc("POST /api/internal/jobs/{id}/fields", h.internalWriteFields)
}

// CORSMiddleware adds permissive CORS headers for the Vite dev server.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ── Route handlers ────────────────────────────────────────────────────────────

func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	query := ListJobsQuery{
		TenantID: q.Get("tenant_id"),
		SystemID: q.Get("system_id"),
		Search:   q.Get("search"),
		DateFrom: q.Get("date_from"),
		DateTo:   q.Get("date_to"),
		Sort:     q.Get("sort"),
		Dir:      q.Get("dir"),
		Statuses: q["status"], // multiple status= params → []string
	}

	jobs, err := h.store.ListJobs(r.Context(), query)
	if err != nil {
		h.log.Error("list jobs", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list jobs")
		return
	}
	if jobs == nil {
		jobs = []AdminJob{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (h *Handler) getJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	detail, err := h.store.GetJobDetail(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		h.log.Error("get job detail", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to fetch job")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) getTransitions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	transitions, err := h.store.GetTransitions(r.Context(), id)
	if err != nil {
		h.log.Error("get transitions", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to fetch transitions")
		return
	}
	if transitions == nil {
		transitions = []JobTransition{}
	}
	writeJSON(w, http.StatusOK, transitions)
}

func (h *Handler) getArtifacts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	raw, err := h.store.GetArtifactsJSON(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		h.log.Error("get artifacts json", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to fetch artifacts")
		return
	}

	// Decode stored artifact records
	type storedArtifact struct {
		Key       string    `json:"key"`
		Type      string    `json:"type"`
		SizeBytes int64     `json:"size_bytes"`
		CreatedAt time.Time `json:"created_at"`
	}
	var stored []storedArtifact
	if err := json.Unmarshal(raw, &stored); err != nil {
		h.log.Error("unmarshal artifacts", "error", err)
		writeError(w, http.StatusInternalServerError, "invalid artifact data")
		return
	}

	// Enrich with presigned URLs
	out := make([]ArtifactResponse, len(stored))
	for i, a := range stored {
		resp := ArtifactResponse{
			Key:       a.Key,
			Type:      a.Type,
			SizeBytes: a.SizeBytes,
			CreatedAt: a.CreatedAt,
		}
		u, err := h.minio.PresignedGetObject(r.Context(), h.bucket, a.Key, artifactPresignTTL, nil)
		if err != nil {
			h.log.Warn("presign failed", "key", a.Key, "error", err)
		} else {
			s := u.String()
			resp.PresignedURL = &s
		}
		out[i] = resp
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) getFieldsSummary(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	summary, err := h.store.GetFieldsSummary(r.Context(), id)
	if err != nil {
		h.log.Error("get fields summary", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to fetch fields summary")
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (h *Handler) holdJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.svc.HoldJob(r.Context(), id, "admin-api"); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		h.log.Error("hold job", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to hold job")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) releaseJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.svc.ReleaseJob(r.Context(), id, "admin-api"); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		h.log.Error("release job", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to release job")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.svc.DeleteJob(r.Context(), id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		h.log.Error("delete job", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete job")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Internal endpoints (Lambda callbacks) ─────────────────────────────────────

// internalTransition advances a job's status. Called by Lambda functions after
// they finish processing (e.g. IVO → RECOGNIZED, Classification → CLASSIFIED).
func (h *Handler) internalTransition(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req struct {
		ToStatus job.Status     `json:"to_status"`
		WorkerID string         `json:"worker_id"`
		Note     string         `json:"note"`
		NewState job.StateData  `json:"new_state,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ToStatus == "" {
		writeError(w, http.StatusBadRequest, "to_status is required")
		return
	}

	j, err := h.svc.Transition(r.Context(), job.TransitionRequest{
		JobID:    id,
		ToStatus: req.ToStatus,
		WorkerID: req.WorkerID,
		Note:     req.Note,
		NewState: req.NewState,
	})
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "no rows") {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		if strings.Contains(err.Error(), "illegal transition") {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		h.log.Error("internal transition", "id", id, "to", req.ToStatus, "error", err)
		writeError(w, http.StatusInternalServerError, "transition failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": string(j.Status)})
}

// internalWriteFields stores field values written by a Lambda (e.g. Recognition,
// Classification). Each element maps directly to a job.Field row.
func (h *Handler) internalWriteFields(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var fields []struct {
		PageID      *int64          `json:"page_id,omitempty"`
		FieldName   string          `json:"field_name"`
		FieldOrder  int             `json:"field_order"`
		IsJobLevel  bool            `json:"is_job_level"`
		FinalValue  *string         `json:"final_value,omitempty"`
		FieldState  *string         `json:"field_state,omitempty"`
		ValueSource *string         `json:"value_source,omitempty"`
		Confidence  *int            `json:"confidence,omitempty"`
		Recognition json.RawMessage `json:"recognition,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	for _, f := range fields {
		if f.FieldName == "" {
			writeError(w, http.StatusBadRequest, "field_name is required for every field")
			return
		}
		if err := h.svc.CreateField(r.Context(), &job.Field{
			JobID:       id,
			PageID:      f.PageID,
			FieldName:   f.FieldName,
			FieldOrder:  f.FieldOrder,
			IsJobLevel:  f.IsJobLevel,
			FinalValue:  f.FinalValue,
			FieldState:  f.FieldState,
			ValueSource: f.ValueSource,
			Confidence:  f.Confidence,
			Recognition: f.Recognition,
		}); err != nil {
			h.log.Error("write field", "id", id, "field", f.FieldName, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to write field: "+f.FieldName)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]int{"written": len(fields)})
}
