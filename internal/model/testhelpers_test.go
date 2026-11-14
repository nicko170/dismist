package model

import bolt "go.etcd.io/bbolt"

type boltTx = bolt.Tx

func clearBucket(tx *bolt.Tx, name string) error {
	if err := tx.DeleteBucket([]byte(name)); err != nil {
		return err
	}
	_, err := tx.CreateBucket([]byte(name))
	return err
}
