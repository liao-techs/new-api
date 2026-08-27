package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createSubscriptionGroupTestUser(t *testing.T, id int, group string) {
	t.Helper()
	require.NoError(t, DB.Create(&User{
		Id:       id,
		Username: fmt.Sprintf("subscription_group_user_%d", id),
		Status:   common.UserStatusEnabled,
		Group:    group,
	}).Error)
}

func getSubscriptionGroupTestUserGroup(t *testing.T, id int) string {
	t.Helper()
	var user User
	require.NoError(t, DB.Where("id = ?", id).First(&user).Error)
	return user.Group
}

func TestExpireDueSubscriptionsRevertsWhenLatestRenewalHasNoPreviousGroup(t *testing.T) {
	truncateTables(t)
	const userID = 9001
	createSubscriptionGroupTestUser(t, userID, "vip")

	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&UserSubscription{
		UserId:        userID,
		PlanId:        1,
		Status:        "active",
		StartTime:     now - 100,
		EndTime:       now - 20,
		UpgradeGroup:  "vip",
		PrevUserGroup: "default",
	}).Error)
	require.NoError(t, DB.Create(&UserSubscription{
		UserId:        userID,
		PlanId:        1,
		Status:        "active",
		StartTime:     now - 50,
		EndTime:       now - 10,
		UpgradeGroup:  "vip",
		PrevUserGroup: "",
	}).Error)

	expired, err := ExpireDueSubscriptions(100)

	require.NoError(t, err)
	assert.Equal(t, 2, expired)
	assert.Equal(t, "default", getSubscriptionGroupTestUserGroup(t, userID))
}

func TestCreateUserSubscriptionFromPlanTxRenewalInheritsPreviousGroup(t *testing.T) {
	truncateTables(t)
	const userID = 9002
	createSubscriptionGroupTestUser(t, userID, "default")

	plan := &SubscriptionPlan{
		Id:            50,
		Title:         "VIP",
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       true,
		UpgradeGroup:  "vip",
	}
	require.NoError(t, DB.Create(plan).Error)

	first, err := CreateUserSubscriptionFromPlanTx(DB, userID, plan, "order")
	require.NoError(t, err)
	assert.Equal(t, "default", first.PrevUserGroup)

	renewal, err := CreateUserSubscriptionFromPlanTx(DB, userID, plan, "order")

	require.NoError(t, err)
	assert.Equal(t, "default", renewal.PrevUserGroup)
}

func TestExpireDueSubscriptionsKeepsExplicitDowngradeGroupPriority(t *testing.T) {
	truncateTables(t)
	const userID = 9003
	createSubscriptionGroupTestUser(t, userID, "vip")

	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&UserSubscription{
		UserId:        userID,
		PlanId:        1,
		Status:        "expired",
		StartTime:     now - 200,
		EndTime:       now - 100,
		UpgradeGroup:  "vip",
		PrevUserGroup: "default",
	}).Error)
	require.NoError(t, DB.Create(&UserSubscription{
		UserId:         userID,
		PlanId:         1,
		Status:         "active",
		StartTime:      now - 50,
		EndTime:        now - 10,
		UpgradeGroup:   "vip",
		PrevUserGroup:  "",
		DowngradeGroup: "free",
	}).Error)

	expired, err := ExpireDueSubscriptions(100)

	require.NoError(t, err)
	assert.Equal(t, 1, expired)
	assert.Equal(t, "free", getSubscriptionGroupTestUserGroup(t, userID))
}

func TestExpireDueSubscriptionsKeepsGroupWhileAnotherUpgradeIsActive(t *testing.T) {
	truncateTables(t)
	const userID = 9004
	createSubscriptionGroupTestUser(t, userID, "vip")

	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&UserSubscription{
		UserId:        userID,
		PlanId:        1,
		Status:        "active",
		StartTime:     now - 100,
		EndTime:       now - 10,
		UpgradeGroup:  "vip",
		PrevUserGroup: "default",
	}).Error)
	require.NoError(t, DB.Create(&UserSubscription{
		UserId:        userID,
		PlanId:        1,
		Status:        "active",
		StartTime:     now - 50,
		EndTime:       now + 1000,
		UpgradeGroup:  "vip",
		PrevUserGroup: "default",
	}).Error)

	expired, err := ExpireDueSubscriptions(100)

	require.NoError(t, err)
	assert.Equal(t, 1, expired)
	assert.Equal(t, "vip", getSubscriptionGroupTestUserGroup(t, userID))
}

