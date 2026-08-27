package cachex

import (
	"testing"
	"time"

	"github.com/samber/hot"
	"github.com/stretchr/testify/require"
)

type getManyValue struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func newMemoryCacheForTest() *HybridCache[getManyValue] {
	return NewHybridCache[getManyValue](HybridCacheConfig[getManyValue]{
		Namespace:    Namespace("test:getmany:v1"),
		RedisEnabled: func() bool { return false },
		Memory: func() *hot.HotCache[string, getManyValue] {
			return hot.NewHotCache[string, getManyValue](hot.LRU, 100).
				WithTTL(time.Minute).
				Build()
		},
	})
}

func TestGetMany_ReturnsPresentKeysByFullKey(t *testing.T) {
	c := newMemoryCacheForTest()
	require.NoError(t, c.SetWithTTL("a", getManyValue{ID: 1, Name: "one"}, time.Minute))
	require.NoError(t, c.SetWithTTL("b", getManyValue{ID: 2, Name: "two"}, time.Minute))

	got, err := c.GetMany([]string{"a", "b"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, getManyValue{ID: 1, Name: "one"}, got[c.FullKey("a")])
	require.Equal(t, getManyValue{ID: 2, Name: "two"}, got[c.FullKey("b")])
}

func TestGetMany_AcceptsBothRawAndFullKeys(t *testing.T) {
	c := newMemoryCacheForTest()
	require.NoError(t, c.SetWithTTL("a", getManyValue{ID: 1}, time.Minute))

	// Keys() hands back fully namespaced keys; callers may also pass raw ones.
	got, err := c.GetMany([]string{c.FullKey("a"), "a"})
	require.NoError(t, err)
	require.Len(t, got, 1, "the same key in both forms must collapse to one entry")
	require.Equal(t, getManyValue{ID: 1}, got[c.FullKey("a")])
}

func TestGetMany_OmitsMissingKeys(t *testing.T) {
	c := newMemoryCacheForTest()
	require.NoError(t, c.SetWithTTL("present", getManyValue{ID: 1}, time.Minute))

	// A key can expire between Keys() and GetMany(); that must not be an error.
	got, err := c.GetMany([]string{"present", "absent"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	_, ok := got[c.FullKey("absent")]
	require.False(t, ok)
}

func TestGetMany_EmptyAndBlankInput(t *testing.T) {
	c := newMemoryCacheForTest()

	got, err := c.GetMany(nil)
	require.NoError(t, err)
	require.Empty(t, got)

	got, err = c.GetMany([]string{"", "   "})
	require.NoError(t, err)
	require.Empty(t, got)
}
