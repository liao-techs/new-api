package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRawRelayArchiveCapturesRequestResponseAndLogJoinMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalEnabled := rawRelayArchiveEnabled
	originalStore := storeRawRelayExchange
	t.Cleanup(func() {
		rawRelayArchiveEnabled = originalEnabled
		storeRawRelayExchange = originalStore
	})

	var captured service.RawRelayExchange
	rawRelayArchiveEnabled = func() bool { return true }
	storeRawRelayExchange = func(exchange service.RawRelayExchange) error {
		captured = exchange
		return nil
	}

	router := gin.New()
	router.Use(BodyStorageCleanup())
	router.Use(func(c *gin.Context) {
		c.Set(common.RequestIdKey, "req-joined-to-logs")
		c.Set("id", 42)
		c.Set("token_id", 99)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "auto")
		c.Next()
	})
	router.Use(RawRelayArchive())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-test")
		common.SetContextKey(c, constant.ContextKeyAutoGroup, "vip")
		c.Set("use_channel", []string{"7", "bad", "8"})
		c.Status(http.StatusOK)
		_, _ = c.Writer.WriteString("data: {\"delta\":1}\n\n")
		_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
	})

	requestBody := `{ "model": "gpt-test", "stream": true }`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "req-joined-to-logs", captured.RequestID)
	require.Equal(t, int32(42), captured.UserID)
	require.Equal(t, int32(99), captured.TokenID)
	require.Equal(t, "vip", captured.GroupName)
	require.Equal(t, "gpt-test", captured.ModelName)
	require.Equal(t, []int32{7, 8}, captured.ChannelIDs)
	require.Equal(t, requestBody, string(captured.RequestBody))
	require.Equal(t, response.Body.String(), string(captured.ResponseBody))
	require.Equal(t, uint64(len(requestBody)), captured.RequestBytes)
	require.Equal(t, uint64(response.Body.Len()), captured.ResponseBytes)
	require.Equal(t, "complete", captured.CaptureStatus)
	require.Empty(t, captured.CaptureError)
}

func TestRawRelayArchiveSkipsWebSocketUpgrade(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalEnabled := rawRelayArchiveEnabled
	originalStore := storeRawRelayExchange
	t.Cleanup(func() {
		rawRelayArchiveEnabled = originalEnabled
		storeRawRelayExchange = originalStore
	})

	stored := false
	rawRelayArchiveEnabled = func() bool { return true }
	storeRawRelayExchange = func(service.RawRelayExchange) error {
		stored = true
		return nil
	}

	router := gin.New()
	router.Use(RawRelayArchive())
	router.GET("/v1/realtime", func(c *gin.Context) { c.Status(http.StatusSwitchingProtocols) })
	request := httptest.NewRequest(http.MethodGet, "/v1/realtime", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	router.ServeHTTP(httptest.NewRecorder(), request)
	require.False(t, stored)
}

func TestReadRawRelayRequestBodyRestoresBodyForDownstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("exact-body"))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = request

	result := readRawRelayRequestBody(c)
	require.NoError(t, result.err)
	require.Equal(t, "exact-body", string(result.body))
	downstream, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	require.Equal(t, "exact-body", string(downstream))
	common.CleanupBodyStorage(c)
}
