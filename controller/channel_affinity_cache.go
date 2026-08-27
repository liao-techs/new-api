package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetChannelAffinityCacheStats(c *gin.Context) {
	stats := service.GetChannelAffinityCacheStats()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    stats,
	})
}

// parseChannelAffinityFilterQuery reads the shared channel_id / user_id / rule_name
// filter used by both the entry listing and the filtered clear endpoint.
func parseChannelAffinityFilterQuery(c *gin.Context) (service.ChannelAffinityEntryFilter, error) {
	filter := service.ChannelAffinityEntryFilter{
		RuleName: strings.TrimSpace(c.Query("rule_name")),
	}

	if raw := strings.TrimSpace(c.Query("channel_id")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return filter, fmt.Errorf("参数 channel_id 无效：%s", raw)
		}
		filter.ChannelID = v
	}
	if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return filter, fmt.Errorf("参数 user_id 无效：%s", raw)
		}
		filter.UserID = v
	}
	return filter, nil
}

// GetChannelAffinityEntries lists affinity bindings, optionally narrowed by
// channel, user or rule. Aggregates cover the full match set while the entries
// array is capped by limit.
func GetChannelAffinityEntries(c *gin.Context) {
	filter, err := parseChannelAffinityFilterQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	limit := 0
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		v, convErr := strconv.Atoi(raw)
		if convErr != nil || v <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": fmt.Sprintf("参数 limit 无效：%s", raw),
			})
			return
		}
		limit = v
	}

	list, err := service.ListChannelAffinityEntries(filter, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    list,
	})
}

func ClearChannelAffinityCache(c *gin.Context) {
	all := strings.TrimSpace(c.Query("all"))
	ruleName := strings.TrimSpace(c.Query("rule_name"))

	if all == "true" {
		deleted := service.ClearChannelAffinityCacheAll()
		setChannelAffinityClearAuditDetail(c, map[string]interface{}{
			"scope":   "all",
			"deleted": deleted,
		})
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data": gin.H{
				"deleted": deleted,
			},
		})
		return
	}

	filter, err := parseChannelAffinityFilterQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// channel_id / user_id need a value scan, so they take the filtered path even
	// when combined with rule_name. Plain rule_name keeps the original
	// prefix-delete fast path.
	if filter.ChannelID > 0 || filter.UserID > 0 {
		deleted, clearErr := service.ClearChannelAffinityCacheByFilter(filter)
		if clearErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": clearErr.Error(),
			})
			return
		}
		detail := map[string]interface{}{
			"scope":   "filter",
			"deleted": deleted,
		}
		if filter.ChannelID > 0 {
			detail["channel_id"] = filter.ChannelID
		}
		if filter.UserID > 0 {
			detail["user_id"] = filter.UserID
		}
		if filter.RuleName != "" {
			detail["rule_name"] = filter.RuleName
		}
		setChannelAffinityClearAuditDetail(c, detail)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data": gin.H{
				"deleted": deleted,
			},
		})
		return
	}

	if ruleName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "缺少参数：rule_name / channel_id / user_id，或使用 all=true 清空全部",
		})
		return
	}

	deleted, err := service.ClearChannelAffinityCacheByRuleName(ruleName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	setChannelAffinityClearAuditDetail(c, map[string]interface{}{
		"scope":     "rule",
		"rule_name": ruleName,
		"deleted":   deleted,
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"deleted": deleted,
		},
	})
}

// setChannelAffinityClearAuditDetail records what a clear call actually targeted.
// The fallback audit only stores method and route, which cannot distinguish
// "cleared one channel" from "wiped every binding".
func setChannelAffinityClearAuditDetail(c *gin.Context, detail map[string]interface{}) {
	common.SetContextKey(c, constant.ContextKeyAuditDetail, detail)
}

func GetChannelAffinityUsageCacheStats(c *gin.Context) {
	ruleName := strings.TrimSpace(c.Query("rule_name"))
	usingGroup := strings.TrimSpace(c.Query("using_group"))
	keyFp := strings.TrimSpace(c.Query("key_fp"))

	if ruleName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "missing param: rule_name",
		})
		return
	}
	if keyFp == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "missing param: key_fp",
		})
		return
	}

	stats := service.GetChannelAffinityUsageCacheStats(ruleName, usingGroup, keyFp)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    stats,
	})
}
