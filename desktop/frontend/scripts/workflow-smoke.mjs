#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
const main = readFileSync(resolve(root, 'src/main.tsx'), 'utf8');
const api = readFileSync(resolve(root, 'src/api.ts'), 'utf8');
const types = readFileSync(resolve(root, 'src/types.ts'), 'utf8');

const checks = [
  {
    name: 'project-mode conflict guidance',
    source: main,
    needles: [
      'Another project-mode task is already active for this project.',
      'choose Worktree mode before creating a new task',
    ],
  },
  {
    name: 'default agent setting',
    source: main,
    needles: [
      '<strong>Default agent</strong>',
      'UpdateDefaultAgent',
    ],
  },
  {
    name: 'Discord hard sync button',
    source: main,
    needles: [
      'Sync with Discord',
      'api.DiscordTaskSync(task.id)',
    ],
  },
  {
    name: 'Discord disconnect requires fresh token',
    source: main,
    needles: [
      'Discord bot token is required',
      'disconnect clears the stored token',
      'canReuseStoredToken = hasStoredToken && status.enabled',
    ],
  },
  {
    name: 'Discord sync state DTO',
    source: types,
    needles: [
      'discordSync?: TaskDiscordSync',
      "status: 'pending' | 'synced' | 'failed'",
      'lastError?: string',
    ],
  },
  {
    name: 'Discord task sync API bridge',
    source: api,
    needles: [
      'DiscordTaskSync(taskID: string)',
      'app()?.DiscordTaskSync(taskID)',
    ],
  },
];

const failures = [];
for (const check of checks) {
  for (const needle of check.needles) {
    if (!check.source.includes(needle)) {
      failures.push(`${check.name}: missing ${JSON.stringify(needle)}`);
    }
  }
}

if (failures.length > 0) {
  console.error('frontend workflow smoke failed');
  for (const failure of failures) {
    console.error(`- ${failure}`);
  }
  process.exit(1);
}

console.log('frontend workflow smoke passed');
