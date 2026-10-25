// Package effect defines the access levels stored in group_policies.effect and how
// they combine. It depends only on the standard library so every part of the backend
// (control panel, database management, authorization) shares one definition.
package effect

const (
	// ViewOnly lets a group see schema, data and migration history.
	ViewOnly = "ViewOnly"
	// ViewEdit includes everything in ViewOnly and also allows creating, changing and
	// running migrations.
	ViewEdit = "ViewEdit"
	// Blocked denies all access and overrides every other group the user belongs to.
	Blocked = "Blocked"
)

// IsValid reports whether e is one of the values the database schema allows
// (CHECK (effect IN ('ViewEdit','ViewOnly','Blocked'))). Matching is exact.
func IsValid(e string) bool {
	switch e {
	case ViewOnly, ViewEdit, Blocked:
		return true
	}
	return false
}

// Grants resolves the effects a user receives through all of their groups for one
// database. Blocked in any group denies; otherwise ViewEdit allows view and edit, and
// ViewOnly allows view only. Unknown values grant nothing, and no effects means no access.
func Grants(effects []string, edit bool) bool {
	allowed := false
	for _, e := range effects {
		switch e {
		case Blocked:
			return false
		case ViewEdit:
			allowed = true
		case ViewOnly:
			if !edit {
				allowed = true
			}
		}
	}
	return allowed
}
