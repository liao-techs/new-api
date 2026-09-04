package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionFundingStaysWithinRequestedGroup(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&SubscriptionPreConsumeRecord{}))
	t.Cleanup(func() { DB.Where("user_id = ?", 12361).Delete(&SubscriptionPreConsumeRecord{}) })
	now := GetDBTimestamp()
	for _, plan := range []SubscriptionPlan{{Id: 601, Title: "Codex"}, {Id: 602, Title: "Domestic"}, {Id: 603, Title: "Generic"}} {
		require.NoError(t, DB.Create(&plan).Error)
		InvalidateSubscriptionPlanCache(plan.Id)
	}
	subs := []UserSubscription{
		{Id: 601, UserId: 12361, PlanId: 601, UpgradeGroup: "codex-sub", Status: "active", StartTime: now - 10, EndTime: now + 100, AmountTotal: 100},
		{Id: 602, UserId: 12361, PlanId: 602, UpgradeGroup: "domestic-sub", Status: "active", StartTime: now - 10, EndTime: now + 300, AmountTotal: 100},
		{Id: 603, UserId: 12361, PlanId: 602, UpgradeGroup: "domestic-sub", Status: "active", StartTime: now - 10, EndTime: now + 200, AmountTotal: 100},
		{Id: 604, UserId: 12361, PlanId: 602, UpgradeGroup: "domestic-sub", Status: "cancelled", StartTime: now - 10, EndTime: now + 50, AmountTotal: 100},
		{Id: 605, UserId: 12361, PlanId: 602, UpgradeGroup: "domestic-sub", Status: "active", StartTime: now + 10, EndTime: now + 60, AmountTotal: 100},
	}
	require.NoError(t, DB.Create(&subs).Error)

	reservation, err := PreConsumeUserSubscription("domestic-request", 12361, "domestic-sub", 80)
	require.NoError(t, err)
	assert.Equal(t, 603, reservation.UserSubscriptionId)
	assert.Equal(t, "domestic-sub", reservation.UpgradeGroup)
	reservation, err = PreConsumeUserSubscription("codex-request", 12361, "codex-sub", 80)
	require.NoError(t, err)
	assert.Equal(t, 601, reservation.UserSubscriptionId)

	// Replays stay on the original reservation and cannot cross users/groups.
	reservation, err = PreConsumeUserSubscription("domestic-request", 12361, "domestic-sub", 80)
	require.NoError(t, err)
	assert.Equal(t, 603, reservation.UserSubscriptionId)
	_, err = PreConsumeUserSubscription("domestic-request", 12361, "codex-sub", 80)
	require.ErrorContains(t, err, "does not match")
	_, err = PreConsumeUserSubscription("domestic-request", 12362, "domestic-sub", 80)
	require.ErrorContains(t, err, "does not match")
	_, err = PreConsumeUserSubscription("codex-exhausted", 12361, "codex-sub", 30)
	require.ErrorContains(t, err, "subscription quota insufficient")
	_, err = PreConsumeUserSubscription("wallet-group", 12361, "domestic-wallet", 30)
	require.ErrorContains(t, err, "no active subscription")

	require.NoError(t, PostConsumeUserSubscriptionDelta(603, 5))
	require.NoError(t, DB.Find(&subs).Error)
	used := make(map[int]int64)
	for _, sub := range subs {
		used[sub.Id] = sub.AmountUsed
	}
	assert.Equal(t, map[int]int64{601: 80, 602: 0, 603: 85, 604: 0, 605: 0}, used)
	require.NoError(t, RefundSubscriptionPreConsume("codex-request"))
	require.NoError(t, RefundSubscriptionPreConsume("codex-request"))
	var refunded UserSubscription
	require.NoError(t, DB.First(&refunded, 601).Error)
	assert.Zero(t, refunded.AmountUsed)

	// Plans without a group keep their existing generic quota semantics.
	require.NoError(t, DB.Create(&UserSubscription{Id: 606, UserId: 12361, PlanId: 603, Status: "active", EndTime: now + 500, AmountTotal: 0}).Error)
	reservation, err = PreConsumeUserSubscription("generic-request", 12361, "wallet", 1000)
	require.NoError(t, err)
	assert.Equal(t, 606, reservation.UserSubscriptionId)
	assert.Empty(t, reservation.UpgradeGroup)
}

