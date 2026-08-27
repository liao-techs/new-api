package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/QuantumNous/new-api/common"
)

const (
	rawRelayArchiveTable                = "ck.ooioo_raw_relay_exchanges"
	defaultRawRelayInsertTimeoutSeconds = 15
	defaultRawRelayMaxConcurrent        = 16
)

const rawRelayArchiveInsertSQL = `INSERT INTO ` + rawRelayArchiveTable + ` (
	event_time, duration_ms, request_id, user_id, token_id, group_name,
	method, request_path, model_name, channel_ids, status_code,
	request_body, response_body, request_bytes, response_bytes,
	capture_status, capture_error
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// RawRelayExchange is the exact request/response pair captured for one client
// relay request. RequestID is the canonical join key to the existing logs table.
type RawRelayExchange struct {
	EventTime     time.Time `json:"event_time"`
	DurationMs    uint64    `json:"duration_ms"`
	RequestID     string    `json:"request_id"`
	UserID        int32     `json:"user_id"`
	TokenID       int32     `json:"token_id"`
	GroupName     string    `json:"group_name"`
	Method        string    `json:"method"`
	RequestPath   string    `json:"request_path"`
	ModelName     string    `json:"model_name"`
	ChannelIDs    []int32   `json:"channel_ids"`
	StatusCode    uint16    `json:"status_code"`
	RequestBody   []byte    `json:"request_body"`
	ResponseBody  []byte    `json:"response_body"`
	RequestBytes  uint64    `json:"request_bytes"`
	ResponseBytes uint64    `json:"response_bytes"`
	CaptureStatus string    `json:"capture_status"`
	CaptureError  string    `json:"capture_error"`
}

type rawRelayInserter interface {
	Insert(context.Context, RawRelayExchange) error
	Close() error
}

type clickHouseRawRelayInserter struct {
	conn driver.Conn
}

func (i *clickHouseRawRelayInserter) Insert(ctx context.Context, exchange RawRelayExchange) error {
	return i.conn.Exec(
		ctx,
		rawRelayArchiveInsertSQL,
		exchange.EventTime,
		exchange.DurationMs,
		exchange.RequestID,
		exchange.UserID,
		exchange.TokenID,
		exchange.GroupName,
		exchange.Method,
		exchange.RequestPath,
		exchange.ModelName,
		exchange.ChannelIDs,
		exchange.StatusCode,
		string(exchange.RequestBody),
		string(exchange.ResponseBody),
		exchange.RequestBytes,
		exchange.ResponseBytes,
		exchange.CaptureStatus,
		exchange.CaptureError,
	)
}

func (i *clickHouseRawRelayInserter) Close() error {
	return i.conn.Close()
}

type rawRelayArchiver struct {
	inserter      rawRelayInserter
	insertTimeout time.Duration
	insertSlots   chan struct{}
	pending       sync.WaitGroup
	stateMu       sync.Mutex
	closing       bool
	logMu         sync.Mutex
	lastErrorLog  time.Time
	closeOnce     sync.Once
}

var (
	rawRelayArchiveMu      sync.RWMutex
	activeRawRelayArchiver *rawRelayArchiver
)

// InitRawRelayArchive initializes the direct ClickHouse writer. The table is a
// ClickHouse Buffer table, so batching and flushing are handled server-side.
func InitRawRelayArchive() error {
	if !common.GetEnvOrDefaultBool("RAW_RELAY_ARCHIVE_ENABLED", false) {
		return nil
	}

	dsn := strings.TrimSpace(os.Getenv("RAW_RELAY_ARCHIVE_CLICKHOUSE_DSN"))
	if dsn == "" {
		return errors.New("RAW_RELAY_ARCHIVE_CLICKHOUSE_DSN is required when raw relay archive is enabled")
	}
	options, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return fmt.Errorf("parse raw relay archive ClickHouse DSN: %w", err)
	}
	options.DialTimeout = 5 * time.Second
	options.ReadTimeout = 30 * time.Second
	options.MaxOpenConns = 16
	options.MaxIdleConns = 4
	options.ConnMaxLifetime = 30 * time.Minute
	if options.Compression == nil {
		options.Compression = &clickhouse.Compression{Method: clickhouse.CompressionLZ4}
	}

	insertTimeoutSeconds := common.GetEnvOrDefault(
		"RAW_RELAY_ARCHIVE_INSERT_TIMEOUT_SECONDS",
		defaultRawRelayInsertTimeoutSeconds,
	)
	if insertTimeoutSeconds <= 0 {
		insertTimeoutSeconds = defaultRawRelayInsertTimeoutSeconds
	}
	insertTimeout := time.Duration(insertTimeoutSeconds) * time.Second
	maxConcurrent := common.GetEnvOrDefault(
		"RAW_RELAY_ARCHIVE_MAX_CONCURRENT_INSERTS",
		defaultRawRelayMaxConcurrent,
	)
	if maxConcurrent <= 0 {
		maxConcurrent = defaultRawRelayMaxConcurrent
	}

	conn, err := clickhouse.Open(options)
	if err != nil {
		return fmt.Errorf("open raw relay archive ClickHouse connection: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(context.Background(), insertTimeout)
	err = conn.Ping(pingCtx)
	cancel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("ping raw relay archive ClickHouse: %w", err)
	}

	archiver, err := newRawRelayArchiver(
		&clickHouseRawRelayInserter{conn: conn},
		insertTimeout,
		maxConcurrent,
	)
	if err != nil {
		_ = conn.Close()
		return err
	}

	rawRelayArchiveMu.Lock()
	if activeRawRelayArchiver != nil {
		rawRelayArchiveMu.Unlock()
		_ = archiver.inserter.Close()
		return errors.New("raw relay archive is already initialized")
	}
	activeRawRelayArchiver = archiver
	rawRelayArchiveMu.Unlock()
	common.SysLog("raw relay archive enabled with asynchronous ClickHouse Buffer writes")
	return nil
}

func newRawRelayArchiver(inserter rawRelayInserter, insertTimeout time.Duration, maxConcurrent int) (*rawRelayArchiver, error) {
	if inserter == nil {
		return nil, errors.New("raw relay archive inserter is nil")
	}
	if insertTimeout <= 0 {
		return nil, errors.New("raw relay archive insert timeout must be positive")
	}
	if maxConcurrent <= 0 {
		return nil, errors.New("raw relay archive max concurrent inserts must be positive")
	}
	return &rawRelayArchiver{
		inserter:      inserter,
		insertTimeout: insertTimeout,
		insertSlots:   make(chan struct{}, maxConcurrent),
	}, nil
}

func RawRelayArchiveEnabled() bool {
	rawRelayArchiveMu.RLock()
	defer rawRelayArchiveMu.RUnlock()
	return activeRawRelayArchiver != nil
}

// StoreRawRelayExchange hands one completed exchange to a background insert.
// The request handler never waits for a ClickHouse connection or response.
func StoreRawRelayExchange(exchange RawRelayExchange) error {
	rawRelayArchiveMu.RLock()
	archiver := activeRawRelayArchiver
	rawRelayArchiveMu.RUnlock()
	if archiver == nil {
		return nil
	}
	return archiver.store(exchange)
}

func (a *rawRelayArchiver) store(exchange RawRelayExchange) error {
	if exchange.CaptureStatus == "" {
		exchange.CaptureStatus = "complete"
	}

	a.stateMu.Lock()
	if a.closing {
		a.stateMu.Unlock()
		return errors.New("raw relay archive is shutting down")
	}
	a.pending.Add(1)
	a.stateMu.Unlock()

	go a.insert(exchange)
	return nil
}

func (a *rawRelayArchiver) insert(exchange RawRelayExchange) {
	defer a.pending.Done()
	a.insertSlots <- struct{}{}
	defer func() { <-a.insertSlots }()

	ctx, cancel := context.WithTimeout(context.Background(), a.insertTimeout)
	defer cancel()
	if err := a.inserter.Insert(ctx, exchange); err != nil {
		a.logInsertError(exchange.RequestID, err)
	}
}

func (a *rawRelayArchiver) logInsertError(requestID string, err error) {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	if time.Since(a.lastErrorLog) < 30*time.Second {
		return
	}
	a.lastErrorLog = time.Now()
	common.SysError(fmt.Sprintf("asynchronous raw relay archive insert failed for request %s: %v", requestID, err))
}

func CloseRawRelayArchive(ctx context.Context) error {
	rawRelayArchiveMu.Lock()
	archiver := activeRawRelayArchiver
	activeRawRelayArchiver = nil
	rawRelayArchiveMu.Unlock()
	if archiver == nil {
		return nil
	}
	return archiver.close(ctx)
}

func (a *rawRelayArchiver) close(ctx context.Context) error {
	a.stateMu.Lock()
	a.closing = true
	a.stateMu.Unlock()

	done := make(chan struct{})
	go func() {
		a.pending.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		_ = a.inserter.Close()
		return ctx.Err()
	}

	var closeErr error
	a.closeOnce.Do(func() {
		closeErr = a.inserter.Close()
	})
	return closeErr
}
