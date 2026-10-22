import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';

const workflow = fs.readFileSync(new URL('../.github/workflows/release.yml', import.meta.url), 'utf8');

// Extract these steps' shell commands without adding a YAML dependency.
function stepScript(name) {
  const step = workflow.split(`      - name: ${name}\n`)[1]?.split('\n      - ')[0];
  assert.ok(step, `Missing workflow step: ${name}`);
  const run = step.match(/^        run: (.+)$/m);
  assert.ok(run, `Missing run command: ${name}`);
  const script = run[1] === '|'
    ? step.slice(run.index + run[0].length).split('\n').map(line => line.replace(/^          /, '')).join('\n')
    : run[1];
  return script.replaceAll('${{ github.ref_name }}', 'v9.9.9');
}

function runScript(script, mocks, env = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ianchor-release-test-'));
  const log = path.join(dir, 'commands.log');
  try {
    for (const [name, body] of Object.entries(mocks)) {
      fs.writeFileSync(path.join(dir, name), `#!/bin/bash\n${body}\n`, { mode: 0o755 });
    }
    // Match GitHub Actions' default bash error handling. All network commands are mocked.
    const result = spawnSync('bash', ['-e', '-o', 'pipefail', '-c', script], {
      cwd: dir,
      encoding: 'utf8',
      env: { ...process.env, PATH: `${dir}${path.delimiter}${process.env.PATH}`, LOG: log, ...env },
    });
    assert.ifError(result.error);
    return { ...result, log: fs.existsSync(log) ? fs.readFileSync(log, 'utf8') : '' };
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

const ghMock = `
printf '%s\\n' "$*" >> "$LOG"
case "$1 $2" in
  'release view') exit "$VIEW_STATUS" ;;
  'release create') exit "\${CREATE_STATUS:-0}" ;;
  *) exit 90 ;;
esac`;

const downloadMocks = {
  curl: `
printf 'curl' >> "$LOG"
output=''
while [ "$#" -gt 0 ]; do
  printf ' [%s]' "$1" >> "$LOG"
  if [ "$1" = '--output' ]; then
    shift
    output="$1"
    printf ' [%s]' "$1" >> "$LOG"
  fi
  shift
done
printf '\\n' >> "$LOG"
if [ -n "$output" ]; then
  printf 'tarball fixture' > "$output"
else
  printf 'tarball fixture'
fi
exit "\${CURL_STATUS:-0}"`,
  shasum: `
printf 'shasum [%s] [%s] [%s]\\n' "$1" "$2" "$3" >> "$LOG"
[ "$1" = '-a' ] && [ "$2" = '256' ] && [ -f "$3" ] || exit 91
[ "$(< "$3")" = 'tarball fixture' ] || exit 92
printf 'fixture-sha256  %s\\n' "$3"`,
};

function downloadScript() {
  // Limit execution to the download/checksum portion, before any tap filesystem changes.
  return stepScript('Update Homebrew formula').split(/^git clone /m)[0];
}

// Clone into the workflow's temporary directory, but never contact a remote or commit/push.
const tapMocks = {
  ...downloadMocks,
  git: `
printf 'git %s\\n' "$*" >> "$LOG"
case "$1" in
  clone)
    mkdir -p "$3/Formula"
    if [ "\${MISSING_FORMULA:-0}" != '1' ]; then
      if [ "\${UNCHANGED_FORMULA:-0}" = '1' ]; then
        url='https://github.com/puppetdevz/ianchor/archive/refs/tags/v9.9.9.tar.gz'
        sha='fixture-sha256'
      else
        url='https://github.com/puppetdevz/ianchor/archive/refs/tags/v1.0.0.tar.gz'
        sha='old-sha256'
      fi
      printf 'class Ianchor < Formula\\n  url "%s"\\n  sha256 "%s"\\nend\\n' "$url" "$sha" > "$3/Formula/ianchor.rb"
      cp "$3/Formula/ianchor.rb" "$3/original-ianchor.rb"
    fi
    printf 'unrelated formula\\n' > "$3/Formula/other.rb"
    printf 'tap readme\\n' > "$3/README.md"
    ;;
  diff)
    [ "$#" = '3' ] && [ "$2" = '--name-only' ] && [ "$3" = 'Formula/ianchor.rb' ] || exit 93
    printf 'updated formula: %s\\n' "$(< Formula/ianchor.rb)" >> "$LOG"
    printf 'other formula: %s\\n' "$(< Formula/other.rb)" >> "$LOG"
    printf 'readme: %s\\n' "$(< README.md)" >> "$LOG"
    if ! cmp -s original-ianchor.rb Formula/ianchor.rb; then
      printf 'Formula/ianchor.rb\\n'
    fi
    ;;
  add)
    [ "$#" = '2' ] && [ "$2" = 'Formula/ianchor.rb' ] || exit 94
    ;;
  config|commit|push) ;;
  *) exit 95 ;;
esac`,
  sed: `
printf 'sed %s\\n' "$*" >> "$LOG"
exec /usr/bin/sed "$@"`,
};

function updateFormula(env = {}) {
  const script = stepScript('Update Homebrew formula');
  // Also prevents the pre-fix workflow from touching its hard-coded /tmp clone path.
  assert.match(script, /github\.com\/puppetdevz\/homebrew-puppetdevz\.git/);
  return runScript(script, tapMocks, { GH_TOKEN: 'test-token', ...env });
}

describe('release workflow', () => {
  it('skips creating an existing GitHub release', () => {
    const result = runScript(stepScript('Create GitHub release'), { gh: ghMock }, { VIEW_STATUS: '0' });
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.log, /release view v9\.9\.9/);
    assert.doesNotMatch(result.log, /release create/);
  });

  it('creates a GitHub release when it does not exist', () => {
    const result = runScript(stepScript('Create GitHub release'), { gh: ghMock }, { VIEW_STATUS: '1' });
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.log, /release view v9\.9\.9[\s\S]*release create v9\.9\.9/);
  });

  it('propagates GitHub release creation failures', () => {
    const result = runScript(stepScript('Create GitHub release'), { gh: ghMock }, {
      VIEW_STATUS: '1', CREATE_STATUS: '7',
    });
    assert.equal(result.status, 7, result.stderr);
  });

  it('downloads successfully to a file before calculating its SHA256', () => {
    const result = runScript(downloadScript(), downloadMocks);
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.log, /curl .*\[--fail\].*\[--location\].*\[--retry\] \[3\].*\[--output\]/);
    assert.match(result.log, /https:\/\/github\.com\/puppetdevz\/ianchor\/archive\/refs\/tags\/v9\.9\.9\.tar\.gz/);
    assert.match(result.stdout, /sha256: fixture-sha256/);
    assert.ok(result.log.indexOf('curl') < result.log.indexOf('shasum'));
  });

  it('stops on download failure without calculating a checksum', () => {
    const result = runScript(downloadScript(), downloadMocks, { CURL_STATUS: '22' });
    assert.equal(result.status, 22, result.stderr);
    assert.doesNotMatch(result.log, /shasum/);
    assert.doesNotMatch(result.stdout, /sha256:/);
  });

  it('updates only ianchor in the personal tap using the source repository tarball', () => {
    const result = updateFormula();
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.log, /git clone https:\/\/x-access-token:test-token@github\.com\/puppetdevz\/homebrew-puppetdevz\.git /);
    assert.doesNotMatch(result.log, /homebrew-ianchor\.git/);
    assert.match(result.log, /updated formula: class Ianchor < Formula\n  url "https:\/\/github\.com\/puppetdevz\/ianchor\/archive\/refs\/tags\/v9\.9\.9\.tar\.gz"\n  sha256 "fixture-sha256"\nend/);
    assert.match(result.log, /other formula: unrelated formula\nreadme: tap readme\n/);
    assert.match(result.log, /^git add Formula\/ianchor\.rb$/m);
    assert.match(result.log, /^git commit -m chore: update formula for v9\.9\.9$/m);
    assert.match(result.log, /^git push origin main$/m);
    for (const command of result.log.split('\n').filter(line => line.startsWith('sed '))) {
      assert.ok(command.endsWith(' Formula/ianchor.rb'), command);
    }
  });

  it('fails clearly when the tap has no ianchor formula, before editing or committing', () => {
    const result = updateFormula({ MISSING_FORMULA: '1' });
    assert.equal(result.status, 1, result.stderr);
    assert.match(result.stderr, /Missing Formula\/ianchor\.rb in puppetdevz\/homebrew-puppetdevz/);
    assert.doesNotMatch(result.log, /^sed |^git (diff|add|commit|push) /m);
  });

  it('does not commit or push an already up-to-date formula', () => {
    const result = updateFormula({ UNCHANGED_FORMULA: '1' });
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.log, /^git diff --name-only Formula\/ianchor\.rb$/m);
    assert.doesNotMatch(result.log, /^git (config|add|commit|push) /m);
  });

  it('documents the personal tap and legacy tap migration in both READMEs', () => {
    for (const filename of ['README.md', 'README.en.md']) {
      const readme = fs.readFileSync(new URL(`../${filename}`, import.meta.url), 'utf8');
      assert.match(readme, /brew tap puppetdevz\/puppetdevz/);
      assert.match(readme, /brew install puppetdevz\/puppetdevz\/ianchor/);
      assert.match(readme, /brew upgrade puppetdevz\/puppetdevz\/ianchor/);
      assert.match(readme, /brew uninstall ianchor/);
      assert.match(readme, /brew untap vissong\/ianchor/);
      assert.doesNotMatch(readme, /brew (install|upgrade) puppetdevz\/ianchor\/ianchor/);
    }
    const instructions = fs.readFileSync(new URL('../AGENTS.md', import.meta.url), 'utf8');
    assert.match(instructions, /puppetdevz\/homebrew-puppetdevz/);
    assert.match(instructions, /puppetdevz\/puppetdevz/);
    assert.doesNotMatch(instructions, /puppetdevz\/homebrew-ianchor/);
  });
});
