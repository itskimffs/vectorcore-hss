import React, { useState, useEffect, lazy, Suspense } from 'react'
import { Plus, Pencil, Trash2, Search, RefreshCw, Phone, ChevronUp, ChevronDown, ChevronsUpDown, Database } from 'lucide-react'
import { useSort } from '../hooks/useSort.js'
import { useInfiniteScroll } from '../hooks/useInfiniteScroll.js'
import Spinner from '../components/Spinner.jsx'
import Modal from '../components/Modal.jsx'
import DiscardConfirm from '../components/DiscardConfirm.jsx'
import SearchableSelect from '../components/SearchableSelect.jsx'
import { useToast } from '../components/Toast.jsx'
import { useDirtyState } from '../hooks/useDirtyState.js'
import { useConfirmClose } from '../hooks/useConfirmClose.js'
import {
  getIMSSubscribers, createIMSSubscriber, updateIMSSubscriber, deleteIMSSubscriber,
  getSubscribers, getIFCProfiles, getIMSSubscriberRepositoryData,
  putIMSSubscriberRepositoryData, deleteIMSSubscriberRepositoryData,
} from '../api/client.js'
import IFCProfiles from './IFCProfiles.jsx'

const IFCCodeEditor = lazy(() => import('../components/IFCCodeEditor.jsx'))

const TAB_STYLE = (active) => ({
  padding: '6px 16px',
  fontSize: '0.82rem',
  fontWeight: active ? 600 : 400,
  color: active ? 'var(--accent)' : 'var(--text-muted)',
  background: 'none',
  border: 'none',
  borderBottom: active ? '2px solid var(--accent)' : '2px solid transparent',
  cursor: 'pointer',
  whiteSpace: 'nowrap',
})

const SECTION_STYLE = {
  fontSize: '0.75rem',
  fontWeight: 600,
  textTransform: 'uppercase',
  letterSpacing: '0.08em',
  color: 'var(--text-muted)',
  marginBottom: 8,
  marginTop: 16,
  borderBottom: '1px solid var(--border-subtle)',
  paddingBottom: 4,
}

// prettyXML indents an XML fragment for reading. It returns the text as it is
// when it does not parse.
function prettyXML(src) {
  try {
    const doc = new DOMParser().parseFromString(`<root>${src}</root>`, 'application/xml')
    if (doc.getElementsByTagName('parsererror').length) return src
    const lines = []
    const esc = s => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    const walk = (node, depth) => {
      const pad = '  '.repeat(depth)
      if (node.nodeType === Node.TEXT_NODE || node.nodeType === Node.CDATA_SECTION_NODE) {
        const t = node.nodeValue.trim()
        if (t) lines.push(pad + esc(t))
        return
      }
      if (node.nodeType !== Node.ELEMENT_NODE) return
      const attrs = Array.from(node.attributes).map(a => ` ${a.name}="${esc(a.value).replace(/"/g, '&quot;')}"`).join('')
      const kids = Array.from(node.childNodes).filter(n => n.nodeType === Node.ELEMENT_NODE || (n.nodeValue || '').trim())
      if (kids.length === 0) {
        lines.push(`${pad}<${node.nodeName}${attrs}/>`)
      } else if (kids.length === 1 && kids[0].nodeType !== Node.ELEMENT_NODE) {
        lines.push(`${pad}<${node.nodeName}${attrs}>${esc(kids[0].nodeValue.trim())}</${node.nodeName}>`)
      } else {
        lines.push(`${pad}<${node.nodeName}${attrs}>`)
        kids.forEach(k => walk(k, depth + 1))
        lines.push(`${pad}</${node.nodeName}>`)
      }
    }
    Array.from(doc.documentElement.childNodes).forEach(n => walk(n, 0))
    return lines.join('\n')
  } catch {
    return src
  }
}

const MAX_SEQUENCE = 65535
const nextSequence = n => (n % MAX_SEQUENCE) + 1

function validateServiceData(value) {
  if (!value.trim()) return { valid: false, message: 'Service data is required.' }
  if (value.trim().startsWith('<?xml')) return { valid: false, message: 'Leave out the XML declaration.' }
  try {
    const doc = new DOMParser().parseFromString(`<ServiceData>${value}</ServiceData>`, 'application/xml')
    const err = doc.querySelector('parsererror')
    if (!err) return { valid: true, message: '' }
    return { valid: false, message: (err.textContent || '').replace(/\s+/g, ' ').trim() || 'The XML is not well-formed.' }
  } catch {
    return { valid: false, message: 'Unable to check the XML in this browser.' }
  }
}

