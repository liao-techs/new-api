package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

func TestGetChannelPreservesPriceLimitErrorDuringAutoRetry(t *testing.T) {
	oldAutoGroups := setting.AutoGroups2JsonString()
	oldGroupRatios := ratio_setting.GroupRatio2JSONString()
	if err := setting.UpdateAutoGroupsByJsonString(`["default"]`); err != nil {
		t.Fatalf("failed to set auto groups: %v", err)
	}
	if err := ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`); err != nil {
		t.Fatalf("failed to set group ratios: %v", err)
	}
	t.Cleanup(func() {
		_ = setting.UpdateAutoGroupsByJsonString(oldAutoGroups)
		_ = ratio_setting.UpdateGroupRatioByJSONString(oldGroupRatios)
	})

	ctx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenMaxGroupRatio, 0.5)
	info := &relaycommon.RelayInfo{
		TokenGroup:      "auto",
		UserGroup:       "default",
		UsingGroup:      "auto",
		OriginModelName: "test-model",
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}

	_, apiErr := getChannel(ctx, info, &service.RetryParam{
		Ctx:         ctx,
		TokenGroup:  "auto",
		ModelName:   "test-model",
		RequestPath: "/v1/responses",
	})
	if apiErr == nil {
		t.Fatal("expected retry selection to return a price limit error")
	}
	if apiErr.StatusCode != 402 || apiErr.GetErrorCode() != types.ErrorCodePriceLimitExceeded {
		t.Fatalf("unexpected retry error: status=%d code=%s", apiErr.StatusCode, apiErr.GetErrorCode())
	}
	var limitErr *service.TokenGroupRatioLimitError
	if !errors.As(apiErr, &limitErr) {
		t.Fatalf("expected wrapped TokenGroupRatioLimitError, got %v", apiErr)
	}
}

func TestRetryFinalPriceGuardUsesIngressSnapshotAfterPriceChange(t *testing.T) {
	oldGroupRatios := ratio_setting.GroupRatio2JSONString()
	if err := ratio_setting.UpdateGroupRatioByJSONString(`{"premium":0.1}`); err != nil {
		t.Fatalf("failed to set initial group ratio: %v", err)
	}
	t.Cleanup(func() {
		_ = ratio_setting.UpdateGroupRatioByJSONString(oldGroupRatios)
	})

	maxRatio := 0.15
	ctx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(ctx, constant.ContextKeyTokenMaxGroupRatio, maxRatio)
	service.CaptureTokenGroupRatioSnapshot(ctx, "member")
	if err := ratio_setting.UpdateGroupRatioByJSONString(`{"premium":0.3}`); err != nil {
		t.Fatalf("failed to raise group ratio: %v", err)
	}

	info := &relaycommon.RelayInfo{
		UserGroup:  "member",
		UsingGroup: "premium",
	}
	if err := refreshRetryGroupRatioAndCheck(ctx, info); err != nil {
		t.Fatalf("in-flight retry must retain ingress price snapshot, got %v", err)
	}
	if info.PriceData.GroupRatioInfo.GroupRatio != 0.1 {
		t.Fatalf("expected ingress ratio 0.1, got %v", info.PriceData.GroupRatioInfo.GroupRatio)
	}

	nextRequest, _ := gin.CreateTestContext(nil)
	common.SetContextKey(nextRequest, constant.ContextKeyTokenMaxGroupRatio, maxRatio)
	service.CaptureTokenGroupRatioSnapshot(nextRequest, "member")
	nextInfo := &relaycommon.RelayInfo{UserGroup: "member", UsingGroup: "premium"}
	err := refreshRetryGroupRatioAndCheck(nextRequest, nextInfo)
	var limitErr *service.TokenGroupRatioLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("next request must see raised price and be blocked, got %v", err)
	}
}

func TestAttachPriceGuardAddsTopLevelMetadata(t *testing.T) {
	apiErr := service.NewTokenGroupRatioLimitAPIError(&service.TokenGroupRatioLimitError{
		Group:   "premium",
		Current: 0.15,
		Max:     0.12,
	})
	response := gin.H{"error": apiErr.ToOpenAIError()}

	attachPriceGuard(response, apiErr)

	priceGuard, ok := response["price_guard"].(map[string]float64)
	if !ok {
		t.Fatalf("expected top-level price_guard metadata, got %#v", response["price_guard"])
	}
	if priceGuard["current_group_ratio"] != 0.15 || priceGuard["max_group_ratio"] != 0.12 {
		t.Fatalf("unexpected price_guard metadata: %#v", priceGuard)
	}
}

func TestRespondMidjourneyErrorPreservesPriceLimitStatusAndType(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/mj/submit/imagine", nil)

	respondMidjourneyError(ctx, &taskdto.MidjourneyResponse{
		Code:        http.StatusPaymentRequired,
		Description: "当前分组倍率高于 API Key 上限",
		Properties: map[string]float64{
			"current_group_ratio": 0.15,
			"max_group_ratio":     0.12,
		},
	})

	if recorder.Code != http.StatusPaymentRequired {
		t.Fatalf("expected status 402, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"type":"price_limit_exceeded"`) {
		t.Fatalf("expected price_limit_exceeded response, got %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"price_guard":{"current_group_ratio":0.15,"max_group_ratio":0.12}`) {
		t.Fatalf("expected price_guard metadata, got %s", recorder.Body.String())
	}
}
