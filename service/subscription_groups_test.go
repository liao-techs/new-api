package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedMultiGroupBilling(t *testing.T, domesticUsed int64) {
	t.Helper()
	truncate(t)
	t.Cleanup(func() {
		model.DB.Where("id IN ?", []int{701, 702}).Delete(&model.SubscriptionPlan{})
		model.DB.Where("user_id = ?", 12361).Delete(&model.SubscriptionPreConsumeRecord{})
	})
	seedUser(t, 12361, 1000)
	now := common.GetTimestamp()
	for _, plan := range []model.SubscriptionPlan{{Id: 701, Title: "Codex"}, {Id: 702, Title: "Domestic"}} {
		require.NoError(t, model.DB.Create(&plan).Error)
		model.InvalidateSubscriptionPlanCache(plan.Id)
	}
	require.NoError(t, model.DB.Create(&[]model.UserSubscription{
		{Id: 701, UserId: 12361, PlanId: 701, UpgradeGroup: "codex-sub", Status: "active", EndTime: now + 100, AmountTotal: 100, AmountUsed: 100, AllowWalletOverflow: false},
		{Id: 702, UserId: 12361, PlanId: 702, UpgradeGroup: "domestic-sub", Status: "active", EndTime: now + 200, AmountTotal: 100, AmountUsed: domesticUsed, AllowWalletOverflow: true},
	}).Error)
}

func TestMultiSubscriptionBillingPreferencesAndOverflow(t *testing.T) {
	for _, tc := range []struct {
		name, group, preference, source string
		used                            int64
		denied                          bool
	}{
		{"matching-subscription", "domestic-sub", "subscription_first", BillingSourceSubscription, 0, false},
		{"exhausted-codex-does-not-spend-domestic", "codex-sub", "subscription_first", "", 0, true},
		{"domestic-overflow-ignores-codex-policy", "domestic-sub", "subscription_first", BillingSourceWallet, 100, false},
		{"unrelated-group-uses-wallet", "wallet", "subscription_first", BillingSourceWallet, 0, false},
		{"subscription-only-does-not-use-other-group", "wallet", "subscription_only", "", 0, true},
		{"wallet-preference-preserved", "domestic-sub", "wallet_only", BillingSourceWallet, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seedMultiGroupBilling(t, tc.used)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{UserId: 12361, UsingGroup: tc.group, RequestId: tc.name, IsPlayground: true, UserSetting: dto.UserSetting{BillingPreference: tc.preference}}
			session, apiErr := NewBillingSession(c, info, 40)
			if tc.denied {
				require.NotNil(t, apiErr)
				assert.Nil(t, session)
				assert.EqualValues(t, tc.used, getSubscriptionUsed(t, 702))
			} else {
				require.Nil(t, apiErr)
				assert.Equal(t, tc.source, info.BillingSource)
				require.NoError(t, session.Settle(30))
				if tc.source == BillingSourceSubscription {
					assert.EqualValues(t, 30, getSubscriptionUsed(t, 702))
				}
			}
			assert.EqualValues(t, 100, getSubscriptionUsed(t, 701))
		})
	}
}

func TestScopedSubscriptionReservationPinsAutoRetryAndRefund(t *testing.T) {
	seedMultiGroupBilling(t, 0)
	oldGroups, oldRatios, oldAuto := setting.UserUsableGroups2JSONString(), ratio_setting.GroupRatio2JSONString(), setting.AutoGroups2JsonString()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"wallet":"Wallet"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"wallet":1,"codex-sub":0.2,"domestic-sub":2.8}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["codex-sub","domestic-sub","wallet"]`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldRatios))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(oldAuto))
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("id", 12361)
	common.SetContextKey(c, constant.ContextKeyAutoGroupIndex, 1)
	common.SetContextKey(c, constant.ContextKeyTokenAutoGroups, []string{"codex-sub", "domestic-sub", "wallet"})
	info := &relaycommon.RelayInfo{UserId: 12361, UserGroup: "codex-sub", UsingGroup: "domestic-sub", TokenGroup: "auto", RequestId: "auto-domestic", IsPlayground: true}
	session, apiErr := NewBillingSession(c, info, 40)
	require.Nil(t, apiErr)
	assert.Equal(t, 702, info.SubscriptionId)
	groups, err := GetRequestAutoGroups(c, info.UserGroup)
	require.NoError(t, err)
	assert.Equal(t, []string{"domestic-sub"}, groups)
	assert.Zero(t, common.GetContextKeyInt(c, constant.ContextKeyAutoGroupIndex))
	require.NoError(t, session.funding.Refund())
	require.NoError(t, session.funding.Refund())
	assert.Zero(t, getSubscriptionUsed(t, 702))
	assert.EqualValues(t, 100, getSubscriptionUsed(t, 701))
}

func TestExplicitConfiguredGroupsSurviveUnrelatedSubscriptionExpiry(t *testing.T) {
	truncate(t)
	seedUser(t, 12361, 1000)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 12361).Update("group", "manual").Error)
	oldGroups := setting.UserUsableGroups2JSONString()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"public":"Public"}`))
	t.Cleanup(func() { require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldGroups)) })
	require.NoError(t, model.DB.Create(&model.UserSubscription{UserId: 12361, UpgradeGroup: "public", Status: "expired", EndTime: common.GetTimestamp() - 1}).Error)
	groups, err := GetUserUsableGroupsForUser(12361)
	require.NoError(t, err)
	assert.Contains(t, groups, "manual")
	assert.Contains(t, groups, "public")
}

func TestDeletedSubscriptionCannotBeRegrantedByStaleUserCache(t *testing.T) {
	truncate(t)
	seedUser(t, 12361, 1000)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 12361).Update("group", "wallet").Error)
	oldGroups := setting.UserUsableGroups2JSONString()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"wallet":"Wallet"}`))
	t.Cleanup(func() { require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldGroups)) })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("id", 12361)
	// The subscription was hard-deleted and the database group restored, but
	// the request arrived with the old group from Redis.
	groups, err := GetRequestUsableGroups(c, "codex-sub")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"wallet": "Wallet"}, groups)
}
