// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package handlers

import (
	"net/http"

	"github.com/autobrr/qui/internal/models"
)

func (h *RacingHandler) receptionPolicies(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		RespondError(w, http.StatusServiceUnavailable, "Reception configuration unavailable")
		return
	}
	policies, err := h.store.InstancePolicies(r.Context())
	if err != nil {
		RespondError(w, http.StatusServiceUnavailable, "Reception configuration unavailable")
		return
	}
	RespondJSON(w, http.StatusOK, policies)
}

func (h *RacingHandler) saveReceptionPolicy(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		RespondError(w, http.StatusServiceUnavailable, "Reception configuration unavailable")
		return
	}
	id, ok := racingID(w, r)
	if !ok {
		return
	}
	var policy models.RacingInstancePolicy
	if err := decodeRacing(w, r, &policy); err != nil {
		racingError(w, err)
		return
	}
	if policy.InstanceID != id {
		racingError(w, models.ErrRacingInvalid)
		return
	}
	if err := h.store.SaveInstancePolicy(r.Context(), policy); err != nil {
		racingError(w, err)
		return
	}
	if h.service != nil {
		h.service.ConfigurationChanged()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *RacingHandler) addIntents(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		RespondError(w, http.StatusServiceUnavailable, "Reception history unavailable")
		return
	}
	items, err := h.store.AddIntents(r.Context(), r.URL.Query().Get("after"), 100)
	if err != nil {
		RespondError(w, http.StatusServiceUnavailable, "Reception history unavailable")
		return
	}
	next := ""
	if len(items) == 100 {
		next = items[len(items)-1].CandidateKey
	}
	RespondJSON(w, http.StatusOK, struct {
		Items      []models.RacingAddIntent `json:"items"`
		NextCursor string                   `json:"nextCursor,omitempty"`
	}{items, next})
}
