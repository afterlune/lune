import { expect, test, type Page } from '@playwright/test';

async function openMockDesktop(page: Page, flags: Record<string, boolean> = {}) {
  await page.route('**/src/main.ts', async (route) => {
    const response = await route.fetch();
    const entry = await response.text();
    await route.fulfill({ response, body: `
      import { installDesktopApi } from '/src/lib/desktopApi.ts';
      const flags = ${JSON.stringify(flags)};
      const calls = [];
      window.desktopCalls = calls;
      const workspace = 'D:\\workspace';
      const models = { providers: [], profiles: [{ name:'test', provider:'host', model:'test', maxOutputTokens:0, supportsInputAudio:false }], defaultModel:'test', maxParallelAgents:4, configured:true };
      const appearance = {theme:'light',wallpaperType:'static',wallpaperFile:'',wallpaperName:'',wallpaperURL:'',panelOpacity:80};
      const policies = Object.fromEntries(['a','b','c'].map(id => [id,{permissionMode:'workspace',effectivePermissionMode:'workspace',planMode:false}]));
      const sessions = ['a','b','c'].map(id => ({id,title:'Session '+id.toUpperCase(),updatedAt:'2026-10-18T10:00:00Z',mode:'local',workingDirectory:workspace,status:'idle'}));
      const snapshot = id => ({workspace,projectRoot:workspace,workspaceMode:'local',workingDirectory:workspace,session:{id,title:'Session '+id.toUpperCase(),updatedAt:'2026-10-18T10:00:00Z',settings:{model_profile:'test'},messages:[]},sessions,controls:{...policies[id]},models,pending:[],executionHistory:[],runtimeState:{Task:{Status:flags.runningA && id==='a'?'running':'idle',ExecutionID:flags.runningA && id==='a'?'exec-a':''},Queue:[]}});
      installDesktopApi({
        Bootstrap: async () => ({theme:'light',appearance,recentWorkspaces:[{path:workspace,lastUsed:''}],models,workspace:'',sessionId:'',lastWorkspace:(flags.restore || flags.staleRestore) ? workspace : '',lastSessionId:flags.restore||flags.staleRestore?'b':'',controls:policies.a}),
        OpenWorkspace: async () => {
          if (flags.staleRestore) throw new Error('workspace missing');
          return {workspace,sessions,permissionMode:'workspace',worktreeAvailable:false};
        },
        CloseWorkspace: async () => {},
        ForgetDesktopLocation: async () => {},
        OpenSession: async id => {
          calls.push(['open',id]);
          if (flags.delayB && id==='b') await new Promise(resolve => window.releaseSessionB=resolve);
          return snapshot(id);
        },
        RefreshSession: async id => snapshot(id),
        ListPendingSessions: async () => [],
        ListDesktopProcesses: async () => ({processes:[],unavailableSessions:[]}),
        SetPermissionMode: async (id,mode) => {
          calls.push(['permission',id,mode]);
          if(flags.failPermission) throw new Error('permission rejected');
          policies[id]={permissionMode:mode,effectivePermissionMode:mode,planMode:false};
          return {...policies[id]};
        },
        SetPlanMode: async (id,enabled) => {
          calls.push(['plan',id,enabled]);
          if(flags.delayPlan) await new Promise(resolve => window.releasePlan=resolve);
          policies[id]={...policies[id],effectivePermissionMode:enabled?'read-only':policies[id].permissionMode,planMode:enabled};
          return {...policies[id]};
        },
        Interrupt: async (id,executionId) => { calls.push(['interrupt',id,executionId]); await new Promise(resolve => window.releaseInterrupt=resolve); return true; },
        Submit: async (id,input) => {
          calls.push(['submit',id,input.text]);
          if(flags.delaySubmit) await new Promise(resolve => window.releaseSubmit=resolve);
          return {Status:'accepted',QueueID:'queue-a',ExecutionID:'exec-a',Position:0};
        },
      });
      ${entry}
    ` });
  });
  await page.goto('/');
  if (flags.restore) {
    await expect(page.locator('.topbar')).toContainText('Session B');
    return;
  }
  if (flags.staleRestore) {
    await expect(page.getByRole('main').getByRole('heading', { name: '把想法带进你的工作区' })).toBeVisible();
    return;
  }
  await page.locator('.recent-row').click();
  await expect(page.locator('.topbar')).toContainText('Session A');
}

