package service

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func withTestGroupRatios(t *testing.T, groupRatios, specialRatios string) {
	t.Helper()
	oldGroupRatios := ratio_setting.GroupRatio2JSONString()
	oldSpecialRatios := ratio_setting.GroupGroupRatio2JSONString()
	if err := ratio_setting.UpdateGroupRatioByJSONString(groupRatios); err != nil {
		t.Fatalf("failed to set test group ratios: %v", err)
	}
	if err := ratio_setting.UpdateGroupGroupRatioByJSONString(specialRatios); err != nil {
		t.Fatalf("failed to set test special ratios: %v", err)
	}
	t.Cleanup(func() {
		_ = ratio_setting.UpdateGroupRatioByJSONString(oldGroupRatios)
		_ = ratio_setting.UpdateGroupGroupRatioByJSONString(oldSpecialRatios)
	})
}

func newRatioLimitContext(maxRatio *float64) *gin.Context {
	ctx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "member")
	if maxRatio != nil {
		common.SetContextKey(ctx, constant.ContextKeyTokenMaxGroupRatio, *maxRatio)
	}
	return ctx
}

func TestCheckTokenGroupRatioLimitAllowsNilEqualAndZero(t *testing.T) {
	if err := CheckTokenGroupRatioLimit(newRatioLimitContext(nil), "paid", 99); err != nil {
		t.Fatalf("expected nil limit to allow any ratio, got %v", err)
	}

	equal := 0.12
	if err := CheckTokenGroupRatioLimit(newRatioLimitContext(&equal), "paid", 0.12); err != nil {
		t.Fatalf("expected equal ratio to be allowed, got %v", err)
	}

	zero := 0.0
	if err := CheckTokenGroupRatioLimit(newRatioLimitContext(&zero), "free", 0); err != nil {
		t.Fatalf("expected zero ratio under zero limit to be allowed, got %v", err)
	}
	err := CheckTokenGroupRatioLimit(newRatioLimitContext(&zero), "paid", 0.0001)
	var limitErr *TokenGroupRatioLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected paid ratio to exceed zero limit, got %v", err)
	}
}

func TestFilterAutoGroupsByTokenRatioLimitUsesEffectiveUserRatio(t *testing.T) {
	withTestGroupRatios(t,
		`{"low":0.1,"high":0.2,"special":0.4}`,
		`{"member":{"special":0.11}}`,
	)
	maxRatio := 0.12
	ctx := newRatioLimitContext(&maxRatio)

	groups, err := FilterAutoGroupsByTokenRatioLimit(ctx, "member", []string{"low", "high", "special"})
	if err != nil {
		t.Fatalf("expected eligible auto groups, got %v", err)
	}
	if len(groups) != 2 || groups[0] != "low" || groups[1] != "special" {
		t.Fatalf("expected low and special groups, got %#v", groups)
	}
}

func TestFilterAutoGroupsByTokenRatioLimitReturnsPriceErrorWhenAllExceed(t *testing.T) {
	withTestGroupRatios(t,
		`{"first":0.2,"closest":0.15}`,
		`{}`,
	)
	maxRatio := 0.12
	ctx := newRatioLimitContext(&maxRatio)

	groups, err := FilterAutoGroupsByTokenRatioLimit(ctx, "member", []string{"first", "closest"})
	if len(groups) != 0 {
		t.Fatalf("expected no eligible groups, got %#v", groups)
	}
	var limitErr *TokenGroupRatioLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected TokenGroupRatioLimitError, got %v", err)
	}
	if limitErr.Group != "closest" || limitErr.Current != 0.15 || limitErr.Max != maxRatio {
		t.Fatalf("unexpected closest limit error: %#v", limitErr)
	}
}

