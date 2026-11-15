#!/usr/bin/env python3
"""Opt-in legacy interoperability acceptance; default Go tests require no Node.

Prepare an isolated baseline with --prepare (network access), then reuse the
same directory offline with --legacy-dir. Never reads/writes real app data.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import selectors
import shutil
import subprocess
import tarfile
import tempfile
import time

BASELINE = '941d9336f1c1b097aa752519ead6af5e009d4664'
REPO = Path(__file__).resolve().parents[2]


def run(args, **kwargs):
    return subprocess.run(args, check=True, text=True, capture_output=True, **kwargs).stdout.strip()


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def assert_utc_time(value, started):
    assert re.fullmatch(r'\d{4}-\d\d-\d\d \d\d:\d\d:\d\d', value), value
    instant = datetime.strptime(value, '%Y-%m-%d %H:%M:%S').replace(tzinfo=timezone.utc).timestamp()
    assert int(started) <= instant <= int(time.time()), value


class Peer:
    def __init__(self, args, env):
        self.p = subprocess.Popen(args, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=subprocess.PIPE, text=True, env=env)

    def send(self, op, **fields):
        self.p.stdin.write(json.dumps(dict(Op=op, **fields)) + '\n')
        self.p.stdin.flush()

    def recv(self, allow_error=False):
        with selectors.DefaultSelector() as selector:
            selector.register(self.p.stdout, selectors.EVENT_READ)
            assert selector.select(20), 'peer response timeout'
        line = self.p.stdout.readline()
        assert line, 'peer exited: ' + self.p.stderr.read()
        response = json.loads(line)
        assert allow_error or 'error' not in response, response
        return response

    def call(self, op, **fields):
        self.send(op, **fields)
        return self.recv()['value']

    def close(self):
        self.p.stdin.close()
        try:
            self.p.wait(timeout=10)
            assert self.p.returncode == 0, self.p.stderr.read()
        except BaseException:
            self.p.kill()
            self.p.wait()
            raise
        finally:
            self.p.stdout.close()
            self.p.stderr.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--legacy-dir', type=Path)
    parser.add_argument('--prepare', type=Path, help='new empty isolation directory')
    parser.add_argument('--go', default='go', help='exact Go 1.27.0 executable')
    parser.add_argument('--node', default='node')
    args = parser.parse_args()
    assert bool(args.prepare) != bool(args.legacy_dir), 'choose --prepare or --legacy-dir'
    env = dict(os.environ, GOTOOLCHAIN='local', CGO_ENABLED='0')
    env['GOPATH'] = run([args.go, 'env', 'GOPATH'], env=env)
    env['GOCACHE'] = run([args.go, 'env', 'GOCACHE'], env=env)
    assert run([args.go, 'version'], env=env).split()[2] == 'go1.27.0'
    if args.prepare:
        root = args.prepare.resolve()
        root.mkdir(parents=True, exist_ok=False)
        legacy = root / 'legacy'
        legacy.mkdir()
        archive = root / 'baseline.tar'
        with archive.open('wb') as out:
            subprocess.run(['git', 'archive', BASELINE], cwd=REPO, stdout=out, check=True)
        with tarfile.open(archive) as tar:
            tar.extractall(legacy, filter='data')
        home = root / 'home'
        home.mkdir()
        npmrc = root / 'npmrc'
        npmrc.write_text('ignore-scripts=true\naudit=false\nfund=false\n')
        prepare_env = dict(env, HOME=str(home), NPM_CONFIG_USERCONFIG=str(npmrc),
                           NPM_CONFIG_CACHE=str(root / 'npm-cache'))
        print(run(['npm', 'ci', '--ignore-scripts', '--no-audit', '--no-fund'],
                  cwd=legacy, env=prepare_env), flush=True)
        print(run([args.node, '../prebuild-install/bin.js', '--verbose'],
                  cwd=legacy / 'node_modules/better-sqlite3', env=prepare_env), flush=True)
    else:
        legacy = args.legacy_dir.resolve()
    # Prove that this is the original product source and the exact dependency lock.
    for name in ('package.json', 'package-lock.json', 'lib/db.js'):
        baseline = subprocess.check_output(['git', 'show', f'{BASELINE}:{name}'], cwd=REPO)
        assert (legacy / name).read_bytes() == baseline, f'baseline changed: {name}'
    assert json.loads((legacy / 'node_modules/better-sqlite3/package.json').read_text())['version'] == '12.10.0'
    native = legacy / 'node_modules/better-sqlite3/build/Release/better_sqlite3.node'
    assert native.is_file()
    with tempfile.TemporaryDirectory(prefix='ianchor-interop-') as temp:
        work = Path(temp)
        (work / 'home').mkdir()
        env['HOME'] = str(work / 'home')
        module = work / 'peer'
        module.mkdir()
        shutil.copy(REPO / 'internal/store/testdata/interop.go', module / 'main.go')
        shutil.copy(REPO / 'go.sum', module / 'go.sum')
        (module / 'go.mod').write_text('module github.com/puppetdevz/ianchor/acceptance\n\ngo 1.27.0\n'
            'require github.com/puppetdevz/ianchor v0.0.0\n'
            f'replace github.com/puppetdevz/ianchor => {json.dumps(str(REPO))}\n')
        binary = module / 'peer'
        # This test queries PRAGMA on the actual store connection, including
        # after connection recycling. The auxiliary lock connection is not proof.
        timeout_test = run([args.go, 'test', '-count=1', './internal/store', '-run',
                            '^TestBusyTimeoutPersistsAcrossReopenedConnections$'], cwd=REPO, env=env)
        run([args.go, 'build', '-mod=mod', '-o', str(binary), '.'], cwd=module, env=env)
        dbdir = work / '数据库 空格 #? %'
        dbdir.mkdir()
        report = dict(baseline=BASELINE, node=run([args.node, '--version']),
                      go=run([args.go, 'version'], env=env), native_sha256=digest(native),
                      lock_sha256=digest(legacy / 'package-lock.json'), source_sha256=digest(REPO / 'internal/store/store.go'),
                      production_timeout_test=timeout_test)
        node = Peer([args.node, str(REPO / 'internal/store/testdata/interop.mjs'), str(legacy), str(dbdir)], env)
        go = None
        try:
            seeded_at = time.time()
            node.call('seed')
            before = node.call('list')
            assert [r['name'] for r in before] == ['null-times', 'z-last', '旧 #? 配置']
            assert before[0]['id'] == 100 and before[0]['created_at'] is None and before[0]['updated_at'] is None
            assert before[1]['original_path'] is None
            for row in before[1:]:
                for field in ('created_at', 'updated_at'):
                    assert_utc_time(row[field], seeded_at)
            baseline_meta = node.call('meta')
            assert baseline_meta['journal'] == 'wal' and baseline_meta['timeout'] == 5000
            go = Peer([str(binary), str(dbdir)], env)
            assert go.call('list') == before
            meta = go.call('meta')
            assert meta == {key: baseline_meta[key] for key in ('schema', 'journal')}
            assert node.call('meta') == baseline_meta
            assert (dbdir / 'ianchor.db-wal').stat().st_size > 0
            go.call('add', Name='go-first', Path='/go/路径 #?')
            first = node.call('get', Name='go-first')
            assert first == go.call('get', Name='go-first') and first['id'] > 100
            go.call('add', Name='go-first', Path='/ignored')
            assert node.call('get', Name='go-first') == first  # INSERT OR IGNORE
            for operation, fields in [('status', dict(Status='unlinked')), ('path', dict(Path='/go/updated'))]:
                node.call('age', Name='go-first')
                started = time.time()
                go.call(operation, Name='go-first', **fields)
                assert_utc_time(node.call('get', Name='go-first')['updated_at'], started)
            updated = node.call('get', Name='go-first')
            assert updated['id'] == first['id'] and updated['created_at'] == first['created_at']
            assert updated['original_path'] == '/go/updated' and updated['status'] == 'unlinked'
            assert re.fullmatch(r'\d{4}-\d\d-\d\d \d\d:\d\d:\d\d', updated['updated_at'])
            node.call('age', Name='go-first')
            started = time.time()
            node.call('update', Name='go-first', Path='/node/updated', Status='linked')
            assert_utc_time(go.call('get', Name='go-first')['updated_at'], started)
            assert go.call('get', Name='go-first') == node.call('get', Name='go-first')
            node.call('add', Name='node-after-go', Path='/node/new')
            assert go.call('get', Name='node-after-go') == node.call('get', Name='node-after-go')
            go.call('delete', Name='node-after-go')
            assert node.call('get', Name='node-after-go') is None
            node.call('delete', Name='go-first')
            assert go.call('get', Name='go-first') is None
            missing = work / 'empty'
            missing.mkdir()
            for peer in (node, go):
                assert peer.call('readonly') == before
                peer.call('missing', Path=str(missing))
                assert list(missing.iterdir()) == []
                assert peer.call('list') == before
            waits = []
            for label, holder, writer in [('node-holds-go-writes', node, go), ('go-holds-node-writes', go, node)]:
                holder.call('lock')
                try:
                    start = time.monotonic()
                    writer.send('add', Name=label, Path='/lock/probe')
                    response = writer.recv(allow_error=True)
                    elapsed = time.monotonic() - start
                    assert 'database is locked' in response.get('error', ''), response
                    assert 4.5 <= elapsed <= 15, elapsed
                    waits.append(dict(direction=label, timeout_seconds=round(elapsed, 3)))
                finally:
                    holder.call('unlock')
                assert writer.call('get', Name=label) is None
                holder.call('lock')
                writer.send('add', Name=label, Path='/released/probe')
                time.sleep(0.2)
                holder.call('unlock')
                writer.recv()
                assert holder.call('get', Name=label)['original_path'] == '/released/probe'
                writer.call('delete', Name=label)
            go.call('add', Name='go-persist', Path='/persistent/go value #?')
            go.call('update', Name='旧 #? 配置', Path='/persistent/go update', Status='unlinked')
            expected_final = go.call('list')
            assert node.call('list') == expected_final
            assert node.call('meta') == baseline_meta
            report.update(waits=waits, live_wal_bytes=(dbdir / 'ianchor.db-wal').stat().st_size,
                          checks=['real Node→Go→Node APIs', 'schema/fields/null/UTC time/order/id',
                                  'INSERT OR IGNORE', 'update/delete', 'special-path DSN',
                                  'live WAL', 'both lock timeouts and release', 'readonly/missing no create'])
        finally:
            try:
                if go is not None:
                    go.close()
            finally:
                node.close()
        # Closed-product reopens prove checkpointed content is still usable both ways.
        for argv in ([str(binary), str(dbdir)],
                     [args.node, str(REPO / 'internal/store/testdata/interop.mjs'), str(legacy), str(dbdir)]):
            peer = Peer(argv, env)
            try:
                assert peer.call('list') == expected_final
                assert peer.call('meta')['schema'] == baseline_meta['schema']
            finally:
                peer.close()
        report['close_and_reopen'] = 'passed'
        print(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
