#!/usr/bin/env node

import { program } from 'commander';
import chalk from 'chalk';
import readline from 'node:readline/promises';
import { getICloudDrivePath, getConfigDir, saveConfig } from '../lib/icloud.js';
import { openDb, getAllEntries } from '../lib/db.js';
import { initAnchor } from '../lib/init.js';
import { listEntries } from '../lib/list.js';
import { addFile } from '../lib/add.js';
import { recoverFile } from '../lib/recover.js';
import { relinkFile } from '../lib/relink.js';
import { removeFile } from '../lib/remove.js';
import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';

const require = createRequire(import.meta.url);
const pkg = require('../package.json');

program
  .name('ianchor')
  .description('Sync config files to iCloud Drive via symlinks')
  .version(pkg.version);

program
  .command('init')
  .description('Initialize ianchor: create iCloud config directory and database')
  .option('--dir <name>', 'config directory name in iCloud Drive')
  .action(async (options) => {
    try {
      const icloudBase = getICloudDrivePath();
      let dirName = options.dir;

      // If user didn't explicitly pass --dir, prompt interactively
      if (!dirName) {
        const rl = readline.createInterface({ input: process.stdin, output: process.stdout });
        const answer = await rl.question(
          `iCloud config directory name ${chalk.dim('(default: ianchor)')}: `
        );
        rl.close();
        dirName = answer.trim() || 'ianchor';
      }

      const result = initAnchor({ icloudBase, dirName });
      saveConfig({ dirName });
      console.log(chalk.green('iAnchor initialized!'));
      console.log(`Config directory: ${result.configDir}`);
      if (result.discovered > 0) {
        console.log(`Discovered ${result.discovered} existing file(s).`);
      }
      if (result.entries.length > 0) {
        console.log(`\nTracked entries:`);
        for (const entry of result.entries) {
          const orig = entry.original_path ?? chalk.dim('(unknown)');
          console.log(`  ${entry.name} -> ${orig}`);
        }
      }
    } catch (err) {
      console.error(chalk.red(`Error: ${err.message}`));
      process.exit(1);
    }
  });

program
  .command('list')
  .description('List all tracked config files')
  .option('--json', 'output as JSON')
  .action(async (options) => {
    try {
      const icloudBase = getICloudDrivePath();
      const configDir = getConfigDir(icloudBase);
      const db = openDb(configDir);

      const frames = ['⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'];
      let frameIdx = 0;
      let spinnerInterval;
      const spinnerTimeout = setTimeout(() => {
        process.stderr.write(`${frames[0]} Loading entries…`);
        spinnerInterval = setInterval(() => {
          frameIdx = (frameIdx + 1) % frames.length;
          process.stderr.write(`\r${frames[frameIdx]} Loading entries…`);
        }, 80);
      }, 100);

      const output = await listEntries(db, { json: options.json, configDir });

      clearTimeout(spinnerTimeout);
      if (spinnerInterval) {
        clearInterval(spinnerInterval);
        process.stderr.write('\r\x1b[K');
      }

      console.log(output);
      db.close();
    } catch (err) {
      console.error(chalk.red(`Error: ${err.message}`));
      process.exit(1);
    }
  });

program
  .command('add <source>')
  .description('Add a file or directory to iCloud and create a symlink')
  .option('--name <name>', 'custom name in iCloud directory')
  .option('--force', 'overwrite if already exists', false)
  .action(async (source, options) => {
    try {
      const icloudBase = getICloudDrivePath();
      const configDir = getConfigDir(icloudBase);
      const db = openDb(configDir);
      const result = await addFile(db, {
        sourcePath: source,
        configDir,
        name: options.name,
        force: options.force,
        onConflict: async (destName) => {
          const rl = readline.createInterface({ input: process.stdin, output: process.stdout });
          const prefix = await rl.question(
            chalk.yellow(`"${destName}" already exists. `) +
            `Enter a prefix ${chalk.dim(`(e.g. abc -> abc_${destName})`)}: `
          );
          rl.close();
          return prefix.trim() || null;
        },
      });
      console.log(chalk.green(`Added "${result.name}" to iCloud.`));
      console.log(`  ${result.originalPath} -> ${configDir}/${result.name}`);
      db.close();
    } catch (err) {
      console.error(chalk.red(`Error: ${err.message}`));
      process.exit(1);
    }
  });