func TestInterleavedSubscriptionRenewalRestoresOriginalBaseline(t *testing.T) {
	truncateTables(t)
	createSubscriptionGroupTestUser(t, 12361, "domestic-sub")
	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&[]UserSubscription{
		{UserId: 12361, UpgradeGroup: "domestic-sub", PrevUserGroup: "default", Status: "active", StartTime: now - 30, EndTime: now - 1},
		{UserId: 12361, UpgradeGroup: "codex-sub", PrevUserGroup: "domestic-sub", Status: "active", StartTime: now - 20, EndTime: now - 1},
		{UserId: 12361, UpgradeGroup: "domestic-sub", PrevUserGroup: "codex-sub", Status: "active", StartTime: now - 10, EndTime: now - 1},
	}).Error)
	_, err := ExpireDueSubscriptions(100)
	require.NoError(t, err)
	assert.Equal(t, "default", getSubscriptionGroupTestUserGroup(t, 12361))
}

func TestSubscriptionWalletOverflowIsScopedToGroup(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&[]UserSubscription{
		{UserId: 12361, UpgradeGroup: "codex-sub", Status: "active", EndTime: now + 100, AllowWalletOverflow: false},
		{UserId: 12361, UpgradeGroup: "domestic-sub", Status: "active", EndTime: now + 100, AllowWalletOverflow: true},
	}).Error)
	for _, tc := range []struct {
		group            string
		active, overflow bool
	}{
		{"codex-sub", true, false}, {"domestic-sub", true, true}, {"wallet", false, true},
	} {
		has, err := HasActiveUserSubscription(12361, tc.group)
		require.NoError(t, err)
		assert.Equal(t, tc.active, has, tc.group)
		allow, err := UserActiveSubscriptionsAllowWalletOverflow(12361, tc.group)
		require.NoError(t, err)
		assert.Equal(t, tc.overflow, allow, tc.group)
	}
}

func TestDifferentSubscriptionGroupsEndIndependently(t *testing.T) {
	for _, operation := range []string{"expire", "cancel", "delete"} {
		for _, first := range []string{"codex-sub", "domestic-sub"} {
			t.Run(operation+"/"+first, func(t *testing.T) {
				truncateTables(t)
				createSubscriptionGroupTestUser(t, 12361, "codex-sub")
				now := GetDBTimestamp()
				domestic := UserSubscription{UserId: 12361, UpgradeGroup: "domestic-sub", PrevUserGroup: "default", Status: "active", StartTime: now - 20, EndTime: now + 100}
				codex := UserSubscription{UserId: 12361, UpgradeGroup: "codex-sub", PrevUserGroup: "domestic-sub", Status: "active", StartTime: now - 10, EndTime: now + 100}
				require.NoError(t, DB.Create(&domestic).Error)
				require.NoError(t, DB.Create(&codex).Error)
				ending, remaining := codex, domestic
				if first == "domestic-sub" {
					ending, remaining = domestic, codex
				}
				for i, sub := range []UserSubscription{ending, remaining} {
					switch operation {
					case "expire":
						require.NoError(t, DB.Model(&sub).Update("end_time", now-1).Error)
						_, err := ExpireDueSubscriptions(100)
						require.NoError(t, err)
					case "cancel":
						_, err := AdminInvalidateUserSubscription(sub.Id)
						require.NoError(t, err)
					case "delete":
						_, err := AdminDeleteUserSubscription(sub.Id)
						require.NoError(t, err)
					}
					want := remaining.UpgradeGroup
					if i == 1 {
						want = "default"
					}
					assert.Equal(t, want, getSubscriptionGroupTestUserGroup(t, 12361))
				}
			})
		}
	}
}

func TestSubscriptionGroupsExcludeExpiredCancelledAndFutureGrants(t *testing.T) {
	truncateTables(t)
	createSubscriptionGroupTestUser(t, 12361, "codex-sub")
	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&[]UserSubscription{
		{UserId: 12361, UpgradeGroup: "codex-sub", Status: "active", EndTime: now + 100},
		{UserId: 12361, UpgradeGroup: "domestic-sub", Status: "active", EndTime: now + 100, AmountTotal: 100, AmountUsed: 100, AllowWalletOverflow: true},
		{UserId: 12361, UpgradeGroup: "expired", Status: "active", EndTime: now - 1},
		{UserId: 12361, UpgradeGroup: "cancelled", Status: "cancelled", EndTime: now + 100},
		{UserId: 12361, UpgradeGroup: "future", Status: "active", StartTime: now + 10, EndTime: now + 100},
		{UserId: 12362, UpgradeGroup: "other-user", Status: "active", EndTime: now + 100},
	}).Error)
	group, groups, err := GetUserSubscriptionGroups(12361)
	require.NoError(t, err)
	assert.Equal(t, "codex-sub", group)
	assert.Equal(t, map[string]bool{"codex-sub": true, "domestic-sub": true, "expired": false, "cancelled": false, "future": false}, groups)
}
