#!/usr/bin/env python3
"""Fail-closed SQLite schema planner for the Gateway MySQL migration.

This tool never connects to MySQL and never copies data. It emits a private
manifest and refuses to label SQLite DDL as MySQL until every dialect blocker
has an explicit reviewed mapping.
"""
import argparse, json, pathlib, re, sqlite3, sys


def quote(name): return '"'+name.replace('"','""')+'"'

def inspect(path):
    path=path.resolve(strict=True)
    db=sqlite3.connect(path.as_uri()+"?mode=ro",uri=True,timeout=5)
    db.execute('PRAGMA query_only=ON')
    try:
        if db.execute('PRAGMA integrity_check').fetchone() != ('ok',): raise RuntimeError('integrity_failed')
        objects=db.execute("SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE substr(name,1,7)<>'sqlite_' ORDER BY type,name").fetchall()
        tables=[]; blockers=[]
        for typ,name,tbl,sql in objects:
            sql=sql or ''
            if typ=='table':
                info=db.execute('PRAGMA table_xinfo('+quote(name)+')').fetchall()
                tables.append({'name':name,'columns':[{'name':r[1],'type':r[2],'pk':r[5],'hidden':r[6]} for r in info], 'rows':db.execute('SELECT count(*) FROM '+quote(name)).fetchone()[0]})
                if 'WITHOUT ROWID' in sql.upper(): blockers.append({'kind':'without_rowid','name':name})
                if 'AUTOINCREMENT' in sql.upper(): blockers.append({'kind':'autoincrement','name':name})
                if re.search(r'JSON_VALID|JSON_EXTRACT|JSON_EACH',sql,re.I): blockers.append({'kind':'json_constraint','name':name})
            if typ in ('index','trigger') and (' WHERE ' in sql.upper() or 'SELECT ' in sql.upper() or 'WHEN ' in sql.upper()): blockers.append({'kind':'conditional_object','name':name,'type':typ})
        return {'objects':{k:sum(r[0]==k for r in objects) for k in ('table','index','trigger','view')},'tables':tables,'blockers':blockers}
    finally: db.close()

def main():
    ap=argparse.ArgumentParser(); ap.add_argument('--source',required=True,type=pathlib.Path); ap.add_argument('--plan',required=True,type=pathlib.Path); ap.add_argument('--allow-reviewed-mappings',action='store_true'); a=ap.parse_args()
    if a.plan.exists(): raise SystemExit('plan_destination_exists')
    report=inspect(a.source)
    report['mysql_ready']=not report['blockers'] or a.allow_reviewed_mappings
    if not report['mysql_ready']:
        report['error']='mysql_schema_mapping_required'
    a.plan.write_text(json.dumps(report,ensure_ascii=True,sort_keys=True,indent=2) + chr(10)); a.plan.chmod(0o600)
    print(json.dumps({'table_count':report['objects']['table'],'index_count':report['objects']['index'],'trigger_count':report['objects']['trigger'],'blocker_count':len(report['blockers']),'mysql_ready':report['mysql_ready']},sort_keys=True))
    return 0 if report['mysql_ready'] else 2
if __name__=='__main__': sys.exit(main())
