package service

import (
	"context"
	"errors"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeRawRelayInserter struct {
	mu        sync.Mutex
	exchanges []RawRelayExchange
	err       error
	closed    bool
}

func (f *fakeRawRelayInserter) Insert(_ context.Context, exchanges []RawRelayExchange) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.exchanges = append(f.exchanges, exchanges...)
	return nil
}

func (f *fakeRawRelayInserter) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func TestRawRelayArchiverDurablyStoresAndCommitsExactBodies(t *testing.T) {
	spoolDir := t.TempDir()
	inserter := &fakeRawRelayInserter{}
	archiver, err := newRawRelayArchiver(spoolDir, 10, time.Hour, inserter)
	require.NoError(t, err)

	want := RawRelayExchange{
		EventTime:     time.Date(2026, 8, 10, 12, 0, 0, 123000000, time.FixedZone("CST", 8*60*60)),
		DurationMs:    456,
		RequestID:     "req-archive-test",
		UserID:        12,
		TokenID:       34,
		GroupName:     "vip",
		Method:        "POST",
		RequestPath:   "/v1/chat/completions",
		ModelName:     "gpt-test",
		ChannelIDs:    []int32{7, 8},
		StatusCode:    200,
		RequestBody:   []byte{'{', '}', 0, 0xff},
		ResponseBody:  []byte("data: {\"ok\":true}\n\ndata: [DONE]\n\n"),
		RequestBytes:  4,
		ResponseBytes: 36,
		CaptureStatus: "complete",
	}
	require.NoError(t, archiver.store(want))

	entries, err := os.ReadDir(spoolDir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "record must exist on disk before ClickHouse acknowledgement")
	require.True(t, slices.ContainsFunc(entries, func(entry os.DirEntry) bool { return !entry.IsDir() }))

	count, err := archiver.processBatch(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Len(t, inserter.exchanges, 1)
	got := inserter.exchanges[0]
	require.True(t, want.EventTime.Equal(got.EventTime))
	want.EventTime = time.Time{}
	got.EventTime = time.Time{}
	require.Equal(t, want, got)

	entries, err = os.ReadDir(spoolDir)
	require.NoError(t, err)
	require.Empty(t, entries, "spool file is removed only after ClickHouse acknowledgement")
}

func TestRawRelayArchiverKeepsSpoolWhenInsertFails(t *testing.T) {
	spoolDir := t.TempDir()
	inserter := &fakeRawRelayInserter{err: errors.New("clickhouse unavailable")}
	archiver, err := newRawRelayArchiver(spoolDir, 10, time.Hour, inserter)
	require.NoError(t, err)
	require.NoError(t, archiver.store(RawRelayExchange{
		EventTime: time.Now(),
		RequestID: "req-retry-test",
	}))

	_, err = archiver.processBatch(context.Background())
	require.ErrorContains(t, err, "clickhouse unavailable")
	entries, readErr := os.ReadDir(spoolDir)
	require.NoError(t, readErr)
	require.Len(t, entries, 1, "failed batches must remain durable for retry")
}
