package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi"
	"github.com/h2non/filetype"
	"github.com/icza/dyno"
	"github.com/redis/go-redis/v9"
	"github.com/subosito/gozaru"
	"gopkg.in/yaml.v3"
)

// errFileNotFound means no metadata exists for an id, in Redis or on disk.
// It is distinct from a failed read: a caller that prunes an index entry
// for a missing record must never do so on a transient Redis error.
var errFileNotFound = errors.New("file not found")

// compressWithTinyPng compresses an image using the TinyPNG API.
// Returns the compressed bytes, or the original bytes if compression fails or is not applicable.
func compressWithTinyPng(ctx context.Context, apiKey string, data []byte, mimeType string) []byte {
	// Only compress supported image types
	switch mimeType {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return data
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tinify.com/shrink", bytes.NewReader(data))
	if err != nil {
		return data
	}
	req.SetBasicAuth("api", apiKey)
	req.Header.Set("Content-Type", mimeType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return data
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return data
	}

	var result struct {
		Output struct {
			URL string `json:"url"`
		} `json:"output"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.Output.URL == "" {
		return data
	}

	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, result.Output.URL, nil)
	if err != nil {
		return data
	}
	dlReq.SetBasicAuth("api", apiKey)

	dlResp, err := http.DefaultClient.Do(dlReq)
	if err != nil {
		return data
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != http.StatusOK {
		return data
	}

	compressed, err := io.ReadAll(dlResp.Body)
	if err != nil || len(compressed) == 0 {
		return data
	}
	return compressed
}

var rootUploadPath = "/app/files/"

type FileResponse struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	FileType string `json:"filetype"`
	// Pixel dimensions, present only for images whose header could be read.
	// The client turns them into an aspect ratio so a lazily-loaded picture
	// reserves its space before it arrives; omitted, it simply falls back to
	// the old reflow-on-load behaviour.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

type FileMetadata struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	Hash        string `json:"hash"`
	Type        string `json:"type"`
	Delete      bool   `json:"delete"`
	Size        int64  `json:"size"`        // bytes
	ChannelSlug string `json:"channelSlug"` // which channel owns this file
	// Zero for non-images and for images whose header could not be read.
	// Recorded at upload time; historical records simply have neither.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

// allowLegacyUnscopedFiles re-opens the pre-migration corpus (records with no
// ChannelSlug) to every tenant. Off by default: unscoped records are the normal
// shape of every YAML-era file, so exempting them from isolation would leave a
// permanent cross-tenant read path.
var allowLegacyUnscopedFiles = os.Getenv("ALLOW_LEGACY_UNSCOPED_FILES") == "true"

// fileVisibleToChannel reports whether a file record may be served under slug.
func fileVisibleToChannel(meta *FileMetadata, slug string) bool {
	if meta.ChannelSlug == slug {
		return true
	}
	if meta.ChannelSlug == "" && allowLegacyUnscopedFiles {
		log.Printf("legacy unscoped file %s served to channel %s\n", meta.ID, slug)
		return true
	}
	return false
}

// dbSaveFileMetadata stores file metadata in Redis.
func dbSaveFileMetadata(ctx context.Context, meta *FileMetadata) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, "file:"+meta.ID, data, 0).Err()
}

// dbGetFileMetadata retrieves file metadata from Redis, falling back to YAML for old files.
func dbGetFileMetadata(ctx context.Context, id string) (*FileMetadata, error) {
	data, err := rdb.Get(ctx, "file:"+id).Result()
	if err == nil {
		var meta FileMetadata
		if err := json.Unmarshal([]byte(data), &meta); err != nil {
			// Unreadable is as gone as missing: nothing can be released from
			// it, and a caller that waits for it would wait for ever.
			return nil, fmt.Errorf("%w: corrupt metadata for %s: %v", errFileNotFound, id, err)
		}
		return &meta, nil
	}
	if err != redis.Nil {
		return nil, err
	}

	// Fallback: read from YAML (legacy local files). The slicing below panics on
	// short ids, and callers feed this ids parsed out of Redis index members, so
	// malformed data must be an error rather than a crash.
	if len(id) < 4 {
		return nil, errFileNotFound
	}
	metadataFilePath := filepath.Join(rootUploadPath, id[:2], id[2:4], id+".yaml")
	yamlData, err := os.ReadFile(metadataFilePath)
	if err != nil {
		return nil, errFileNotFound
	}
	var raw map[string]any
	if err := yaml.Unmarshal(yamlData, &raw); err != nil {
		return nil, err
	}
	deleted, _ := raw["delete"].(bool)
	hash, _ := dyno.GetString(raw["hash"])
	filename, _ := dyno.GetString(raw["filename"])
	fileType, _ := dyno.GetString(raw["type"])
	return &FileMetadata{
		ID:       id,
		Filename: filename,
		Hash:     hash,
		Type:     fileType,
		Delete:   deleted,
	}, nil
}

// localBlobPath returns the on-disk path of a blob and whether the hash is
// well-formed enough to build one. Every caller slices hash[:2]/hash[2:4], so a
// short or empty hash (malformed metadata, a YAML-era record with no hash at
// all) would panic the handler; they go through here instead.
func localBlobPath(hash string) (string, bool) {
	if len(hash) < 4 {
		return "", false
	}
	return filepath.Join(rootUploadPath, hash[:2], hash[2:4], hash), true
}

// localBlobExists reports whether the local copy of a blob is present.
func localBlobExists(hash string) (string, bool) {
	p, ok := localBlobPath(hash)
	if !ok {
		return "", false
	}
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return p, true
}

// contentDispositionAttachment builds the download header for a stored name.
//
// filename* is percent-encoded as RFC 5987 defines it. url.QueryEscape was used
// before, and it writes a space as "+", which browsers do not decode in this
// header: "דוח שנתי.pdf" was saved as "דוח+שנתי.pdf". A plain ASCII filename=
// is kept alongside for the few clients that ignore filename*.
func contentDispositionAttachment(name string) string {
	return `attachment; filename="` + asciiFilename(name) + `"; filename*=UTF-8''` + rfc5987Encode(name)
}

// rfc5987Encode percent-encodes everything outside the attr-char set of
// RFC 5987 §3.2.1, byte by byte, so multi-byte UTF-8 survives intact.
func rfc5987Encode(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '!', c == '#', c == '$', c == '&', c == '+', c == '-', c == '.',
			c == '^', c == '_', c == '`', c == '|', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		}
	}
	return b.String()
}

// asciiFilename is the lossy fallback name: anything a quoted-string cannot
// carry, or that is not printable ASCII, becomes "_". A name with nothing
// printable left (a purely Hebrew one) gets a placeholder instead of "____".
func asciiFilename(s string) string {
	var b strings.Builder
	kept := false
	for _, r := range s {
		if r >= 0x20 && r < 0x7f && r != '"' && r != '\\' {
			b.WriteRune(r)
			kept = kept || r != ' '
		} else {
			b.WriteByte('_')
		}
	}
	if !kept {
		return "file"
	}
	return b.String()
}

func serveFile(w http.ResponseWriter, r *http.Request) {
	fileId := chi.URLParam(r, "fileid")
	if len(fileId) < 4 {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	meta, err := dbGetFileMetadata(ctx, fileId)
	if err != nil || meta.Delete {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	// Enforce channel isolation: reject if the file belongs to another channel.
	if !fileVisibleToChannel(meta, slug) {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", contentDispositionAttachment(meta.Filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if r2Enabled && len(meta.Hash) >= 4 {
		key := r2ObjectKey(meta.Hash)

		// Flipping R2 on does not move the historical corpus with it: the
		// local-to-R2 migration runs on the next boot and may not have reached
		// this blob (or may have failed for it). Redirecting to a key that is
		// not there yet would 404 every legacy image, so a miss falls back to
		// the local copy, which the migration never deletes.
		if !r2ExistsCached(ctx, key) {
			if filePath, ok := localBlobExists(meta.Hash); ok {
				log.Printf("serveFile: %s/%s not in R2 (key %s), serving local copy\n", slug, fileId, key)
				http.ServeFile(w, r, filePath)
				return
			}
			log.Printf("serveFile: %s/%s missing in R2 (key %s) and on local disk\n", slug, fileId, key)
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}

		// The redirect itself is cacheable (private: a channel may require a
		// login), so the browser skips this round trip on the next view.
		if r2PublicURL != "" {
			// Public bucket: redirect to CDN URL (fastest, no auth needed)
			w.Header().Set("Cache-Control", "private, max-age=3600")
			http.Redirect(w, r, r2PublicURL+"/"+key, http.StatusFound)
			return
		}
		// Private bucket: a pre-signed URL, reused for a while so the browser
		// can serve the file from its cache (see r2_cache.go).
		// The client fetches directly from R2 — backend is not in the data path.
		presignedURL, left, err := r2PresignCached(ctx, key, contentDispositionAttachment(meta.Filename))
		if err == nil {
			w.Header().Set("Cache-Control", "private, max-age="+strconv.Itoa(int(left.Seconds())))
			http.Redirect(w, r, presignedURL, http.StatusFound)
			return
		}
		log.Printf("serveFile: presign failed for %s/%s (key %s): %v\n", slug, fileId, key, err)
		// Fallback: proxy through backend if presigning fails
		body, contentType, err := r2Download(ctx, key)
		if err != nil {
			log.Printf("serveFile: R2 download failed for %s/%s (key %s): %v\n", slug, fileId, key, err)
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		defer body.Close()
		if contentType != nil {
			w.Header().Set("Content-Type", *contentType)
		}
		io.Copy(w, body)
		return
	}

	// Local storage fallback
	filePath, ok := localBlobPath(meta.Hash)
	if !ok {
		log.Printf("serveFile: %s/%s has malformed hash %q\n", slug, fileId, meta.Hash)
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, filePath)
}

func uploadFile(w http.ResponseWriter, r *http.Request) {
	// Derived from the request so a client disconnect stops the work, and wide
	// enough to cover reading the body off the wire. The hostile sub-operations
	// (TinyPNG, the R2 write) get their own budgets below.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	slug := channelSlugFromCtx(r)
	cfg := getChannelConfig(ctx, slug)

	// Defensive clamp: getChannelConfig is not the only way a config can reach
	// here, and a non-positive or huge value would either reject every upload
	// or let a single request buffer unbounded bytes in memory.
	maxMB := cfg.MaxFileSize
	if maxMB <= 0 || maxMB > maxAllowedFileSizeMB {
		maxMB = defaultMaxFileSizeMB
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMB<<20)

	// Before the body is read: a channel that is already full (and cannot
	// free space) used to ingest the whole file into memory, and spend one of
	// the owner's TinyPNG compressions, only to answer 507 afterwards.
	if storageAlreadyFull(ctx, slug) {
		http.Error(w, "storage quota exceeded", http.StatusInsufficientStorage)
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		// The errors.As target must be a local: a package-level one is written
		// concurrently by every upload.
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			log.Printf("uploadFile: %s: rejected upload over the %d MB limit: %v\n", slug, maxMB, err)
			http.Error(w, "File too large", http.StatusRequestEntityTooLarge)
			return
		}
		log.Printf("uploadFile: %s: reading multipart form failed: %v\n", slug, err)
		http.Error(w, "error", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Each upload is buffered whole (up to the channel's limit, at most 512 MB)
	// and the per-user limiter allows a burst of ten, so ten concurrent uploads
	// from one writer could take the whole backend past its memory. A small
	// process-wide cap turns that into a 503 the client retries. It is taken
	// here, after the body has been read off the wire, and not before
	// r.FormFile: held across the network read it bounded socket time rather
	// than memory, so four slow phone uploads (there is no server read timeout)
	// stalled every upload on every channel for as long as they trickled.
	select {
	case uploadSlots <- struct{}{}:
		defer func() { <-uploadSlots }()
	default:
		w.Header().Set("Retry-After", "5")
		http.Error(w, "too many uploads in progress — please retry", http.StatusServiceUnavailable)
		return
	}

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		log.Printf("uploadFile: %s: reading uploaded body %q failed: %v\n", slug, handler.Filename, err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	// Not just the first 512 bytes: Office documents are ZIP containers whose
	// identifying entry sits past that, so a .docx/.xlsx was typed as a plain
	// archive and served as application/zip.
	t, _ := filetype.Match(fileBytes[:min(64<<10, len(fileBytes))])

	// Compress images with TinyPNG if the channel has an API key configured
	if cfg.TinyPngApiKey != "" {
		tinyCtx, tinyCancel := context.WithTimeout(ctx, 20*time.Second)
		fileBytes = compressWithTinyPng(tinyCtx, cfg.TinyPngApiKey, fileBytes, t.MIME.Value)
		tinyCancel()
	}

	// After compression, not before: TinyPNG returns a re-encoded file, so the
	// dimensions recorded must be the ones the browser will actually receive.
	var imgWidth, imgHeight int
	if t.MIME.Type == "image" {
		imgWidth, imgHeight = imageDimensions(fileBytes, t.MIME.Value)
	}

	fileSize := int64(len(fileBytes))
	hashBytes := sha256.Sum256(fileBytes)
	fileHash := hex.EncodeToString(hashBytes[:])

	// Quota check + auto-cleanup. This RESERVES the bytes: the counter moves
	// here, not after the blob is written, because everything between the two
	// points — an R2 PutObject with a 60-second budget — is time during which
	// every other concurrent upload would otherwise read a counter that does
	// not yet include this file and pass a quota check it should have failed.
	//
	// committed stays false until the file record exists; every early return
	// below therefore gives the reservation back rather than leaking quota that
	// no stored file accounts for.
	committed := false
	if err := reserveStorageQuota(ctx, slug, fileSize); err != nil {
		log.Printf("uploadFile: %s: quota check rejected %d bytes: %v\n", slug, fileSize, err)
		http.Error(w, err.Error(), http.StatusInsufficientStorage)
		return
	}
	// The releases below run on a context that survives the request's: the
	// usual reason an upload is abandoned is the client going away (or the
	// deadline passing) during the R2 write, and go-redis refuses a cancelled
	// context outright — so the quota bytes and the hash reference were leaked
	// exactly when they most needed giving back.
	releaseCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	}
	defer func() {
		if !committed {
			rctx, rcancel := releaseCtx()
			defer rcancel()
			releaseStorageReservation(rctx, slug, fileSize)
		}
	}()

	id := generatedRandomID(20)
	if id == "" {
		log.Printf("uploadFile: %s: crypto/rand failed to generate a file id\n", slug)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	safeFilename := gozaru.Sanitize(handler.Filename)
	contentType := t.MIME.Value
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Claim a reference to the blob BEFORE looking at whether it exists.
	//
	// Incrementing afterwards left a window in which a concurrent delete could
	// take the count to zero and physically remove the very blob this upload had
	// just decided it did not need to write — leaving a file record, charged
	// against the channel's quota and present in every index, that 404s forever.
	// Claiming first means any concurrent delete sees this reference and leaves
	// the blob alone.
	refs, refErr := dbIncrFileHashRefsResult(ctx, fileHash)
	if refErr != nil {
		log.Printf("uploadFile: %s: claiming a reference to hash %s failed: %v\n", slug, fileHash, refErr)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	defer func() {
		if !committed {
			rctx, rcancel := releaseCtx()
			defer rcancel()
			if _, err := dbDecrFileHashRefs(rctx, fileHash); err != nil {
				log.Printf("uploadFile: %s: releasing the reference to hash %s failed: %v\n", slug, fileHash, err)
			}
		}
	}()

	// A last-reference delete of these very bytes may be mid-flight: it has
	// taken the count to zero and is about to remove the object. Looking for
	// the blob now would find it, adopt it, and leave this record pointing at
	// nothing for good — so wait, briefly and bounded, for that delete to
	// finish before deciding whether the blob needs writing.
	waitFileHashUnlocked(ctx, fileHash)

	isNewHash := true
	if r2Enabled {
		key := r2ObjectKey(fileHash)
		if r2Exists(ctx, key) {
			isNewHash = false
		} else {
			upCtx, upCancel := context.WithTimeout(ctx, 60*time.Second)
			err := r2Upload(upCtx, key, bytes.NewReader(fileBytes), contentType)
			upCancel()
			if err != nil {
				// The SDK error is printed verbatim, not summarised: its code is
				// the only thing that separates AccessDenied (a read-only or
				// wrong-bucket API token), NoSuchBucket, InvalidAccessKeyId and
				// SignatureDoesNotMatch — all of which surface here as the same
				// opaque 500 for the operator.
				log.Printf("uploadFile: %s: R2 upload of file %s failed (bucket %q, key %s, %d bytes, content-type %s): %v\n",
					slug, id, r2Bucket, key, fileSize, contentType, err)
				http.Error(w, "error uploading file", http.StatusInternalServerError)
				return
			}
		}
	} else {
		if err := os.MkdirAll(rootUploadPath, os.ModePerm); err != nil {
			log.Printf("uploadFile: %s: mkdir root upload path %s failed: %v\n", slug, rootUploadPath, err)
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
		hashSubDir := filepath.Join(rootUploadPath, fileHash[:2], fileHash[2:4])
		if err := os.MkdirAll(hashSubDir, os.ModePerm); err != nil {
			log.Printf("uploadFile: %s: mkdir %s for file %s failed: %v\n", slug, hashSubDir, id, err)
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
		destPath := filepath.Join(hashSubDir, fileHash)
		if _, statErr := os.Stat(destPath); os.IsNotExist(statErr) {
			// Written beside its final name and renamed into place: the path is
			// content-addressed, and every later upload of the same bytes
			// trusts whatever sits there. A process killed mid-write used to
			// leave a truncated blob at the exact hash path, deduplicated
			// against — and served — from then on.
			tmpPath := destPath + ".tmp-" + id
			if err := os.WriteFile(tmpPath, fileBytes, 0644); err != nil {
				log.Printf("uploadFile: %s: writing %s for file %s (%d bytes) failed: %v\n", slug, tmpPath, id, fileSize, err)
				http.Error(w, "error", http.StatusInternalServerError)
				return
			}
			if err := os.Rename(tmpPath, destPath); err != nil {
				os.Remove(tmpPath)
				log.Printf("uploadFile: %s: moving %s into place for file %s failed: %v\n", slug, tmpPath, id, err)
				http.Error(w, "error", http.StatusInternalServerError)
				return
			}
		} else {
			isNewHash = false
		}
	}
	// A count of 1 means this upload created the counter, so as far as the
	// counter knows it is the only reference. If the blob was nonetheless
	// already there, some record predating reference counting points at it too.
	// Count that unknown reference, or this channel deleting its own copy would
	// destroy the other channel's file. Erring this way can at worst strand a
	// blob nothing references; the other direction loses somebody's data.
	if !isNewHash && refs == 1 {
		log.Printf("uploadFile: %s: hash %s had no reference count but its blob already existed; counting the pre-existing reference\n", slug, fileHash)
		if err := dbIncrFileHashRefs(ctx, fileHash); err != nil {
			log.Printf("uploadFile: %s: counting the pre-existing reference to %s failed: %v\n", slug, fileHash, err)
		}
	}

	meta := &FileMetadata{
		ID:          id,
		Filename:    safeFilename,
		Hash:        fileHash,
		Type:        t.MIME.Type,
		Delete:      false,
		Size:        fileSize,
		ChannelSlug: slug,
		Width:       imgWidth,
		Height:      imgHeight,
	}
	if err := dbSaveFileMetadata(ctx, meta); err != nil {
		log.Printf("uploadFile: %s: saving metadata for file %s failed: %v\n", slug, id, err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	// The reservation above already accounted for these bytes; incrementing
	// again here would double-count every upload.
	committed = true

	// The index entry is what auto-cleanup and channel deletion walk; a file
	// whose ZADD failed (its error used to be dropped) stayed charged to the
	// quota, held its hash reference and kept its blob, with nothing able to
	// reclaim any of it. A fresh context: the upload's own may already be
	// cancelled by a client that disconnected right after the blob landed,
	// and that cancellation must not orphan the file either.
	ictx, icancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer icancel()
	if err := dbAddChannelFile(ictx, slug, id, time.Now().Unix(), fileSize); err != nil {
		log.Printf("uploadFile: %s: indexing file %s failed, releasing it: %v\n", slug, id, err)
		// Releases the quota bytes, the hash reference and (on the last
		// reference) the blob, exactly as a delete would.
		deleteFileByID(ictx, slug, id, false)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	fileUrl := "/api/channel/" + slug + "/files/" + id

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(FileResponse{
		URL:      fileUrl,
		Filename: handler.Filename,
		FileType: t.MIME.Type,
		Width:    imgWidth,
		Height:   imgHeight,
	})
}

// releaseStorageReservation gives back bytes reserved by reserveStorageQuota
// for an upload that then failed. Best effort: a lost release overstates usage
// for the channel until a file is deleted, which is the safe direction to err.
func releaseStorageReservation(ctx context.Context, slug string, size int64) {
	if size <= 0 {
		return
	}
	if err := dbDecrChannelStorageUsed(ctx, slug, size); err != nil {
		log.Printf("releaseStorageReservation: %s: returning %d reserved bytes failed: %v\n", slug, size, err)
	}
}

// reserveStorageQuota atomically claims newFileSize bytes of the channel's
// quota, or reports why it cannot.
//
// The claim is made with a single INCRBY and only then compared against the
// quota, which is what makes it safe under concurrency: a plain read-compare-
// write lets N simultaneous uploads all read the same "used" value, all decide
// they fit, and all proceed — overshooting the quota by up to N times the file
// size. Because the increment is the same atomic operation for every caller,
// exactly one of them can be the request that crosses the limit, and that one
// hands its bytes straight back.
//
// The caller owns the reserved bytes until it either records a file (making the
// reservation the real accounting) or calls releaseStorageReservation.
func reserveStorageQuota(ctx context.Context, slug string, newFileSize int64) error {
	// All three config reads fail closed: dbGetEffectiveStorageQuota and
	// dbGetChannelStorageUsed already map redis.Nil to defaults, so only real
	// failures surface here — and a real failure silently disabling quota
	// enforcement would let usage grow past quota unnoticed.
	quota, err := dbGetEffectiveStorageQuota(ctx, slug)
	if err != nil {
		// The client only ever sees "storage configuration unavailable", which
		// is indistinguishable from a genuine quota breach; the real cause (a
		// failed Redis read) only exists here.
		log.Printf("reserveStorageQuota: %s: reading effective quota failed: %v\n", slug, err)
		return fmt.Errorf("storage configuration unavailable")
	}
	if quota == 0 {
		// Unlimited: still count the bytes, since used_bytes is what the storage
		// screens report and what a later quota would be measured against.
		if err := dbIncrChannelStorageUsed(ctx, slug, newFileSize); err != nil {
			log.Printf("reserveStorageQuota: %s: counting %d bytes failed: %v\n", slug, newFileSize, err)
			return fmt.Errorf("storage configuration unavailable")
		}
		return nil
	}

	// Claim first, judge second. INCRBY returns this caller's own post-increment
	// total, so concurrent uploads get distinct values and cannot all conclude
	// they fit.
	used, err := dbIncrChannelStorageUsedResult(ctx, slug, newFileSize)
	if err != nil {
		log.Printf("reserveStorageQuota: %s: reserving %d bytes failed: %v\n", slug, newFileSize, err)
		return fmt.Errorf("storage configuration unavailable")
	}
	if used <= quota {
		return nil // within quota, bytes already accounted for
	}

	autoCleanup, err := dbGetChannelAutoCleanup(ctx, slug)
	if err != nil {
		log.Printf("reserveStorageQuota: %s: reading auto-cleanup flag failed: %v\n", slug, err)
		releaseStorageReservation(ctx, slug, newFileSize)
		return fmt.Errorf("storage configuration unavailable")
	}
	if !autoCleanup {
		releaseStorageReservation(ctx, slug, newFileSize)
		return fmt.Errorf("storage quota exceeded (%d/%d bytes)", used-newFileSize, quota)
	}

	// Auto-cleanup: delete oldest files until we have enough space (target: 80%
	// of quota). used already includes this file's reservation, so freeing down
	// to the target makes room for it too.
	target := int64(float64(quota) * 0.80)
	needToFree := used - target

	files, err := dbGetOldestChannelFiles(ctx, slug, 200)
	if err != nil {
		log.Printf("reserveStorageQuota: %s: listing oldest files for auto-cleanup failed: %v\n", slug, err)
		releaseStorageReservation(ctx, slug, newFileSize)
		return fmt.Errorf("storage quota exceeded")
	}

	// The channel's logo goes through the same upload path and is usually the
	// oldest file a channel has, so it was the first thing evicted: the header
	// showed a broken image and nothing told the owner. It stays.
	logoID := ""
	if details, derr := getChannelDetails(ctx, slug); derr == nil {
		if u := details["logoUrl"]; u != "" {
			logoID = path.Base(u)
		}
	}

	for _, f := range files {
		if needToFree <= 0 {
			break
		}
		if f.ID == logoID {
			continue
		}
		// Count what the delete actually released. Subtracting f.Size
		// unconditionally meant a no-op still counted as freed space: two
		// concurrent near-quota uploads read the same oldest-files list, and the
		// one that lost every deletion claim still reached needToFree <= 0 and
		// was admitted against bytes the other upload had freed for itself.
		freed, released := deleteFileByID(ctx, slug, f.ID, true)
		needToFree -= freed
		if released && freed == 0 {
			// Gone or tombstoned before this pass: index debris. Nothing else
			// ever removed such a member, so each one stayed among the oldest
			// entries for ever and shrank the 200-entry window this loop
			// scans; once 200 accumulated every pass freed nothing and every
			// upload on the channel was refused with thousands of newer,
			// deletable files behind them.
			if err := dbRemoveChannelFile(ctx, slug, f.ID, f.Size); err != nil {
				log.Printf("reserveStorageQuota: %s: pruning stale index entry %s failed: %v\n", slug, f.ID, err)
			}
		}
	}

	// Cleanup is best-effort: it walks at most the 200 oldest files, so it can
	// finish without having made room. Reporting success here would let the
	// channel grow past its quota indefinitely.
	if needToFree > 0 {
		releaseStorageReservation(ctx, slug, newFileSize)
		return fmt.Errorf("storage quota exceeded (%d/%d bytes); auto-cleanup could not free enough space", used-newFileSize, quota)
	}

	return nil
}

// fileHashLockKey marks a last-reference removal of a blob in progress. Blobs
// are shared by hash across tenants, and an upload of the same bytes claims
// its reference before looking for the blob (see uploadFile): one that claimed
// between the delete's DECR and its physical removal found the object, adopted
// it, and was left with a record that answered 404 for good while its bytes
// stayed charged to the channel. The delete holds this key while the blob
// goes and the upload waits for it to clear before deciding whether the blob
// needs writing. The TTL only bounds a lock leaked by a crash.
func fileHashLockKey(hash string) string { return "file:hash:" + hash + ":lock" }

const fileHashLockTTL = 30 * time.Second

// waitFileHashUnlocked pauses while a last-reference delete of hash is in
// progress, for a few seconds at most: the wait is the rare case, and an
// upload must not hang on a leaked lock for its whole TTL.
func waitFileHashUnlocked(ctx context.Context, hash string) {
	for i := 0; i < 50; i++ {
		n, err := rdb.Exists(ctx, fileHashLockKey(hash)).Result()
		if err != nil || n == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// deleteFileByID marks a file as deleted, decrements storage counter, and removes
// from R2/disk if no more refs. removeFromIndex controls whether the channel's
// file tracking set is updated; dbDeleteChannel passes false because it drops
// the whole set itself (and a per-file O(N) scan would make deletion O(N^2)).
// It returns the number of bytes it actually released, which is 0 whenever it
// no-ops — an unreadable record, one already deleted, or a claim lost to a
// concurrent caller. Callers that are freeing space to make room must count
// that return value rather than the size they hoped to free.
// deleteFileByID releases one file: tombstones its metadata, gives its bytes
// back to the channel quota, drops its hash reference and removes the blob
// once the last reference is gone. It returns the bytes freed by this call
// and whether the file is released at all — now, or earlier (already
// tombstoned, or no metadata anywhere). released is false on a failed read,
// a failed claim or a claim held by a concurrent deleter: callers that go on
// to delete the file's own records must not do so in that case, or the blob
// and its reference count are orphaned with nothing left that points at them.
func deleteFileByID(ctx context.Context, slug, fileID string, removeFromIndex bool) (freed int64, released bool) {
	meta, err := dbGetFileMetadata(ctx, fileID)
	if err != nil {
		return 0, errors.Is(err, errFileNotFound)
	}
	if meta.Delete {
		return 0, true
	}

	// Claim the deletion atomically: two concurrent callers (e.g. parallel
	// near-quota uploads running auto-cleanup) would otherwise both pass the
	// meta.Delete check and double-decrement used_bytes and the hash refcount,
	// deleting a blob another channel still references.
	claimed, err := rdb.SetNX(ctx, "file:"+fileID+":deleting", 1, time.Minute).Result()
	if err != nil || !claimed {
		return 0, false
	}

	meta.Delete = true
	dbSaveFileMetadata(ctx, meta)
	dbDecrChannelStorageUsed(ctx, slug, meta.Size)
	if removeFromIndex {
		dbRemoveChannelFile(ctx, slug, fileID, meta.Size)
	}

	// Decrement hash refs and delete from storage if no more references
	refs, err := dbDecrFileHashRefs(ctx, meta.Hash)
	if err == nil && refs <= 0 {
		// Hold the per-hash lock while the blob goes (see fileHashLockKey),
		// then re-read the counter: an upload that claimed the hash between
		// the DECR above and the lock is not waiting on it and would find the
		// object and adopt it — so if anyone holds a reference now, the blob
		// and its counter are theirs and stay. The lock itself is best
		// effort: a failed SETNX only reopens the old window.
		lockKey := fileHashLockKey(meta.Hash)
		locked, _ := rdb.SetNX(ctx, lockKey, 1, fileHashLockTTL).Result()
		if n, gerr := dbGetFileHashRefs(ctx, meta.Hash); gerr == nil && n > 0 {
			log.Printf("deleteFileByID: %s: hash %s was claimed by an upload while %s was being deleted; keeping the blob\n", slug, meta.Hash, fileID)
		} else {
			if r2Enabled && len(meta.Hash) >= 4 {
				if err := r2Delete(ctx, r2ObjectKey(meta.Hash)); err != nil {
					log.Printf("deleteFileByID: %s: R2 delete of %s (key %s) failed: %v\n", slug, fileID, r2ObjectKey(meta.Hash), err)
				}
			}
			// The local copy is removed either way: with R2 on it is the migration's
			// fallback copy of the same blob, and the last reference is gone.
			if p, ok := localBlobPath(meta.Hash); ok {
				os.Remove(p)
			}
			// Drop the counter itself, so a fresh upload of the same hash starts
			// at 1 again — unless an upload claimed the hash after the re-read
			// above. It is waiting on the lock and will write the blob anew,
			// and an unconditional DEL here wiped its reference: the next
			// same-hash delete then removed a blob that record still served.
			// The removal is conditional on the count, atomically.
			if kept, derr := dbDelFileHashRefs(ctx, meta.Hash); derr == nil && kept > 0 {
				log.Printf("deleteFileByID: %s: hash %s was claimed by %d upload(s) while the blob of %s was being removed; keeping the counter\n", slug, meta.Hash, kept, fileID)
			}
		}
		if locked {
			// On a fresh context: the caller's may have expired mid-delete, and
			// a lingering lock stalls every same-hash upload for its TTL.
			uctx, ucancel := context.WithTimeout(context.Background(), 5*time.Second)
			rdb.Del(uctx, lockKey)
			ucancel()
		}
	}
	return meta.Size, true
}

func generatedFileHash(file io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func generatedRandomID(length int) string {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// serveDefaultFavicon answers with the platform icon. The runtime image did
// not ship assets/favicon.ico (only the build stage had it), so every tab
// showed no icon and the log filled with 404s; the frontend bundle in the
// static folder always carries one, so it is the fallback.
func serveDefaultFavicon(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat("assets/favicon.ico"); err == nil {
		http.ServeFile(w, r, "assets/favicon.ico")
		return
	}
	if cfg := getGlobalConfig(); cfg != nil && cfg.RootStaticFolder != "" {
		http.ServeFile(w, r, filepath.Join(cfg.RootStaticFolder, "favicon.ico"))
		return
	}
	http.NotFound(w, r)
}

func getFavicon(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := r.URL.Query().Get("slug")
	if slug == "" {
		serveDefaultFavicon(w, r)
		return
	}
	// This handler sits outside channelMiddleware, so it is the one place a raw
	// ?slug= still reached Redis unchecked; a hash-typed key such as
	// "foo:messages:5" would resolve as a phantom channel. Same guard as the
	// middleware, same answer as "not found" (no oracle).
	if !isPlausibleSlug(slug) {
		serveDefaultFavicon(w, r)
		return
	}

	c, err := getChannelDetails(ctx, slug)
	if err != nil {
		serveDefaultFavicon(w, r)
		return
	}

	// This route is mounted at the router root, so none of channelMiddleware's
	// disabled check, channelIfRequireAuth or channelIfRequireAuthFiles runs —
	// yet it serves a blob out of the channel's own file store. A favicon
	// request cannot carry a meaningful session (browsers fetch it out of band),
	// so instead of trying to authenticate it, a channel that has asked not to
	// be readable anonymously simply gets the platform default. That leaks
	// neither the logo nor the fact that the channel exists.
	if ch, chErr := dbGetChannel(ctx, slug); chErr != nil ||
		ch.Features.Disabled || ch.Features.RequireAuth || ch.Features.RequireAuthFiles {
		serveDefaultFavicon(w, r)
		return
	}

	logoUrl := c["logoUrl"]
	if logoUrl == "" {
		serveDefaultFavicon(w, r)
		return
	}

	fileId := path.Base(logoUrl)
	if len(fileId) < 4 {
		serveDefaultFavicon(w, r)
		return
	}

	meta, err := dbGetFileMetadata(ctx, fileId)
	// The logo URL is arbitrary moderator-supplied input, so the same channel
	// isolation serveFile applies must hold here too.
	if err != nil || meta.Delete || !fileVisibleToChannel(meta, slug) {
		serveDefaultFavicon(w, r)
		return
	}

	// Only an image is streamed from here. This route is anonymous, mounted at
	// the root, and — unlike serveFile — sends the blob inline: uploads accept
	// any file type, and the logo URL is whatever a moderator saved, so it
	// could name an uploaded HTML file, which the browser would then render on
	// the app's own origin with the viewer's session — stored XSS. The R2
	// redirect below is exempt: R2 serves it from another origin.
	if r2Enabled && len(meta.Hash) >= 4 {
		key := r2ObjectKey(meta.Hash)
		// Same partial-migration fallback as serveFile: a logo whose blob has
		// not been copied to R2 yet is still on local disk.
		if !r2ExistsCached(ctx, key) {
			if filePath, ok := localBlobExists(meta.Hash); ok {
				log.Printf("getFavicon: %s: logo %s not in R2 (key %s), serving local copy\n", slug, fileId, key)
				serveLocalImageOrFavicon(w, r, meta, filePath)
				return
			}
			log.Printf("getFavicon: %s: logo %s missing in R2 (key %s) and on local disk\n", slug, fileId, key)
			serveDefaultFavicon(w, r)
			return
		}
		if r2PublicURL != "" {
			http.Redirect(w, r, r2PublicURL+"/"+key, http.StatusFound)
			return
		}
		body, contentType, err := r2Download(ctx, key)
		if err != nil {
			log.Printf("getFavicon: %s: R2 download of logo %s (key %s) failed: %v\n", slug, fileId, key, err)
			serveDefaultFavicon(w, r)
			return
		}
		defer body.Close()
		br := bufio.NewReader(body)
		head, _ := br.Peek(512)
		if !isImageBlob(meta, head) {
			serveDefaultFavicon(w, r)
			return
		}
		ct := http.DetectContentType(head)
		if contentType != nil && strings.HasPrefix(*contentType, "image/") {
			ct = *contentType
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		io.Copy(w, br)
		return
	}

	filePath, ok := localBlobPath(meta.Hash)
	if !ok {
		log.Printf("getFavicon: %s: logo %s has malformed hash %q\n", slug, fileId, meta.Hash)
		serveDefaultFavicon(w, r)
		return
	}
	serveLocalImageOrFavicon(w, r, meta, filePath)
}

// isImageBlob reports whether a blob may be sent inline as an image: typed
// "image" at upload (by magic bytes), or — for a record written before the
// type was stored — sniffed as one from its first bytes. SVG never passes:
// neither the upload typer nor DetectContentType classify it as an image,
// and it can carry script.
func isImageBlob(meta *FileMetadata, head []byte) bool {
	if meta.Type == "image" {
		return true
	}
	return meta.Type == "" && strings.HasPrefix(http.DetectContentType(head), "image/")
}

// serveLocalImageOrFavicon serves a local blob inline if it is an image and
// the default favicon otherwise (see getFavicon).
func serveLocalImageOrFavicon(w http.ResponseWriter, r *http.Request, meta *FileMetadata, filePath string) {
	f, err := os.Open(filePath)
	if err != nil {
		serveDefaultFavicon(w, r)
		return
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	f.Close()
	if !isImageBlob(meta, head[:n]) {
		serveDefaultFavicon(w, r)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(head[:n]))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, filePath)
}

// uploadSlots bounds how many uploads are buffered at once (see uploadFile).
var uploadSlots = make(chan struct{}, 4)

// storageAlreadyFull reports whether the channel is at or over its quota with
// no way to make room, so an upload can be refused before its body is read.
// Errors read as "not full": the authoritative check is reserveStorageQuota,
// which fails closed later.
func storageAlreadyFull(ctx context.Context, slug string) bool {
	quota, err := dbGetEffectiveStorageQuota(ctx, slug)
	if err != nil || quota == 0 {
		return false
	}
	used, err := dbGetChannelStorageUsed(ctx, slug)
	if err != nil || used < quota {
		return false
	}
	autoCleanup, err := dbGetChannelAutoCleanup(ctx, slug)
	return err == nil && !autoCleanup
}
