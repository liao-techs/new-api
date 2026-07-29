package service

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// registerAffinityRuleForTest installs a uniquely named rule so that scans in this
// file can filter down to their own entries; the affinity cache is a process-wide
// singleton shared with every other test in the package.
func registerAffinityRuleForTest(t *testing.T, rule operation_setting.ChannelAffinityRule) {
	t.Helper()

	setting := operation_setting.GetChannelAffinitySetting()
	require.NotNil(t, setting)

	original := setting.Rules
	setting.Rules = append([]operation_setting.ChannelAffinityRule{rule}, original...)
	t.Cleanup(func() {
		setting.Rules = original
	})
}

func seedAffinityEntry(t *testing.T, rule operation_setting.ChannelAffinityRule, model, group, value string, entry ChannelAffinityEntry) string {
	t.Helper()

	suffix := buildChannelAffinityCacheKeySuffix(rule, model, group, value)
	cache := getChannelAffinityCache()
	require.NoError(t, cache.SetWithTTL(suffix, entry, time.Minute))
	t.Cleanup(func() {
		_, _ = cache.DeleteMany([]string{suffix})
	})
	return suffix
}

func TestParseChannelAffinityKey_SegmentCombinations(t *testing.T) {
	cases := []struct {
		name      string
		rule      operation_setting.ChannelAffinityRule
		model     string
		group     string
		value     string
		wantModel string
		wantGroup string
		wantValue string
	}{
		{
			name: "rule name only",
			rule: operation_setting.ChannelAffinityRule{
				Name:            "parse-rule-only",
				IncludeRuleName: true,
			},
			model:     "gpt-5",
			group:     "default",
			value:     "abc123",
			wantValue: "abc123",
		},
		{
			name: "rule name and group",
			rule: operation_setting.ChannelAffinityRule{
				Name:              "parse-rule-group",
				IncludeRuleName:   true,
				IncludeUsingGroup: true,
			},
			model:     "gpt-5",
			group:     "cc-max",
			value:     "abc123",
			wantGroup: "cc-max",
			wantValue: "abc123",
		},
		{
			name: "all segments",
			rule: operation_setting.ChannelAffinityRule{
				Name:              "parse-all",
				IncludeRuleName:   true,
				IncludeModelName:  true,
				IncludeUsingGroup: true,
			},
			model:     "gpt-5",
			group:     "cc-max",
			value:     "abc123",
			wantModel: "gpt-5",
			wantGroup: "cc-max",
			wantValue: "abc123",
		},
		{
			// Codex prompt_cache_key values routinely contain colons, so the
			// trailing remainder must never be split further.
			name: "affinity value containing colons",
			rule: operation_setting.ChannelAffinityRule{
				Name:              "parse-colon",
				IncludeRuleName:   true,
				IncludeUsingGroup: true,
			},
			model:     "gpt-5",
			group:     "default",
			value:     "sess:abc:def",
			wantGroup: "default",
			wantValue: "sess:abc:def",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ruleByName := map[string]operation_setting.ChannelAffinityRule{
				tc.rule.Name: tc.rule,
			}
			suffix := buildChannelAffinityCacheKeySuffix(tc.rule, tc.model, tc.group, tc.value)
			fullKey := channelAffinityCacheNamespace + ":" + suffix

			ruleName, model, group, value, ok := parseChannelAffinityKey(fullKey, ruleByName)
			require.True(t, ok)
			require.Equal(t, tc.rule.Name, ruleName)
			require.Equal(t, tc.wantModel, model)
			require.Equal(t, tc.wantGroup, group)
			require.Equal(t, tc.wantValue, value)
		})
	}
}

func TestParseChannelAffinityKey_RejectsUnknownAndMalformed(t *testing.T) {
	rule := operation_setting.ChannelAffinityRule{
		Name:              "parse-known",
		IncludeRuleName:   true,
		IncludeUsingGroup: true,
	}
	ruleByName := map[string]operation_setting.ChannelAffinityRule{rule.Name: rule}

	_, _, _, _, ok := parseChannelAffinityKey("some:other:namespace:key", ruleByName)
	require.False(t, ok, "keys outside the affinity namespace must be rejected")

	_, _, _, _, ok = parseChannelAffinityKey(channelAffinityCacheNamespace+":unknown-rule:default:v", ruleByName)
	require.False(t, ok, "rules not present in settings must be rejected")

	// Group segment present but no affinity value left.
	_, _, _, _, ok = parseChannelAffinityKey(channelAffinityCacheNamespace+":parse-known:default", ruleByName)
	require.False(t, ok, "a key without an affinity value must be rejected")
}

