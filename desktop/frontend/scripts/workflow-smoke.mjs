#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
const app = readFileSync(resolve(root, 'src/App.tsx'), 'utf8');
const appLogic = readFileSync(resolve(root, 'src/appLogic.ts'), 'utf8');
const discordTaskDetail = readFileSync(resolve(root, 'src/features/discord/DiscordTaskDetail.tsx'), 'utf8');
const discordTaskSyncAction = readFileSync(resolve(root, 'src/features/discord/DiscordTaskSyncAction.tsx'), 'utf8');
const discordView = readFileSync(resolve(root, 'src/features/discord/DiscordView.tsx'), 'utf8');
const api = readFileSync(resolve(root, 'src/api.ts'), 'utf8');
const types = readFileSync(resolve(root, 'src/types.ts'), 'utf8');

const checks = [
  {
    name: 'project-mode conflict guidance',
    source: appLogic,
    needles: [
      'Another project-mode task is already active for this project.',
      'choose Worktree mode before creating a new task',
    ],
  },
  {
    name: 'default agent setting',
    source: app,
    needles: [
      '<strong>Default agent</strong>',
      'UpdateDefaultAgent',
    ],
  },
  {
    name: 'Discord hard sync button',
    source: `${app}\n${discordTaskDetail}\n${discordTaskSyncAction}`,
    needles: [
      'DiscordTaskSyncAction taskId={task.id}',
      'Sync with Discord',
      'api.DiscordTaskSync(taskId)',
    ],
  },
  {
    name: 'Discord disconnect requires fresh token',
    source: discordView,
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
