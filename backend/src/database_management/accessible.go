package databasemanagement

import (
	"fmt"
	"sort"

	fx "backend/access_control/effect"

	"github.com/gin-gonic/gin"
)

type accessibleDatabase struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Permission string `json:"permission"`
}

type policyRow struct {
	ID     string `gorm:"column:id"`
	Name   string `gorm:"column:name"`
	Effect string `gorm:"column:effect"`
}

func summarizeAccessible(rows []policyRow) []accessibleDatabase {
	type entry struct {
		name    string
		effects []string
	}
	byID := map[string]*entry{}
	for _, r := range rows {
		e := byID[r.ID]
		if e == nil {
			e = &entry{name: r.Name}
			byID[r.ID] = e
		}
		e.effects = append(e.effects, r.Effect)
	}
	out := []accessibleDatabase{}
	for id, e := range byID {
		switch {
		case fx.Grants(e.effects, true):
			out = append(out, accessibleDatabase{ID: id, Name: e.name, Permission: fx.ViewEdit})
		case fx.Grants(e.effects, false):
			out = append(out, accessibleDatabase{ID: id, Name: e.name, Permission: fx.ViewOnly})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (h *Handler) accessibleDatabases(c *gin.Context) {
	principal := fmt.Sprint(c.MustGet("principal_id"))
	var rows []policyRow
	err := h.platform.WithContext(c.Request.Context()).
		Table("databases d").
		Select("d.id::text AS id, d.name AS name, gp.effect AS effect").
		Joins("JOIN group_policies gp ON gp.database_id = d.id").
		Joins("JOIN user_groups ug ON ug.group_id = gp.group_id").
		Where("ug.user_id = ?", principal).
		Scan(&rows).Error
	if err != nil {
		fail(c, 500, "DATABASE_LIST_FAILED", "Could not list accessible databases")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"databases": summarizeAccessible(rows)})
}
