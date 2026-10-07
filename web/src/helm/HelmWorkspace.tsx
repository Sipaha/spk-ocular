import { useCallback, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import type { Client } from '../api/client'
import type { Column, Row, Ref, ScopeSel, Target } from '../api/types'
import { DetailsPanel } from '../components/DetailsPanel'
import { LoadingOverlay } from '../components/LoadingOverlay'
import { SearchIcon } from '../components/icons'
import { columnWidthsKey } from '../components/columnWidths'
import { persistTargetEntries } from '../components/navigationPersist'
import { memoOf, remember as rememberPage, rememberSort } from '../components/pageMemo'
import { showNotice, targetKey } from '../store'
import { ResourceTable } from '../components/ResourceTable'
import { Select } from '../components/Select'
import { holdEdits, mayLeave } from '../edit/guard'
import { t } from '../i18n'
import type { HelmChart, HelmDetail, HelmOperation, HelmPlan, HelmRelease, HelmRequest, HelmResponse, HelmSettings } from './types'

const emptySettings: HelmSettings = { repositories: [], storage: { driver: 'secret' } }
const initialOperation = (namespace: string): HelmOperation => ({ action: 'install', name: '', namespace, chart: { repository: '', name: '', version: '' }, values: '{}\n', revision: 0, timeoutSeconds: 300, wait: true, disableHooks: false, createNamespace: false, keepHistory: false })
const errorText = (e: unknown) => e instanceof Error ? e.message : String(e)
const button = 'whitespace-nowrap rounded border border-line px-2 py-1 hover:bg-hover disabled:opacity-50'

export function HelmWorkspace({ client, target, scope, scopePicker, initialTab, onSection, onResource }: { client: Client; target: Target; scope: ScopeSel; scopePicker: ReactNode; initialTab: 'releases' | 'charts'; onSection?: (tab: 'releases' | 'charts') => void; onResource?: (ref: Ref) => void }) {
  const tkey = targetKey(target)
  const [tab, setTab] = useState<'releases' | 'charts' | 'settings'>(initialTab)
  const [settings, setSettings] = useState<HelmSettings>(emptySettings)
  const [settingsReady, setSettingsReady] = useState(false)
  const [releases, setReleases] = useState<HelmRelease[]>([])
  const [charts, setCharts] = useState<HelmChart[]>([])
  const [repository, setRepository] = useState('')
  const [filter, setFilter] = useState('')
  const [detail, setDetail] = useState<HelmDetail | null>(null)
  const [detailTab, setDetailTab] = useState<'values' | 'computedValues' | 'manifest' | 'hooks' | 'notes' | 'history' | 'resources'>('values')
  const [chart, setChart] = useState<HelmChart | null>(null)
  const [operation, setOperation] = useState<HelmOperation | null>(null)
  const [plan, setPlan] = useState<HelmPlan | null>(null)
  const [configRev, setConfigRev] = useState('')
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [dirtySettings, setDirtySettings] = useState(false)
  const [refresh, setRefresh] = useState(0)
  const alive = useRef(true)
  const controller = useRef<AbortController | null>(null)
  const dirty = useRef(false)
  const scopeKey = JSON.stringify(scope)
  useEffect(() => { alive.current = true; return () => { alive.current = false; controller.current?.abort() } }, [])
  useEffect(() => { dirty.current = operation !== null || dirtySettings }, [operation, dirtySettings])
  useEffect(() => holdEdits({ dirty: () => dirty.current, discard: () => { dirty.current = false; setOperation(null); setPlan(null); setDirtySettings(false) } }), [])
  const call = useCallback(async (request: HelmRequest): Promise<HelmResponse | null> => {
    controller.current?.abort()
    const c = new AbortController(); controller.current = c
    setBusy(true); setError(''); setNotice('')
    try {
      const response = await client.helm({ ...request, provider: target.provider, target: target.id }, c.signal)
      if (!alive.current || controller.current !== c) return null
      return response
    } catch (e) { if (alive.current && controller.current === c) setError(c.signal.aborted ? t('helm.cancelled') : errorText(e)); return null }
    finally { if (alive.current && controller.current === c) setBusy(false) }
  }, [client, target.provider, target.id])
  useEffect(() => {
    let live = true
    client.helm({ command: 'settings' }).then(r => { if (live && r.settings) { setSettings(r.settings); setSettingsReady(true) } }, e => { if (live) setError(errorText(e)) })
    return () => { live = false }
  }, [client])
  useEffect(() => {
    const request: HelmRequest | null = tab === 'releases' ? { command: 'list', scope: JSON.parse(scopeKey) as ScopeSel } : tab === 'charts' ? { command: 'catalog', repository } : null
    if (!request) return
    const c = new AbortController(); controller.current = c
    let live = true
    client.helm({ ...request, provider: target.provider, target: target.id }, c.signal).then(r => {
      if (!live || controller.current !== c) return
      if (tab === 'releases') { setReleases(r.releases ?? []); setConfigRev(r.configRev ?? '') }
      else setCharts(r.charts ?? [])
      if (r.problems?.length) setError(r.problems.join(' · '))
    }, e => { if (live && controller.current === c) setError(c.signal.aborted ? t('helm.cancelled') : errorText(e)) }).finally(() => { if (live && controller.current === c) setBusy(false) })
    return () => { live = false; c.abort() }
  }, [client, target.provider, target.id, scopeKey, tab, repository, refresh])
  const navigate = (next: typeof tab) => mayLeave(() => { if (next !== 'settings' && next !== initialTab && onSection) { onSection(next); return }; setDetail(null); setChart(null); setOperation(null); setPlan(null); setTab(next); setFilter(''); setNotice(''); setBusy(next !== 'settings'); setError('') })
  const openRelease = async (r: HelmRelease, revision = 0) => {
    const response = await call({ command: 'detail', namespace: r.namespace, name: r.name, revision })
    if (response?.detail) { setDetail(response.detail); setChart(null); setConfigRev(response.configRev ?? ''); setDetailTab('values') }
  }
  const openChart = async (r: HelmChart) => { const response = await call({ command: 'chart', chart: r }); if (response?.chart) { setChart(response.chart); setDetail(null) } }
  const begin = (action: HelmOperation['action'], revision = 0) => {
    const o = initialOperation(detail?.namespace ?? (scope.mode === 'one' ? scope.name ?? '' : ''))
    o.action = action; o.revision = revision
    if (detail) { o.name = detail.name; o.values = detail.values; o.chart = { repository: repository || settings.repositories[0]?.name || '', name: detail.chart, version: detail.chartVersion } }
    if (chart) { o.chart = chart; o.values = chart.values ?? '{}\n' }
    setOperation(o); setPlan(null); setNotice('')
  }
  const prepare = async () => { if (!operation) return; const response = await call({ command: 'prepare', operation }); if (response?.plan) { setPlan(response.plan); setConfigRev(response.configRev ?? '') } }
  const run = async () => {
    if (!plan) return
    const response = await call({ command: 'run', planId: plan.id, configRev })
    setPlan(null)
    if (response?.result) { setNotice(response.result.outcome === 'done' ? t('helm.done') : t('helm.unknown') + ' ' + response.result.message); if (response.result.outcome === 'done') { dirty.current = false; setOperation(null); setDetail(null); setChart(null); setRefresh(v => v + 1) } }
  }
  const patch = (p: Partial<HelmOperation>) => setOperation(o => o ? { ...o, ...p } : o)
  const cancelEdit = () => mayLeave(() => { if (plan) void client.helm({ command: 'forget', provider: target.provider, target: target.id, planId: plan.id }).catch(() => {}); setPlan(null); setOperation(null) })
  const shownCharts = [...new Map(charts.slice().reverse().map(c => [`${c.repository}/${c.name}`, c])).values()].sort((a, b) => a.name.localeCompare(b.name))
  const columns: Column[] = (tab === 'releases' ? ['name', 'namespace', 'chartVersion', 'appVersion', 'revision', 'status', 'updated'] : ['name', 'repository', 'description', 'chartVersion', 'appVersion']).map(id => ({ id, title: t(`helm.${id}` as 'helm.name'), type: id === 'revision' ? 'number' : id === 'status' ? 'status' : 'text', scopeColumn: id === 'namespace', width: id === 'description' ? 280 : id === 'updated' ? 220 : undefined }))
  const tableRows: Row[] = tab === 'releases' ? releases.map(r => ({
    id: `${r.namespace}/${r.name}`, ref: { provider: target.provider, target: target.id, kind: 'ocular.helm.releases', scope: r.namespace, name: r.name },
    cells: [{ text: r.name }, { text: r.namespace }, { text: `${r.chart} ${r.chartVersion}` }, { text: r.appVersion }, { num: r.revision, text: String(r.revision) }, { text: r.status }, { text: new Date(r.updated).toLocaleString(document.documentElement.lang || undefined), num: Date.parse(r.updated) }],
    health: { state: r.status === 'failed' ? 'error' : r.status.startsWith('pending') ? 'progressing' : 'ok' },
  })) : shownCharts.map(c => ({
    id: `${c.repository}/${c.name}`, ref: { provider: target.provider, target: target.id, kind: 'ocular.helm.charts', name: c.name },
    cells: [{ text: c.name }, { text: c.repository }, { text: c.description }, { text: c.version + (c.deprecated ? ` (${t('helm.deprecated')})` : '') }, { text: c.appVersion }],
    health: { state: c.deprecated ? 'warning' : 'ok' },
  }))
  const openRow = (row: Row) => {
    if (busy) return
    mayLeave(() => {
      if (tab === 'releases') { const r = releases.find(r => `${r.namespace}/${r.name}` === row.id); if (r) void openRelease(r) }
      else { const c = shownCharts.find(c => `${c.repository}/${c.name}` === row.id); if (c) void openChart(c) }
    })
  }
  return <section className="helm-workspace flex min-h-0 flex-1 flex-col">
    <header className="resource-toolbar">
      <h1 className="resource-title">{t(`helm.${tab}`)}</h1>
      {tab !== 'settings' && <span className="resource-count" aria-label="count">{busy && !tableRows.length ? '…' : tableRows.length}</span>}
      <Select label={t('helm.section')} value={tab} options={(['releases', 'charts', 'settings'] as const).map(v => ({ value: v, label: t(`helm.${v}`) }))} onChange={v => navigate(v as typeof tab)} disabled={busy} className="min-w-0 max-w-56" />
      {tab !== 'settings' && !operation && (tab === 'releases' ? scopePicker : <Select value={repository} options={[{ value: '', label: t('helm.allRepositories') }, ...settings.repositories.map(r => ({ value: r.name, label: r.name }))]} label={t('helm.repository')} onChange={v => { setRepository(v); setChart(null); setCharts([]); setBusy(true); setError('') }} disabled={busy} />)}
      {busy ? <button className={button} onClick={() => controller.current?.abort()}>{t('helm.stop')}</button> : <button className={button} onClick={() => { setBusy(tab !== 'settings'); setError(''); setRefresh(v => v + 1) }} disabled={!!operation || dirtySettings}>{t('helm.refresh')}</button>}
      {tab !== 'settings' && !operation && <label className="resource-filter field-shell"><SearchIcon className="h-3.5 w-3.5 text-fg-subtle" /><input data-primary-filter className="w-full bg-transparent outline-none placeholder:text-fg-subtle" aria-label={t('helm.search')} placeholder={t('table.filter')} value={filter} onChange={e => setFilter(e.target.value)} spellCheck={false} /></label>}
    </header>
    {error && <div role="alert" className="border-b border-line px-3 py-2 text-danger">{error}</div>}
    {notice && <div role="status" className="border-b border-line px-3 py-2">{notice}</div>}
    {busy && <div role="status" className="px-3 py-1 text-fg-muted">{t('app.loading')}</div>}
    {tab === 'settings' ? <SettingsEditor value={settings} onChange={v => { setSettings(v); setDirtySettings(true) }} busy={busy || !settingsReady} onSave={async () => { const r = await call({ command: 'save-settings', settings }); if (r) { dirty.current = false; setDirtySettings(false); if (r.settings) setSettings(r.settings); setNotice(t('helm.saved')); if (!settings.repositories.some(r => r.name === repository)) setRepository(settings.repositories[0]?.name ?? '') } }} /> : <>
      <div className="relative flex min-h-0 flex-1 overflow-hidden">
        {!operation && <ResourceTable key={tab} columns={columns} rows={tableRows} hideScope={tab === 'releases' && scope.mode === 'one'} filter={filter}
          selected={detail ? `${detail.namespace}/${detail.name}` : chart ? `${chart.repository}/${chart.name}` : null}
          initialWidths={memoOf(tkey).columnWidths?.[`ocular.helm.${tab}`]} initialSort={memoOf(tkey).sorts[`ocular.helm.${tab}`]}
          onSort={sort => rememberSort(tkey, `ocular.helm.${tab}`, sort)}
          onWidths={widths => {
            const kind = `ocular.helm.${tab}`
            rememberPage(tkey, { columnWidths: { ...memoOf(tkey).columnWidths, [kind]: widths } })
            void persistTargetEntries(client, target.provider, target.id, { [columnWidthsKey(kind)]: JSON.stringify(widths) }).catch(() => showNotice(t('table.widthsNotSaved')))
          }}
          areaFocus onSelect={openRow} emptyMessage={!busy && !error ? t(tab === 'charts' && !settings.repositories.length ? 'helm.addRepositoryFirst' : 'helm.empty') : null} />}
        {operation ? <div className="flex min-w-0 flex-1 flex-col overflow-auto p-3">
          <div className="mb-3 flex items-center gap-2"><h2 className="font-semibold">{t(`helm.${operation.action}`)} · {target.title}</h2><span className="flex-1" /><button className={button} disabled={busy} onClick={cancelEdit}>{t('helm.cancel')}</button></div>
          {plan ? <>
            <p>{operation.namespace} / {operation.name} · {t('helm.revision')} {plan.currentRevision} → {operation.action === 'rollback' ? operation.revision : t(`helm.${operation.action}`)}</p>
            <p className="my-2 text-fg-muted">{t('helm.reviewWarning')}</p>{plan.previewMode === 'client' && <p className="my-2 text-warning">{t('helm.crdPreview')}</p>}
            <div className="flex gap-2"><button autoFocus className={button} disabled={busy} onClick={() => { void client.helm({ command: 'forget', provider: target.provider, target: target.id, planId: plan.id }).catch(() => {}); setPlan(null) }}>{t('helm.back')}</button><button className={`${button} text-accent`} disabled={busy} onClick={() => { void run() }}>{t('helm.execute')}</button></div>
            <details className="mt-3"><summary>{t('helm.previousManifest')}</summary><pre className="helm-code">{plan.previousManifest}</pre></details>
            <h3 className="mt-3">{t('helm.manifest')}</h3><pre className="helm-code">{plan.manifest}</pre>
          </> : <>
            <div className="helm-form-grid">
              <Field label={t('helm.name')}><input className="helm-input" value={operation.name} disabled={operation.action !== 'install' || busy} onChange={e => patch({ name: e.target.value })} /></Field>
              <Field label={t('helm.namespace')}><input className="helm-input" value={operation.namespace} disabled={operation.action !== 'install' || busy} onChange={e => patch({ namespace: e.target.value })} /></Field>
              {(operation.action === 'install' || operation.action === 'upgrade') && <>
                <Field label={t('helm.repository')}><Select label={t('helm.repository')} value={operation.chart.repository} options={settings.repositories.map(r => ({ value: r.name, label: r.name }))} onChange={v => patch({ chart: { ...operation.chart, repository: v } })} disabled={busy} /></Field>
                <Field label={t('helm.chart')}><input className="helm-input" value={operation.chart.name} onChange={e => patch({ chart: { ...operation.chart, name: e.target.value } })} disabled={busy} /></Field>
                <Field label={t('helm.chartVersion')}><input className="helm-input" value={operation.chart.version} onChange={e => patch({ chart: { ...operation.chart, version: e.target.value } })} disabled={busy} /></Field>
              </>}
              <Field label={t('helm.timeout')}><input className="helm-input" type="number" min={1} max={1800} value={operation.timeoutSeconds} onChange={e => patch({ timeoutSeconds: Number(e.target.value) })} disabled={busy} /></Field>
            </div>
            <div className="my-3 flex flex-wrap gap-4">{(['wait', 'disableHooks', ...(operation.action === 'install' ? ['createNamespace'] : []), ...(operation.action === 'uninstall' ? ['keepHistory'] : [])] as const).map(k => <label key={k} className="flex items-center gap-2"><input type="checkbox" checked={!!operation[k as keyof HelmOperation]} disabled={busy} onChange={e => patch({ [k]: e.target.checked })} />{t(`helm.${k}` as 'helm.wait')}</label>)}</div>
            {(operation.action === 'install' || operation.action === 'upgrade') && <><label htmlFor="helm-values">{t('helm.values')}</label><textarea id="helm-values" className="helm-code min-h-64 flex-1" spellCheck={false} value={operation.values} disabled={busy} onChange={e => patch({ values: e.target.value })} /></>}
            <div className="mt-3"><button className={button} disabled={busy || !operation.name || !operation.namespace} onClick={() => { void prepare() }}>{t('helm.prepare')}</button></div>
          </>}
        </div> : (detail || chart) && <DetailsPanel widthKey="helmDetails" label={`Helm ${detail?.namespace ? detail.namespace + '/' : ''}${detail?.name ?? chart?.name}`}>
          <header className="drawer-heading"><div className="flex min-w-0 items-center gap-2"><h2 className="resource-title min-w-0 break-words">{detail?.name ?? chart?.name}</h2><span className="flex-1" /><button className={button} onClick={() => { controller.current?.abort(); controller.current = null; setBusy(false); setDetail(null); setChart(null) }}>{t('helm.close')}</button></div></header>
          <div className="flex shrink-0 flex-wrap gap-2 p-2">{detail ? <><button className={button} disabled={busy} onClick={() => begin('upgrade')}>{t('helm.upgrade')}</button><button className={button} disabled={busy} onClick={() => begin('uninstall')}>{t('helm.uninstall')}</button></> : <button className={button} disabled={busy} onClick={() => begin('install')}>{t('helm.install')}</button>}</div>
          <div className="relative flex min-h-0 flex-1 flex-col" aria-busy={busy}><div className="flex min-h-0 flex-1 flex-col" inert={busy}>
          {detail ? <>
            <nav role="tablist" className="drawer-tabs flex shrink-0 flex-wrap gap-1 border-b border-line px-3">{(['resources', 'values', 'computedValues', 'manifest', 'hooks', 'notes', 'history'] as const).map(v => <button key={v} role="tab" aria-selected={detailTab === v} className={`border-b-2 px-3 py-1.5 ${detailTab === v ? 'border-accent text-accent' : 'border-transparent text-fg-muted hover:text-fg'}`} onClick={() => setDetailTab(v)}>{t(`helm.${v}`)}</button>)}</nav>
            {detailTab === 'resources' ? <div className="overflow-auto"><p className="px-2 py-1 text-fg-muted">{t('helm.declaredResources')}</p><table className="helm-table"><tbody>{detail.resources.map((r, i) => <tr key={i}><td>{r.kind}</td><td>{r.ref && onResource ? <button onClick={() => onResource(r.ref!)}>{r.name}</button> : r.name}</td><td>{r.namespace || '—'}</td><td>{r.apiVersion}</td></tr>)}</tbody></table></div> : detailTab === 'history' ? <div className="overflow-auto p-2">{detail.history.map(r => <div className="mb-2 flex flex-wrap items-center gap-2" key={r.revision}><button className={button} disabled={busy} onClick={() => { void openRelease(r, r.revision) }}>#{r.revision} · {r.chartVersion} · {r.status}</button><button className={button} disabled={busy} onClick={() => begin('rollback', r.revision)}>{t('helm.rollback')}</button></div>)}</div> : <pre className="helm-code m-0 flex-1">{detail[detailTab]}</pre>}
          </> : <div className="overflow-auto p-2"><p>{chart?.description}</p>{chart && <Select value={chart.version} label={t('helm.chartVersion')} options={charts.filter(c => c.name === chart.name && c.repository === chart.repository).map(c => ({ value: c.version, label: c.version }))} onChange={v => { void openChart({ ...chart, version: v }) }} disabled={busy} />}<p>{chart?.appVersion}</p><pre className="helm-code">{chart?.readme || chart?.values}</pre></div>}
          </div>{busy && <LoadingOverlay />}</div>
        </DetailsPanel>}
      </div>
    </>}
  </section>
}
function Field({ label, children }: { label: string; children: ReactNode }) { return <label className="flex min-w-0 flex-col gap-1 text-fg-muted">{label}{children}</label> }
function SettingsEditor({ value, onChange, onSave, busy }: { value: HelmSettings; onChange: (s: HelmSettings) => void; onSave: () => Promise<void>; busy: boolean }) {
  return <div className="min-h-0 flex-1 overflow-auto p-3"><p className="mb-3 text-fg-muted">{t('helm.settingsHint')}</p>
    {value.repositories.map((r, i) => <fieldset key={i} className="mb-4 border border-line p-3" disabled={busy}><legend className="px-1">{r.name || t('helm.repository')}</legend><div className="helm-form-grid">
      {(['name', 'url', 'username', 'password', 'caFile', 'certFile', 'keyFile'] as const).map(k => <Field key={k} label={t(`helm.${k}`)}><input className="helm-input" type={k === 'password' ? 'password' : 'text'} autoComplete="off" value={r[k] ?? ''} placeholder={k === 'password' && r.hasPassword ? t('helm.passwordUnchanged') : ''} onChange={e => onChange({ ...value, repositories: value.repositories.map((v, n) => n === i ? { ...v, [k]: e.target.value, ...(k === 'password' ? { hasPassword: false } : {}) } : v) })} /></Field>)}
    </div><div className="mt-3 flex gap-4"><label><input type="checkbox" checked={!!r.insecure} onChange={e => onChange({ ...value, repositories: value.repositories.map((v, n) => n === i ? { ...v, insecure: e.target.checked } : v) })} /> {t('helm.insecure')}</label><button className={button} onClick={() => onChange({ ...value, repositories: value.repositories.filter((_, n) => n !== i) })}>{t('helm.remove')}</button></div></fieldset>)}
    <button className={button} disabled={busy} onClick={() => onChange({ ...value, repositories: [...value.repositories, { name: '', url: '' }] })}>{t('helm.addRepository')}</button>
    <div className="my-4 max-w-xl"><Field label={t('helm.storage')}><Select label={t('helm.storage')} value={value.storage.driver} options={['secret', 'configmap', 'sql'].map(v => ({ value: v, label: v }))} onChange={driver => onChange({ ...value, storage: { ...value.storage, driver } })} disabled={busy} /></Field>
      {value.storage.driver === 'sql' && <Field label={t('helm.sqlConnection')}><input className="helm-input" type="password" autoComplete="off" value={value.storage.sqlConnection ?? ''} placeholder={value.hasSQLConnection ? t('helm.passwordUnchanged') : ''} onChange={e => onChange({ ...value, storage: { ...value.storage, sqlConnection: e.target.value }, hasSQLConnection: false })} /></Field>}
    </div><button className={button} disabled={busy} onClick={() => { void onSave() }}>{t('helm.save')}</button>
  </div>
}
