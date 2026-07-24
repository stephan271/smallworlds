package consoleauth

import (
	"errors"
	"strings"
)

// ConsoleRole is an Operator's in-cluster authority level (ADR 0011). The empty
// role is the default-deny state for a user without any Console Role.
type ConsoleRole string

const (
	// RoleNone is assigned to any authenticated user without a Console Role. It
	// grants no permissions — access is denied by default.
	RoleNone ConsoleRole = ""
	// RoleObserver grants read-only observation of cluster state.
	RoleObserver ConsoleRole = "observer"
	// RoleOperator additionally grants routine work: GitOps proposals and bounded
	// Runtime Actions.
	RoleOperator ConsoleRole = "operator"
	// RoleOwner additionally grants sensitive in-cluster administration such as
	// operator-access management.
	RoleOwner ConsoleRole = "owner"
)

// Permission is a server-side capability an endpoint or action requires. Roles
// are ordered: an Operator can do everything an Observer can, and an Owner can
// do everything an Operator can.
type Permission string

const (
	// PermissionObserve reads assessments, overviews, and evidence.
	PermissionObserve Permission = "observe"
	// PermissionPropose creates GitOps proposals and bounded Runtime Actions.
	PermissionPropose Permission = "propose"
	// PermissionAdminister performs sensitive in-cluster administration.
	PermissionAdminister Permission = "administer"
)

// rolePermissions is the closed set of permissions each role holds. It is the
// single source of truth for authorization; hiding a control in the UI is never
// a substitute for a server-side check against this table.
var rolePermissions = map[ConsoleRole][]Permission{
	RoleObserver: {PermissionObserve},
	RoleOperator: {PermissionObserve, PermissionPropose},
	RoleOwner:    {PermissionObserve, PermissionPropose, PermissionAdminister},
}

// roleRank orders roles so the highest granted role wins when several are
// present in a token.
var roleRank = map[ConsoleRole]int{
	RoleObserver: 1,
	RoleOperator: 2,
	RoleOwner:    3,
}

var (
	// ErrNoConsoleRole is returned when an authenticated user holds no Console
	// Role — the default-deny outcome.
	ErrNoConsoleRole = errors.New("no console role assigned")
	// ErrForbidden is returned when a role lacks the required permission.
	ErrForbidden = errors.New("console role lacks the required permission")
)

// Can reports whether the role holds the permission.
func (role ConsoleRole) Can(permission Permission) bool {
	for _, held := range rolePermissions[role] {
		if held == permission {
			return true
		}
	}
	return false
}

// Authorize enforces a required permission for a role, defaulting to denial when
// no Console Role is present. It is the server-side gate every mutating and
// sensitive endpoint must call.
func Authorize(role ConsoleRole, permission Permission) error {
	if role == RoleNone {
		return ErrNoConsoleRole
	}
	if !role.Can(permission) {
		return ErrForbidden
	}
	return nil
}

// recognizeRole maps a single Keycloak role name to a Console Role, ignoring
// case and surrounding space. Unrecognized names yield RoleNone.
func recognizeRole(name string) ConsoleRole {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case string(RoleObserver):
		return RoleObserver
	case string(RoleOperator):
		return RoleOperator
	case string(RoleOwner):
		return RoleOwner
	default:
		return RoleNone
	}
}

// HighestRole selects the strongest recognized Console Role from a set of
// Keycloak role names, or RoleNone when none are recognized (default deny).
func HighestRole(names []string) ConsoleRole {
	best := RoleNone
	for _, name := range names {
		role := recognizeRole(name)
		if roleRank[role] > roleRank[best] {
			best = role
		}
	}
	return best
}
