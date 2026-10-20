import RegistrationSettingsSection from '../../components/org/RegistrationSettings'
import OrgNotificationRulesSection from '../../components/org/NotificationRules'
import AnalysisWeightsSection from '../../components/org/AnalysisWeights'
import { OrgPageFrame } from './OrgPageFrame'

/**
 * The Organization page's Settings tab: who may join, the notification rules
 * and the analysis weights. Owner/admin. Each section loads and
 * saves its own data.
 */
export default function OrgSettingsPage() {
  return (
    <OrgPageFrame>
      <RegistrationSettingsSection />
      <OrgNotificationRulesSection />
      <AnalysisWeightsSection />
    </OrgPageFrame>
  )
}
