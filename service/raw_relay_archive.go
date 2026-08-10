package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/QuantumNous/new-api/common"
)

const (
	rawRelayArchiveTable       = "ck.ooioo_raw_relay_exchanges"
	defaultRawRelaySpoolDir    = "/data/raw-relay-archive"
	defaultRawRelayBatchSize   = 50
	defaultRawRelayFlushSecond = 2
)

const rawRelayArchiveInsertSQL = `INSERT INTO ` + rawRelayArchiveTable + ` (
	event_time, duration_ms, request_id, user_id, token_id, group_name,
	method, request_path, model_name, channel_ids, status_code,
	request_body, response_body, request_bytes, response_bytes,
	capture_status, capture_error
) VALUES`

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

type rawRelayBatchInserter interface {
	Insert(context.Context, []RawRelayExchange) error
	Close() error
}

type clickHouseRawRelayInserter struct {
	conn driver.Conn
}

func (i *clickHouseRawRelayInserter) Insert(ctx context.Context, exchanges []RawRelayExchange) error {
	batch, err := i.conn.PrepareBatch(ctx, rawRelayArchiveInsertSQL)
	if err != nil {
		return err
	}
	for _, exchange := range exchanges {
		if err := batch.Append(
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
		); err != nil {
			return err
		}
	}
	return batch.Send()
}

func (i *clickHouseRawRelayInserter) Close() error {
	return i.conn.Close()
}

type rawRelayArchiver struct {
	spoolDir     string
	batchSize    int
	flushEvery   time.Duration
	inserter     rawRelayBatchInserter
	stop         chan struct{}
	done         chan struct{}
	closeOnce    sync.Once
	lastErrorLog time.Time
	logMu        sync.Mutex
}

var (
	rawRelayArchiveMu      sync.RWMutex
	activeRawRelayArchiver *rawRelayArchiver
)

// InitRawRelayArchive starts the durable spool worker when explicitly enabled.
// A ClickHouse outage does not fail application startup: completed exchanges
// remain in the spool and are retried in the background.
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
	options.ReadTimeout = 15 * time.Second
	options.MaxOpenConns = 4
	options.MaxIdleConns = 2
	options.ConnMaxLifetime = 30 * time.Minute
	if options.Compression == nil {
		options.Compression = &clickhouse.Compression{Method: clickhouse.CompressionLZ4}
	}

	spoolDir := common.GetEnvOrDefaultString("RAW_RELAY_ARCHIVE_SPOOL_DIR", defaultRawRelaySpoolDir)
	batchSize := common.GetEnvOrDefault("RAW_RELAY_ARCHIVE_BATCH_SIZE", defaultRawRelayBatchSize)
	if batchSize <= 0 {
		batchSize = defaultRawRelayBatchSize
	}
	flushSeconds := common.GetEnvOrDefault("RAW_RELAY_ARCHIVE_FLUSH_SECONDS", defaultRawRelayFlushSecond)
	if flushSeconds <= 0 {
		flushSeconds = defaultRawRelayFlushSecond
	}

	conn, err := clickhouse.Open(options)
	if err != nil {
		return fmt.Errorf("open raw relay archive ClickHouse connection: %w", err)
	}
	archiver, err := newRawRelayArchiver(
		spoolDir,
		batchSize,
		time.Duration(flushSeconds)*time.Second,
		&clickHouseRawRelayInserter{conn: conn},
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
	archiver.start()
	common.SysLog("raw relay archive enabled with durable ClickHouse spool")
	return nil
}

func newRawRelayArchiver(spoolDir string, batchSize int, flushEvery time.Duration, inserter rawRelayBatchInserter) (*rawRelayArchiver, error) {
	if strings.TrimSpace(spoolDir) == "" {
		return nil, errors.New("raw relay archive spool directory is empty")
	}
	if inserter == nil {
		return nil, errors.New("raw relay archive inserter is nil")
	}
	if err := os.MkdirAll(spoolDir, 0o700); err != nil {
		return nil, fmt.Errorf("create raw relay archive spool directory: %w", err)
	}
	if err := os.Chmod(spoolDir, 0o700); err != nil {
		return nil, fmt.Errorf("secure raw relay archive spool directory: %w", err)
	}
	return &rawRelayArchiver{
		spoolDir:   spoolDir,
		batchSize:  batchSize,
		flushEvery: flushEvery,
		inserter:   inserter,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}, nil
}

