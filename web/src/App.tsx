import {Navigate, Route, Routes, useLocation, useNavigate} from 'react-router-dom';
import {Theme} from '@astryxdesign/core/theme';
import {Heading} from '@astryxdesign/core/Heading';
import {HStack} from '@astryxdesign/core/HStack';
import {Tab, TabList} from '@astryxdesign/core/TabList';
import {VStack} from '@astryxdesign/core/VStack';
import {neutralTheme} from '@astryxdesign/theme-neutral/built';
import {HealthPage} from './pages/HealthPage';
import {ServicesPage} from './pages/ServicesPage';
import {AuditPage} from './pages/AuditPage';
import {SettingsPage} from './pages/SettingsPage';

const NAV = [
  {path: '/health', label: 'Health'},
  {path: '/services', label: 'Services'},
  {path: '/audit', label: 'Audit'},
  {path: '/settings', label: 'Settings'},
] as const;

function Shell({children}: {children: React.ReactNode}) {
  const {pathname} = useLocation();
  const navigate = useNavigate();
  const current = NAV.find((n) => pathname.startsWith(n.path))?.path ?? NAV[0].path;
  return (
    <div className="page">
      <VStack gap={4}>
        <HStack gap={4} align="center" wrap="wrap">
          <Heading level={1} type="display-3">
            Server Monitor
          </Heading>
          <TabList value={current} onChange={(v) => navigate(v)} hasDivider={false}>
            {NAV.map((n) => (
              <Tab key={n.path} value={n.path} label={n.label} />
            ))}
          </TabList>
        </HStack>
        <main>{children}</main>
      </VStack>
    </div>
  );
}

export function App() {
  return (
    <Theme theme={neutralTheme}>
      <Shell>
        <Routes>
          <Route path="/" element={<Navigate to="/health" replace />} />
          <Route path="/health" element={<HealthPage />} />
          <Route path="/services" element={<ServicesPage />} />
          <Route path="/audit" element={<AuditPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="*" element={<Navigate to="/health" replace />} />
        </Routes>
      </Shell>
    </Theme>
  );
}