program
  .command('recover <name>')
  .description('Restore a local copy without deleting the iCloud file or tracking entry')
  .option('--force', 'overwrite if file exists at original location', false)
  .action((name, options) => {
    try {
      const icloudBase = getICloudDrivePath();
      const configDir = getConfigDir(icloudBase);
      const db = openDb(configDir);
      const result = recoverFile(db, {
        name,
        configDir,
        force: options.force,
      });
      console.log(chalk.green(`Recovered "${result.name}" locally.`));
      console.log('iCloud file and tracking entry preserved; other devices can keep using them.');
      if (result.originalPath) {
        console.log(`  Restored to: ${result.originalPath}`);
      }
      db.close();
    } catch (err) {
      console.error(chalk.red(`Error: ${err.message}`));
      process.exit(1);
    }
  });

program
  .command('relink <name>')
  .description('Re-create symlink for an unlinked file in iCloud')
  .action((name) => {
    try {
      const icloudBase = getICloudDrivePath();
      const configDir = getConfigDir(icloudBase);
      const db = openDb(configDir);
      const result = relinkFile(db, { name, configDir });
      console.log(chalk.green(`Relinked "${result.name}".`));
      console.log(`  ${result.originalPath} -> ${result.icloudPath}`);
      db.close();
    } catch (err) {
      console.error(chalk.red(`Error: ${err.message}`));
      process.exit(1);
    }
  });

program
  .command('remove <name>')
  .description('Remove an entry from the database and delete the file from iCloud')
  .option('-y, --yes', 'skip confirmation', false)
  .action(async (name, options) => {
    try {
      const icloudBase = getICloudDrivePath();
      const configDir = getConfigDir(icloudBase);
      const db = openDb(configDir);

      if (!options.yes) {
        const rl = readline.createInterface({ input: process.stdin, output: process.stdout });
        const answer = await rl.question(
          chalk.yellow(`Remove "${name}"? This will delete the file from iCloud and the database record. [y/N] `)
        );
        rl.close();
        if (answer.trim().toLowerCase() !== 'y') {
          console.log('Cancelled.');
          db.close();
          return;
        }
      }

      const result = removeFile(db, { name, configDir });
      console.log(chalk.green(`Removed "${result.name}".`));
      if (result.originalPath) {
        console.log(`  Original path was: ${result.originalPath}`);
      }
      db.close();
    } catch (err) {
      console.error(chalk.red(`Error: ${err.message}`));
      process.exit(1);
    }
  });

// Hidden command: output entry names for shell completion
program
  .command('__complete', { hidden: true })
  .action(() => {
    let db;
    try {
      const icloudBase = getICloudDrivePath();
      const configDir = getConfigDir(icloudBase);
      db = openDb(configDir, { readonly: true });
      const entries = getAllEntries(db);
      for (const entry of entries) {
        console.log(entry.name);
      }
    } catch {
      // Silently fail during completion
    } finally {
      db?.close();
    }
  });

program
  .command('completion', { hidden: true })
  .description('Output shell completion script')
  .argument('[shell]', 'shell type: zsh or bash', 'zsh')
  .action((shell) => {
    if (!['zsh', 'bash'].includes(shell)) {
      program.error(`Unsupported shell: ${shell}. Expected zsh or bash.`);
    }
    const script = readFileSync(new URL(`../lib/completions/ianchor.${shell}`, import.meta.url), 'utf8');
    process.stdout.write(script);
  });

program.parse();