func RawRelayArchiveEnabled() bool {
	rawRelayArchiveMu.RLock()
	defer rawRelayArchiveMu.RUnlock()
	return activeRawRelayArchiver != nil
}

// StoreRawRelayExchange durably spools a completed exchange before returning.
// The ClickHouse worker deletes the file only after the batch is acknowledged.
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
	payload, err := common.Marshal(exchange)
	if err != nil {
		return fmt.Errorf("marshal raw relay exchange: %w", err)
	}
	tmp, err := os.CreateTemp(a.spoolDir, ".raw-relay-*.tmp")
	if err != nil {
		return fmt.Errorf("create raw relay exchange spool file: %w", err)
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		_ = tmp.Close()
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod raw relay exchange spool file: %w", err)
	}
	if _, err := tmp.Write(payload); err != nil {
		return fmt.Errorf("write raw relay exchange spool file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync raw relay exchange spool file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close raw relay exchange spool file: %w", err)
	}
	finalName := fmt.Sprintf("%020d-%s.raw", exchange.EventTime.UnixNano(), filepath.Base(tmpPath))
	finalPath := filepath.Join(a.spoolDir, finalName)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("commit raw relay exchange spool file: %w", err)
	}
	removeTmp = false
	if dir, err := os.Open(a.spoolDir); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func (a *rawRelayArchiver) start() {
	go a.run()
}

func (a *rawRelayArchiver) run() {
	defer close(a.done)
	ticker := time.NewTicker(a.flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			a.drainAvailable(context.Background())
		case <-a.stop:
			return
		}
	}
}

func (a *rawRelayArchiver) drainAvailable(ctx context.Context) {
	for {
		count, err := a.processBatch(ctx)
		if err != nil {
			a.logWorkerError(err)
			return
		}
		if count < a.batchSize {
			return
		}
	}
}

func (a *rawRelayArchiver) processBatch(ctx context.Context) (int, error) {
	entries, err := os.ReadDir(a.spoolDir)
	if err != nil {
		return 0, fmt.Errorf("read raw relay archive spool: %w", err)
	}
	paths := make([]string, 0, a.batchSize)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".raw") {
			continue
		}
		paths = append(paths, filepath.Join(a.spoolDir, entry.Name()))
	}
	sort.Strings(paths)
	if len(paths) > a.batchSize {
		paths = paths[:a.batchSize]
	}
	if len(paths) == 0 {
		return 0, nil
	}

	exchanges := make([]RawRelayExchange, 0, len(paths))
	validPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		payload, err := os.ReadFile(path)
		if err != nil {
			return 0, fmt.Errorf("read raw relay archive record: %w", err)
		}
		var exchange RawRelayExchange
		if err := common.Unmarshal(payload, &exchange); err != nil {
			badPath := strings.TrimSuffix(path, ".raw") + ".bad"
			if renameErr := os.Rename(path, badPath); renameErr != nil {
				return 0, fmt.Errorf("quarantine invalid raw relay archive record: %w", renameErr)
			}
			a.logWorkerError(fmt.Errorf("invalid raw relay archive record moved to %s: %w", filepath.Base(badPath), err))
			continue
		}
		exchanges = append(exchanges, exchange)
		validPaths = append(validPaths, path)
	}
	if len(exchanges) == 0 {
		return len(paths), nil
	}
	if err := a.inserter.Insert(ctx, exchanges); err != nil {
		return 0, fmt.Errorf("insert raw relay archive batch: %w", err)
	}
	for _, path := range validPaths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("remove committed raw relay archive record: %w", err)
		}
	}
	return len(paths), nil
}

func (a *rawRelayArchiver) logWorkerError(err error) {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	if time.Since(a.lastErrorLog) < 30*time.Second {
		return
	}
	a.lastErrorLog = time.Now()
	common.SysError("raw relay archive worker: " + err.Error())
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
	a.closeOnce.Do(func() { close(a.stop) })
	select {
	case <-a.done:
	case <-ctx.Done():
		_ = a.inserter.Close()
		return ctx.Err()
	}

	for {
		count, err := a.processBatch(ctx)
		if err != nil {
			_ = a.inserter.Close()
			return err
		}
		if count < a.batchSize {
			break
		}
	}
	return a.inserter.Close()
}
