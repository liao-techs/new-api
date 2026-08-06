package service

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

const tokenGroupRatioComparisonEpsilon = 1e-9

type TokenGroupRatioLimitError struct {
	Group   string  `json:"-"`
	Current float64 `json:"current_group_ratio"`
	Max     float64 `json:"max_group_ratio"`
}

type tokenGroupRatioSnapshotEntry struct {
	Ratio        float64
	SpecialRatio float64
	HasSpecial   bool
}

func (e *TokenGroupRatioLimitError) Error() string {
	return fmt.Sprintf(
		"当前分组 %s 的倍率 %g，高于该 API Key 允许的最高倍率 %g；请求未发送，也未扣费",
		e.Group,
		e.Current,
		e.Max,
	)
}

func GetTokenMaxGroupRatio(c *gin.Context) (float64, bool) {
	return common.GetContextKeyType[float64](c, constant.ContextKeyTokenMaxGroupRatio)
}

func currentGroupRatio(userGroup, group string) tokenGroupRatioSnapshotEntry {
	if specialRatio, ok := ratio_setting.GetGroupGroupRatio(userGroup, group); ok {
		return tokenGroupRatioSnapshotEntry{
			Ratio:        specialRatio,
			SpecialRatio: specialRatio,
			HasSpecial:   true,
		}
	}
	return tokenGroupRatioSnapshotEntry{
		Ratio:        ratio_setting.GetGroupRatio(group),
		SpecialRatio: -1,
	}
}

// CaptureTokenGroupRatioSnapshot freezes effective group ratios for a guarded
// request. Price changes after authentication affect only subsequent requests.
func CaptureTokenGroupRatioSnapshot(c *gin.Context, userGroup string) {
	if _, limited := GetTokenMaxGroupRatio(c); !limited {
		return
	}

	snapshot := make(map[string]tokenGroupRatioSnapshotEntry)
	for group := range ratio_setting.GetGroupRatioCopy() {
		snapshot[group] = currentGroupRatio(userGroup, group)
	}
	if specialGroups, ok := ratio_setting.GetGroupRatioSetting().GroupGroupRatio.Get(userGroup); ok {
		for group := range specialGroups {
			snapshot[group] = currentGroupRatio(userGroup, group)
		}
	}
	if userGroup != "" {
		if _, ok := snapshot[userGroup]; !ok {
			snapshot[userGroup] = currentGroupRatio(userGroup, userGroup)
		}
	}
	common.SetContextKey(c, constant.ContextKeyTokenGroupRatioSnapshot, snapshot)
}

func GetRequestGroupRatio(c *gin.Context, userGroup, group string) (ratio float64, specialRatio float64, hasSpecial bool) {
	if snapshot, ok := common.GetContextKeyType[map[string]tokenGroupRatioSnapshotEntry](
		c,
		constant.ContextKeyTokenGroupRatioSnapshot,
	); ok {
		if entry, found := snapshot[group]; found {
			return entry.Ratio, entry.SpecialRatio, entry.HasSpecial
		}
		// A group introduced after request ingress did not have a configured
		// ratio in the snapshot. Preserve the ingress-time fallback semantics
		// instead of reading a newly published live price.
		return 1, -1, false
	}
	entry := currentGroupRatio(userGroup, group)
	return entry.Ratio, entry.SpecialRatio, entry.HasSpecial
}

func CheckTokenGroupRatioLimit(c *gin.Context, group string, currentRatio float64) error {
	maxRatio, limited := GetTokenMaxGroupRatio(c)
	if !limited || currentRatio <= maxRatio+tokenGroupRatioComparisonEpsilon {
		return nil
	}
	return &TokenGroupRatioLimitError{
		Group:   group,
		Current: currentRatio,
		Max:     maxRatio,
	}
}

func FilterAutoGroupsByTokenRatioLimit(c *gin.Context, userGroup string, groups []string) ([]string, error) {
	if _, limited := GetTokenMaxGroupRatio(c); !limited {
		return groups, nil
	}

	eligible := make([]string, 0, len(groups))
	var closest *TokenGroupRatioLimitError
	for _, group := range groups {
		ratio, _, _ := GetRequestGroupRatio(c, userGroup, group)
		err := CheckTokenGroupRatioLimit(c, group, ratio)
		if err == nil {
			eligible = append(eligible, group)
			continue
		}
		limitErr := err.(*TokenGroupRatioLimitError)
		if closest == nil || limitErr.Current < closest.Current {
			closest = limitErr
		}
	}
	if len(eligible) == 0 && closest != nil {
		return eligible, closest
	}
	return eligible, nil
}

func NewTokenGroupRatioLimitAPIError(limitErr *TokenGroupRatioLimitError) *types.NewAPIError {
	apiErr := types.WithOpenAIError(types.OpenAIError{
		Message: limitErr.Error(),
		Type:    string(types.ErrorCodePriceLimitExceeded),
		Param:   "group",
		Code:    string(types.ErrorCodePriceLimitExceeded),
	}, http.StatusPaymentRequired, types.ErrOptionWithSkipRetry())
	apiErr.Err = limitErr
	metadata, err := common.Marshal(limitErr)
	if err == nil {
		apiErr.Metadata = metadata
	}
	return apiErr
}

func RecordTokenGroupRatioLimitAudit(c *gin.Context, limitErr *TokenGroupRatioLimitError) {
	if c == nil || limitErr == nil ||
		common.GetContextKeyBool(c, constant.ContextKeyTokenRatioAuditLogged) {
		return
	}
	common.SetContextKey(c, constant.ContextKeyTokenRatioAuditLogged, true)
	// Audit is intentionally fail-open: an unavailable log/cache backend must
	// never replace the deterministic price-guard response with a panic.
	defer func() {
		if recovered := recover(); recovered != nil {
			common.SysLog(fmt.Sprintf(
				"failed to record token group ratio limit audit: %v",
				recovered,
			))
		}
	}()
	model.RecordErrorLog(
		c,
		c.GetInt("id"),
		c.GetInt("channel_id"),
		c.GetString("original_model"),
		c.GetString("token_name"),
		limitErr.Error(),
		c.GetInt("token_id"),
		0,
		common.GetContextKeyBool(c, constant.ContextKeyIsStream),
		limitErr.Group,
		map[string]interface{}{
			"error_type":  string(types.ErrorCodePriceLimitExceeded),
			"error_code":  string(types.ErrorCodePriceLimitExceeded),
			"status_code": http.StatusPaymentRequired,
			"price_guard": map[string]float64{
				"current_group_ratio": limitErr.Current,
				"max_group_ratio":     limitErr.Max,
			},
		},
	)
}

func MidjourneyErrorFromTokenGroupRatioLimit(c *gin.Context, err error) (*taskdto.MidjourneyResponse, bool) {
	var limitErr *TokenGroupRatioLimitError
	if !errors.As(err, &limitErr) {
		return nil, false
	}
	RecordTokenGroupRatioLimitAudit(c, limitErr)
	return &taskdto.MidjourneyResponse{
		Code:        http.StatusPaymentRequired,
		Description: limitErr.Error(),
		Properties:  limitErr,
	}, true
}
