package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

func configuredUserUsableGroups(userGroup string) map[string]string {
	groupsCopy := setting.GetUserUsableGroupsCopy()
	if userGroup != "" {
		specialSettings, b := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Get(userGroup)
		if b {
			// 处理特殊可用分组
			for specialGroup, desc := range specialSettings {
				if after, ok := strings.CutPrefix(specialGroup, "-:"); ok {
					// 移除分组
					groupToRemove := after
					delete(groupsCopy, groupToRemove)
				} else if after, ok := strings.CutPrefix(specialGroup, "+:"); ok {
					// 添加分组
					groupToAdd := after
					groupsCopy[groupToAdd] = desc
				} else {
					// 直接添加分组
					groupsCopy[specialGroup] = desc
				}
			}
		}
	}
	return groupsCopy
}

// GetUserUsableGroups resolves configuration-only access (including anonymous
// pricing). Authenticated callers must use the user/request-aware resolver.
func GetUserUsableGroups(userGroup string) map[string]string {
	groups := configuredUserUsableGroups(userGroup)
	if userGroup != "" {
		if _, ok := groups[userGroup]; !ok {
			groups[userGroup] = "用户分组"
		}
	}
	return groups
}

func GetUserUsableGroupsForUser(userId int) (map[string]string, error) {
	userGroup, subscriptionGroups, err := model.GetUserSubscriptionGroups(userId)
	if err != nil {
		return nil, err
	}
	groups := configuredUserUsableGroups(userGroup)
	// Explicit public/admin grants keep their configuration semantics. Only
	// the implicit users.group grant is withdrawn when its subscription ends.
	if active, subscribed := subscriptionGroups[userGroup]; userGroup != "" && (!subscribed || active) {
		if _, ok := groups[userGroup]; !ok {
			groups[userGroup] = "用户分组"
		}
	}
	for group, active := range subscriptionGroups {
		if active {
			if _, ok := groups[group]; !ok {
				groups[group] = "用户分组"
			}
		}
	}
	return groups, nil
}

// Resolve once per request; never cache subscription grants across requests.
// Expiry/cancellation therefore cannot retain access through the user cache.
func GetRequestUsableGroups(c *gin.Context, userGroup string) (map[string]string, error) {
	if groups, ok := common.GetContextKeyType[map[string]string](c, constant.ContextKeyUserUsableGroups); ok {
		return groups, nil
	}
	if userId := c.GetInt("id"); userId > 0 {
		groups, err := GetUserUsableGroupsForUser(userId)
		if err != nil {
			return nil, err
		}
		common.SetContextKey(c, constant.ContextKeyUserUsableGroups, groups)
		return groups, nil
	}
	return GetUserUsableGroups(userGroup), nil
}

func IsUserSelectableGroup(usableGroups map[string]string, groupName string) bool {
	if groupName == "" || groupName == "auto" {
		return false
	}
	_, ok := usableGroups[groupName]
	return ok && ratio_setting.ContainsGroupRatio(groupName)
}

// GetUserAutoGroup 根据用户分组获取自动分组设置
func GetUserAutoGroup(usableGroups map[string]string) []string {
	autoGroups := make([]string, 0)
	seen := make(map[string]struct{})
	for _, group := range setting.GetAutoGroups() {
		if !IsUserSelectableGroup(usableGroups, group) {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		autoGroups = append(autoGroups, group)
	}
	return autoGroups
}

// FilterUserTokenAutoGroups applies current permissions before the current
// per-token limit. It intentionally does not fall back to the global Auto list.
func FilterUserTokenAutoGroups(usableGroups map[string]string, groups []string) []string {
	maxCount := setting.GetMaxTokenAutoGroups()
	filtered := make([]string, 0, min(len(groups), maxCount))
	seen := make(map[string]struct{})
	for _, group := range groups {
		if !IsUserSelectableGroup(usableGroups, group) {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		filtered = append(filtered, group)
		if len(filtered) == maxCount {
			break
		}
	}
	return filtered
}

// GetRequestAutoGroups resolves the ordered Auto groups for the current token.
// The absence of the context value means that the token inherits the complete
// global Auto list; a present (even empty) value is an explicit token snapshot.
func GetRequestAutoGroups(c *gin.Context, userGroup string) ([]string, error) {
	usableGroups, err := GetRequestUsableGroups(c, userGroup)
	if err != nil {
		return nil, err
	}
	if group := common.GetContextKeyString(c, constant.ContextKeySubscriptionGroup); group != "" {
		allowed := make(map[string]string)
		if desc, ok := usableGroups[group]; ok {
			allowed[group] = desc
		}
		usableGroups = allowed
	}
	value, ok := common.GetContextKey(c, constant.ContextKeyTokenAutoGroups)
	if !ok {
		return GetUserAutoGroup(usableGroups), nil
	}
	groups, ok := value.([]string)
	if !ok {
		return []string{}, nil
	}
	return FilterUserTokenAutoGroups(usableGroups, groups), nil
}

func GetUserAutoGroupMaxRatio(userGroup string, usableGroups map[string]string) (float64, bool) {
	autoGroups := GetUserAutoGroup(usableGroups)
	var maxRatio float64
	found := false
	for _, group := range autoGroups {
		ratio := GetUserGroupRatio(userGroup, group)
		if !found || ratio > maxRatio {
			maxRatio = ratio
			found = true
		}
	}
	return maxRatio, found
}

// GetGroupsEnabledModels 按 groups 顺序获取各分组启用的模型并去重
func GetGroupsEnabledModels(groups []string) []string {
	seen := make(map[string]struct{})
	models := make([]string, 0)
	for _, group := range groups {
		for _, modelName := range model.GetGroupEnabledModels(group) {
			if _, ok := seen[modelName]; !ok {
				seen[modelName] = struct{}{}
				models = append(models, modelName)
			}
		}
	}
	return models
}

// GetUserGroupRatio 获取用户使用某个分组的倍率
// userGroup 用户分组
// group 需要获取倍率的分组
func GetUserGroupRatio(userGroup, group string) float64 {
	ratio, ok := ratio_setting.GetGroupGroupRatio(userGroup, group)
	if ok {
		return ratio
	}
	return ratio_setting.GetGroupRatio(group)
}
