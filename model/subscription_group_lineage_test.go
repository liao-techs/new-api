package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteSubscriptionPreservesIndependentGroupCycles(t *testing.T) {
	for _, olderStillActive := range []bool{false, true} {
		name := "expired-old-cycle"
		if olderStillActive {
			name = "overlapping-old-cycle"
		}
		t.Run(name, func(t *testing.T) {
			truncateTables(t)
			createSubscriptionGroupTestUser(t, 19901, "codex-sub")
			now := GetDBTimestamp()
			old := UserSubscription{UserId: 19901, UpgradeGroup: "domestic-sub", PrevUserGroup: "default", Status: "expired", StartTime: now - 300, EndTime: now - 200}
			if olderStillActive {
				old.Status, old.EndTime = "active", now+300
			}
			current := UserSubscription{UserId: 19901, UpgradeGroup: "domestic-sub", PrevUserGroup: "premium", Status: "active", StartTime: now - 100, EndTime: now + 100}
			codex := UserSubscription{UserId: 19901, UpgradeGroup: "codex-sub", PrevUserGroup: "domestic-sub", Status: "active", StartTime: now - 50, EndTime: now + 200}
			for _, sub := range []*UserSubscription{&old, &current, &codex} {
				require.NoError(t, DB.Create(sub).Error)
			}

			_, err := AdminDeleteUserSubscription(old.Id)
			require.NoError(t, err)
			var linked UserSubscription
			require.NoError(t, DB.First(&linked, codex.Id).Error)
			assert.Equal(t, "domestic-sub", linked.PrevUserGroup)
			_, err = AdminInvalidateUserSubscription(current.Id)
			require.NoError(t, err)
			_, err = AdminInvalidateUserSubscription(codex.Id)
			require.NoError(t, err)
			assert.Equal(t, "premium", getSubscriptionGroupTestUserGroup(t, 19901))
		})
	}
}

func TestSubscriptionRollbackPreservesManualBaselineAfterPastGrant(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		endOffset    int64
	}{
		{"expired-before-purchase", "expired", -200},
		{"cancelled-before-purchase", "cancelled", -200},
		{"expires-at-purchase", "expired", -100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			createSubscriptionGroupTestUser(t, 19902, "domestic-sub")
			now := GetDBTimestamp()
			old := UserSubscription{UserId: 19902, UpgradeGroup: "manual-tier", PrevUserGroup: "default", Status: tc.status, StartTime: now - 300, EndTime: now + tc.endOffset}
			// The past grant ended before the administrator assigned manual-tier.
			// This later purchase captured the independently assigned account group.
			current := UserSubscription{UserId: 19902, UpgradeGroup: "domestic-sub", PrevUserGroup: "manual-tier", Status: "active", StartTime: now - 100, EndTime: now + 200}
			require.NoError(t, DB.Create(&old).Error)
			require.NoError(t, DB.Create(&current).Error)

			_, err := AdminInvalidateUserSubscription(current.Id)
			require.NoError(t, err)
			assert.Equal(t, "manual-tier", getSubscriptionGroupTestUserGroup(t, 19902))
		})
	}
}

func TestDeleteSubscriptionReconnectsOnlyActualPredecessor(t *testing.T) {
	for _, sameSecond := range []bool{false, true} {
		name := "distinct-start-times"
		if sameSecond {
			name = "same-second-ordered-by-id"
		}
		t.Run(name, func(t *testing.T) {
			truncateTables(t)
			createSubscriptionGroupTestUser(t, 19903, "codex-sub")
			now := GetDBTimestamp()
			parent := UserSubscription{UserId: 19903, UpgradeGroup: "domestic-sub", PrevUserGroup: "premium", Status: "active", StartTime: now - 20, EndTime: now + 100}
			child := UserSubscription{UserId: 19903, UpgradeGroup: "codex-sub", PrevUserGroup: "domestic-sub", Status: "active", StartTime: now - 10, EndTime: now + 200}
			if sameSecond {
				child.StartTime = parent.StartTime
			}
			require.NoError(t, DB.Create(&parent).Error)
			require.NoError(t, DB.Create(&child).Error)

			_, err := AdminDeleteUserSubscription(parent.Id)
			require.NoError(t, err)
			var linked UserSubscription
			require.NoError(t, DB.First(&linked, child.Id).Error)
			assert.Equal(t, "premium", linked.PrevUserGroup)
			_, err = AdminInvalidateUserSubscription(child.Id)
			require.NoError(t, err)
			assert.Equal(t, "premium", getSubscriptionGroupTestUserGroup(t, 19903))
		})
	}
}

func TestCancelSubscriptionPreservesChildrenPurchasedInSameSecond(t *testing.T) {
	truncateTables(t)
	createSubscriptionGroupTestUser(t, 19904, "codex-sub")
	now := GetDBTimestamp()
	parent := UserSubscription{UserId: 19904, UpgradeGroup: "domestic-sub", PrevUserGroup: "premium", Status: "active", StartTime: now, EndTime: now + 100}
	child := UserSubscription{UserId: 19904, UpgradeGroup: "codex-sub", PrevUserGroup: "domestic-sub", Status: "active", StartTime: now, EndTime: now + 200}
	require.NoError(t, DB.Create(&parent).Error)
	require.NoError(t, DB.Create(&child).Error)

	_, err := AdminInvalidateUserSubscription(parent.Id)
	require.NoError(t, err)
	var linked UserSubscription
	require.NoError(t, DB.First(&linked, child.Id).Error)
	assert.Equal(t, "premium", linked.PrevUserGroup)
	_, err = AdminInvalidateUserSubscription(child.Id)
	require.NoError(t, err)
	assert.Equal(t, "premium", getSubscriptionGroupTestUserGroup(t, 19904))
}

func TestSubscriptionRenewalUsesOriginalBaselineCaptureTime(t *testing.T) {
	truncateTables(t)
	createSubscriptionGroupTestUser(t, 19905, "domestic-sub")
	now := GetDBTimestamp()
	previous := UserSubscription{UserId: 19905, UpgradeGroup: "codex-sub", PrevUserGroup: "premium", Status: "expired", StartTime: now - 300, EndTime: now - 100}
	current := UserSubscription{UserId: 19905, UpgradeGroup: "domestic-sub", PrevUserGroup: "codex-sub", Status: "active", StartTime: now - 200, EndTime: now + 100}
	require.NoError(t, DB.Create(&previous).Error)
	require.NoError(t, DB.Create(&current).Error)
	plan := SubscriptionPlan{Title: "Domestic renewal", UpgradeGroup: "domestic-sub", DurationUnit: SubscriptionDurationMonth, DurationValue: 1}
	require.NoError(t, DB.Create(&plan).Error)

	renewal, err := CreateUserSubscriptionFromPlanTx(DB, 19905, &plan, "order")
	require.NoError(t, err)
	_, err = AdminInvalidateUserSubscription(current.Id)
	require.NoError(t, err)
	_, err = AdminInvalidateUserSubscription(renewal.Id)
	require.NoError(t, err)
	assert.Equal(t, "premium", getSubscriptionGroupTestUserGroup(t, 19905))
}
