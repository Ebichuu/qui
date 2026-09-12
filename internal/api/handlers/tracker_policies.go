// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/qui/internal/models"
)

func (h *InstancesHandler) TrackerPolicies(w http.ResponseWriter, r *http.Request) {
	instanceID, err := strconv.Atoi(chi.URLParam(r, "instanceID"))
	if err != nil || instanceID <= 0 {
		RespondError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}
	if r.Method == http.MethodPut {
		var policy models.ReannounceTrackerPolicy
		if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
			RespondError(w, http.StatusBadRequest, "Invalid tracker policy")
			return
		}
		if err := h.reannounceStore.SaveTrackerPolicy(r.Context(), instanceID, policy); err != nil {
			if errors.Is(err, models.ErrTrackerPolicyInvalid) {
				RespondError(w, http.StatusBadRequest, "Invalid tracker policy")
			} else {
				RespondError(w, http.StatusInternalServerError, "Failed to save tracker policy")
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	policies, err := h.reannounceStore.TrackerPolicies(r.Context(), instanceID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "Failed to load tracker policies")
		return
	}
	RespondJSON(w, http.StatusOK, policies)
}

func (h *InstancesHandler) TrackerDeletionCheck(w http.ResponseWriter, r *http.Request) {
	instanceID, err := strconv.Atoi(chi.URLParam(r, "instanceID"))
	if err != nil || instanceID <= 0 {
		RespondError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}
	addedOn, err := strconv.ParseInt(r.URL.Query().Get("addedOn"), 10, 64)
	if err != nil || addedOn <= 0 || r.URL.Query().Get("hash") == "" {
		RespondError(w, http.StatusBadRequest, "Task generation required")
		return
	}
	result, err := h.reannounceSvc.CheckTrackerDeletion(r.Context(), instanceID, r.URL.Query().Get("hash"), addedOn, true)
	if err != nil {
		RespondError(w, http.StatusServiceUnavailable, "Tracker protection evidence unavailable")
		return
	}
	RespondJSON(w, http.StatusOK, result)
}
