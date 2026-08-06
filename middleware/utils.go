package middleware

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func abortWithTokenGroupRatioLimit(c *gin.Context, limitErr *service.TokenGroupRatioLimitError) {
	service.RecordTokenGroupRatioLimitAudit(c, limitErr)
	message := common.MessageWithRequestId(limitErr.Error(), c.GetString(common.RequestIdKey))
	c.JSON(http.StatusPaymentRequired, gin.H{
		"error": types.OpenAIError{
			Message: message,
			Type:    string(types.ErrorCodePriceLimitExceeded),
			Param:   "group",
			Code:    string(types.ErrorCodePriceLimitExceeded),
		},
		"price_guard": limitErr,
	})
	c.Abort()
	logger.LogError(c.Request.Context(), fmt.Sprintf("user %d | %s", c.GetInt("id"), message))
}

func abortWithOpenAiMessage(c *gin.Context, statusCode int, message string, code ...types.ErrorCode) {
	codeStr := ""
	if len(code) > 0 {
		codeStr = string(code[0])
	}
	userId := c.GetInt("id")
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"message": common.MessageWithRequestId(message, c.GetString(common.RequestIdKey)),
			"type":    "new_api_error",
			"code":    codeStr,
		},
	})
	c.Abort()
	logger.LogError(c.Request.Context(), fmt.Sprintf("user %d | %s", userId, message))
}

func abortWithMidjourneyMessage(c *gin.Context, statusCode int, code int, description string) {
	c.JSON(statusCode, gin.H{
		"description": description,
		"type":        "new_api_error",
		"code":        code,
	})
	c.Abort()
	logger.LogError(c.Request.Context(), description)
}
