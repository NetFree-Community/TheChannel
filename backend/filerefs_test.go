package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func refsKey(hash string) string { return "file:hash:" + hash + ":refs" }

func readRefs(t *testing.T, ctx context.Context, hash string) int64 {
	t.Helper()
	v, err := rdb.Get(ctx, refsKey(hash)).Int64()
	if err != nil {
		return 0 // absent counts as zero
	}
	return v
}

// Blobs are deduplicated by hash across every tenant, so the reference count is
// the only thing standing between one channel deleting its copy and another
// channel's file disappearing. These pin the two ways that count used to be
// wrong.
func TestFileHashRefsClaimedBeforeExistenceCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const hash = "refs-test-claim-first"
	rdb.Del(ctx, refsKey(hash))
	t.Cleanup(func() {
		cctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		rdb.Del(cctx, refsKey(hash))
	})

	// First uploader claims: it is the only reference.
	first, err := dbIncrFileHashRefsResult(ctx, hash)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if first != 1 {
		t.Fatalf("first claim returned %d, want 1", first)
	}

	// A second uploader of identical bytes claims before deciding to skip the
	// write. Its own post-increment value must show it is not the only holder.
	second, err := dbIncrFileHashRefsResult(ctx, hash)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if second != 2 {
		t.Errorf("second claim returned %d, want 2", second)
	}

	// The first tenant now deletes its file. One reference remains, so the blob
	// must survive.
	remaining, err := dbDecrFileHashRefs(ctx, hash)
	if err != nil {
		t.Fatalf("decr: %v", err)
	}
	if remaining != 1 {
		t.Errorf("after one delete the count is %d, want 1", remaining)
	}
	if remaining <= 0 {
		t.Error("the blob would have been deleted while another channel still references it")
	}
}

// A blob written before reference counting existed has no counter. An upload
// that dedupes against it would create the counter at 1 — recording one
// reference where two exist — so the new tenant deleting its own copy would
// destroy the original tenant's file. uploadFile detects that case (its own
// claim came back as 1 even though the blob was already there) and counts the
// pre-existing reference.
func TestFileHashRefsAdoptsPreexistingBlob(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const hash = "refs-test-legacy"
	rdb.Del(ctx, refsKey(hash))
	t.Cleanup(func() {
		cctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		rdb.Del(cctx, refsKey(hash))
	})

	// The legacy state: a blob on disk/R2, no counter at all.
	if got := readRefs(t, ctx, hash); got != 0 {
		t.Fatalf("precondition: counter should be absent, got %d", got)
	}

	// A new tenant uploads identical bytes. It claims first...
	claimed, err := dbIncrFileHashRefsResult(ctx, hash)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	// ...and, seeing the blob already present with a claim of 1, adopts the
	// untracked reference — this is the branch uploadFile takes when
	// !isNewHash && refs == 1.
	blobAlreadyExisted := true
	if blobAlreadyExisted && claimed == 1 {
		if err := dbIncrFileHashRefs(ctx, hash); err != nil {
			t.Fatalf("adopt: %v", err)
		}
	}

	if got := readRefs(t, ctx, hash); got != 2 {
		t.Fatalf("count is %d, want 2 (the legacy record plus the new one)", got)
	}

	// The new tenant deletes its file. The legacy record still points at the
	// blob, so the count must not reach zero.
	remaining, err := dbDecrFileHashRefs(ctx, hash)
	if err != nil {
		t.Fatalf("decr: %v", err)
	}
	if remaining <= 0 {
		t.Errorf("count fell to %d: deleting the new copy would destroy the "+
			"pre-existing channel's blob", remaining)
	}
}

// A failed upload must give its claim back, or every failure permanently
// inflates the count and strands the blob forever.
func TestFileHashRefsReleasedOnFailedUpload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const hash = "refs-test-release"
	rdb.Del(ctx, refsKey(hash))
	t.Cleanup(func() {
		cctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		rdb.Del(cctx, refsKey(hash))
	})

	if _, err := dbIncrFileHashRefsResult(ctx, hash); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// The upload then fails; uploadFile's deferred release runs.
	if _, err := dbDecrFileHashRefs(ctx, hash); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := readRefs(t, ctx, hash); got != 0 {
		t.Errorf("count is %d after a failed upload, want 0", got)
	}
}

