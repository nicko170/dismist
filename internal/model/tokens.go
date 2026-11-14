package model

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Token scopes (design §4.8).
const (
	TokenScopeRead  = "read"
	TokenScopeAdmin = "admin"
)

// TokenPrefix starts every token ID and every bearer token.
const TokenPrefix = "dsm_"

// TokenRecord is one API token. The secret part is never stored, only its
// SHA-256 (the secret is 256 random bits, so an unkeyed hash is enough).
type TokenRecord struct {
	ID       string    `json:"id"` // "dsm_<id>"
	Name     string    `json:"name"`
	Scope    string    `json:"scope"`
	SHA256   string    `json:"sha256"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"last_used,omitzero"`
}

// ErrNotFound is returned by the small stores when a key does not exist.
var ErrNotFound = errors.New("not found")

// TokenStore is the tokens bucket.
type TokenStore struct{ db *bolt.DB }

// Tokens returns the API token store.
func (s *BoltStore) Tokens() *TokenStore { return &TokenStore{db: s.db} }

// Create makes a token and returns its record and the full bearer token
// "dsm_<id>_<secret>". The bearer token is shown once and never stored.
func (t *TokenStore) Create(name, scope string, now time.Time) (TokenRecord, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return TokenRecord{}, "", errors.New("token name must be 1 to 64 characters")
	}
	if scope != TokenScopeRead && scope != TokenScopeAdmin {
		return TokenRecord{}, "", fmt.Errorf("token scope must be %q or %q", TokenScopeRead, TokenScopeAdmin)
	}
	idBytes := make([]byte, 8)
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(idBytes); err != nil {
		return TokenRecord{}, "", err
	}
	if _, err := rand.Read(secretBytes); err != nil {
		return TokenRecord{}, "", err
	}
	id := TokenPrefix + hex.EncodeToString(idBytes)
	secret := hex.EncodeToString(secretBytes)
	record := TokenRecord{ID: id, Name: name, Scope: scope, SHA256: tokenDigest(secret), Created: now.UTC()}
	err := t.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketTokens))
		if bucket == nil {
			return errors.New("tokens bucket missing")
		}
		if bucket.Get([]byte(id)) != nil {
			return errors.New("token ID collision")
		}
		return putJSON(bucket, []byte(id), record)
	})
	if err != nil {
		return TokenRecord{}, "", err
	}
	return record, id + "_" + secret, nil
}

// SplitToken splits a bearer token "dsm_<id>_<secret>" into its ID
// ("dsm_<id>") and secret.
func SplitToken(token string) (id, secret string, ok bool) {
	if !strings.HasPrefix(token, TokenPrefix) {
		return "", "", false
	}
	rest := token[len(TokenPrefix):]
	cut := strings.IndexByte(rest, '_')
	if cut <= 0 || cut == len(rest)-1 {
		return "", "", false
	}
	return TokenPrefix + rest[:cut], rest[cut+1:], true
}

// Verify checks a bearer token and returns its record. It compares digests
// in constant time and never says which part was wrong.
func (t *TokenStore) Verify(token string) (TokenRecord, bool) {
	id, secret, ok := SplitToken(token)
	if !ok {
		return TokenRecord{}, false
	}
	record, err := t.Get(id)
	if err != nil {
		return TokenRecord{}, false
	}
	if subtle.ConstantTimeCompare([]byte(record.SHA256), []byte(tokenDigest(secret))) != 1 {
		return TokenRecord{}, false
	}
	return record, true
}

// Get returns one token by ID.
func (t *TokenStore) Get(id string) (TokenRecord, error) {
	var record TokenRecord
	err := t.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketTokens))
		if bucket == nil {
			return errors.New("tokens bucket missing")
		}
		raw := bucket.Get([]byte(id))
		if raw == nil {
			return ErrNotFound
		}
		return json.Unmarshal(raw, &record)
	})
	return record, err
}

// List returns every token, oldest first.
func (t *TokenStore) List() ([]TokenRecord, error) {
	result := make([]TokenRecord, 0)
	err := t.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketTokens))
		if bucket == nil {
			return errors.New("tokens bucket missing")
		}
		return bucket.ForEach(func(_, raw []byte) error {
			var record TokenRecord
			if err := json.Unmarshal(raw, &record); err != nil {
				return err
			}
			result = append(result, record)
			return nil
		})
	})
	sort.SliceStable(result, func(i, j int) bool { return result[i].Created.Before(result[j].Created) })
	return result, err
}

// Delete revokes a token. Deleting a missing token returns ErrNotFound.
func (t *TokenStore) Delete(id string) error {
	return t.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketTokens))
		if bucket == nil {
			return errors.New("tokens bucket missing")
		}
		if bucket.Get([]byte(id)) == nil {
			return ErrNotFound
		}
		return bucket.Delete([]byte(id))
	})
}

// TokenTouchInterval limits last_used writes to one per token per interval.
const TokenTouchInterval = time.Minute

// Touch records a use of the token. It writes only when the stored last_used
// is older than TokenTouchInterval, so busy clients do not cause a write per
// request.
func (t *TokenStore) Touch(id string, at time.Time) error {
	return t.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketTokens))
		if bucket == nil {
			return errors.New("tokens bucket missing")
		}
		raw := bucket.Get([]byte(id))
		if raw == nil {
			return ErrNotFound
		}
		var record TokenRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return err
		}
		if !record.LastUsed.IsZero() && at.Sub(record.LastUsed) < TokenTouchInterval {
			return nil
		}
		record.LastUsed = at.UTC()
		return putJSON(bucket, []byte(id), record)
	})
}

func tokenDigest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