// RepositoryDataEditor changes or adds one Service-Indication. It sends the
// Sequence-Number it read, so a change the AS made in the meantime is refused
// instead of overwritten.
function RepositoryDataEditor({ row, entry, existing, onDone, onCancel }) {
  const toast = useToast()
  const isNew = !entry
  const [si, setSi] = useState(isNew ? (existing.includes('MMTEL-Services') ? '' : 'MMTEL-Services') : entry.service_indication)
  const [seq, setSeq] = useState(String(isNew ? 0 : nextSequence(entry.sequence_number)))
  const [data, setData] = useState(isNew ? '' : prettyXML(entry.service_data))
  const [saving, setSaving] = useState(false)
  const validation = validateServiceData(data)
  const seqNum = Number(seq)
  const seqValid = seq !== '' && Number.isInteger(seqNum) && seqNum >= 0 && seqNum <= MAX_SEQUENCE

  async function save() {
    if (!si.trim()) { toast.error('Validation', 'Service-Indication is required'); return }
    if (isNew && existing.includes(si.trim())) { toast.error('Validation', `${si.trim()} already exists — edit it instead`); return }
    if (!seqValid) { toast.error('Validation', `Sequence number must be 0 to ${MAX_SEQUENCE}`); return }
    if (!validation.valid) { toast.error('Validation', validation.message); return }
    setSaving(true)
    try {
      await putIMSSubscriberRepositoryData(row.ims_subscriber_id, si.trim(), {
        service_data: data,
        sequence_number: seqNum,
        expected_sequence_number: isNew ? -1 : entry.sequence_number,
      })
      toast.success('Saved', `${si.trim()} saved with sequence number ${seqNum}`)
      onDone()
    } catch (err) {
      toast.error('Save failed', err.message)
      if (/changed|removed/.test(err.message)) onDone()
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={{ marginBottom: 16 }}>
      <div style={SECTION_STYLE}>{isNew ? 'Add Service-Indication' : `Edit ${entry.service_indication}`}</div>
      <div className="form-row">
        <div className="form-group">
          <label className="form-label">Service-Indication</label>
          <input className="input mono" value={si} onChange={e => setSi(e.target.value)} readOnly={!isNew} placeholder="MMTEL-Services" />
        </div>
        <div className="form-group">
          <label className="form-label">Sequence number</label>
          <input className="input mono" type="number" min={0} max={MAX_SEQUENCE} value={seq} onChange={e => setSeq(e.target.value)} />
        </div>
      </div>
      <div style={{ fontSize: '0.75rem', color: 'var(--text-muted)', marginBottom: 8 }}>
        {isNew
          ? 'New data normally starts at 0. The AS must then send 1 in its next update.'
          : `Now ${entry.sequence_number}. The AS must send this number plus one in its next update. It is not told about this change: its next update gets out of sync (5105) and it has to read the data again.`}
      </div>
      <Suspense fallback={<div className="loading-center"><Spinner /></div>}>
        <IFCCodeEditor value={data} onChange={setData} validation={validation} rows={14} />
      </Suspense>
      <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 8 }}>
        <button type="button" className="btn btn-ghost" onClick={onCancel} disabled={saving}>Cancel</button>
        <button type="button" className="btn btn-primary" onClick={save} disabled={saving}>
          {saving ? <Spinner size="sm" /> : null} Save
        </button>
      </div>
    </div>
  )
}

