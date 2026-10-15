package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Pre-signing is local, so a client with fake credentials is enough.
func withFakeR2(t *testing.T) {
	t.Helper()
	prevClient, prevBucket := r2Client, r2Bucket
	r2Client = s3.New(s3.Options{
		Region:       "auto",
		Credentials:  credentials.NewStaticCredentialsProvider("ak", "sk", ""),
		BaseEndpoint: aws.String("https://acct.r2.cloudflarestorage.com"),
		UsePathStyle: true,
	})
	r2Bucket = "bucket"
	t.Cleanup(func() {
		r2Client, r2Bucket = prevClient, prevBucket
		r2CacheMu.Lock()
		r2PresignCache = map[string]r2PresignEntry{}
		r2PresentCache = map[string]time.Time{}
		r2CacheMu.Unlock()
	})
}

func TestR2PresignCachedReusesURL(t *testing.T) {
	withFakeR2(t)
	ctx := context.Background()
	u1, left1, err := r2PresignCached(ctx, "files/aa/bb/aabb", "attachment")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u1, "response-cache-control=") {
		t.Fatalf("signed URL carries no cache header: %s", u1)
	}
	if left1 != r2PresignReuse {
		t.Fatalf("fresh URL: left %v, want %v", left1, r2PresignReuse)
	}
	u2, left2, err := r2PresignCached(ctx, "files/aa/bb/aabb", "attachment")
	if err != nil || u2 != u1 {
		t.Fatalf("second call signed a new URL (%v): %s vs %s", err, u2, u1)
	}
	if left2 > left1 || left2 <= time.Minute {
		t.Fatalf("reused URL: left %v", left2)
	}
	if u3, _, _ := r2PresignCached(ctx, "files/aa/bb/aabb", "other"); u3 == u1 {
		t.Fatal("a different disposition reused the same URL")
	}

	// An entry close to the end of its reuse window is replaced.
	r2CacheMu.Lock()
	e := r2PresignCache["files/aa/bb/aabb\x00attachment"]
	e.at = time.Now().Add(-r2PresignReuse + 30*time.Second)
	e.url = "stale"
	r2PresignCache["files/aa/bb/aabb\x00attachment"] = e
	r2CacheMu.Unlock()
	if u4, _, _ := r2PresignCached(ctx, "files/aa/bb/aabb", "attachment"); u4 == "stale" {
		t.Fatal("an almost expired URL was handed out")
	}
}

func TestR2ForgetKey(t *testing.T) {
	withFakeR2(t)
	ctx := context.Background()
	r2CacheMu.Lock()
	r2PresentCache["files/aa/bb/aabb"] = time.Now()
	r2PresentCache["files/aa/bb/aabbcc"] = time.Now()
	r2CacheMu.Unlock()
	r2PresignCached(ctx, "files/aa/bb/aabb", "x")
	r2PresignCached(ctx, "files/aa/bb/aabbcc", "x")

	r2ForgetKey("files/aa/bb/aabb")

	r2CacheMu.Lock()
	defer r2CacheMu.Unlock()
	if _, ok := r2PresentCache["files/aa/bb/aabb"]; ok {
		t.Fatal("presence survived the delete")
	}
	if _, ok := r2PresignCache["files/aa/bb/aabb\x00x"]; ok {
		t.Fatal("signed URL survived the delete")
	}
	if _, ok := r2PresentCache["files/aa/bb/aabbcc"]; !ok {
		t.Fatal("another key was evicted")
	}
	if _, ok := r2PresignCache["files/aa/bb/aabbcc\x00x"]; !ok {
		t.Fatal("another key's URL was evicted")
	}
}
