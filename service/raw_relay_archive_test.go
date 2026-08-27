package service

import (
	"context"
	"errors"
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
	started   chan struct{}
	block     chan struct{}
	startOnce sync.Once
}

func (f *fakeRawRelayInserter) Insert(_ context.Context, exchange RawRelayExchange) error {
	if f.started != nil {
		f.startOnce.Do(func() { close(f.started) })
	}
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.exchanges = append(f.exchanges, exchange)
	return nil
}

func (f *fakeRawRelayInserter) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func TestRawRelayArchiverWritesExactBodiesDirectly(t *testing.T) {
	inserter := &fakeRawRelayInserter{}
	archiver, err := newRawRelayArchiver(inserter, time.Second, 1)
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
	require.NoError(t, archiver.close(context.Background()))
	require.Equal(t, []RawRelayExchange{want}, inserter.exchanges)
}

func TestRawRelayArchiverHandlesInsertFailureInBackground(t *testing.T) {
	inserter := &fakeRawRelayInserter{err: errors.New("clickhouse unavailable")}
	archiver, err := newRawRelayArchiver(inserter, time.Second, 1)
	require.NoError(t, err)

	require.NoError(t, archiver.store(RawRelayExchange{
		EventTime: time.Now(),
		RequestID: "req-direct-write-test",
	}))
	require.NoError(t, archiver.close(context.Background()))
}

func TestRawRelayArchiverDefaultsCaptureStatus(t *testing.T) {
	inserter := &fakeRawRelayInserter{}
	archiver, err := newRawRelayArchiver(inserter, time.Second, 1)
	require.NoError(t, err)

	require.NoError(t, archiver.store(RawRelayExchange{RequestID: "req-default-status"}))
	require.NoError(t, archiver.close(context.Background()))
	require.Equal(t, "complete", inserter.exchanges[0].CaptureStatus)
}

func TestRawRelayArchiverDoesNotBlockRequestOnInsert(t *testing.T) {
	started := make(chan struct{})
	block := make(chan struct{})
	inserter := &fakeRawRelayInserter{started: started, block: block}
	archiver, err := newRawRelayArchiver(inserter, time.Second, 1)
	require.NoError(t, err)

	require.NoError(t, archiver.store(RawRelayExchange{RequestID: "req-async-test"}))
	<-started
	close(block)
	require.NoError(t, archiver.close(context.Background()))
}
