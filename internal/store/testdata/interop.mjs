// Acceptance oracle only. Imports the locked baseline product, never reimplements it.
import { createInterface } from 'node:readline';
import { pathToFileURL } from 'node:url';
import path from 'node:path';
const [legacy, dir] = process.argv.slice(2);
const { openDb, addEntry, getEntry, getAllEntries, updateEntry, deleteEntry } =
  await import(pathToFileURL(path.join(legacy, 'lib/db.js')));
const db = openDb(dir);
try {
  for await (const line of createInterface({input: process.stdin})) {
    const r = JSON.parse(line);
    let value = null;
    let error;
    try {
      switch (r.Op) {
        case 'seed':
          addEntry(db, {name: '旧 #? 配置', originalPath: '/old/路径 #?', type: 'file'});
          addEntry(db, {name: 'z-last', type: 'directory', status: 'unlinked'});
          db.prepare('INSERT INTO entries (id,name,type,status,created_at,updated_at) VALUES (?,?,?,?,?,?)')
            .run(100, 'null-times', 'file', 'deleted', null, null);
          break;
        case 'age': db.prepare('UPDATE entries SET updated_at=? WHERE name=?').run('2001-02-03 04:05:06',r.Name); break;
        case 'list': value = getAllEntries(db); break;
        case 'get': value = getEntry(db,r.Name) ?? null; break;
        case 'add': addEntry(db, {name:r.Name, originalPath:r.Path, type:'file'}); break;
        case 'update': updateEntry(db,r.Name,{originalPath:r.Path,status:r.Status}); break;
        case 'delete': deleteEntry(db,r.Name); break;
        case 'lock': db.exec('BEGIN IMMEDIATE'); break;
        case 'unlock': db.exec('ROLLBACK'); break;
        case 'meta': value = {
          schema:db.prepare("SELECT sql FROM sqlite_master WHERE name='entries'").get().sql,
          journal:db.pragma('journal_mode',{simple:true}),
          timeout:db.pragma('busy_timeout',{simple:true})}; break;
        case 'readonly': {
          const ro = openDb(dir,{readonly:true});
          try {
            value = getAllEntries(ro);
            let refused = false;
            try { addEntry(ro,{name:'readonly-must-not-exist',type:'file'}); }
            catch(e) { if (!e.message.includes('readonly')) throw e; refused = true; }
            if (!refused) throw new Error('readonly write was not rejected');
          } finally { ro.close(); }
          break;
        }
        case 'missing': {
          let refused = false;
          try { const ro = openDb(r.Path,{readonly:true}); ro.close(); }
          catch { refused = true; }
          if (!refused) throw new Error('missing readonly database opened');
          break;
        }
        default: throw new Error(`unknown op ${r.Op}`);
      }
    } catch(e) { error = e.message; }
    console.log(JSON.stringify({value,...(error ? {error} : {})}));
  }
} finally { db.close(); }