func TestTokenGroupRatioSnapshotFreezesEffectiveRatiosForRequest(t *testing.T) {
	withTestGroupRatios(t,
		`{"low":0.1,"high":0.2,"member":0.3}`,
		`{"member":{"high":0.15}}`,
	)
	maxRatio := 0.2
	ctx := newRatioLimitContext(&maxRatio)

	CaptureTokenGroupRatioSnapshot(ctx, "member")

	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(
		`{"low":0.7,"high":0.8,"member":0.9,"introduced_later":0.05}`,
	))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(
		`{"member":{"high":0.75}}`,
	))

	lowRatio, lowSpecialRatio, lowHasSpecial := GetRequestGroupRatio(ctx, "member", "low")
	require.Equal(t, 0.1, lowRatio)
	require.Equal(t, -1.0, lowSpecialRatio)
	require.False(t, lowHasSpecial)

	highRatio, highSpecialRatio, highHasSpecial := GetRequestGroupRatio(ctx, "member", "high")
	require.Equal(t, 0.15, highRatio)
	require.Equal(t, 0.15, highSpecialRatio)
	require.True(t, highHasSpecial)

	newRatio, newSpecialRatio, newHasSpecial := GetRequestGroupRatio(
		ctx,
		"member",
		"introduced_later",
	)
	require.Equal(t, 1.0, newRatio)
	require.Equal(t, -1.0, newSpecialRatio)
	require.False(t, newHasSpecial)
}

func TestFilterAutoGroupsUsesRequestSnapshotAfterPriceChange(t *testing.T) {
	withTestGroupRatios(t,
		`{"low":0.1,"high":0.2}`,
		`{}`,
	)
	maxRatio := 0.15
	ctx := newRatioLimitContext(&maxRatio)
	CaptureTokenGroupRatioSnapshot(ctx, "member")

	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(
		`{"low":0.9,"high":0.05}`,
	))

	groups, err := FilterAutoGroupsByTokenRatioLimit(ctx, "member", []string{"low", "high"})
	require.NoError(t, err)
	require.Equal(t, []string{"low"}, groups)
}

func TestCacheGetRandomSatisfiedChannelRejectsAutoWhenAllGroupsExceedLimit(t *testing.T) {
	oldAutoGroups := setting.AutoGroups2JsonString()
	if err := setting.UpdateAutoGroupsByJsonString(`["default"]`); err != nil {
		t.Fatalf("failed to set auto groups: %v", err)
	}
	t.Cleanup(func() {
		_ = setting.UpdateAutoGroupsByJsonString(oldAutoGroups)
	})
	withTestGroupRatios(t, `{"default":1}`, `{}`)

	maxRatio := 0.5
	ctx := newRatioLimitContext(&maxRatio)
	_, _, err := CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx:         ctx,
		TokenGroup:  "auto",
		ModelName:   "test-model",
		RequestPath: "/v1/chat/completions",
	})

	var limitErr *TokenGroupRatioLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected auto selection to fail with price limit error, got %v", err)
	}
}

func TestGetUserAutoGroupMaxRatioUsesConfiguredCandidatesAndSpecialRatio(t *testing.T) {
	oldAutoGroups := setting.AutoGroups2JsonString()
	oldUsableGroups := setting.UserUsableGroups2JSONString()
	if err := setting.UpdateAutoGroupsByJsonString(`["low","special"]`); err != nil {
		t.Fatalf("failed to set auto groups: %v", err)
	}
	if err := setting.UpdateUserUsableGroupsByJSONString(`{"low":"Low","special":"Special","visible_only":"Visible"}`); err != nil {
		t.Fatalf("failed to set usable groups: %v", err)
	}
	t.Cleanup(func() {
		_ = setting.UpdateAutoGroupsByJsonString(oldAutoGroups)
		_ = setting.UpdateUserUsableGroupsByJSONString(oldUsableGroups)
	})
	withTestGroupRatios(t,
		`{"low":0.1,"special":0.4,"visible_only":9}`,
		`{"member":{"special":0.11}}`,
	)

	maxRatio, found := GetUserAutoGroupMaxRatio("member")
	if !found || maxRatio != 0.11 {
		t.Fatalf("expected auto max ratio 0.11 from configured candidates, got %v (found=%v)", maxRatio, found)
	}
}