func TestListChannelAffinityEntries_FilterAndAggregate(t *testing.T) {
	ruleName := fmt.Sprintf("admin-list-%d", time.Now().UnixNano())
	rule := operation_setting.ChannelAffinityRule{
		Name:              ruleName,
		IncludeRuleName:   true,
		IncludeUsingGroup: true,
	}
	registerAffinityRuleForTest(t, rule)

	seedAffinityEntry(t, rule, "gpt-5", "default", "v-a", ChannelAffinityEntry{ChannelID: 101, UserID: 7, TokenID: 70, UpdatedAt: 300})
	seedAffinityEntry(t, rule, "gpt-5", "default", "v-b", ChannelAffinityEntry{ChannelID: 101, UserID: 8, TokenID: 80, UpdatedAt: 200})
	seedAffinityEntry(t, rule, "gpt-5", "default", "v-c", ChannelAffinityEntry{ChannelID: 202, UserID: 7, TokenID: 71, UpdatedAt: 100})

	// Whole rule: two channels, two users.
	list, err := ListChannelAffinityEntries(ChannelAffinityEntryFilter{RuleName: ruleName}, 0)
	require.NoError(t, err)
	require.Equal(t, 3, list.Total)
	require.Equal(t, 3, list.Returned)
	require.Equal(t, map[string]int{"101": 2, "202": 1}, list.ByChannelID)
	require.Equal(t, map[string]int{"7": 2, "8": 1}, list.ByUserID)

	// Entries are newest first.
	require.Equal(t, int64(300), list.Entries[0].UpdatedAt)
	require.Equal(t, int64(100), list.Entries[2].UpdatedAt)

	// "How many bindings does this channel hold".
	list, err = ListChannelAffinityEntries(ChannelAffinityEntryFilter{RuleName: ruleName, ChannelID: 101}, 0)
	require.NoError(t, err)
	require.Equal(t, 2, list.Total)
	require.Equal(t, map[string]int{"101": 2}, list.ByChannelID)

	// "Which channels is this user pinned to".
	list, err = ListChannelAffinityEntries(ChannelAffinityEntryFilter{RuleName: ruleName, UserID: 7}, 0)
	require.NoError(t, err)
	require.Equal(t, 2, list.Total)
	require.Equal(t, map[string]int{"101": 1, "202": 1}, list.ByChannelID)

	// Combined filters intersect.
	list, err = ListChannelAffinityEntries(ChannelAffinityEntryFilter{RuleName: ruleName, UserID: 7, ChannelID: 202}, 0)
	require.NoError(t, err)
	require.Equal(t, 1, list.Total)
	require.Equal(t, 202, list.Entries[0].ChannelID)
	require.Equal(t, 7, list.Entries[0].UserID)
	require.Equal(t, 71, list.Entries[0].TokenID)
}

func TestListChannelAffinityEntries_LimitTruncatesEntriesNotAggregates(t *testing.T) {
	ruleName := fmt.Sprintf("admin-limit-%d", time.Now().UnixNano())
	rule := operation_setting.ChannelAffinityRule{
		Name:            ruleName,
		IncludeRuleName: true,
	}
	registerAffinityRuleForTest(t, rule)

	for i := 0; i < 5; i++ {
		seedAffinityEntry(t, rule, "", "", fmt.Sprintf("v-%d", i), ChannelAffinityEntry{
			ChannelID: 900,
			UserID:    5,
			UpdatedAt: int64(i),
		})
	}

	list, err := ListChannelAffinityEntries(ChannelAffinityEntryFilter{RuleName: ruleName}, 2)
	require.NoError(t, err)
	require.Equal(t, 5, list.Total, "total must reflect every match")
	require.Equal(t, 2, list.Returned)
	require.Len(t, list.Entries, 2)
	require.Equal(t, map[string]int{"900": 5}, list.ByChannelID, "aggregates must not be truncated by limit")
}

func TestListChannelAffinityEntries_KeyFingerprintMatchesUsageCacheStats(t *testing.T) {
	ruleName := fmt.Sprintf("admin-fp-%d", time.Now().UnixNano())
	rule := operation_setting.ChannelAffinityRule{
		Name:              ruleName,
		IncludeRuleName:   true,
		IncludeUsingGroup: true,
	}
	registerAffinityRuleForTest(t, rule)

	affinityValue := "fp-check-value"
	seedAffinityEntry(t, rule, "", "cc-max", affinityValue, ChannelAffinityEntry{ChannelID: 11, UserID: 3})

	list, err := ListChannelAffinityEntries(ChannelAffinityEntryFilter{RuleName: ruleName}, 0)
	require.NoError(t, err)
	require.Len(t, list.Entries, 1)

	// The listing must be joinable with /api/log/channel_affinity_usage_cache,
	// which is keyed by the same fingerprint.
	require.Equal(t, affinityFingerprint(affinityValue), list.Entries[0].KeyFingerprint)
	require.Equal(t, buildChannelAffinityKeyHint(affinityValue), list.Entries[0].KeyHint)
	require.Equal(t, "cc-max", list.Entries[0].UsingGroup)
}

