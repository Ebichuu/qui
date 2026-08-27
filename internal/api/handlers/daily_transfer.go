// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/autobrr/qui/internal/services/dailytransfer"
)

type DailyTransferHandler struct {
	service *dailytransfer.Service
}

func NewDailyTransferHandler(service *dailytransfer.Service) *DailyTransferHandler {
	return &DailyTransferHandler{service: service}
}

func (h *DailyTransferHandler) GetCurrent(w http.ResponseWriter, r *http.Request) {
	instanceID, err := strconv.Atoi(chi.URLParam(r, "instanceID"))
	if err != nil || instanceID <= 0 {
		RespondError(w, http.StatusBadRequest, "Invalid instance ID")
		return
	}
	if h == nil || h.service == nil {
		RespondError(w, http.StatusServiceUnavailable, "Daily transfer statistics are unavailable")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	stats, err := h.service.Current(ctx, instanceID)
	if err != nil {
		log.Debug().Err(err).Int("instanceID", instanceID).Msg("Failed to load daily transfer statistics")
		RespondError(w, http.StatusServiceUnavailable, "Failed to load daily transfer statistics")
		return
	}
	RespondJSON(w, http.StatusOK, stats)
}
