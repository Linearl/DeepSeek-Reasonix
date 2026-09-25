package session

import (
	"encoding/json"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// Task 244 B4 (experimental_recovery_orphan_sweep): the open path settles
// operations that point past coverage_sequence — a crash/rewind leftover must
// not keep representing an in-flight recovery surface. Off keeps the open path
// byte-identical; a generation mismatch takes the existing delete-and-recreate
// branch and therefore never sweeps.

func writeTestOperation(t *testing.T, store *recoveryStore, key string, op recoveryOperation) {
	t.Helper()
	raw, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(recoveryOperationBucket)
		if bucket == nil {
			t.Fatal("operations bucket missing")
		}
		return bucket.Put([]byte(key), raw)
	}); err != nil {
		t.Fatal(err)
	}
}

func readTestOperation(t *testing.T, store *recoveryStore, key string) (recoveryOperation, bool) {
	t.Helper()
	var out recoveryOperation
	found := false
	if err := store.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(recoveryOperationBucket)
		if bucket == nil {
			return nil
		}
		if raw := bucket.Get([]byte(key)); raw != nil {
			found = true
			return json.Unmarshal(raw, &out)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out, found
}

func setTestCoverage(t *testing.T, store *recoveryStore, seq uint64) {
	t.Helper()
	if err := store.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket(recoveryMetaBucket)
		if meta == nil {
			t.Fatal("meta bucket missing")
		}
		return meta.Put([]byte("coverage_sequence"), []byte(strconvFormatUint(seq)))
	}); err != nil {
		t.Fatal(err)
	}
}

func strconvFormatUint(v uint64) string {
	return string(fmtUint(v))
}

func fmtUint(v uint64) []byte {
	if v == 0 {
		return []byte("0")
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return buf[i:]
}

func TestRecoveryOrphanSweepSettlesPastCoverage(t *testing.T) {
	dir := t.TempDir()
	identity := storageIdentity{Version: 1, SessionID: "s1", Generation: "gen-a"}

	first, err := openRecoveryStore(dir, identity)
	if err != nil {
		t.Fatal(err)
	}
	orphan := recoveryOperation{Hash: "h1", CommitID: "c1", FirstSequence: 999, OperationID: "op-orphan"}
	covered := recoveryOperation{Hash: "h2", CommitID: "c2", FirstSequence: 50, OperationID: "op-covered"}
	writeTestOperation(t, first, "op-orphan", orphan)
	writeTestOperation(t, first, "op-covered", covered)
	setTestCoverage(t, first, 100)
	if err := first.db.Close(); err != nil {
		t.Fatal(err)
	}

	// On: the orphan past coverage settles; the covered one stays untouched.
	SetOrphanSweepProbe(func() bool { return true })
	defer SetOrphanSweepProbe(nil)
	on, err := openRecoveryStore(dir, identity)
	if err != nil {
		t.Fatal(err)
	}
	if op, ok := readTestOperation(t, on, "op-orphan"); !ok || !op.Settled {
		t.Fatalf("orphan must settle at open: ok=%v op=%+v", ok, op)
	}
	if op, ok := readTestOperation(t, on, "op-covered"); !ok || op.Settled {
		t.Fatalf("covered operation must stay untouched: ok=%v op=%+v", ok, op)
	}
	// Idempotent: reopening changes nothing and does not error.
	if err := on.db.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := openRecoveryStore(dir, identity)
	if err != nil {
		t.Fatalf("second open must be a no-op: %v", err)
	}
	if op, ok := readTestOperation(t, again, "op-orphan"); !ok || !op.Settled {
		t.Fatalf("settled must persist: ok=%v op=%+v", ok, op)
	}
	if err := again.db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryOrphanSweepOffKeepsRecords(t *testing.T) {
	dir := t.TempDir()
	identity := storageIdentity{Version: 1, SessionID: "s1", Generation: "gen-a"}

	first, err := openRecoveryStore(dir, identity)
	if err != nil {
		t.Fatal(err)
	}
	writeTestOperation(t, first, "op-orphan", recoveryOperation{FirstSequence: 999, OperationID: "op-orphan"})
	setTestCoverage(t, first, 100)
	if err := first.db.Close(); err != nil {
		t.Fatal(err)
	}

	// Off (probe nil, the default): the record keeps decoding unsettled.
	SetOrphanSweepProbe(nil)
	off, err := openRecoveryStore(dir, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer off.db.Close()
	if op, ok := readTestOperation(t, off, "op-orphan"); !ok || op.Settled {
		t.Fatalf("off state must not sweep: ok=%v op=%+v", ok, op)
	}
}

func TestRecoveryOrphanSweepSkipsOtherGeneration(t *testing.T) {
	dir := t.TempDir()
	identity := storageIdentity{Version: 1, SessionID: "s1", Generation: "gen-a"}

	first, err := openRecoveryStore(dir, identity)
	if err != nil {
		t.Fatal(err)
	}
	writeTestOperation(t, first, "op-orphan", recoveryOperation{FirstSequence: 999, OperationID: "op-orphan"})
	setTestCoverage(t, first, 100)
	if err := first.db.Close(); err != nil {
		t.Fatal(err)
	}

	// A generation mismatch takes the delete-and-recreate branch — the sweep
	// never runs against the old generation's records (they are gone instead).
	SetOrphanSweepProbe(func() bool { return true })
	defer SetOrphanSweepProbe(nil)
	other := identity
	other.Generation = "gen-b"
	store, err := openRecoveryStore(dir, other)
	if err != nil {
		t.Fatal(err)
	}
	defer store.db.Close()
	if _, ok := readTestOperation(t, store, "op-orphan"); ok {
		t.Fatal("generation mismatch must rebuild the bucket, not settle in place")
	}
}