func TestClearChannelAffinityCacheByFilter_ByChannel(t *testing.T) {
	ruleName := fmt.Sprintf("admin-clear-%d", time.Now().UnixNano())
	rule := operation_setting.ChannelAffinityRule{
		Name:              ruleName,
		IncludeRuleName:   true,
		IncludeUsingGroup: true,
	}
	registerAffinityRuleForTest(t, rule)

	seedAffinityEntry(t, rule, "", "default", "keep-1", ChannelAffinityEntry{ChannelID: 555, UserID: 1})
	seedAffinityEntry(t, rule, "", "default", "drop-1", ChannelAffinityEntry{ChannelID: 666, UserID: 1})
	seedAffinityEntry(t, rule, "", "default", "drop-2", ChannelAffinityEntry{ChannelID: 666, UserID: 2})

	deleted, err := ClearChannelAffinityCacheByFilter(ChannelAffinityEntryFilter{ChannelID: 666})
	require.NoError(t, err)
	require.Equal(t, 2, deleted)

	list, err := ListChannelAffinityEntries(ChannelAffinityEntryFilter{RuleName: ruleName}, 0)
	require.NoError(t, err)
	require.Equal(t, 1, list.Total, "only the targeted channel must be cleared")
	require.Equal(t, 555, list.Entries[0].ChannelID)
}

func TestClearChannelAffinityCacheByFilter_ByUser(t *testing.T) {
	ruleName := fmt.Sprintf("admin-clear-user-%d", time.Now().UnixNano())
	rule := operation_setting.ChannelAffinityRule{
		Name:              ruleName,
		IncludeRuleName:   true,
		IncludeUsingGroup: true,
	}
	registerAffinityRuleForTest(t, rule)

	seedAffinityEntry(t, rule, "", "default", "u9-a", ChannelAffinityEntry{ChannelID: 1, UserID: 9})
	seedAffinityEntry(t, rule, "", "default", "u9-b", ChannelAffinityEntry{ChannelID: 2, UserID: 9})
	seedAffinityEntry(t, rule, "", "default", "u10-a", ChannelAffinityEntry{ChannelID: 1, UserID: 10})

	deleted, err := ClearChannelAffinityCacheByFilter(ChannelAffinityEntryFilter{UserID: 9, RuleName: ruleName})
	require.NoError(t, err)
	require.Equal(t, 2, deleted)

	list, err := ListChannelAffinityEntries(ChannelAffinityEntryFilter{RuleName: ruleName}, 0)
	require.NoError(t, err)
	require.Equal(t, 1, list.Total)
	require.Equal(t, 10, list.Entries[0].UserID)
}

func TestListChannelAffinityEntries_EmptyResultSerializesAsArray(t *testing.T) {
	// entries must marshal to [] rather than null so clients need no null check.
	list, err := ListChannelAffinityEntries(ChannelAffinityEntryFilter{ChannelID: 987654}, 0)
	require.NoError(t, err)
	require.Zero(t, list.Total)
	require.Zero(t, list.Returned)
	require.NotNil(t, list.Entries)
	require.NotNil(t, list.ByChannelID)
	require.NotNil(t, list.ByUserID)

	encoded, err := json.Marshal(list)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"entries":[]`)
	require.Contains(t, string(encoded), `"by_channel_id":{}`)
}

func TestClearChannelAffinityCacheByFilter_NoMatchIsNoop(t *testing.T) {
	deleted, err := ClearChannelAffinityCacheByFilter(ChannelAffinityEntryFilter{ChannelID: 987654})
	require.NoError(t, err)
	require.Zero(t, deleted)
}

func TestRecordChannelAffinityPersistsRequesterIdentity(t *testing.T) {
	ruleName := fmt.Sprintf("admin-record-%d", time.Now().UnixNano())
	rule := operation_setting.ChannelAffinityRule{
		Name:              ruleName,
		IncludeRuleName:   true,
		IncludeUsingGroup: true,
	}
	registerAffinityRuleForTest(t, rule)

	affinityValue := fmt.Sprintf("record-%d", time.Now().UnixNano())
	suffix := buildChannelAffinityCacheKeySuffix(rule, "", "default", affinityValue)
	fullKey := channelAffinityCacheNamespace + ":" + suffix

	ctx := buildChannelAffinityTemplateContextForTest(channelAffinityMeta{
		CacheKey:   fullKey,
		TTLSeconds: 60,
		RuleName:   ruleName,
	})
	ctx.Set("id", 4242)
	ctx.Set("token_id", 777)

	RecordChannelAffinity(ctx, 313)
	cache := getChannelAffinityCache()
	t.Cleanup(func() {
		_, _ = cache.DeleteMany([]string{suffix})
	})

	entry, found, err := cache.Get(suffix)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 313, entry.ChannelID)
	require.Equal(t, 4242, entry.UserID)
	require.Equal(t, 777, entry.TokenID)
	require.NotZero(t, entry.UpdatedAt)

	list, err := ListChannelAffinityEntries(ChannelAffinityEntryFilter{UserID: 4242}, 0)
	require.NoError(t, err)
	require.Equal(t, 1, list.Total)
	require.Equal(t, map[string]int{"313": 1}, list.ByChannelID)
}
