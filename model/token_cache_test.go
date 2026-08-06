package model

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type blockingTokenCacheWriteHook struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type failNextTokenCachePipelineHook struct {
	fail atomic.Bool
}

func (h *failNextTokenCachePipelineHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (h *failNextTokenCachePipelineHook) AfterProcess(context.Context, redis.Cmder) error {
	return nil
}

func (h *failNextTokenCachePipelineHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	if h.fail.CompareAndSwap(true, false) {
		return ctx, errors.New("forced token cache pipeline failure")
	}
	return ctx, nil
}

func (h *failNextTokenCachePipelineHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func (h *blockingTokenCacheWriteHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (h *blockingTokenCacheWriteHook) AfterProcess(context.Context, redis.Cmder) error {
	return nil
}

func (h *blockingTokenCacheWriteHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	h.once.Do(func() { close(h.entered) })
	<-h.release
	return ctx, nil
}

func (h *blockingTokenCacheWriteHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func useTokenCacheMiniRedis(t *testing.T) {
	t.Helper()
	server := miniredis.RunT(t)
	oldRedisEnabled := common.RedisEnabled
	oldRDB := common.RDB
	oldSyncFrequency := common.SyncFrequency
	common.RedisEnabled = true
	common.SyncFrequency = 60
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RedisEnabled = oldRedisEnabled
		common.RDB = oldRDB
		common.SyncFrequency = oldSyncFrequency
	})
}

func TestTokenCacheRoundTripsMaxGroupRatio(t *testing.T) {
	useTokenCacheMiniRedis(t)
	maxRatio := 0.12
	token := Token{Id: 7, Key: "cache-ratio-key", MaxGroupRatio: &maxRatio}

	if err := cacheSetToken(token); err != nil {
		t.Fatalf("failed to cache token: %v", err)
	}
	cached, err := cacheGetTokenByKey(token.Key)
	if err != nil {
		t.Fatalf("failed to read cached token: %v", err)
	}
	if cached.MaxGroupRatio == nil || *cached.MaxGroupRatio != maxRatio {
		t.Fatalf("expected cached max_group_ratio %v, got %v", maxRatio, cached.MaxGroupRatio)
	}
}

func TestTokenCacheRoundTripsUnlimitedMaxGroupRatio(t *testing.T) {
	useTokenCacheMiniRedis(t)
	token := Token{Id: 8, Key: "cache-unlimited-key", MaxGroupRatio: nil}

	if err := cacheSetToken(token); err != nil {
		t.Fatalf("failed to cache token: %v", err)
	}
	cached, err := cacheGetTokenByKey(token.Key)
	if err != nil {
		t.Fatalf("failed to read cached token: %v", err)
	}
	if cached.MaxGroupRatio != nil {
		t.Fatalf("expected cached max_group_ratio to remain nil, got %v", *cached.MaxGroupRatio)
	}
}

func TestTokenUpdateWaitsForCacheRefreshBeforeReturning(t *testing.T) {
	useTokenCacheMiniRedis(t)
	oldDB := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	DB = db
	t.Cleanup(func() { DB = oldDB })
	if err := db.AutoMigrate(&Token{}); err != nil {
		t.Fatalf("failed to migrate token: %v", err)
	}

	oldRatio := 1.0
	token := Token{UserId: 1, Key: "cache-coherent-key", Name: "guarded", MaxGroupRatio: &oldRatio}
	if err := db.Create(&token).Error; err != nil {
		t.Fatalf("failed to seed token: %v", err)
	}
	if err := cacheSetToken(token); err != nil {
		t.Fatalf("failed to seed token cache: %v", err)
	}

	hook := &blockingTokenCacheWriteHook{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	common.RDB.AddHook(hook)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(hook.release) }) }
	t.Cleanup(release)

	newRatio := 0.12
	token.MaxGroupRatio = &newRatio
	updateDone := make(chan error, 1)
	go func() {
		updateDone <- token.Update()
	}()

	select {
	case <-hook.entered:
	case <-time.After(time.Second):
		t.Fatal("token update never attempted to refresh Redis")
	}
	select {
	case err := <-updateDone:
		t.Fatalf("token update returned before cache refresh completed: %v", err)
	default:
	}

	release()
	if err := <-updateDone; err != nil {
		t.Fatalf("token update failed: %v", err)
	}
	cached, err := cacheGetTokenByKey(token.Key)
	if err != nil {
		t.Fatalf("failed to read refreshed token cache: %v", err)
	}
	if cached.MaxGroupRatio == nil || *cached.MaxGroupRatio != newRatio {
		t.Fatalf("expected refreshed max_group_ratio %v, got %v", newRatio, cached.MaxGroupRatio)
	}
}

