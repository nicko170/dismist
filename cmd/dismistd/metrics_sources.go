package main

import (
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/metrics"
	"github.com/nicko170/dismist/internal/model"
)

// storeSchema reads meta.schema and meta.compat for dismist_store_schema and
// dismist_store_compat.
func storeSchema(store *model.BoltStore) func() (int, int, bool) {
	return func() (int, int, bool) {
		info, err := store.Schema()
		if err != nil {
			return 0, 0, false
		}
		return int(info.Schema), int(info.Compat), true
	}
}

// compileStatus reports each registered AP's compile state from its control
// session (design §4.4 per-AP isolation): failing while it is served its
// delivered config or none, and the failures its session has counted.
func compileStatus(manager *controller.Manager) func() map[string]metrics.CompileStatus {
	return func() map[string]metrics.CompileStatus {
		servers := manager.Servers()
		result := make(map[string]metrics.CompileStatus, len(servers))
		for id, server := range servers {
			status := server.Status()
			result[id] = metrics.CompileStatus{
				Failing:  status.ConfigSource != "" && status.ConfigSource != controller.ConfigSourceCompiled,
				Failures: server.Counters().CompileErrors,
			}
		}
		return result
	}
}
