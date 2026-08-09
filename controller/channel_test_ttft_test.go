package controller

import (
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChannelTestTTFTOnlyReturnsPositiveStreamingMeasurement(t *testing.T) {
	start := time.UnixMilli(1_000)
	tests := []struct {
		name     string
		info     *relaycommon.RelayInfo
		isStream bool
		want     int64
	}{
		{name: "streaming measurement", info: &relaycommon.RelayInfo{StartTime: start, FirstResponseTime: start.Add(812 * time.Millisecond)}, isStream: true, want: 812},
		{name: "non-streaming request", info: &relaycommon.RelayInfo{StartTime: start, FirstResponseTime: start.Add(812 * time.Millisecond)}, want: 0},
		{name: "missing first response", info: &relaycommon.RelayInfo{StartTime: start}, isStream: true, want: 0},
		{name: "non-positive measurement", info: &relaycommon.RelayInfo{StartTime: start, FirstResponseTime: start}, isStream: true, want: 0},
		{name: "missing relay info", isStream: true, want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, channelTestTTFT(test.info, test.isStream))
		})
	}
}

func TestAppendChannelTestAdminInfoMarksErrorLogs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(contextKeyChannelTestRequest, true)
	adminInfo := map[string]interface{}{}
	appendChannelTestAdminInfo(ctx, adminInfo)
	require.Equal(t, true, adminInfo["is_channel_test"])
}
