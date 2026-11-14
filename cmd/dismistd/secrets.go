package main

import (
	"errors"
	"log"

	"github.com/nicko170/dismist/internal/model"
)

var errNothingToImport = errors.New("no AP secret files to import")

// importSecretFiles moves legacy on-disk AP secrets into the database, which
// is where dismist keeps them now. A file that can't be read is left in the
// record and logged; that AP stays inactive until its secret is uploaded.
func importSecretFiles(store model.StateStore) error {
	pending := false
	for _, ap := range store.Snapshot().APs {
		if ap.SecretFile != "" {
			pending = true
			break
		}
	}
	if !pending {
		return nil
	}
	var imported, cleared []string
	err := store.Update(func(state *model.State) error {
		for i := range state.APs {
			ap := &state.APs[i]
			if ap.SecretFile == "" {
				continue
			}
			if len(ap.Secret) == model.SecretSize {
				ap.SecretFile = ""
				cleared = append(cleared, ap.ID)
				continue
			}
			secret, err := loadSecret(ap.SecretFile)
			if err != nil {
				log.Printf("AP %s: cannot import secret file %s: %v; upload its secret in the UI", ap.ID, ap.SecretFile, err)
				continue
			}
			log.Printf("AP %s: imported its secret from %s into the database; the file is no longer used", ap.ID, ap.SecretFile)
			ap.Secret = append([]byte(nil), secret[:]...)
			ap.SecretFile = ""
			imported = append(imported, ap.ID)
		}
		if len(imported) == 0 && len(cleared) == 0 {
			return errNothingToImport
		}
		return nil
	})
	if errors.Is(err, errNothingToImport) {
		return nil
	}
	if err == nil && len(cleared) > 0 {
		log.Printf("cleared stale secret file paths for APs %v", cleared)
	}
	return err
}

// withSecrets drops APs that have no usable secret yet, so one AP awaiting
// its secret doesn't stop the daemon. The live registry retries them.
func withSecrets(state model.State) model.State {
	ready := make([]model.APRecord, 0, len(state.APs))
	for _, ap := range state.APs {
		if len(ap.Secret) != model.SecretSize {
			if ap.SiteID != "" {
				log.Printf("AP %s has no secret yet; it stays inactive until one is uploaded", ap.ID)
			}
			continue
		}
		ready = append(ready, ap)
	}
	state.APs = ready
	return state
}
