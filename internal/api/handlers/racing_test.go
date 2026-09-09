// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/autobrr/qui/internal/models"
	"github.com/autobrr/qui/internal/services/racing"
	"github.com/autobrr/qui/internal/testutil/testdb"
)

func TestRacingHandlersRedactAndPreserveConfiguration(t *testing.T) {
	db := testdb.NewMigratedSQLite(t, "racing-handlers")
	store, err := models.NewRacingStore(db, bytes.Repeat([]byte{9}, 32))
	require.NoError(t, err)
	service := racing.NewService(store)
	require.NoError(t, service.Start(t.Context()))
	t.Cleanup(service.Stop)
	router := chi.NewRouter()
	router.Route("/racing", NewRacingHandler(store, service).Register)
	request := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(body)))
		require.Equal(t, status, response.Code, response.Body.String())
		return response
	}
	response := request(http.MethodPost, "/racing/sites", `{"name":"Synthetic site","baseUrl":"https://example.invalid","enabled":true,"requestIntervalSeconds":5,"credential":"synthetic-private-value"}`, http.StatusCreated)
	var saved map[string]int
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &saved))
	require.Positive(t, saved["id"])
	request(http.MethodPost, "/racing/sources", `{"name":"Synthetic feed","siteId":1,"kind":"rss","enabled":true,"intervalSeconds":15,"url":"https://example.invalid/hidden?passkey=synthetic-private-key"}`, http.StatusCreated)
	config := request(http.MethodGet, "/racing/configuration", "", http.StatusOK)
	require.Contains(t, config.Body.String(), `"hasCredential":true`)
	require.NotContains(t, config.Body.String(), "synthetic-private")
	require.NotContains(t, config.Body.String(), "hidden")
	request(http.MethodDelete, "/racing/sites/1", "", http.StatusConflict)
	request(http.MethodPut, "/racing/sources/1", `{"name":"Renamed feed","siteId":1,"kind":"rss","enabled":false,"intervalSeconds":15}`, http.StatusOK)
	request(http.MethodPut, "/racing/sites/0", `{}`, http.StatusBadRequest)
	request(http.MethodPost, "/racing/groups", `{"name":"group","unknownField":true}`, http.StatusBadRequest)
	request(http.MethodPost, "/racing/groups", `{"name":"group"} {}`, http.StatusBadRequest)
	malformed := request(http.MethodPost, "/racing/sites", `{"credential":"synthetic-private-value`, http.StatusBadRequest)
	require.NotContains(t, malformed.Body.String(), "synthetic-private-value")
	status := request(http.MethodGet, "/racing/status", "", http.StatusOK)
	require.Contains(t, status.Body.String(), `"mode":"observe_only"`)
	request(http.MethodDelete, "/racing/sources/1", "", http.StatusNoContent)
	request(http.MethodDelete, "/racing/sites/1", "", http.StatusNoContent)
}
