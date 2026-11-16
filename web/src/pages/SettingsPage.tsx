import {useState} from 'react';
import {Tab, TabList} from '@astryxdesign/core/TabList';
import {VStack} from '@astryxdesign/core/VStack';
import {ConnectorsTab} from './settings/ConnectorsTab';
import {HistoryTab} from './settings/HistoryTab';
import {RulesTab} from './settings/RulesTab';

type SettingsTab = 'rules' | 'connectors' | 'history';

export function SettingsPage() {
  const [tab, setTab] = useState<SettingsTab>('rules');
  return (
    <VStack gap={3}>
      <TabList role="tablist" value={tab} onChange={(v) => setTab(v as SettingsTab)}>
        <Tab value="rules" label="Alert rules" />
        <Tab value="connectors" label="Connectors" />
        <Tab value="history" label="History" />
      </TabList>
      {tab === 'rules' ? <RulesTab /> : tab === 'connectors' ? <ConnectorsTab /> : <HistoryTab />}
    </VStack>
  );
}
