package main

import (
	"net/http"
	"strings"

	"approvalflow/internal/platform/httpx"
)

const demoRoleHeader = "X-Demo-Role"

func requireDemoRole(next http.HandlerFunc, allowedRoles ...string) http.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowedRoles))
	for _, role := range allowedRoles {
		allowed[strings.ToLower(strings.TrimSpace(role))] = struct{}{}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		role := strings.ToLower(strings.TrimSpace(r.Header.Get(demoRoleHeader)))
		if _, ok := allowed[role]; !ok {
			httpx.WriteError(w, r, http.StatusForbidden, "forbidden: missing required demo role")
			return
		}

		next(w, r)
	}
}