func TestTokenMaxGroupRatioTighteningRestoresCacheWhenDatabaseUpdateFails(t *testing.T) {
	useTokenCacheMiniRedis(t)
	oldDB := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	t.Cleanup(func() { DB = oldDB })
	require.NoError(t, db.AutoMigrate(&Token{}))

	oldRatio := 1.0
	token := Token{UserId: 1, Key: "tighten-before-db", Name: "guarded", MaxGroupRatio: &oldRatio}
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, cacheSetToken(token))
	previous := token

	callbackName := "test:fail_token_ratio_tightening_db_update"
	var ratioObservedBeforeDatabaseUpdate *float64
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		cached, cacheErr := cacheGetTokenByKey(token.Key)
		if cacheErr == nil && cached.MaxGroupRatio != nil {
			observed := *cached.MaxGroupRatio
			ratioObservedBeforeDatabaseUpdate = &observed
		}
		tx.AddError(errors.New("forced token update failure"))
	}))
	t.Cleanup(func() {
		_ = db.Callback().Update().Remove(callbackName)
	})

	newRatio := 0.12
	token.MaxGroupRatio = &newRatio
	require.Error(t, token.UpdateWithMaxGroupRatioSafety(previous))
	require.NotNil(t, ratioObservedBeforeDatabaseUpdate)
	require.Equal(t, newRatio, *ratioObservedBeforeDatabaseUpdate)

	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	require.NotNil(t, cached.MaxGroupRatio)
	require.Equal(t, oldRatio, *cached.MaxGroupRatio)

	var stored Token
	require.NoError(t, db.First(&stored, token.Id).Error)
	require.NotNil(t, stored.MaxGroupRatio)
	require.Equal(t, oldRatio, *stored.MaxGroupRatio)
}

func TestTokenMaxGroupRatioTighteningStopsBeforeDatabaseWhenCacheFails(t *testing.T) {
	useTokenCacheMiniRedis(t)
	oldDB := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	t.Cleanup(func() { DB = oldDB })
	require.NoError(t, db.AutoMigrate(&Token{}))

	oldRatio := 1.0
	token := Token{UserId: 1, Key: "tighten-cache-failure", Name: "guarded", MaxGroupRatio: &oldRatio}
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, cacheSetToken(token))
	previous := token

	hook := &failNextTokenCachePipelineHook{}
	hook.fail.Store(true)
	common.RDB.AddHook(hook)

	newRatio := 0.12
	token.MaxGroupRatio = &newRatio
	require.Error(t, token.UpdateWithMaxGroupRatioSafety(previous))

	var stored Token
	require.NoError(t, db.First(&stored, token.Id).Error)
	require.NotNil(t, stored.MaxGroupRatio)
	require.Equal(t, oldRatio, *stored.MaxGroupRatio)
}

func TestTokenMaxGroupRatioLooseningCommitsDatabaseBeforeCacheRefresh(t *testing.T) {
	useTokenCacheMiniRedis(t)
	oldDB := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	t.Cleanup(func() { DB = oldDB })
	require.NoError(t, db.AutoMigrate(&Token{}))

	oldRatio := 0.12
	token := Token{UserId: 1, Key: "loosen-cache-failure", Name: "guarded", MaxGroupRatio: &oldRatio}
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, cacheSetToken(token))
	previous := token

	hook := &failNextTokenCachePipelineHook{}
	hook.fail.Store(true)
	common.RDB.AddHook(hook)

	newRatio := 1.0
	token.MaxGroupRatio = &newRatio
	require.Error(t, token.UpdateWithMaxGroupRatioSafety(previous))

	var stored Token
	require.NoError(t, db.First(&stored, token.Id).Error)
	require.NotNil(t, stored.MaxGroupRatio)
	require.Equal(t, newRatio, *stored.MaxGroupRatio)

	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	require.NotNil(t, cached.MaxGroupRatio)
	require.Equal(t, oldRatio, *cached.MaxGroupRatio)
}
