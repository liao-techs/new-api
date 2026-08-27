package middleware

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

var (
	rawRelayArchiveEnabled = service.RawRelayArchiveEnabled
	storeRawRelayExchange  = service.StoreRawRelayExchange
)

type rawRelayResponseWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
}

func (w *rawRelayResponseWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if n > 0 {
		_, _ = w.body.Write(data[:n])
	}
	return n, err
}

func (w *rawRelayResponseWriter) WriteString(data string) (int, error) {
	n, err := w.ResponseWriter.WriteString(data)
	if n > 0 {
		_, _ = w.body.WriteString(data[:n])
	}
	return n, err
}

// RawRelayArchive captures the exact decompressed client request body and the
// exact response bytes written by Gin. It runs after TokenAuth so user/token
// metadata is available, and before distribution so validation failures are
// archived as well. WebSocket traffic is intentionally excluded because its
// frames are no longer HTTP response-body bytes after the connection hijack.
func RawRelayArchive() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !rawRelayArchiveEnabled() || isRawRelayWebSocket(c.Request) {
			c.Next()
			return
		}

		startedAt := time.Now()
		captureStatus := "complete"
		captureErrors := make([]string, 0, 1)
		requestBody := readRawRelayRequestBody(c)
		if requestBody.err != nil {
			captureStatus = "partial"
			captureErrors = append(captureErrors, "request_body: "+requestBody.err.Error())
		}

		writer := &rawRelayResponseWriter{ResponseWriter: c.Writer}
		c.Writer = writer
		c.Next()

		exchange := service.RawRelayExchange{
			EventTime:     startedAt,
			DurationMs:    durationMilliseconds(startedAt, time.Now()),
			RequestID:     c.GetString(common.RequestIdKey),
			UserID:        safeInt32(c.GetInt("id")),
			TokenID:       safeInt32(c.GetInt("token_id")),
			GroupName:     rawRelayGroupName(c),
			Method:        c.Request.Method,
			RequestPath:   c.Request.URL.Path,
			ModelName:     common.GetContextKeyString(c, constant.ContextKeyOriginalModel),
			ChannelIDs:    rawRelayChannelIDs(c.GetStringSlice("use_channel")),
			StatusCode:    safeUint16(writer.Status()),
			RequestBody:   requestBody.body,
			ResponseBody:  bytes.Clone(writer.body.Bytes()),
			RequestBytes:  uint64(len(requestBody.body)),
			ResponseBytes: uint64(writer.body.Len()),
			CaptureStatus: captureStatus,
			CaptureError:  strings.Join(captureErrors, "; "),
		}
		if err := storeRawRelayExchange(exchange); err != nil {
			common.SysError(fmt.Sprintf("persist raw relay exchange %s: %v", exchange.RequestID, err))
		}
	}
}

type rawRelayRequestBodyResult struct {
	body []byte
	err  error
}

func readRawRelayRequestBody(c *gin.Context) rawRelayRequestBodyResult {
	if c.Request.Body == nil || c.Request.Body == http.NoBody {
		return rawRelayRequestBodyResult{body: []byte{}}
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return rawRelayRequestBodyResult{err: err}
	}
	body, err := storage.Bytes()
	if err != nil {
		return rawRelayRequestBodyResult{err: err}
	}
	if _, err := storage.Seek(0, io.SeekStart); err != nil {
		return rawRelayRequestBodyResult{body: bytes.Clone(body), err: err}
	}
	c.Request.Body = io.NopCloser(storage)
	return rawRelayRequestBodyResult{body: bytes.Clone(body)}
}

func isRawRelayWebSocket(request *http.Request) bool {
	if request == nil {
		return false
	}
	return strings.EqualFold(request.Header.Get("Upgrade"), "websocket") ||
		strings.Contains(strings.ToLower(request.Header.Get("Connection")), "upgrade")
}

func rawRelayGroupName(c *gin.Context) string {
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if group == "auto" {
		if actual := common.GetContextKeyString(c, constant.ContextKeyAutoGroup); actual != "" {
			return actual
		}
	}
	return group
}

func rawRelayChannelIDs(values []string) []int32 {
	ids := make([]int32, 0, len(values))
	for _, value := range values {
		id, err := strconv.ParseInt(value, 10, 32)
		if err == nil {
			ids = append(ids, int32(id))
		}
	}
	return ids
}

func durationMilliseconds(start, end time.Time) uint64 {
	if !end.After(start) {
		return 0
	}
	return uint64(end.Sub(start) / time.Millisecond)
}

func safeInt32(value int) int32 {
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	if value < math.MinInt32 {
		return math.MinInt32
	}
	return int32(value)
}

func safeUint16(value int) uint16 {
	if value < 0 {
		return 0
	}
	if value > math.MaxUint16 {
		return math.MaxUint16
	}
	return uint16(value)
}