func TestNewTokenGroupRatioLimitAPIErrorContract(t *testing.T) {
	limitErr := &TokenGroupRatioLimitError{Group: "premium", Current: 0.15, Max: 0.12}
	apiErr := NewTokenGroupRatioLimitAPIError(limitErr)

	if apiErr.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("expected status 402, got %d", apiErr.StatusCode)
	}
	if apiErr.GetErrorCode() != types.ErrorCodePriceLimitExceeded {
		t.Fatalf("expected price_limit_exceeded code, got %s", apiErr.GetErrorCode())
	}
	openAIError := apiErr.ToOpenAIError()
	if openAIError.Param != "group" || openAIError.Code != string(types.ErrorCodePriceLimitExceeded) {
		t.Fatalf("unexpected OpenAI error contract: %#v", openAIError)
	}
	var metadata TokenGroupRatioLimitError
	if err := common.Unmarshal(apiErr.Metadata, &metadata); err != nil {
		t.Fatalf("failed to decode price guard metadata: %v", err)
	}
	if metadata.Current != limitErr.Current || metadata.Max != limitErr.Max {
		t.Fatalf("unexpected price guard metadata: %#v", metadata)
	}
}

func TestRecordTokenGroupRatioLimitAuditIsIdempotentAndContainsNoKey(t *testing.T) {
	oldDB, oldLogDB := model.DB, model.LOG_DB
	db, err := gorm.Open(sqlite.Open(
		fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_")),
	), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	ctx, _ := gin.CreateTestContext(nil)
	ctx.Set("id", 9)
	ctx.Set("username", "audit-user")
	ctx.Set("token_id", 17)
	ctx.Set("token_name", "guarded")
	ctx.Set("token", "sk-secret-must-not-appear")
	ctx.Set("original_model", "gpt-test")
	limitErr := &TokenGroupRatioLimitError{Group: "premium", Current: 0.2, Max: 0.1}

	RecordTokenGroupRatioLimitAudit(ctx, limitErr)
	RecordTokenGroupRatioLimitAudit(ctx, limitErr)

	var logs []model.Log
	require.NoError(t, db.Find(&logs).Error)
	require.Len(t, logs, 1)
	require.Equal(t, model.LogTypeError, logs[0].Type)
	require.Equal(t, 0, logs[0].Quota)
	require.Contains(t, logs[0].Other, `"error_code":"price_limit_exceeded"`)
	require.Contains(t, logs[0].Other, `"current_group_ratio":0.2`)
	require.Contains(t, logs[0].Other, `"max_group_ratio":0.1`)
	require.NotContains(t, logs[0].Other, "sk-secret-must-not-appear")
	require.NotContains(t, logs[0].Content, "sk-secret-must-not-appear")
}

func TestTaskErrorWrapperPreservesTokenGroupRatioLimitContract(t *testing.T) {
	limitErr := &TokenGroupRatioLimitError{Group: "premium", Current: 0.15, Max: 0.12}

	taskErr := TaskErrorWrapperLocal(limitErr, "model_price_error", http.StatusBadRequest)

	if taskErr.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("expected status 402, got %d", taskErr.StatusCode)
	}
	if taskErr.Code != string(types.ErrorCodePriceLimitExceeded) {
		t.Fatalf("expected price_limit_exceeded code, got %q", taskErr.Code)
	}
	if !taskErr.LocalError {
		t.Fatal("expected local price guard error to remain local")
	}
	if taskErr.Data != limitErr {
		t.Fatalf("expected task price_guard metadata, got %#v", taskErr.Data)
	}
}

func TestMidjourneyErrorFromTokenGroupRatioLimitPreservesContract(t *testing.T) {
	limitErr := &TokenGroupRatioLimitError{Group: "premium", Current: 0.15, Max: 0.12}

	midjourneyErr, ok := MidjourneyErrorFromTokenGroupRatioLimit(nil, limitErr)

	if !ok {
		t.Fatal("expected price limit error to be recognized")
	}
	if midjourneyErr.Code != http.StatusPaymentRequired {
		t.Fatalf("expected Midjourney price guard code 402, got %d", midjourneyErr.Code)
	}
	if midjourneyErr.Description != limitErr.Error() {
		t.Fatalf("unexpected Midjourney price guard description: %q", midjourneyErr.Description)
	}
	if midjourneyErr.Properties != limitErr {
		t.Fatalf("expected Midjourney price_guard metadata, got %#v", midjourneyErr.Properties)
	}
	if converted, ok := MidjourneyErrorFromTokenGroupRatioLimit(nil, errors.New("other")); ok || converted != nil {
		t.Fatalf("expected unrelated error to remain unhandled, got %#v", converted)
	}
}
