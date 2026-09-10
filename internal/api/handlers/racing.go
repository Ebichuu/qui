// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/services/racing"
)

type RacingHandler struct {
	store   *models.RacingStore
	service *racing.Service
}

func NewRacingHandler(store *models.RacingStore, service *racing.Service) *RacingHandler {
	return &RacingHandler{store: store, service: service}
}

// Register must be called under the existing authenticated API router.
func (h *RacingHandler) Register(r chi.Router) {
	r.Get("/configuration", h.configuration)
	r.Get("/status", h.status)
	r.Get("/observations", h.observations)
	for _, resource := range []string{"sites", "sources", "groups", "rules", "storage-pools", "path-mappings"} {
		r.Route("/"+resource, func(r chi.Router) {
			r.Post("/", func(w http.ResponseWriter, r *http.Request) { h.save(w, r, resource, false) })
			r.Put("/{id}", func(w http.ResponseWriter, r *http.Request) { h.save(w, r, resource, true) })
			r.Delete("/{id}", func(w http.ResponseWriter, r *http.Request) { h.delete(w, r, resource) })
		})
	}
}

func (h *RacingHandler) configuration(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		RespondError(w, http.StatusServiceUnavailable, "Racing configuration is unavailable")
		return
	}
	config, err := h.store.Configuration(r.Context())
	if err != nil {
		RespondError(w, http.StatusServiceUnavailable, "Racing configuration is unavailable")
		return
	}
	RespondJSON(w, http.StatusOK, config)
}

func (h *RacingHandler) status(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		RespondError(w, http.StatusServiceUnavailable, "Racing service is unavailable")
		return
	}
	RespondJSON(w, http.StatusOK, h.service.Status())
}

func racingID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		RespondError(w, http.StatusBadRequest, "Invalid configuration ID")
		return 0, false
	}
	return id, true
}

func decodeRacing(w http.ResponseWriter, r *http.Request, input any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil {
		return models.ErrRacingInvalid
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return models.ErrRacingInvalid
	}
	return nil
}

func (h *RacingHandler) save(w http.ResponseWriter, r *http.Request, resource string, update bool) {
	if h.store == nil {
		RespondError(w, http.StatusServiceUnavailable, "Racing configuration is unavailable")
		return
	}
	var id int
	if update {
		var ok bool
		id, ok = racingID(w, r)
		if !ok {
			return
		}
	}
	var err error
	switch resource {
	case "storage-pools":
		var input models.RacingStoragePoolInput
		if err = decodeRacing(w, r, &input); err == nil {
			id, err = h.store.SaveStoragePool(r.Context(), id, input)
		}
	case "path-mappings":
		var input models.RacingPathMappingInput
		if err = decodeRacing(w, r, &input); err == nil {
			id, err = h.store.SavePathMapping(r.Context(), id, input)
		}
	case "sites":
		var input models.RacingSiteInput
		if err = decodeRacing(w, r, &input); err == nil {
			id, err = h.store.SaveSite(r.Context(), id, input)
		}
	case "sources":
		var input models.RacingSourceInput
		if err = decodeRacing(w, r, &input); err == nil {
			id, err = h.store.SaveSource(r.Context(), id, input)
		}
	case "groups":
		var input models.RacingGroupInput
		if err = decodeRacing(w, r, &input); err == nil {
			id, err = h.store.SaveGroup(r.Context(), id, input)
		}
	case "rules":
		var input models.RacingRuleInput
		if err = decodeRacing(w, r, &input); err == nil {
			id, err = h.store.SaveRule(r.Context(), id, input)
		}
	}
	if err != nil {
		racingError(w, err)
		return
	}
	if h.service != nil {
		h.service.ConfigurationChanged()
	}
	status := http.StatusCreated
	if update {
		status = http.StatusOK
	}
	RespondJSON(w, status, map[string]int{"id": id})
}

func (h *RacingHandler) delete(w http.ResponseWriter, r *http.Request, resource string) {
	if h.store == nil {
		RespondError(w, http.StatusServiceUnavailable, "Racing configuration is unavailable")
		return
	}
	id, ok := racingID(w, r)
	if !ok {
		return
	}
	if err := h.store.Delete(r.Context(), resource, id); err != nil {
		racingError(w, err)
		return
	}
	if h.service != nil {
		h.service.ConfigurationChanged()
	}
	w.WriteHeader(http.StatusNoContent)
}

func racingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, models.ErrRacingInvalid):
		RespondError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, sql.ErrNoRows):
		RespondError(w, http.StatusNotFound, "Configuration not found")
	case errors.Is(err, models.ErrRacingReferenced):
		RespondError(w, http.StatusConflict, "Remove configuration references before deleting")
	default:
		// SQL/parse errors can contain source URLs or credential values. Never echo
		// or log arbitrary driver error strings for these configuration writes.
		RespondError(w, http.StatusInternalServerError, "Unable to save racing configuration")
	}
}

func (h *RacingHandler) observations(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		RespondError(w, http.StatusServiceUnavailable, "Racing service is unavailable")
		return
	}
	RespondJSON(w, http.StatusOK, h.service.Observations())
}