func TestExpireDueSubscriptionsDoesNotOverrideManualGroup(t *testing.T) {
	truncateTables(t)
	const userID = 9005
	createSubscriptionGroupTestUser(t, userID, "manual")

	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&UserSubscription{
		UserId:        userID,
		PlanId:        1,
		Status:        "active",
		StartTime:     now - 100,
		EndTime:       now - 10,
		UpgradeGroup:  "vip",
		PrevUserGroup: "default",
	}).Error)

	expired, err := ExpireDueSubscriptions(100)

	require.NoError(t, err)
	assert.Equal(t, 1, expired)
	assert.Equal(t, "manual", getSubscriptionGroupTestUserGroup(t, userID))
}

func TestAdminInvalidateUserSubscriptionRecoversPreviousGroupFromChain(t *testing.T) {
	truncateTables(t)
	const userID = 9006
	createSubscriptionGroupTestUser(t, userID, "vip")

	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&UserSubscription{
		UserId:        userID,
		PlanId:        1,
		Status:        "expired",
		StartTime:     now - 100,
		EndTime:       now - 50,
		UpgradeGroup:  "vip",
		PrevUserGroup: "default",
	}).Error)
	renewal := &UserSubscription{
		UserId:        userID,
		PlanId:        1,
		Status:        "active",
		StartTime:     now - 40,
		EndTime:       now + 1000,
		UpgradeGroup:  "vip",
		PrevUserGroup: "",
	}
	require.NoError(t, DB.Create(renewal).Error)

	message, err := AdminInvalidateUserSubscription(renewal.Id)

	require.NoError(t, err)
	assert.Contains(t, message, "default")
	assert.Equal(t, "default", getSubscriptionGroupTestUserGroup(t, userID))
}

func TestAdminDeleteUserSubscriptionRecoversPreviousGroupFromChain(t *testing.T) {
	truncateTables(t)
	const userID = 9007
	createSubscriptionGroupTestUser(t, userID, "vip")

	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&UserSubscription{
		UserId:        userID,
		PlanId:        1,
		Status:        "expired",
		StartTime:     now - 100,
		EndTime:       now - 50,
		UpgradeGroup:  "vip",
		PrevUserGroup: "default",
	}).Error)
	renewal := &UserSubscription{
		UserId:        userID,
		PlanId:        1,
		Status:        "active",
		StartTime:     now - 40,
		EndTime:       now + 1000,
		UpgradeGroup:  "vip",
		PrevUserGroup: "",
	}
	require.NoError(t, DB.Create(renewal).Error)

	message, err := AdminDeleteUserSubscription(renewal.Id)

	require.NoError(t, err)
	assert.Contains(t, message, "default")
	assert.Equal(t, "default", getSubscriptionGroupTestUserGroup(t, userID))
	var count int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", renewal.Id).Count(&count).Error)
	assert.Zero(t, count)
}

func TestExpireDueSubscriptionsLeavesGroupWhenNoBaselineCanBeRecovered(t *testing.T) {
	truncateTables(t)
	const userID = 9008
	createSubscriptionGroupTestUser(t, userID, "vip")

	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&UserSubscription{
		UserId:        userID,
		PlanId:        1,
		Status:        "active",
		StartTime:     now - 50,
		EndTime:       now - 10,
		UpgradeGroup:  "vip",
		PrevUserGroup: "",
	}).Error)

	expired, err := ExpireDueSubscriptions(100)

	require.NoError(t, err)
	assert.Equal(t, 1, expired)
	assert.Equal(t, "vip", getSubscriptionGroupTestUserGroup(t, userID))
}

func TestExpireDueSubscriptionsUsesMostRecentSubscriptionChainBaseline(t *testing.T) {
	truncateTables(t)
	const userID = 9009
	createSubscriptionGroupTestUser(t, userID, "vip")

	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&UserSubscription{
		UserId:        userID,
		PlanId:        1,
		Status:        "expired",
		StartTime:     now - 300,
		EndTime:       now - 250,
		UpgradeGroup:  "vip",
		PrevUserGroup: "default",
	}).Error)
	require.NoError(t, DB.Create(&UserSubscription{
		UserId:        userID,
		PlanId:        1,
		Status:        "expired",
		StartTime:     now - 100,
		EndTime:       now - 60,
		UpgradeGroup:  "vip",
		PrevUserGroup: "premium",
	}).Error)
	require.NoError(t, DB.Create(&UserSubscription{
		UserId:        userID,
		PlanId:        1,
		Status:        "active",
		StartTime:     now - 50,
		EndTime:       now - 10,
		UpgradeGroup:  "vip",
		PrevUserGroup: "",
	}).Error)

	expired, err := ExpireDueSubscriptions(100)

	require.NoError(t, err)
	assert.Equal(t, 1, expired)
	assert.Equal(t, "premium", getSubscriptionGroupTestUserGroup(t, userID))
}
