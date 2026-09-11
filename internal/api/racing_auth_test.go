// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRacingRoutesRequireAuthentication(t *testing.T) {
	server := NewServer(newTestDependencies(t))
	router, err := server.Handler()
	require.NoError(t, err)
	routes := make([]struct{ method, path string }, 0, 24)
	routes = append(routes, struct{ method, path string }{http.MethodGet, "/api/racing/configuration"}, struct{ method, path string }{http.MethodGet, "/api/racing/status"})
	routes = append(routes, struct{ method, path string }{http.MethodGet, "/api/racing/observations"}, struct{ method, path string }{http.MethodGet, "/api/racing/discoveries"}, struct{ method, path string }{http.MethodGet, "/api/racing/candidates"})
	routes = append(routes, struct{ method, path string }{http.MethodPut, "/api/racing/rules/order"}, struct{ method, path string }{http.MethodGet, "/api/racing/reception-policies"}, struct{ method, path string }{http.MethodPut, "/api/racing/reception-policies/1"}, struct{ method, path string }{http.MethodGet, "/api/racing/add-intents"})
	for _, resource := range []string{"sites", "sources", "groups", "rules", "storage-pools", "path-mappings"} {
		routes = append(routes, struct{ method, path string }{http.MethodPost, "/api/racing/" + resource}, struct{ method, path string }{http.MethodPut, "/api/racing/" + resource + "/1"}, struct{ method, path string }{http.MethodDelete, "/api/racing/" + resource + "/1"})
	}
	for _, route := range routes {
		request := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusForbidden, response.Code, route.method+" "+route.path)
	}
}
