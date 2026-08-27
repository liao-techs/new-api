package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

func TestDistributeRejectsPriceLimitBeforeRelayAcrossPrimaryTextPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldGroupRatios := ratio_setting.GroupRatio2JSONString()
	oldSpecialRatios := ratio_setting.GroupGroupRatio2JSONString()
	if err := ratio_setting.UpdateGroupRatioByJSONString(`{"premium":0.15}`); err != nil {
		t.Fatalf("failed to set group ratios: %v", err)
	}
	if err := ratio_setting.UpdateGroupGroupRatioByJSONString(`{}`); err != nil {
		t.Fatalf("failed to clear special group ratios: %v", err)
	}
	t.Cleanup(func() {
		_ = ratio_setting.UpdateGroupRatioByJSONString(oldGroupRatios)
		_ = ratio_setting.UpdateGroupGroupRatioByJSONString(oldSpecialRatios)
	})

	for _, path := range []string{
		"/v1/responses",
		"/v1/chat/completions",
		"/v1/messages",
	} {
		t.Run(path, func(t *testing.T) {
			billingCalled := false
			upstreamCalled := false
			router := gin.New()
			router.Use(func(c *gin.Context) {
				common.SetContextKey(c, constant.ContextKeyUserGroup, "member")
				common.SetContextKey(c, constant.ContextKeyUsingGroup, "premium")
				common.SetContextKey(c, constant.ContextKeyTokenMaxGroupRatio, 0.12)
				c.Set("id", 9)
				c.Next()
			})
			router.POST(path, Distribute(), func(c *gin.Context) {
				billingCalled = true
				c.Next()
			}, func(c *gin.Context) {
				upstreamCalled = true
				c.Status(http.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"test-model"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusPaymentRequired {
				t.Fatalf("expected 402, got %d: %s", response.Code, response.Body.String())
			}
			if billingCalled {
				t.Fatal("expected price guard to stop the request before billing")
			}
			if upstreamCalled {
				t.Fatal("expected price guard to stop the request before upstream relay")
			}
			if !strings.Contains(response.Body.String(), `"code":"price_limit_exceeded"`) {
				t.Fatalf("expected price_limit_exceeded response, got %s", response.Body.String())
			}
			if !strings.Contains(response.Body.String(), `"current_group_ratio":0.15`) ||
				!strings.Contains(response.Body.String(), `"max_group_ratio":0.12`) {
				t.Fatalf("expected price guard metadata, got %s", response.Body.String())
			}
		})
	}
}
