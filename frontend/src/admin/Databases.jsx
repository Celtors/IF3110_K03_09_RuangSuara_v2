import { useEffect, useState } from 'react'
import { http } from '../api/client'

const empty = { dbms: 'postgres', name: '', host: '', port: 5432, database_name: '', username: '', password: '', ssl_mode: 'verify-full' }
export default function Databases() {
  const [items,setItems]=useState([]), [form,setForm]=useState(empty), [editing,setEditing]=useState(null), [error,setError]=useState(''),[message,setMessage]=useState(''),[busy,setBusy]=useState(false)
  const load=async()=>{ try {setItems(await http.get('/api/admin/databases'))} catch(e){setError(e.message)} }
  useEffect(()=>{load()},[])
  const field=(key,label,type='text')=><label className="field" key={key}><span>{label}</span><input className="input" type={type} value={form[key]} onChange={e=>setForm(v=>({...v,[key]:type==='number'?Number(e.target.value):e.target.value}))} required={key!=='password'||!editing}/></label>
  const selectEngine=(e)=>setForm(v=>({...v,dbms:e.target.value,port:e.target.value==='mariadb'?3306:5432,ssl_mode:e.target.value==='mariadb'?'disable':'verify-full'}))
  const save=async e=>{e.preventDefault();setBusy(true);setError('');setMessage('');try{if(editing){await http.patch('/api/admin/databases/'+editing,form)}else{await http.post('/api/admin/databases',form)}setForm(empty);setEditing(null);setMessage('Database connection saved.');await load()}catch(ex){setError(ex.message)}finally{setBusy(false)}}
  const edit=d=>{setEditing(d.id);setForm({dbms:d.dbms||'postgres',name:d.name,host:d.host,port:d.port,database_name:d.database_name,username:d.username,password:'',ssl_mode:d.ssl_mode})}
  const test=async id=>{setError('');try{const r=await http.post('/api/admin/databases/'+id+'/test');setMessage(r.message);if(!r.success)setError(r.message)}catch(ex){setError(ex.message)}}
  const remove=async id=>{if(!window.confirm('Delete this database registration?'))return;try{await http.delete('/api/admin/databases/'+id);await load()}catch(e){setError(e.message)}}
  return <><header className="page-head"><div><h1>Database connections</h1><p className="muted">Register PostgreSQL or MariaDB databases. Credentials are kept encrypted on the backend.</p></div></header>
    {error&&<p className="alert alert-error" role="alert">{error}</p>}{message&&<p role="status">{message}</p>}
    <form className="stack" onSubmit={save}><h2>{editing?'Edit connection':'Add connection'}</h2>
      <label className="field"><span>DBMS</span><select className="select" value={form.dbms} onChange={selectEngine}><option value="postgres">PostgreSQL</option><option value="mariadb">MariaDB</option></select></label>
      {field('name','Connection name')}{field('host','Host')}{field('port','Port','number')}{field('database_name','Database name')}{field('username','Username')}{field('password',editing?'New password (leave blank to retain)':'Password','password')}
      <label className="field"><span>SSL/TLS mode</span><select className="select" value={form.ssl_mode} onChange={e=>setForm(v=>({...v,ssl_mode:e.target.value}))}>{(form.dbms==='mariadb'?['disable','require']:['verify-full','verify-ca','require','disable']).map(v=><option key={v} value={v}>{v}</option>)}</select></label>
      <div className="actions"><button className="btn btn-primary" disabled={busy}>{busy?'Saving…':'Save connection'}</button>{editing&&<button type="button" className="btn" onClick={()=>{setForm(empty);setEditing(null)}}>Cancel</button>}</div>
    </form>
    <h2>Registered connections</h2><div className="table-wrap"><table><thead><tr><th>Name</th><th>DBMS</th><th>Host</th><th>Database</th><th>Actions</th></tr></thead><tbody>{items.map(d=><tr key={d.id}><td>{d.name}</td><td>{d.dbms||'postgres'}</td><td>{d.host}:{d.port}</td><td>{d.database_name}</td><td><button className="btn" onClick={()=>edit(d)}>Edit</button> <button className="btn" onClick={()=>test(d.id)}>Test</button> <button className="btn" onClick={()=>remove(d.id)}>Delete</button></td></tr>)}</tbody></table></div>
    <p className="muted">The database viewer supports both engines. Migration execution remains PostgreSQL-only.</p>
  </>
}