test('Plan and permissions restore independently after switching sessions', async ({ page }) => {
  await openMockDesktop(page);
  await page.getByRole('combobox', { name: '工具权限' }).selectOption('full-access');
  await expect(page.getByRole('combobox', { name: '工具权限' })).toHaveValue('full-access');
  await page.getByRole('button', { name: 'Plan 模式' }).click();
  await expect(page.getByRole('combobox', { name: '工具权限' })).toHaveValue('read-only');
  await page.locator('.session-row').filter({ hasText: 'Session B' }).click();
  await expect(page.locator('.topbar')).toContainText('Session B');
  await expect(page.getByRole('button', { name: 'Plan 模式' })).not.toHaveClass(/plan-active/);
  await expect(page.getByRole('combobox', { name: '工具权限' })).toHaveValue('workspace');
  await page.locator('.session-row').filter({ hasText: 'Session A' }).click();
  await expect(page.getByRole('button', { name: 'Plan 模式' })).toHaveClass(/plan-active/);
  await page.getByRole('button', { name: 'Plan 模式' }).click();
  await expect(page.getByRole('combobox', { name: '工具权限' })).toHaveValue('full-access');
});

test('rejected permission changes keep the selector at the confirmed value', async ({ page }) => {
  await openMockDesktop(page, { failPermission: true });
  await page.getByRole('combobox', { name: '工具权限' }).selectOption('full-access');
  await expect(page.locator('.toast-error')).toContainText('permission rejected');
  await expect(page.getByRole('combobox', { name: '工具权限' })).toHaveValue('workspace');
});

test('rapid switches serialize requests and keep only the latest selection', async ({ page }) => {
  await openMockDesktop(page, { delayB: true });
  await page.locator('.session-row').filter({ hasText: 'Session B' }).click();
  await expect(page.getByRole('textbox', { name: '消息输入' })).toBeDisabled();
  await page.locator('.session-row').filter({ hasText: 'Session C' }).click();
  await expect.poll(() => page.evaluate(() => (window as any).desktopCalls.filter((c: any[]) => c[0] === 'open'))).toEqual([['open','a'],['open','b']]);
  await page.evaluate(() => (window as any).releaseSessionB());
  await expect(page.locator('.topbar')).toContainText('Session C');
  await expect(page.getByRole('textbox', { name: '消息输入' })).toBeEnabled();
});

test('a late control response cannot change another session', async ({ page }) => {
  await openMockDesktop(page, { delayPlan: true });
  await page.getByRole('button', { name: 'Plan 模式' }).click();
  await page.locator('.session-row').filter({ hasText: 'Session B' }).click();
  await expect(page.locator('.topbar')).toContainText('Session B');
  await page.evaluate(() => (window as any).releasePlan());
  await expect(page.getByRole('button', { name: 'Plan 模式' })).not.toHaveClass(/plan-active/);
  await expect(page.getByRole('combobox', { name: '工具权限' })).toHaveValue('workspace');
});

test('a late submission remains addressed to its original session', async ({ page }) => {
  await openMockDesktop(page, { delaySubmit: true });
  await page.getByRole('textbox', { name: '消息输入' }).fill('message for A');
  await page.getByRole('button', { name: '发送', exact: true }).click();
  await page.locator('.session-row').filter({ hasText: 'Session B' }).click();
  await expect(page.locator('.topbar')).toContainText('Session B');
  await page.evaluate(() => (window as any).releaseSubmit());
  await expect(page.getByRole('textbox', { name: '消息输入' })).toBeEnabled();
  await expect(page.locator('.conversation-content')).not.toContainText('message for A');
  expect(await page.evaluate(() => (window as any).desktopCalls.filter((c: any[]) => c[0] === 'submit'))).toEqual([['submit','a','message for A']]);
});

test('a late interrupt response cannot interrupt the selected session', async ({ page }) => {
  await openMockDesktop(page, { runningA: true });
  await page.getByRole('button', { name: '中断任务' }).click();
  await page.locator('.session-row').filter({ hasText: 'Session B' }).click();
  await expect(page.locator('.topbar')).toContainText('Session B');
  await page.evaluate(() => (window as any).releaseInterrupt());
  await expect(page.getByRole('textbox', { name: '消息输入' })).toBeEnabled();
  await expect(page.getByRole('button', { name: '■ 中断' })).toHaveCount(0);
  expect(await page.evaluate(() => (window as any).desktopCalls.filter((c: any[]) => c[0] === 'interrupt'))).toEqual([['interrupt','a','exec-a']]);
});

test('startup restores the last workspace and session', async ({ page }) => {
  await openMockDesktop(page, { restore: true });
  await expect(page.locator('.topbar')).toContainText('Session B');
  await expect(page.locator('.session-entry.chosen').filter({ hasText: 'Session B' })).toBeVisible();
  expect(await page.evaluate(() => (window as any).desktopCalls.filter((c: any[]) => c[0] === 'open'))).toEqual([['open', 'b']]);
});

test('startup falls back to welcome when the last workspace is unavailable', async ({ page }) => {
  await openMockDesktop(page, { staleRestore: true });
  await expect(page.getByRole('main').getByRole('heading', { name: '把想法带进你的工作区' })).toBeVisible();
  await expect(page.locator('.composer')).toHaveCount(0);
});
