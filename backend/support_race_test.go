package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// A ticket is one JSON blob holding its whole thread, so an unguarded
// read-mutate-Set loses writes: two participants load the same thread, both
// append, and whichever SET lands second discards the other's message — while
// both callers get a 200. dbUpdateSupportTicket closes that with a
// WATCH-guarded transaction.
//
// This appends from many goroutines at once and asserts every message survives.
func TestSupportTicketConcurrentRepliesAllSurvive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const id = "sup-race-concurrent"
	const writers = 12

	now := time.Now()
	seed := &SupportTicket{
		ID: id, Subject: "race", Name: "tester", Email: "race@example.com",
		Status:      SupportStatusOpen,
		Messages:    []SupportMessage{{Author: "user", AuthorName: "tester", Body: "first", CreatedAt: now}},
		CreatedAt:   now,
		UpdatedAt:   now,
		AccessToken: "tok-race",
	}
	if err := dbSaveSupportTicket(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		rdb.Del(cctx, supportTicketKey(id))
		rdb.ZRem(cctx, supportTicketIndexKey, id)
		rdb.ZRem(cctx, supportUserIndexKey("race@example.com"), id)
	})

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)

	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		done.Add(1)
		go func(n int) {
			defer done.Done()
			start.Wait() // all of them collide on the same blob
			_, err := dbUpdateSupportTicket(ctx, id, func(cur *SupportTicket) error {
				appendSupportMessage(cur, "user", "tester", fmt.Sprintf("msg-%d", n), SupportStatusOpen)
				return nil
			})
			errs[n] = err
		}(i)
	}

	start.Done()
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("writer %d failed: %v", i, err)
		}
	}

	final, err := dbGetSupportTicket(ctx, id)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	// Seed message plus one per writer, with none lost to a clobbering write.
	if want := writers + 1; len(final.Messages) != want {
		t.Errorf("thread holds %d messages, want %d: concurrent replies are being lost",
			len(final.Messages), want)
	}

	seen := map[string]bool{}
	for _, m := range final.Messages {
		seen[m.Body] = true
	}
	for i := 0; i < writers; i++ {
		if body := fmt.Sprintf("msg-%d", i); !seen[body] {
			t.Errorf("message %q is missing from the thread", body)
		}
	}
}

// A mutator error must abort the write, so a condition judged against fresh
// state (a ticket closed since the handler read it) cannot be applied anyway.
func TestSupportTicketMutatorErrorAbortsWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const id = "sup-race-abort"
	now := time.Now()
	if err := dbSaveSupportTicket(ctx, &SupportTicket{
		ID: id, Subject: "abort", Name: "t", Email: "abort@example.com",
		Status:    SupportStatusClosed,
		Messages:  []SupportMessage{{Author: "user", Body: "only", CreatedAt: now}},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		rdb.Del(cctx, supportTicketKey(id))
		rdb.ZRem(cctx, supportTicketIndexKey, id)
		rdb.ZRem(cctx, supportUserIndexKey("abort@example.com"), id)
	})

	_, err := dbUpdateSupportTicket(ctx, id, func(cur *SupportTicket) error {
		if cur.Status == SupportStatusClosed {
			return errTicketClosed
		}
		appendSupportMessage(cur, "user", "t", "should not land", SupportStatusOpen)
		return nil
	})
	if err != errTicketClosed {
		t.Fatalf("got %v, want errTicketClosed", err)
	}

	after, err := dbGetSupportTicket(ctx, id)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(after.Messages) != 1 {
		t.Errorf("thread grew to %d messages despite the mutator aborting", len(after.Messages))
	}
}
