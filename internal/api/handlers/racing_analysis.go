// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package handlers

import (
	"net/http"

	"github.com/autobrr/qui/internal/models"
)

func (h *RacingHandler) analysisSettings(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		RespondError(w, http.StatusServiceUnavailable, "Racing analysis is unavailable")
		return
	}
	items, err := h.store.AnalysisSettings(r.Context())
	if err != nil {
		racingError(w, err)
		return
	}
	RespondJSON(w, http.StatusOK, items)
}

func (h *RacingHandler) saveAnalysisSetting(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		RespondError(w, http.StatusServiceUnavailable, "Racing analysis is unavailable")
		return
	}
	id, ok := racingID(w, r)
	if !ok {
		return
	}
	var input models.RacingAnalysisSetting
	if err := decodeRacing(w, r, &input); err != nil {
		racingError(w, err)
		return
	}
	input.InstanceID = id
	if err := h.store.SaveAnalysisSetting(r.Context(), input); err != nil {
		racingError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *RacingHandler) analysisHistory(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		RespondError(w, http.StatusServiceUnavailable, "Racing analysis is unavailable")
		return
	}
	key := r.URL.Query().Get("candidateKey")
	if key == "" || len(key) > 512 {
		RespondError(w, http.StatusBadRequest, "A candidate key is required")
		return
	}
	item, err := h.store.AnalysisHistory(r.Context(), key)
	if err != nil {
		racingError(w, err)
		return
	}
	if item == nil {
		RespondError(w, http.StatusNotFound, "No analysis sample has been recorded")
		return
	}
	RespondJSON(w, http.StatusOK, item)
}
