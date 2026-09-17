package service

import (
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseText2UsageWhitespaceDoesNotEstimatePrompt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, text := range []string{"", " ", "  ", "\n", " \t "} {
		t.Run(strconv.Quote(text), func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			usage := ResponseText2Usage(c, text, "gpt-4o", 89074)
			assert.Zero(t, usage.CompletionTokens)
			assert.Zero(t, usage.PromptTokens)
			assert.Zero(t, usage.TotalTokens)
			assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
		})
	}
}

func TestResponseText2UsageRealTextEstimatesPrompt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	usage := ResponseText2Usage(c, "hello", "gpt-4o", 100)
	require.NotZero(t, usage.CompletionTokens)
	assert.Equal(t, 100, usage.PromptTokens)
	assert.Equal(t, 100+usage.CompletionTokens, usage.TotalTokens)
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
}