// RepositoryDataModal shows the Sh repository data that Application Servers
// have stored for an IMS subscriber, and lets an operator change, add or
// remove it.
function RepositoryDataModal({ row, onClose }) {
  const toast = useToast()
  const [entries, setEntries] = useState(null)
  const [error, setError] = useState(null)
  const [editing, setEditing] = useState(null) // null, 'new' or a Service-Indication
  const [deleting, setDeleting] = useState(null)

  function load() {
    setError(null)
    getIMSSubscriberRepositoryData(row.ims_subscriber_id)
      .then(d => setEntries(Array.isArray(d) ? d : []))
      .catch(err => setError(err.message || 'Failed to load'))
  }
  useEffect(load, [row.ims_subscriber_id])

  async function remove(e) {
    try {
      await deleteIMSSubscriberRepositoryData(row.ims_subscriber_id, e.service_indication, e.sequence_number)
      toast.success('Deleted', `${e.service_indication} deleted`)
    } catch (err) {
      toast.error('Delete failed', err.message)
    }
    setDeleting(null)
    load()
  }

  const done = () => { setEditing(null); load() }
  const existing = (entries || []).map(e => e.service_indication)

  return (
    <Modal title={`Sh Repository Data — ${row.msisdn}`} onClose={onClose} size="lg">
      <div className="modal-body">
        <div style={{ color: 'var(--text-muted)', fontSize: '0.8rem', marginBottom: 12 }}>
          Transparent data that Application Servers store over Sh (TS 29.328), one entry per Service-Indication.
          The AS writes it with Sh PUR. A change here is not sent to the AS.
        </div>
        {error && <div className="error-state">{error}</div>}
        {!error && entries === null && <div className="loading-center"><Spinner /> <span>Loading...</span></div>}
        {!error && entries !== null && entries.length === 0 && editing !== 'new' && (
          <div className="empty-state">No repository data stored for this subscriber.</div>
        )}
        {!error && entries !== null && entries.map(e => (
          editing === e.service_indication ? (
            <RepositoryDataEditor key={e.sh_repository_data_id} row={row} entry={e} existing={existing} onDone={done} onCancel={() => setEditing(null)} />
          ) : (
            <div key={e.sh_repository_data_id} style={{ marginBottom: 16 }}>
              <div style={{ ...SECTION_STYLE, display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                <span>{e.service_indication}</span>
                <span style={{ display: 'flex', gap: 4, textTransform: 'none', letterSpacing: 0 }}>
                  {deleting === e.service_indication ? (<>
                    <button type="button" className="btn btn-danger" style={{ padding: '2px 10px' }} onClick={() => remove(e)}>Delete {e.service_indication}</button>
                    <button type="button" className="btn btn-ghost" style={{ padding: '2px 10px' }} onClick={() => setDeleting(null)}>Cancel</button>
                  </>) : (<>
                    <button type="button" className="btn-icon" title="Edit" disabled={editing !== null} onClick={() => setEditing(e.service_indication)}><Pencil size={13} /></button>
                    <button type="button" className="btn-icon danger" title="Delete" disabled={editing !== null} onClick={() => setDeleting(e.service_indication)}><Trash2 size={13} /></button>
                  </>)}
                </span>
              </div>
              <div style={{ display: 'flex', gap: 24, fontSize: '0.8rem', color: 'var(--text-muted)', marginBottom: 8 }}>
                <span>Sequence number <span className="mono" style={{ color: 'var(--text)' }}>{e.sequence_number}</span></span>
                <span>Last modified <span style={{ color: 'var(--text)' }}>{e.last_modified ? new Date(e.last_modified).toLocaleString() : '—'}</span></span>
              </div>
              <pre className="mono" style={{
                margin: 0, padding: 10, maxHeight: 360, overflow: 'auto', fontSize: '0.75rem', lineHeight: 1.5,
                background: 'var(--bg-input)', border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)',
                whiteSpace: 'pre', color: 'var(--text)',
              }}>{prettyXML(e.service_data)}</pre>
            </div>
          )
        ))}
        {editing === 'new' && <RepositoryDataEditor row={row} existing={existing} onDone={done} onCancel={() => setEditing(null)} />}
      </div>
      <div className="modal-footer">
        <button type="button" className="btn btn-ghost" onClick={() => setEditing('new')} disabled={editing !== null || entries === null}>
          <Plus size={14} /> Add Service-Indication
        </button>
        <button type="button" className="btn btn-ghost" onClick={onClose}>Close</button>
      </div>
    </Modal>
  )
}

function IMSModal({ row, onClose, onSaved, subscriberList, ifcProfiles }) {
  const toast = useToast()
  const isEdit = !!row
  const [availableIfcProfiles, setAvailableIfcProfiles] = useState(ifcProfiles)
  const [loadingIfcProfiles, setLoadingIfcProfiles] = useState(true)
  const [form, setForm, dirty] = useDirtyState(isEdit ? {
    imsi: row.imsi || '',
    msisdn: row.msisdn || '',
    ifc_profile_id: row.ifc_profile_id != null ? String(row.ifc_profile_id) : '',
  } : {
    imsi: '',
    msisdn: '',
    ifc_profile_id: '',
  })
  const [saving, setSaving] = useState(false)
  const { requestClose: guardedClose, confirming, confirmDiscard, cancelDiscard } = useConfirmClose(dirty, onClose)

  useEffect(() => {
    let active = true

    async function loadIfcProfiles() {
      setLoadingIfcProfiles(true)
      try {
        const data = await getIFCProfiles()
        if (active) setAvailableIfcProfiles(Array.isArray(data) ? data : [])
      } catch (err) {
        if (active) {
          setAvailableIfcProfiles(Array.isArray(ifcProfiles) ? ifcProfiles : [])
          toast.error('IFC profiles', err.message || 'Failed to load IFC profiles')
        }
      } finally {
        if (active) setLoadingIfcProfiles(false)
      }
    }

    loadIfcProfiles()
    return () => { active = false }
  }, [ifcProfiles, toast])

  function set(k, v) {
    setForm(prev => ({ ...prev, [k]: v }))
  }

  function handleImsiChange(imsi) {
    set('imsi', imsi)
    if (imsi) {
      const sub = subscriberList.find(s => s.imsi === imsi)
      if (sub && sub.msisdn) {
        set('msisdn', sub.msisdn)
      }
    }
  }

  async function handleSubmit(e) {
    e.preventDefault()
    if (!form.imsi) {
      toast.error('Validation', 'IMSI / MSISDN is required — select a subscriber above')
      return
    }
    if (!form.msisdn.trim()) {
      toast.error('Validation', 'Selected subscriber has no MSISDN on file — set one on the Subscribers page first')
      return
    }
    if (!form.ifc_profile_id) {
      toast.error('Validation', 'IFC Profile is required')
      return
    }
    setSaving(true)
    try {
      const payload = {
        msisdn: form.msisdn,
        imsi: form.imsi,
        ...(form.ifc_profile_id !== '' && { ifc_profile_id: parseInt(form.ifc_profile_id, 10) }),
      }
      if (isEdit) {
        await updateIMSSubscriber(row.ims_subscriber_id, payload)
        toast.success('Updated', `IMS subscriber ${form.msisdn} updated`)
      } else {
        await createIMSSubscriber(payload)
        toast.success('Created', `IMS subscriber ${form.msisdn} created`)
      }
      onSaved()
      onClose()
    } catch (err) {
      toast.error('Save failed', err.message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal title={isEdit ? 'Edit IMS Subscriber' : 'Add IMS Subscriber'} onClose={guardedClose} size="lg" closeOnBackdrop={false} closeOnEscape={false}>
      <form onSubmit={handleSubmit} style={{ display: "flex", flexDirection: "column", flex: 1, minHeight: 0 }}>
        <div className="modal-body">

          <div style={SECTION_STYLE}>Identity</div>
          <div className="form-row">
            <div className="form-group">
              <label className="form-label">IMSI / MSISDN <span style={{ color: 'var(--danger)' }}>*</span></label>
              <SearchableSelect
                options={subscriberList.filter(s => s.msisdn).map(s => ({
                  value: s.imsi,
                  label: `${s.imsi} (${s.msisdn})`,
                  sublabel: s.msisdn,
                }))}
                value={form.imsi}
                onChange={handleImsiChange}
                placeholder="— Select subscriber —"
              />
            </div>
            <div className="form-group">
              <label className="form-label">MSISDN</label>
              <input
                className="input mono"
                value={form.msisdn}
                readOnly
                placeholder="441234567890"
              />
            </div>
          </div>
          <div className="form-group">
            <label className="form-label">IFC Profile <span style={{ color: 'var(--danger)' }}>*</span></label>
            <select
              className="select"
              value={form.ifc_profile_id}
              onChange={e => set('ifc_profile_id', e.target.value)}
              required
              disabled={loadingIfcProfiles}
            >
              <option value="">
                {loadingIfcProfiles ? 'Loading IFC Profiles...' : '— Select IFC Profile —'}
              </option>
              {availableIfcProfiles.map(p => (
                <option key={p.ifc_profile_id} value={String(p.ifc_profile_id)}>{p.name}</option>
              ))}
            </select>
          </div>

        </div>
        <div className="modal-footer">
          <button type="button" className="btn btn-ghost" onClick={onClose} disabled={saving}>Cancel</button>
          <button type="submit" className="btn btn-primary" disabled={saving}>
            {saving ? <Spinner size="sm" /> : null}
            {isEdit ? 'Save' : 'Create'}
          </button>
        </div>
      </form>
      <DiscardConfirm open={confirming} onDiscard={confirmDiscard} onCancel={cancelDiscard} />
    </Modal>
  )
}

export default function IMSSubscribers() {
  const toast = useToast()
  const [activeTab, setActiveTab] = useState('ims')
  const [search, setSearch] = useState('')
  const [modal, setModal] = useState(null)
  const [delConfirm, setDelConfirm] = useState(null)
  const [repoView, setRepoView] = useState(null)
  const [deleting, setDeleting] = useState(null)
  const [subscriberList, setSubscriberList] = useState([])
  const [ifcProfiles, setIfcProfiles] = useState([])

  const { items, total, loading, loadingMore, error, sentinelRef, refresh } = useInfiniteScroll(getIMSSubscribers, search)

  useEffect(() => {
    getSubscribers().then(d => setSubscriberList(Array.isArray(d?.items) ? d.items : [])).catch(() => {})
    getIFCProfiles().then(d => setIfcProfiles(Array.isArray(d) ? d : [])).catch(() => {})
  }, [])

  const enriched = items.map(r => ({ ...r, _ifc_profile: getIfcName(r.ifc_profile_id) }))
  const { sorted: sortedRows, sortKey, sortDir, handleSort } = useSort(enriched, 'msisdn')

  function SortIcon({ col }) {
    if (sortKey !== col) return <span className="sort-icon"><ChevronsUpDown size={11} /></span>
    return <span className="sort-icon">{sortDir === 'asc' ? <ChevronUp size={11} /> : <ChevronDown size={11} />}</span>
  }

  function getIfcName(id) {
    if (id == null) return '—'
    const p = ifcProfiles.find(p => p.ifc_profile_id === id)
    return p ? p.name : String(id)
  }

  async function handleDelete(row) {
    setDeleting(row.ims_subscriber_id)
    try {
      await deleteIMSSubscriber(row.ims_subscriber_id)
      toast.success('Deleted', `IMS subscriber ${row.msisdn} deleted`)
      setDelConfirm(null)
      refresh()
    } catch (err) {
      toast.error('Delete failed', err.message)
    } finally {
      setDeleting(null)
    }
  }

  if (loading) {
    return (
      <div className="loading-center">
        <Spinner size="lg" />
        <span>Loading IMS subscribers...</span>
      </div>
    )
  }

  if (error && items.length === 0) {
    return (
      <div className="error-state">
        <div>{error}</div>
        <button className="btn btn-ghost mt-12" onClick={refresh}><RefreshCw size={14} /> Retry</button>
      </div>
    )
  }

  return (
    <div>
      <div className="page-header">
        <div>
          <div className="page-title">IMS Subscribers</div>
          <div className="page-subtitle">VoLTE/IMS subscriber profiles — {total != null ? total : items.length} total</div>
        </div>
        {activeTab === 'ims' && (
          <div style={{ display: 'flex', gap: 8 }}>
            <button className="btn btn-ghost" onClick={refresh}><RefreshCw size={14} /> Refresh</button>
            <button className="btn btn-primary" onClick={() => setModal('add')}><Plus size={14} /> Add IMS Subscriber</button>
          </div>
        )}
      </div>

      <div style={{ display: 'flex', borderBottom: '1px solid var(--border)', marginBottom: 16 }}>
        <button style={TAB_STYLE(activeTab === 'ims')} onClick={() => setActiveTab('ims')}>IMS Subscribers</button>
        <button style={TAB_STYLE(activeTab === 'ifc')} onClick={() => setActiveTab('ifc')}>IFC Profiles</button>
      </div>

      {activeTab === 'ims' && (<>
        <div style={{ display: 'flex', gap: 8, marginBottom: 16 }}>
          <div style={{ position: 'relative', flex: 1, maxWidth: 340 }}>
            <Search size={14} style={{ position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)', color: 'var(--text-muted)' }} />
            <input className="input" style={{ paddingLeft: 32 }} placeholder="Search by MSISDN, IMSI, or Alias..."
              value={search} onChange={e => setSearch(e.target.value)} />
          </div>
        </div>

        {!loading && items.length === 0 ? (
          <div className="empty-state">
            <div style={{ marginBottom: 8 }}><Phone size={32} style={{ color: 'var(--text-muted)' }} /></div>
            <div>{search ? 'No results match your search.' : 'No IMS subscribers configured.'}</div>
            {!search && <button className="btn btn-primary" style={{ marginTop: 12 }} onClick={() => setModal('add')}><Plus size={14} /> Add IMS Subscriber</button>}
          </div>
        ) : (
          <div className="table-container">
            <table>
              <thead>
                <tr>
                  <th className={`sortable${sortKey === 'msisdn' ? ' sort-active' : ''}`} onClick={() => handleSort('msisdn')}>MSISDN<SortIcon col="msisdn" /></th>
                  <th className={`sortable${sortKey === 'imsi' ? ' sort-active' : ''}`} onClick={() => handleSort('imsi')}>IMSI<SortIcon col="imsi" /></th>
                  <th className={`sortable${sortKey === 'alias' ? ' sort-active' : ''}`} onClick={() => handleSort('alias')}>Alias<SortIcon col="alias" /></th>
                  <th className={`sortable${sortKey === '_ifc_profile' ? ' sort-active' : ''}`} onClick={() => handleSort('_ifc_profile')}>IFC Profile<SortIcon col="_ifc_profile" /></th>
                  <th className={`sortable${sortKey === 'last_modified' ? ' sort-active' : ''}`} onClick={() => handleSort('last_modified')}>Last Modified<SortIcon col="last_modified" /></th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {sortedRows.map(row => (
                  <tr key={row.ims_subscriber_id}>
                    <td className="mono" style={{ fontWeight: 600 }}>{row.msisdn}</td>
                    <td className="mono" style={{ color: 'var(--text-muted)', fontSize: '0.78rem' }}>{row.imsi || '—'}</td>
                    <td style={{ color: 'var(--text-muted)', fontSize: '0.82rem' }}>{row.alias || '—'}</td>
                    <td style={{ color: 'var(--text-muted)', fontSize: '0.82rem' }}>{getIfcName(row.ifc_profile_id)}</td>
                    <td style={{ color: 'var(--text-muted)', fontSize: '0.78rem' }}>
                      {row.last_modified ? new Date(row.last_modified).toLocaleString() : '—'}
                    </td>
                    <td>
                      <div style={{ display: 'flex', gap: 4 }}>
                        <button className="btn-icon" title="Sh repository data" onClick={() => setRepoView(row)}><Database size={13} /></button>
                        <button className="btn-icon" title="Edit" onClick={() => setModal({ row })}><Pencil size={13} /></button>
                        <button className="btn-icon danger" title="Delete" onClick={() => setDelConfirm(row)}><Trash2 size={13} /></button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <div ref={sentinelRef} style={{ height: 1 }} />
            {loadingMore && <div style={{ textAlign: 'center', padding: '12px 0', color: 'var(--text-muted)', fontSize: '0.8rem' }}><Spinner size="sm" /> Loading more…</div>}
          </div>
        )}

        {modal === 'add' && <IMSModal onClose={() => setModal(null)} onSaved={refresh} subscriberList={subscriberList} ifcProfiles={ifcProfiles} />}
        {modal && modal.row && <IMSModal row={modal.row} onClose={() => setModal(null)} onSaved={refresh} subscriberList={subscriberList} ifcProfiles={ifcProfiles} />}

        {repoView && <RepositoryDataModal row={repoView} onClose={() => setRepoView(null)} />}

        {delConfirm && (
          <Modal title="Delete IMS Subscriber" onClose={() => setDelConfirm(null)}>
            <div className="modal-body">
              <p>Delete IMS subscriber <strong>{delConfirm.msisdn}</strong> and its Sh repository data? This cannot be undone.</p>
            </div>
            <div className="modal-footer">
              <button className="btn btn-ghost" onClick={() => setDelConfirm(null)}>Cancel</button>
              <button className="btn btn-danger" onClick={() => handleDelete(delConfirm)} disabled={deleting === delConfirm.ims_subscriber_id}>
                {deleting === delConfirm.ims_subscriber_id ? <Spinner size="sm" /> : 'Delete'}
              </button>
            </div>
          </Modal>
        )}
      </>)}

      {activeTab === 'ifc' && <IFCProfiles compact />}
    </div>
  )
}
