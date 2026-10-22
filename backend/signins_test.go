package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSignInsExportJoinsEverySource(t *testing.T) {
	ctx := testCtx(t)
	seedChannel(t, ctx, "signins-ch", Settings{})

	const (
		reader = "signins-reader@example.com"
		legacy = "signins-legacy@example.com"
		owner  = "signins-owner@example.com"
	)
	chKey := "channel:signins-ch:registered_emails"
	rdb.SAdd(ctx, "registered_emails", reader, legacy, owner)
	rdb.SAdd(ctx, chKey, reader)
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rdb.SRem(cctx, "registered_emails", reader, legacy, owner)
		rdb.Del(cctx, chKey)
		rdb.HDel(cctx, signInsKey, reader, legacy, owner)
		rdb.HDel(cctx, ownerConsentsKey, owner)
	})

	recordSignIn(reader, "קורא ראשון")
	first, _ := rdb.HGet(ctx, signInsKey, reader).Result()
	time.Sleep(10 * time.Millisecond)
	recordSignIn(reader, "קורא ראשון")
	if _, err := dbSaveOwnerConsent(ctx, owner, "+בעלים", true, "channel_create"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	exportSignIns(rec, httptest.NewRequest(http.MethodGet, "/api/super-admin/signins.csv", nil))
	body := rec.Body.String()

	var readerLine string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, reader+",") {
			readerLine = l
		}
	}
	if !strings.Contains(readerLine, ",קורא ראשון,") || !strings.Contains(readerLine, ",signins-ch,") {
		t.Errorf("reader row = %q, want name and channel", readerLine)
	}
	if !strings.Contains(first, `"first"`) || strings.Count(readerLine, "T") < 2 {
		t.Errorf("reader row lacks sign-in times: %q", readerLine)
	}
	if !strings.Contains(body, "\n"+legacy+",,,,,\n") && !strings.Contains(body, "\n"+legacy+",,,,,\r\n") {
		t.Errorf("an address from before the sign-in log must still be listed:\n%s", body)
	}
	if !strings.Contains(body, owner+",'+בעלים,,,,yes") {
		t.Errorf("owner row must carry the consent and a neutralised name:\n%s", body)
	}
}
