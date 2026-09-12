// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/qui/internal/models"
)

func (h *RacingHandler) reclaimConfiguration(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		RespondError(w, http.StatusServiceUnavailable, "Reclaim configuration unavailable")
		return
	}
	config, err := h.store.ReclaimConfiguration(r.Context())
	if err != nil {
		RespondError(w, http.StatusServiceUnavailable, "Reclaim configuration unavailable")
		return
	}
	RespondJSON(w, http.StatusOK, config)
}

func (h *RacingHandler) writeReclaimSetting(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		RespondError(w, http.StatusServiceUnavailable, "Reclaim configuration unavailable")
		return
	}
	id, ok := racingID(w, r)
	if !ok {
		return
	}
	scope := chi.URLParam(r, "scope")
	var err error
	if r.Method == http.MethodDelete {
		err = h.store.DeleteReclaimSetting(r.Context(), scope, id)
	} else {
		var policy models.RacingReclaimPolicy
		if err = decodeRacing(w, r, &policy); err == nil {
			err = h.store.SaveReclaimSetting(r.Context(), scope, id, policy)
		}
	}
	if err != nil {
		racingError(w, err)
		return
	}
	if h.service != nil {
		h.service.ConfigurationChanged()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *RacingHandler) reclaimAssessments(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.ReclaimAssessments(r.Context(), 100)
	if err != nil {
		RespondError(w, http.StatusServiceUnavailable, "Reclaim assessments unavailable")
		return
	}
	RespondJSON(w, http.StatusOK, items)
}
