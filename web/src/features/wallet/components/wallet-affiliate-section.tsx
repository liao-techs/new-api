/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useState } from 'react'

import { useAffiliate } from '../hooks'
import type { UserWalletData } from '../types'
import { AffiliateRewardsCard } from './affiliate-rewards-card'
import { TransferDialog } from './dialogs/transfer-dialog'

interface WalletAffiliateSectionProps {
  enabled: boolean
  user: UserWalletData | null
  complianceConfirmed: boolean
  onBalanceChange: () => Promise<void>
}

export function WalletAffiliateSection({
  enabled,
  ...props
}: WalletAffiliateSectionProps) {
  if (!enabled) return null

  return <EnabledWalletAffiliateSection {...props} />
}

function EnabledWalletAffiliateSection({
  user,
  complianceConfirmed,
  onBalanceChange,
}: Omit<WalletAffiliateSectionProps, 'enabled'>) {
  const [transferDialogOpen, setTransferDialogOpen] = useState(false)
  const { affiliateLink, loading, transferQuota, transferring } = useAffiliate()

  const handleTransfer = async (amount: number) => {
    const success = await transferQuota(amount)
    if (success) await onBalanceChange()
    return success
  }

  return (
    <>
      <AffiliateRewardsCard
        user={user}
        affiliateLink={affiliateLink}
        onTransfer={() => setTransferDialogOpen(true)}
        complianceConfirmed={complianceConfirmed}
        loading={loading}
      />

      <TransferDialog
        open={transferDialogOpen}
        onOpenChange={setTransferDialogOpen}
        onConfirm={handleTransfer}
        availableQuota={user?.aff_quota ?? 0}
        transferring={transferring}
      />
    </>
  )
}
