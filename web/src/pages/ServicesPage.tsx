import {useEffect, useMemo, useRef, useState} from 'react';
import {Banner} from '@astryxdesign/core/Banner';
import {EmptyState} from '@astryxdesign/core/EmptyState';
import {HStack} from '@astryxdesign/core/HStack';
import {Spinner} from '@astryxdesign/core/Spinner';
import {Switch} from '@astryxdesign/core/Switch';
import {Table} from '@astryxdesign/core/Table';
import {Text} from '@astryxdesign/core/Text';
import {TextInput} from '@astryxdesign/core/TextInput';
import {VStack} from '@astryxdesign/core/VStack';
import {ErrorView} from '../components/ErrorView';
import {getUnits, putSelection} from '../lib/api';
import {usePolling} from '../lib/usePolling';
import type {UnitInfo} from '../types/api';

type Row = UnitInfo;

export function ServicesPage() {
  const {data, error, loading, reload} = usePolling(getUnits, 60_000);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [search, setSearch] = useState('');
  const [onlySelected, setOnlySelected] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  // Wybór pochodzi z serwera: synchronizujemy go tylko przy nowych danych i nie w trakcie zapisu
  // (zmiana samego `saving` nie może cofnąć świeżo zapisanego wyboru starymi danymi).
  const savingRef = useRef(false);
  useEffect(() => {
    if (data && !savingRef.current) setSelected(new Set(data.units.filter((u) => u.selected).map((u) => u.unit)));
  }, [data]);

  const rows = useMemo(() => {
    const q = search.trim().toLowerCase();
    return (data?.units ?? []).filter(
      (u) =>
        (!onlySelected || selected.has(u.unit)) &&
        (q === '' || u.unit.toLowerCase().includes(q) || u.description.toLowerCase().includes(q)),
    );
  }, [data, search, onlySelected, selected]);

  async function toggle(unit: string, on: boolean) {
    const next = new Set(selected);
    if (on) next.add(unit);
    else next.delete(unit);
    const previous = selected;
    setSelected(next);
    savingRef.current = true;
    setSaving(true);
    setSaveError(null);
    try {
      const saved = await putSelection([...next]);
      setSelected(new Set(saved.units));
    } catch (e) {
      setSelected(previous);
      setSaveError(e instanceof Error ? e.message : 'Saving failed');
    } finally {
      savingRef.current = false;
      setSaving(false);
      reload();
    }
  }

  if (loading && !data) return <Spinner label="Loading services" />;
  if (!data) return error ? <ErrorView error={error} /> : null;

  return (
    <VStack gap={3}>
      {error && <Banner status="warning" title="Refresh failed — showing last data" description={error.message} />}
      {saveError && (
        <Banner status="error" title="Selection was not saved" description={saveError} isDismissable onDismiss={() => setSaveError(null)} />
      )}
      <HStack gap={3} align="end" wrap="wrap">
        <TextInput label="Search services" type="search" value={search} onChange={(v) => setSearch(v)} width={320} />
        <Switch label="Selected only" value={onlySelected} onChange={(v) => setOnlySelected(v)} />
        <Text type="supporting">
          {selected.size} selected · {rows.length} of {data.units.length} shown
        </Text>
      </HStack>
      {rows.length === 0 ? (
        <EmptyState title="No matching services" description="Try a different search." isCompact />
      ) : (
        <div className="scroll-x">
          <Table<Row>
            data={rows}
            idKey="unit"
            density="compact"
            columns={[
              {key: 'selected', header: 'Monitor', renderCell: (u) => (
                <Switch
                  label={`Monitor ${u.unit}`}
                  isLabelHidden
                  value={selected.has(u.unit)}
                  isDisabled={saving}
                  onChange={(v) => void toggle(u.unit, v)}
                />
              )},
              {key: 'unit', header: 'Unit', renderCell: (u) => <Text type="code">{u.unit}</Text>},
              {key: 'description', header: 'Description', renderCell: (u) => u.description},
              {key: 'state', header: 'State', renderCell: (u) => `${u.active}${u.sub ? ` / ${u.sub}` : ''}`},
            ]}
          />
        </div>
      )}
    </VStack>
  );
}
