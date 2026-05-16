package auth

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/api/problem"
	"github.com/gokaybaz/go-cloud-hypervisor-service/pkg/logging"
)

// ---------------------------------------------------------------------------
// Roles
// ---------------------------------------------------------------------------

// Role represents a permission level in the RBAC hierarchy.
type Role int

const (
	RoleViewer Role = iota
	RoleOperator
	RoleAdmin
)

// String returns the human-readable role name.
func (r Role) String() string {
	switch r {
	case RoleViewer:
		return "viewer"
	case RoleOperator:
		return "operator"
	case RoleAdmin:
		return "admin"
	default:
		return "unknown"
	}
}

// ParseRole converts a string role name to a Role value.
func ParseRole(s string) (Role, bool) {
	switch strings.ToLower(s) {
	case "viewer":
		return RoleViewer, true
	case "operator":
		return RoleOperator, true
	case "admin":
		return RoleAdmin, true
	default:
		return RoleViewer, false
	}
}

// ---------------------------------------------------------------------------
// Permission table
// ---------------------------------------------------------------------------

// Rule maps an HTTP method and path pattern to a minimum required role.
type Rule struct {
	Method   string
	Segments []string // path segments relative to /api/v1; "*" = any segment
	MinRole  Role
}

// DefaultPermissionTable is the built-in RBAC matrix for the Cloud Hypervisor
// API.  Higher roles implicitly inherit lower role permissions.
//
// viewer   — read-only (list, get, console)
// operator — viewer + lifecycle + resource changes
// admin    — operator + create/delete + disk/network management
func DefaultPermissionTable() []Rule {
	return []Rule{
		// viewer
		{Method: http.MethodGet, Segments: []string{"vms"}, MinRole: RoleViewer},
		{Method: http.MethodGet, Segments: []string{"vms", "*"}, MinRole: RoleViewer},
		{Method: http.MethodGet, Segments: []string{"vms", "*", "console"}, MinRole: RoleViewer},

		// operator
		{Method: http.MethodPost, Segments: []string{"vms", "*", "boot"}, MinRole: RoleOperator},
		{Method: http.MethodPost, Segments: []string{"vms", "*", "pause"}, MinRole: RoleOperator},
		{Method: http.MethodPost, Segments: []string{"vms", "*", "resume"}, MinRole: RoleOperator},
		{Method: http.MethodPost, Segments: []string{"vms", "*", "shutdown"}, MinRole: RoleOperator},
		{Method: http.MethodPost, Segments: []string{"vms", "*", "reboot"}, MinRole: RoleOperator},
		{Method: http.MethodPatch, Segments: []string{"vms", "*", "cpu"}, MinRole: RoleOperator},
		{Method: http.MethodPatch, Segments: []string{"vms", "*", "memory"}, MinRole: RoleOperator},

		// admin
		{Method: http.MethodPost, Segments: []string{"vms"}, MinRole: RoleAdmin},
		{Method: http.MethodDelete, Segments: []string{"vms", "*"}, MinRole: RoleAdmin},
		{Method: http.MethodPost, Segments: []string{"vms", "*", "disks"}, MinRole: RoleAdmin},
		{Method: http.MethodDelete, Segments: []string{"vms", "*", "disks", "*"}, MinRole: RoleAdmin},
		{Method: http.MethodPost, Segments: []string{"vms", "*", "disks", "*", "snapshot"}, MinRole: RoleAdmin},
		{Method: http.MethodPost, Segments: []string{"vms", "*", "interfaces"}, MinRole: RoleAdmin},
		{Method: http.MethodDelete, Segments: []string{"vms", "*", "interfaces", "*"}, MinRole: RoleAdmin},
		{Method: http.MethodPatch, Segments: []string{"vms", "*", "interfaces", "*"}, MinRole: RoleAdmin},
	}
}

// ---------------------------------------------------------------------------
// RBAC middleware
// ---------------------------------------------------------------------------

// RBACMiddleware returns an HTTP middleware that enforces role-based access
// control using the provided permission table.
//
// The middleware extracts the roles from the request context (injected by the
// JWT auth middleware).  If no roles are present the request is denied with
// 403 Forbidden.  If the request path does not match any rule in the table the
// request is allowed (permissive default for unlisted endpoints).
//
// On denial a structured log entry is emitted with the subject, roles,
// required role, method, and path.  The response is an RFC 7807 Problem
// Details body with status 403.
func RBACMiddleware(table []Rule, logger logging.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract subject for logging.
			sub, hasSub := SubjectFromContext(r.Context())

			// Extract roles from context.
			roleStrs, hasRoles := RolesFromContext(r.Context())
			if !hasRoles || len(roleStrs) == 0 {
				log := logger.WithContext(r.Context())
				log.Warn("authorization denied",
					"reason", "no roles in context",
					"subject", sub,
					"has_subject", hasSub,
					"method", r.Method,
					"path", r.URL.Path,
				)
				problem.Forbidden(r.URL.Path, "insufficient permissions: no roles assigned").Write(w)
				return
			}

			// Compute the highest role level the caller possesses.
			userLevel := RoleViewer // default lowest
			var userRoles []string
			for _, rs := range roleStrs {
				role, ok := ParseRole(rs)
				if !ok {
					continue
				}
				userRoles = append(userRoles, role.String())
				if role > userLevel {
					userLevel = role
				}
			}

			// Match request against permission table.
			required := matchRule(table, r.Method, r.URL.Path)
			if required == nil {
				// No rule matched — allow.
				next.ServeHTTP(w, r)
				return
			}

			if userLevel < required.MinRole {
				log := logger.WithContext(r.Context())
				log.Warn("authorization denied",
					"reason", "role too low",
					"subject", sub,
					"user_roles", userRoles,
					"required_role", required.MinRole.String(),
					"method", r.Method,
					"path", r.URL.Path,
				)
				problem.Forbidden(r.URL.Path,
					fmt.Sprintf("insufficient permissions: requires %s role", required.MinRole.String())).Write(w)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// matchRule finds the first rule that matches the given method and path.
// The path is matched against rule segments relative to /api/v1.
// Returns nil when no rule matches.
func matchRule(table []Rule, method, path string) *Rule {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	// Strip the "api/v1" prefix if present.
	if len(segments) >= 2 && segments[0] == "api" && segments[1] == "v1" {
		segments = segments[2:]
	}

	for i := range table {
		rule := &table[i]
		if !strings.EqualFold(rule.Method, method) {
			continue
		}
		if len(rule.Segments) != len(segments) {
			continue
		}
		match := true
		for j, seg := range rule.Segments {
			if seg != "*" && seg != segments[j] {
				match = false
				break
			}
		}
		if match {
			return rule
		}
	}
	return nil
}
