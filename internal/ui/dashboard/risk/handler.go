package riskui

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/risk"
)

type Handler struct {
	controller *risk.Controller
}

func NewHandler(controller *risk.Controller) *Handler {
	return &Handler{controller: controller}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /risk/mode", h.setMode)
	mux.HandleFunc("POST /risk/reset", h.reset)
}

func (h *Handler) setMode(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "parse risk mode", http.StatusBadRequest)
		return
	}
	mode, err := risk.ParseMode(r.FormValue("mode"))
	if err != nil {
		http.Error(w, "invalid risk mode", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := h.controller.SetMode(ctx, mode); err != nil {
		http.Error(w, "save risk mode", http.StatusInternalServerError)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) reset(w http.ResponseWriter, r *http.Request) {
	h.controller.Reset()
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
