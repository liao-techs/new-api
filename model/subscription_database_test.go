package model

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TEST_*_DSN must point to dedicated disposable test databases, as do the
// token/prefill migration suites. The ordinary model suite covers SQLite.
func TestSubscriptionDatabaseMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		kind      common.DatabaseType
		open      func(string) gorm.Dialector
	}{
		{"mysql", "TEST_MYSQL_DSN", common.DatabaseTypeMySQL, mysql.Open},
		{"postgres", "TEST_POSTGRES_DSN", common.DatabaseTypePostgreSQL, func(dsn string) gorm.Dialector {
			return postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dsn := os.Getenv(tc.env)
			if dsn == "" {
				t.Skip(tc.env + " is not configured")
			}
			db, err := gorm.Open(tc.open(dsn), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			oldDB, oldLogDB := DB, LOG_DB
			oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
			DB, LOG_DB = db, db
			common.SetDatabaseTypes(tc.kind, tc.kind)
			initCol()
			t.Cleanup(func() {
				DB, LOG_DB = oldDB, oldLogDB
				common.SetDatabaseTypes(oldMainType, oldLogType)
				initCol()
				require.NoError(t, sqlDB.Close())
			})
			var version string
			require.NoError(t, db.Raw("SELECT VERSION()").Scan(&version).Error)
			t.Logf("database: %s", version)
			for range 2 {
				require.NoError(t, migrateDB())
			}
			t.Run("funding-isolation-and-refund", TestSubscriptionFundingStaysWithinRequestedGroup)
			t.Run("wallet-overflow-scope", TestSubscriptionWalletOverflowIsScopedToGroup)
			t.Run("independent-lifecycle", TestDifferentSubscriptionGroupsEndIndependently)
			t.Run("interleaved-renewal", TestInterleavedSubscriptionRenewalRestoresOriginalBaseline)
			t.Run("effective-grants", TestSubscriptionGroupsExcludeExpiredCancelledAndFutureGrants)
			t.Run("independent-group-cycles", TestDeleteSubscriptionPreservesIndependentGroupCycles)
			t.Run("manual-baseline", TestSubscriptionRollbackPreservesManualBaselineAfterPastGrant)
			t.Run("actual-predecessor", TestDeleteSubscriptionReconnectsOnlyActualPredecessor)
			t.Run("same-second-cancellation", TestCancelSubscriptionPreservesChildrenPurchasedInSameSecond)
			t.Run("renewal-baseline-capture", TestSubscriptionRenewalUsesOriginalBaselineCaptureTime)
		})
	}
}
