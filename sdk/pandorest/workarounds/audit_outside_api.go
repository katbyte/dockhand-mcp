package workarounds

import (
	"fmt"
	"net/http"

	pandorest "github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// auditOutsideAPI names the two audit calls that sit outside /api, which would otherwise be named as the two inside it are.
type auditOutsideAPI struct{}

func (auditOutsideAPI) Name() string    { return Service + "-audit-outside-api" }
func (auditOutsideAPI) Service() string { return Service }

func (auditOutsideAPI) Bug() string {
	return "the audit log is listed at GET /audit and its users at GET /audit/users as well as at GET /api/audit and GET /api/audit/users, the two outside /api taking their filters spelled another way (entity_type for entityType); methods are named from a path with /api taken off, so each pair would make one name"
}

// outsideAPI is each call outside /api that has a namesake inside it, and the method it is given.
var outsideAPI = map[string]string{
	"/audit":       "GetAuditOutsideApi",
	"/audit/users": "GetAuditUsersOutsideApi",
}

func (auditOutsideAPI) Apply(spec *openapi.Spec) error {
	for path, name := range outsideAPI {
		op, err := pandorest.Operation(spec, http.MethodGet, path)
		if err != nil {
			return err
		}
		if _, err := pandorest.Operation(spec, http.MethodGet, "/api"+path); err != nil {
			return fmt.Errorf("GET %s no longer has a namesake inside /api: %w", path, err)
		}
		if op.Name != "" {
			return fmt.Errorf("GET %s already has a name of its own, %s", path, op.Name)
		}
		op.Name = name
	}

	return nil
}
