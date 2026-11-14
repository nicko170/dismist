package httpapi

import (
	"log"
	"net/http"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
)

// finaliseAfter is how long M2 must have run before the migration can be
// finalised (design §4.1 rule 5).
const finaliseAfter = 30 * 24 * time.Hour

// migrationView is the migration state for /api/system and the typed
// gate: the migration runner's when wired, else the daemon's, else
// derived from meta.schema.
func (s *Server) migrationView() api.MigrationView {
	if s.config.Migration != nil {
		return s.config.Migration.Status().MigrationView
	}
	if s.system.Migration != nil {
		return s.system.Migration()
	}
	return api.MigrationView{State: api.MigrationStateNotRun}
}

func (s *Server) migrationStatus() api.MigrationStatus {
	if s.config.Migration != nil {
		status := s.config.Migration.Status()
		if status.Paths == nil {
			status.Paths = []string{}
		}
		return status
	}
	status := api.MigrationStatus{MigrationView: s.migrationView(), Paths: []string{}, Schema: 1, Compat: 1}
	if schemas, ok := s.store.(schemaStore); ok {
		if schema, err := schemas.Schema(); err == nil {
			status.Schema, status.Compat = schema.Schema, schema.Compat
		}
	}
	if meta, ok := s.store.(interface {
		MetaJSON(key string, out any) (bool, error)
	}); ok {
		var report map[string]any
		if found, err := meta.MetaJSON(model.MetaMigrationReport, &report); err == nil && found {
			redacted, _ := redactValue(report).(map[string]any)
			status.Report = redacted
		}
		var state struct {
			At    time.Time `json:"at"`
			Paths []string  `json:"paths"`
			Copy  string    `json:"copy"`
		}
		if found, err := meta.MetaJSON(model.MetaMigrationState, &state); err == nil && found {
			status.At = timePtr(state.At)
			if state.Paths != nil {
				status.Paths = state.Paths
			}
			status.Copy = state.Copy
		}
	}
	if status.State == api.MigrationStateCommitted && status.At != nil {
		after := status.At.Add(finaliseAfter)
		status.FinaliseAfter = &after
		status.CanFinalise = !s.now().Before(after) && status.Compat < 2
	}
	status.Finalised = status.Compat >= 2 && status.State == api.MigrationStateCommitted
	return status
}

// getMigration serves GET /api/system/migration.
func (s *Server) getMigration(writer http.ResponseWriter, _ *http.Request, _ params) {
	respond(writer, http.StatusOK, s.migrationStatus())
}

// retryMigration serves POST /api/system/migration/retry[?dry_run=1]: from
// legacy mode, run the migration again (a dry run first, then commit, no
// restart; design §4.7 step 7).
func (s *Server) retryMigration(writer http.ResponseWriter, request *http.Request, _ params) {
	if !requireAdmin(writer, request) {
		return
	}
	if s.config.Migration == nil {
		writeAPIError(writer, newError(http.StatusNotImplemented, api.ErrorCodeUnavailable, "this build has no schema-2 migration to run"))
		return
	}
	if s.migrationView().State == api.MigrationStateCommitted {
		writeAPIError(writer, conflict("the migration has already committed"))
		return
	}
	run := dryRun(request)
	status, err := s.config.Migration.Retry(run)
	if err != nil {
		log.Printf("migration retry (dry run %v): %v", run, err)
		writeAPIError(writer, internalError("the migration could not run; see the controller log"))
		return
	}
	if !run {
		s.recordAction(request, "", "Retried the schema-2 migration: "+string(status.State))
		log.Printf("audit: schema-2 migration retried by %s: %s", principal(request), status.State)
	}
	respond(writer, http.StatusOK, api.MigrationRun{DryRun: run, Status: status})
}

// finaliseMigration serves POST /api/system/migration/finalise[?dry_run=1]
// (phase 4): clears the legacy fields and sets compat 2, only after the
// migration has run 30 days.
func (s *Server) finaliseMigration(writer http.ResponseWriter, request *http.Request, _ params) {
	if !requireAdmin(writer, request) {
		return
	}
	if s.config.Migration == nil {
		writeAPIError(writer, newError(http.StatusNotImplemented, api.ErrorCodeUnavailable, "this build cannot finalise the migration"))
		return
	}
	status := s.migrationStatus()
	if status.State != api.MigrationStateCommitted {
		writeAPIError(writer, conflict("the migration has not committed"))
		return
	}
	if !status.CanFinalise {
		writeAPIError(writer, conflict("finalise is possible 30 days after the migration committed"))
		return
	}
	run := dryRun(request)
	result, err := s.config.Migration.Finalise(run)
	if err != nil {
		log.Printf("migration finalise (dry run %v): %v", run, err)
		writeAPIError(writer, internalError("the migration could not be finalised; see the controller log"))
		return
	}
	if !run {
		s.recordAction(request, "", "Finalised the schema-2 migration")
		log.Printf("audit: schema-2 migration finalised by %s", principal(request))
	}
	respond(writer, http.StatusOK, api.MigrationRun{DryRun: run, Status: result})
}