// An upload of bytes whose last reference is being deleted waits for that
// delete to finish before looking for the blob, instead of adopting an object
// that is about to vanish. The wait is bounded, so a lock leaked by a crash
// cannot hang uploads for its whole TTL.
func TestFileHashLockMakesSameHashUploadWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const hash = "refs-test-lock-wait"
	lock := fileHashLockKey(hash)
	rdb.Del(ctx, lock)
	t.Cleanup(func() { rdb.Del(context.Background(), lock) })

	// No delete in flight: no wait.
	start := time.Now()
	waitFileHashUnlocked(ctx, hash)
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("waited %v with no lock held", d)
	}

	// A delete holds the lock for 400 ms; the upload proceeds once it clears.
	if err := rdb.Set(ctx, lock, 1, 400*time.Millisecond).Err(); err != nil {
		t.Fatalf("lock: %v", err)
	}
	start = time.Now()
	waitFileHashUnlocked(ctx, hash)
	d := time.Since(start)
	if d < 300*time.Millisecond {
		t.Errorf("upload proceeded after %v while the delete still held the lock", d)
	}
	if d > 3*time.Second {
		t.Errorf("waited %v, want a bounded wait that ends with the lock", d)
	}
}

// A last-reference delete removes the blob and its counter as before, and
// leaves no lock behind for the next same-hash upload to wait on.
func TestDeleteFileByIDLastReferenceReleasesLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	prevRoot := rootUploadPath
	rootUploadPath = t.TempDir()
	t.Cleanup(func() { rootUploadPath = prevRoot })

	const hash = "abcdrefs-test-last-ref"
	const id = "refs-test-last-ref-file"
	const slug = "refs-lock-chan"
	blob, ok := localBlobPath(hash)
	if !ok {
		t.Fatal("blob path")
	}
	if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blob, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	keys := []string{"file:" + id, "file:" + id + ":deleting", refsKey(hash), fileHashLockKey(hash), "channel:" + slug + ":storage:used_bytes"}
	rdb.Del(ctx, keys...)
	t.Cleanup(func() { rdb.Del(context.Background(), keys...) })
	if err := rdb.Set(ctx, refsKey(hash), 1, 0).Err(); err != nil {
		t.Fatal(err)
	}
	meta := &FileMetadata{ID: id, Filename: "x.txt", Hash: hash, Type: "text", Size: 1, ChannelSlug: slug}
	if err := dbSaveFileMetadata(ctx, meta); err != nil {
		t.Fatal(err)
	}

	freed, released := deleteFileByID(ctx, slug, id, false)
	if !released || freed != 1 {
		t.Fatalf("deleteFileByID = (%d, %v), want (1, true)", freed, released)
	}
	if _, err := os.Stat(blob); !os.IsNotExist(err) {
		t.Errorf("blob still on disk after its last reference went (%v)", err)
	}
	if got := readRefs(t, ctx, hash); got != 0 {
		t.Errorf("counter is %d after the last reference went, want it gone", got)
	}
	if n, _ := rdb.Exists(ctx, fileHashLockKey(hash)).Result(); n != 0 {
		t.Errorf("the per-hash lock was left behind")
	}
}

// The last-reference delete re-reads the counter, removes the blob and then
// drops the counter. An upload of the same bytes that claimed the hash between
// the re-read and the drop is waiting on the lock and will write the blob anew,
// so its reference must survive the drop — an unconditional DEL wiped it, and
// the next same-hash delete removed a blob that record still served.
func TestFileHashRefsCounterKeptWhenClaimedBeforeRemoval(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const hash = "refs-test-conditional-del"
	rdb.Del(ctx, refsKey(hash))
	t.Cleanup(func() {
		cctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		rdb.Del(cctx, refsKey(hash))
	})

	// Nobody claimed the hash: the counter goes, as before.
	for _, v := range []int{0, -1} {
		if err := rdb.Set(ctx, refsKey(hash), v, 0).Err(); err != nil {
			t.Fatal(err)
		}
		kept, err := dbDelFileHashRefs(ctx, hash)
		if err != nil || kept != 0 {
			t.Fatalf("counter at %d: dbDelFileHashRefs = (%d, %v), want (0, nil)", v, kept, err)
		}
		if n, _ := rdb.Exists(ctx, refsKey(hash)).Result(); n != 0 {
			t.Errorf("counter at %d survived the removal", v)
		}
	}
	// A counter that is already gone is not an error either.
	if kept, err := dbDelFileHashRefs(ctx, hash); err != nil || kept != 0 {
		t.Fatalf("absent counter: dbDelFileHashRefs = (%d, %v), want (0, nil)", kept, err)
	}

	// The delete's re-read saw zero; an upload then claimed the hash and is
	// waiting on the lock. The removal must leave that reference alone.
	if err := rdb.Set(ctx, refsKey(hash), 0, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := dbIncrFileHashRefsResult(ctx, hash); err != nil {
		t.Fatalf("upload's claim: %v", err)
	}
	kept, err := dbDelFileHashRefs(ctx, hash)
	if err != nil {
		t.Fatalf("dbDelFileHashRefs: %v", err)
	}
	if kept != 1 {
		t.Errorf("kept = %d, want 1 (the waiting upload's reference)", kept)
	}
	if got := readRefs(t, ctx, hash); got != 1 {
		t.Errorf("counter is %d after the removal, want 1: the waiting upload's reference was wiped", got)
	}
}
