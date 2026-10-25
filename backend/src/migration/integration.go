package migration

import (
	fx "backend/access_control/effect"
	"github.com/google/uuid"
)

// PermissionFromEffective converts Access Control's final permission into a Migration permission.
// Unknown values deny access.
func PermissionFromEffective(permission string) Permission {
	switch permission {
	case fx.ViewOnly:
		return View
	case fx.ViewEdit:
		return Edit
	default:
		return NoAccess
	}
}

// Normalize database IDs so permissions, connections, and history all refer to the same database.
func canonicalDatabaseID(id string) string {
	registered, err := uuid.Parse(id)
	if err != nil {
		// Allow non-UUID IDs for tests.
		// Only resolve registered database IDs.
		return id
	}
	return registered.String()
}
