package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionGroupsAgreeAcrossKeysModelsPricingAndAuthentication(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}))
	oldUsable, oldRatios, oldAuto := setting.UserUsableGroups2JSONString(), ratio_setting.GroupRatio2JSONString(), setting.AutoGroups2JsonString()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"wallet":"Wallet","auto":"Auto"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"wallet":1,"codex-sub":0.2,"domestic-sub":2.8}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["domestic-sub","codex-sub","wallet"]`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldUsable))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldRatios))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(oldAuto))
	})
	now := common.GetTimestamp()
	user := model.User{Id: 12361, Username: "multi-sub-user", Group: "codex-sub", Status: common.UserStatusEnabled, Role: common.RoleCommonUser}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.User{Id: 12362, Username: "other-user", AffCode: "other", Group: "wallet", Status: common.UserStatusEnabled}).Error)
	subs := []model.UserSubscription{
		{UserId: user.Id, UpgradeGroup: "codex-sub", Status: "active", StartTime: now - 10, EndTime: now + 1000},
		{UserId: user.Id, UpgradeGroup: "domestic-sub", Status: "active", StartTime: now - 20, EndTime: now + 2000},
	}
	require.NoError(t, db.Create(&subs).Error)
	for _, group := range []string{"codex-sub", "domestic-sub"} {
		require.NoError(t, db.Create(&model.Ability{Group: group, Model: group + "-model", ChannelId: 1, Enabled: true}).Error)
	}
	for _, token := range []model.Token{
		{UserId: user.Id, Key: "codextestkey", Group: "codex-sub", Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1},
		{UserId: user.Id, Key: "domestictestkey", Group: "domestic-sub", Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1},
		{UserId: user.Id, Key: "inheritedtestkey", Group: "", Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1},
		{UserId: 12362, Key: "unauthorizedtestkey", Group: "domestic-sub", Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1},
	} {
		require.NoError(t, db.Create(&token).Error)
	}
	router := gin.New()
	dashboard := router.Group("/api", func(c *gin.Context) {
		c.Set("id", user.Id)
		common.SetContextKey(c, constant.ContextKeyUserGroup, user.Group)
	})
	dashboard.GET("/user/self/groups", GetUserGroups)
	dashboard.GET("/user/models", GetUserModels)
	dashboard.GET("/token/auto_groups", GetTokenAutoGroups)
	dashboard.GET("/pricing", GetPricing)
	router.GET("/v1/models", middleware.TokenAuth(), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for _, stage := range []string{"both-active", "codex-expired-before-cron", "domestic-cancelled"} {
		if stage == "codex-expired-before-cron" {
			require.NoError(t, db.Model(&subs[0]).Update("end_time", now-1).Error)
		}
		if stage == "domestic-cancelled" {
			require.NoError(t, db.Model(&subs[1]).Update("status", "cancelled").Error)
		}
		for _, path := range []string{"/api/user/self/groups", "/api/user/models", "/api/token/auto_groups", "/api/pricing"} {
			res := httptest.NewRecorder()
			router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
			assert.Equal(t, http.StatusOK, res.Code, path)
			assert.Contains(t, res.Body.String(), `"success":true`, path)
			if stage == "both-active" {
				assert.Contains(t, res.Body.String(), "codex-sub", path)
			} else {
				assert.NotContains(t, res.Body.String(), "codex-sub", path)
			}
			if stage != "domestic-cancelled" {
				assert.Contains(t, res.Body.String(), "domestic-sub", path)
			} else {
				assert.NotContains(t, res.Body.String(), "domestic-sub", path)
			}
		}
		for _, tc := range []struct {
			key     string
			allowed bool
		}{
			{"codextestkey", stage == "both-active"}, {"domestictestkey", stage != "domestic-cancelled"},
			{"inheritedtestkey", stage == "both-active"}, {"unauthorizedtestkey", false},
		} {
			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			req.Header.Set("Authorization", "Bearer sk-"+tc.key)
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			want := http.StatusForbidden
			if tc.allowed {
				want = http.StatusNoContent
			}
			assert.Equal(t, want, res.Code, "%s %s: %s", stage, tc.key, res.Body.String())
		}
	}
}

func TestPlaygroundCannotInheritExpiredSubscriptionGroup(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, i18n.Init())
	require.NoError(t, db.Create(&model.User{Id: 12361, Username: "playground-test", Group: "expired-subscription"}).Error)
	require.NoError(t, db.Create(&model.UserSubscription{UserId: 12361, UpgradeGroup: "expired-subscription", Status: "active", EndTime: common.GetTimestamp() - 1}).Error)
	router := gin.New()
	router.POST("/pg/chat/completions", func(c *gin.Context) {
		c.Set("id", 12361)
		common.SetContextKey(c, constant.ContextKeyUserGroup, "expired-subscription")
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "expired-subscription")
	}, middleware.Distribute(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for _, body := range []string{`{"model":"test-model"}`, `{"model":"test-model","group":"expired-subscription"}`} {
		req := httptest.NewRequest(http.MethodPost, "/pg/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		assert.Equal(t, http.StatusForbidden, res.Code, res.Body.String())
	}
}

func TestSubscriptionGroupLookupFailureDoesNotReturnPartialPermissions(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{Id: 12361, Username: "failed-lookup", Group: "codex-sub"}).Error)
	require.NoError(t, db.Migrator().DropTable(&model.UserSubscription{}))
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", 12361)
	GetUserGroups(c)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	assert.NotContains(t, recorder.Body.String(), `"codex-sub"`)
}
