import { expect, test } from '@playwright/test';

test('desktop workspace uses the approved two-column shell', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/?preview=1');
  await expect(page.getByRole('main').getByText('重构桌面端提问流程').first()).toBeVisible();
  await expect(page.getByText('帮我检查 Question 面板的交互，并把截图测试流程接入桌面端。')).toBeVisible();
  await expect(page.getByRole('navigation', { name: '主导航' }).getByRole('button', { name: '会话' })).toHaveClass(/active/);
  await expect(page.locator('.sidebar')).toBeVisible();
  await expect(page.locator('.navigation-rail')).toHaveCount(0);
  const sidebarBounds = await page.locator('.sidebar').boundingBox();
  const topbarBounds = await page.locator('.topbar').boundingBox();
  expect(sidebarBounds?.width).toBe(300);
  expect(topbarBounds?.height).toBe(56);
  await expect(page.locator('.wallpaper-layer')).toHaveCount(0);
  await expect(page).toHaveScreenshot('desktop-workspace.png', { fullPage: true });
});

test('compact workspace keeps the chat column balanced', async ({ page }) => {
  await page.setViewportSize({ width: 1152, height: 736 });
  await page.goto('/?preview=1');
  const sidebarBounds = await page.locator('.sidebar').boundingBox();
  const contentBounds = await page.locator('.conversation-content').boundingBox();
  expect(sidebarBounds?.width).toBe(300);
  expect(contentBounds?.width).toBeLessThanOrEqual(820);
  expect(contentBounds?.width).toBeGreaterThan(700);
  await expect(page.locator('.composer')).toBeVisible();
  await expect(page).toHaveScreenshot('desktop-workspace-compact.png', { fullPage: true });
});

test('welcome state uses the two-column shell and lists recent workspaces', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/?preview=welcome');

  await expect(page.locator('.sidebar')).toBeVisible();
  await expect(page.getByRole('main').getByRole('heading', { name: '把想法带进你的工作区' })).toBeVisible();
  await expect(page.locator('.sidebar')).toContainText('打开工作区');
  await expect(page.locator('.sidebar .recent-row')).toHaveCount(2);
  await expect(page).toHaveScreenshot('desktop-welcome.png', { fullPage: true });

  await page.locator('.sidebar .recent-row').nth(1).click();
  await expect(page.getByRole('main').getByText('重构桌面端提问流程').first()).toBeVisible();
});

test('welcome keeps its composition on narrow windows', async ({ page }) => {
  await page.setViewportSize({ width: 720, height: 900 });
  await page.goto('/?preview=welcome');
  await expect(page.getByRole('main').getByRole('heading', { name: '把想法带进你的工作区' })).toBeVisible();
  await expect(page.getByRole('button', { name: '打开工作区' })).toBeVisible();
  await expect(page).toHaveScreenshot('desktop-welcome-compact.png', { fullPage: true });
});

test('empty conversation uses the illustrated opening state', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/?preview=empty');
  await expect(page.locator('.conversation-empty')).toBeVisible();
  await expect(page).toHaveScreenshot('desktop-empty.png', { fullPage: true });
});

test('anime theme keeps the workspace readable over the wallpaper layer', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/?preview=anime');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'anime');
  await expect(page.locator('.composer')).toBeVisible();
  await expect(page).toHaveScreenshot('desktop-anime.png', { fullPage: true });
});

test('the sidebar collapses and switches between sessions and pending views', async ({ page }) => {
  await page.goto('/?preview=1');
  const nav = page.getByRole('navigation', { name: '主导航' });
  await nav.getByRole('button', { name: '待处理' }).click();
  await expect(nav.getByRole('button', { name: /待处理/ })).toHaveClass(/active/);
  await expect(page.locator('.pending-session-list')).toBeVisible();
  await nav.getByRole('button', { name: '会话' }).click();
  await expect(nav.getByRole('button', { name: '会话' })).toHaveClass(/active/);

  await page.getByRole('button', { name: '收起侧栏' }).click();
  await expect(page.locator('.sidebar')).toHaveClass(/sidebar-collapsed/);
  await page.getByRole('button', { name: '展开侧栏' }).click();
  await expect(page.locator('.sidebar')).not.toHaveClass(/sidebar-collapsed/);
});

test('narrow windows open the context sidebar as an overlay', async ({ page }) => {
  await page.setViewportSize({ width: 720, height: 900 });
  await page.goto('/?preview=1');
  await expect(page.locator('.sidebar')).toBeHidden();

  await page.getByRole('button', { name: '展开侧栏' }).click();
  await expect(page.locator('.sidebar')).toBeVisible();
  await page.getByRole('button', { name: '关闭侧栏' }).click();
  await expect(page.locator('.sidebar')).toBeHidden();
});

test('theme and tool controls are available in the intended locations', async ({ page }) => {
  await page.goto('/?preview=1');
  await expect(page.getByRole('group', { name: '主题快捷切换' }).getByRole('button')).toHaveCount(2);
  await expect(page.locator('.topbar select')).toHaveCount(0);
  await expect(page.locator('.composer').getByRole('combobox', { name: '工具权限' })).toHaveCount(1);
  await expect(page.locator('.composer').getByRole('combobox', { name: '会话模型' })).toHaveCount(1);

  await page.getByRole('button', { name: '切换到深色主题' }).click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await expect(page).toHaveScreenshot('desktop-dark.png', { fullPage: true });
  await page.getByRole('button', { name: '切换到浅色主题' }).click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
});

test('anime appearance preview is editable only in settings and Cancel restores it', async ({ page }) => {
  await page.goto('/?preview=1');
  await page.locator('.nav-settings').click();
  await page.getByRole('tab', { name: '外观与主题' }).click();
  await page.getByRole('button', { name: '二次元' }).click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'anime');
  await expect(page.locator('.theme-switch')).not.toContainText('二次元');

  await page.locator('#panel-opacity').evaluate((element: HTMLInputElement) => {
    element.value = '88';
    element.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await expect(page.locator('.opacity-control output')).toHaveText('88%');
  await expect(page).toHaveScreenshot('desktop-settings-appearance.png', { fullPage: true });

  await page.getByRole('button', { name: '取消', exact: true }).click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await expect(page.getByRole('dialog')).toHaveCount(0);
});

test('narrow settings uses a full-height sheet with horizontal tabs', async ({ page }) => {
  await page.setViewportSize({ width: 720, height: 900 });
  await page.goto('/?preview=1');
  await page.getByRole('button', { name: '展开侧栏' }).click();
  await page.locator('.nav-settings').click();
  await expect(page.getByRole('dialog')).toBeVisible();
  const tabsBounds = await page.locator('.settings-tabs').boundingBox();
  expect(tabsBounds?.width).toBeGreaterThan(tabsBounds?.height ?? 0);
  await expect(page).toHaveScreenshot('desktop-settings-compact.png', { fullPage: true });
});

test('dialogs move focus inside, close with Escape, and restore focus', async ({ page }) => {
  await page.goto('/?preview=1');
  const settingsButton = page.locator('.nav-settings');
  await settingsButton.focus();
  await settingsButton.click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog.locator('input, textarea, select').first()).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(settingsButton).toBeFocused();
});

test('assistant responses expose copy and safe last-response regeneration actions', async ({ page }) => {
  await page.goto('/?preview=1');
  const actions = page.locator('.assistant-message-actions');
  await expect(actions.getByRole('button', { name: '复制回复' })).toBeVisible();
  await expect(actions.getByRole('button', { name: '重新生成回复' })).toBeVisible();
  await actions.getByRole('button', { name: '重新生成回复' }).click();
  await expect(page.locator('.message-row').last()).toContainText('修改后的回复。');
});










test('pending center counts background requests and opens the owning session', async ({ page }) => {
  await page.goto('/?preview=pending-center');
  const pendingTab = page.getByRole('navigation', { name: '主导航' }).getByRole('button', { name: /待处理/ });
  await expect(pendingTab).toContainText('8');
  await pendingTab.click();

  await expect(page.locator('.pending-session-list')).toContainText('后台会话需要你处理');
  await expect(page.locator('.pending-session-list')).toContainText('文档登录与会话检查');
  await expect(page.locator('.pending-session-list')).toContainText('8 项待处理');
  await expect(page).toHaveScreenshot('desktop-pending-center.png', { fullPage: true });

  await page.getByRole('button', { name: /后台会话需要你处理，6 项待处理/ }).click();
  await expect(page.getByRole('button', { name: '会话', exact: true })).toHaveClass(/active/);
  await expect(page.getByText('需要授权')).toBeVisible();
  await expect(page.getByText('接下来优先检查哪一部分？')).toBeVisible();
  await expect(page.getByText('执行受阻')).toBeVisible();
  await expect(page.locator('.mcp-interaction-card')).toContainText('请输入文档服务的一次性验证码。');
  await expect(page.locator('.recovery-card')).toHaveCount(3);

  await page.getByLabel('验证码').fill('1234');
  await page.getByRole('button', { name: '提交响应' }).click();
  await expect(page.locator('.mcp-interaction-card')).toHaveCount(0);

  await pendingTab.click();
  await page.getByRole('button', { name: /文档登录与会话检查，2 项待处理/ }).click();
  await page.getByRole('button', { name: '打开验证链接' }).click();
  await expect(page.getByRole('button', { name: '验证完成，继续' })).toBeEnabled();
  await page.getByRole('button', { name: '验证完成，继续' }).click();
  await expect(page.locator('.mcp-interaction-card')).toHaveCount(0);
});

test('process popover groups commands across sessions and shows selected output', async ({ page }) => {
  await page.goto('/?preview=processes');

  const popover = page.getByRole('dialog', { name: '后台进程' });
  await expect(popover).toBeVisible();
  await expect(popover.locator('.process-row')).toHaveCount(3);
  await expect(popover).toContainText('重构桌面端提问流程');
  await expect(popover).toContainText('文档更新');
  await expect(popover.getByRole('button', { name: /go test \.\/internal\/desktop/ })).toBeVisible();
  await expect(popover.locator('.process-detail')).toContainText('go test ./internal/desktop -run TestListDesktopProcesses');
  await expect(popover.locator('.process-output')).toContainText('ok  github.com/afterlune/lune/internal/desktop');
  await expect(popover.locator('.process-unavailable')).toContainText('无法打开的会话');
  await expect(page).toHaveScreenshot('desktop-process-popover.png', { fullPage: true });

  await popover.getByRole('button', { name: /npm run check，失败/ }).click();
  await expect(popover.locator('.process-output')).toContainText('svelte-check --tsconfig');
  await expect(popover.locator('.process-detail-heading')).toContainText('失败');
});

test('process popover stops one active command without a confirmation dialog', async ({ page }) => {
  await page.goto('/?preview=processes');
  const popover = page.getByRole('dialog', { name: '后台进程' });
  await expect(popover.getByRole('button', { name: '停止' })).toBeVisible();
  await popover.getByRole('button', { name: '停止' }).click();
  await expect(popover.getByRole('button', { name: '停止' })).toHaveCount(0);
  await expect(popover.locator('.process-detail-heading')).toContainText('已取消');
  await expect(popover).toBeVisible();
});

test('process popover closes on outside click and Escape', async ({ page }) => {
  await page.goto('/?preview=processes');
  const popover = page.getByRole('dialog', { name: '后台进程' });
  await expect(popover).toBeVisible();
  await page.locator('.conversation-content').click({ position: { x: 8, y: 8 } });
  await expect(popover).toHaveCount(0);

  await page.getByRole('button', { name: /后台进程/ }).click();
  await expect(popover).toBeVisible();
  await expect(popover).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(popover).toHaveCount(0);
  await expect(page.getByRole('button', { name: /后台进程/ })).toBeFocused();
});

test('narrow process popover uses a bottom sheet', async ({ page }) => {
  await page.setViewportSize({ width: 720, height: 900 });
  await page.goto('/?preview=processes');
  const popover = page.getByRole('dialog', { name: '后台进程' });
  await expect(popover).toBeVisible();
  const bounds = await popover.boundingBox();
  expect(bounds?.x).toBeLessThan(16);
  expect(bounds?.width).toBeGreaterThan(680);
  expect((bounds?.y ?? 0) + (bounds?.height ?? 0)).toBeGreaterThan(870);
  await expect(page).toHaveScreenshot('desktop-process-compact.png', { fullPage: true });
});

test('project settings edits runtime limits and MCP definitions', async ({ page }) => {
  await page.goto('/?preview=1');
  await page.getByRole('button', { name: '项目设置' }).click();
  await expect(page.getByRole('dialog', { name: '项目设置与 MCP' })).toBeVisible();
  await expect(page.getByRole('heading', { name: '命令工具链' })).toBeVisible();
  await expect(page.locator('.command-toolchain-candidates .mcp-server-card')).toHaveCount(0);
  await expect(page.getByText(/检测只查询当前 PATH，不会运行程序或自动授权/)).toBeVisible();
  await page.getByLabel('上下文窗口 Token').fill('48000');
  await page.getByRole('button', { name: '添加服务' }).click();
  await page.getByLabel('名称').fill('docs');
  await page.getByLabel('连接方式').selectOption('streamable-http');
  await page.getByLabel('服务 URL').fill('https://docs.example.test/mcp');
  await expect(page.getByText('尚未应用')).toBeVisible();
  await expect(page).toHaveScreenshot('desktop-project-settings.png', { fullPage: true });
});

test('session sidebar supports renaming a conversation', async ({ page }) => {
  await page.goto('/?preview=1');
  const session = page.locator('.session-entry').first();
  await session.hover();
  await session.getByRole('button', { name: /重命名会话/ }).click();
  await page.getByLabel('会话名称').fill('重命名后的会话');
  await page.getByRole('button', { name: '保存' }).click();
  await expect(page.locator('.session-row').filter({ hasText: '重命名后的会话' })).toBeVisible();
});


test('new chat creates a local session without a mode menu', async ({ page }) => {
  await page.goto('/?preview=1');
  await page.getByRole('button', { name: /新建会话/ }).first().click();
  await expect(page.locator('.session-mode-menu')).toHaveCount(0);
  await expect(page.locator('.composer')).toBeVisible();
  await expect(page.locator('.workspace-row')).toBeVisible();
});

test('question panel preview matches its baseline', async ({ page }) => {
  await page.goto('/?preview=question');
  await expect(page.getByText('任务失败或取消后，Tab 标题怎么显示？')).toBeVisible();
  await expect(page.getByText('推荐', { exact: true })).toBeVisible();
  await expect(page.locator('input[type="radio"][value="保留结果状态"]')).toBeChecked();
  await page.getByLabel('全部恢复普通标题').check({ force: true });
  await expect(page).toHaveScreenshot('desktop-question.png', { fullPage: true });
});

test('plan review preview matches its baseline', async ({ page }) => {
  await page.goto('/?preview=plan');
  await expect(page.getByText('要开始实施这个方案吗？')).toBeVisible();
  await expect(page).toHaveScreenshot('desktop-plan-review.png', { fullPage: true });
});

test('plan review survives a same-session refresh after the turn result', async ({ page }) => {
  await page.goto('/?preview=plan-refresh');
  await expect(page.getByText('要开始实施这个方案吗？')).toBeVisible();
  await expect(page.getByText('Plan review remains open after refresh.')).toBeVisible();
  await expect(page.getByText(/<proposed_plan>/)).toHaveCount(0);
  await expect(page.getByRole('button', { name: /继续规划/ })).toBeVisible();
});

test('plan review clears when the active session changes', async ({ page }) => {
  await page.goto('/?preview=plan-refresh-session-switch');
  await expect(page.getByText('要开始实施这个方案吗？')).toHaveCount(0);
  await expect(page.getByText('Plan review remains open after refresh.')).toHaveCount(0);
});

test('assistant Markdown renders GFM structures and escapes untrusted HTML', async ({ page }) => {
  await page.goto('/?preview=markdown');
  await expect(page.locator('.markdown-body h2')).toHaveText('发现的问题');
  await expect(page.locator('.markdown-body table')).toBeVisible();
  await expect(page.locator('.markdown-body pre code')).toContainText('return true');
  await expect(page.locator('.markdown-body script')).toHaveCount(0);
  await expect(page.locator('.markdown-body')).toContainText('<script>window.__markdownXSS = true</script>');
  await expect.poll(() => page.evaluate(() => (window as Window & { __markdownXSS?: boolean }).__markdownXSS)).toBeUndefined();
  await expect(page.locator('.markdown-body a[href^="javascript:"]')).toHaveCount(0);
  await expect(page).toHaveScreenshot('desktop-markdown.png', { fullPage: true });
});

test('execution activity blends into the conversation and expands full tool details', async ({ page }) => {
  await page.goto('/?preview=running');
  const execution = page.locator('.execution-unit[data-execution-id="exec-preview-running"]');
  await expect(execution).toContainText('本次执行');
  await expect(execution).toContainText('我会先检查消息的乐观更新和会话刷新流程。');
  await expect(page.getByRole('status')).toHaveText('模型正在分析请求');
  await expect(page.getByText('我会先检查消息的乐观更新和会话刷新流程。')).toBeVisible();
  await expect(page.locator('.activity-current')).toContainText('internal/desktop/service.go');
  await expect(page.locator('.activity-list')).toHaveCount(0);
  await expect(page).toHaveScreenshot('desktop-running.png', { fullPage: true });

  const toggle = page.getByRole('button', { name: /1 步骤/ });
  await toggle.click();
  await expect(page.locator('.activity-list')).toBeVisible();
  await page.locator('.activity-tool-details summary').click();
  await expect(page.locator('.activity-tool-extra')).toContainText('internal/desktop/service.go');
  await expect(page.locator('.activity-tool-extra')).toContainText('func service()');
});

test('execution unit summarizes changed files and command outcomes at completion', async ({ page }) => {
  await page.goto('/?preview=execution-summary');
  const execution = page.locator('.execution-unit[data-execution-id="exec-preview-summary"]');

  await expect(execution).toContainText('实现 Add 并运行测试。');
  await expect(execution.locator('.execution-unit-status')).toHaveText('已完成');
  await expect(execution.locator('.activity-review')).toContainText('calc.go');
  await expect(execution.locator('.execution-command-summary')).toContainText('go test ./...');
  await expect(execution.locator('.execution-command-summary')).toContainText('已完成');
  await expect(execution.locator('.execution-command-summary')).toContainText('退出码 0');
  await expect(execution.locator('.execution-command-row')).toHaveCount(1);
  await expect(execution.locator('.activity-current')).not.toContainText('command_id');
  await expect(execution).toContainText('已实现 Add');
});

test('waiting questions and Plan review stay inside their execution timeline', async ({ page }) => {
  await page.goto('/?preview=question');
  let execution = page.locator('.execution-unit').last();
  await expect(execution.locator('.execution-unit-status')).toHaveText('待处理');
  await expect(execution.locator('.question-card')).toContainText('任务失败或取消后，Tab 标题怎么显示？');

  await page.goto('/?preview=plan');
  execution = page.locator('.execution-unit').last();
  await expect(execution.locator('.execution-unit-status')).toHaveText('待处理');
  await expect(execution.locator('.plan-review')).toContainText('要开始实施这个方案吗？');
});

test('interrupted activity stays in the transcript and accepts a follow-up message', async ({ page }) => {
  await page.goto('/?preview=interrupt');
  await expect(page.getByRole('status')).toHaveText('模型正在分析请求');
  await page.getByRole('button', { name: '中断任务' }).click();
  await expect(page.getByRole('status')).toHaveText('任务已中断');
  await expect(page.locator('.activity-list')).toHaveCount(0);
  await expect(page.getByText('我会先检查消息的乐观更新和会话刷新流程。')).toBeVisible();

  const composer = page.getByRole('textbox', { name: '消息输入' });
  await expect(composer).toBeEnabled();
  await composer.fill('继续');
  await page.getByRole('button', { name: '发送' }).click();
  await expect(page.getByText('继续', { exact: true })).toBeVisible();
  await expect(page.getByRole('status').last()).toHaveText('正在等待模型响应', { timeout: 5_000 });
  await expect(page.getByText('任务已中断')).toBeVisible();
});

test('running composer queues the next message without replacing the active execution', async ({ page }) => {
  await page.goto('/?preview=queue');
  const composer = page.getByRole('textbox', { name: '消息输入' });
  await expect(composer).toBeEnabled();
  await expect(page.getByRole('status')).toHaveText('模型正在分析请求');
  await composer.fill('排队中的后续消息');
  await page.getByRole('button', { name: '发送' }).click();

  await expect(page.getByText('排队中的后续消息', { exact: true })).toBeVisible();
  await expect(page.locator('.message-delivery-pending')).toContainText('排队中');
  await expect(page.getByText('模型正在分析请求')).toBeVisible();
  await expect(page.getByText('第一条消息已完成。')).toBeVisible({ timeout: 5_000 });
  await expect(page.getByText('队列消息已执行。')).toBeVisible({ timeout: 5_000 });
  await expect(page.getByText('排队中的后续消息', { exact: true })).toHaveCount(1);
});

test('queued message can be withdrawn without interrupting the active task', async ({ page }) => {
  await page.goto('/?preview=queue-edit');
  const composer = page.getByRole('textbox', { name: '消息输入' });
  await composer.fill('撤回这条排队消息');
  await page.getByRole('button', { name: '发送' }).click();
  const queuedRow = page.locator('.message-row.from-user').filter({ hasText: '撤回这条排队消息' });
  await expect(queuedRow.locator('.message-delivery-pending')).toContainText('排队中');
  await queuedRow.getByRole('button', { name: '撤回排队消息' }).click();
  await expect(queuedRow.locator('.message-delivery-cancelled')).toContainText('已撤回');
  await expect(page.getByText('模型正在分析请求')).toBeVisible();
});

test('editing a user message confirms branch rewind and resubmits the replacement', async ({ page }) => {
  await page.goto('/?preview=edit');
  const userRows = page.locator('.message-row.from-user');
  await userRows.first().getByRole('button', { name: '编辑消息' }).click();
  const composer = page.getByRole('textbox', { name: '消息输入' });
  await expect(composer).toHaveValue('帮我检查 Question 面板的交互，并把截图测试流程接入桌面端。');
  await composer.fill('修改后的第一条消息');
  await page.getByRole('button', { name: '保存修改' }).click();
  await expect(page.getByRole('dialog', { name: '确认编辑这条消息？' })).toBeVisible();
  await expect(page.getByText('已发生的文件操作不会撤销。')).toBeVisible();
  await page.getByRole('button', { name: '回退并重跑' }).click();

  await expect(page.getByText('修改后的第一条消息', { exact: true })).toBeVisible();
  await expect(page.getByText('我会先把窗口结构和消息流搭好，再补上浏览器预览与真实窗口启动检查。')).toHaveCount(0);
  await expect(page.getByText('修改后的回复。')).toBeVisible({ timeout: 5_000 });
});

test('editing a history message during execution interrupts the run and cancels queued messages', async ({ page }) => {
  await page.goto('/?preview=queue-edit');
  await page.getByRole('textbox', { name: '消息输入' }).fill('排队中的后续消息');
  await page.getByRole('button', { name: '发送' }).click();
  await expect(page.locator('.message-delivery-pending')).toContainText('排队中');

  const userRows = page.locator('.message-row.from-user');
  await userRows.first().getByRole('button', { name: '编辑消息' }).click();
  await page.getByRole('textbox', { name: '消息输入' }).fill('运行中修改的原始消息');
  await page.getByRole('button', { name: '保存修改' }).click();
  await expect(page.getByRole('dialog', { name: '确认编辑这条消息？' })).toContainText('立即中断当前任务并取消所有排队消息');
  await page.getByRole('button', { name: '中断并回退重跑' }).click();

  await expect(page.getByText('运行中修改的原始消息', { exact: true })).toBeVisible({ timeout: 8_000 });
  await expect(page.getByText('排队中的后续消息', { exact: true })).toHaveCount(0);
  await expect(page.getByText('修改后的回复。')).toBeVisible({ timeout: 5_000 });
});

test('editing a queued message replaces it in place and removes later queued messages', async ({ page }) => {
  await page.goto('/?preview=queue-edit');
  await page.getByRole('textbox', { name: '消息输入' }).fill('排队消息原文');
  await page.getByRole('button', { name: '发送' }).click();
  await expect(page.getByText('排队消息原文', { exact: true })).toBeVisible();
  await expect(page.locator('.message-delivery-pending')).toContainText('排队中');

  const queuedRow = page.locator('.message-row.from-user').filter({ hasText: '排队消息原文' });
  await queuedRow.getByRole('button', { name: '编辑消息' }).click();
  await page.getByRole('textbox', { name: '消息输入' }).fill('原位置替换后的排队消息');
  await page.getByRole('button', { name: '保存修改' }).click();
  await expect(page.getByRole('dialog', { name: '确认编辑这条消息？' })).toContainText('在原位置替换');
  await page.getByRole('button', { name: '替换队列消息' }).click();

  await expect(page.getByText('原位置替换后的排队消息', { exact: true })).toBeVisible();
  await expect(page.getByText('排队消息原文', { exact: true })).toHaveCount(0);
  await expect(page.getByText('第一条消息已完成。')).toBeVisible({ timeout: 8_000 });
  await expect(page.getByText('队列消息已执行。')).toBeVisible({ timeout: 8_000 });
});

test('stored execution details render again when the session is reopened', async ({ page }) => {
  await page.goto('/?preview=activity-history');
  await expect(page.getByRole('status')).toHaveText('任务已中断');
  await page.getByRole('button', { name: /1 步骤/ }).click();
  await page.locator('.activity-tool-details summary').click();
  await expect(page.locator('.activity-tool-extra')).toContainText('persistedDetails');
  await page.reload();
  await expect(page.getByRole('status')).toHaveText('任务已中断');
  await page.getByRole('button', { name: /1 步骤/ }).click();
  await page.locator('.activity-tool-details summary').click();
  await expect(page.locator('.activity-tool-extra')).toContainText('persistedDetails');
});

test('conversation messages do not show speaker names or avatars', async ({ page }) => {
  await page.goto('/?preview=empty');
  await expect(page.locator('.message-author')).toHaveCount(0);
  await expect(page.locator('.message-avatar')).toHaveCount(0);
  await expect(page.locator('.conversation-empty')).toBeVisible();
  await expect(page.locator('.conversation-empty')).not.toContainText('luneAgent');
  await expect(page.getByRole('textbox', { name: '消息输入' })).toHaveAttribute('placeholder', '发送消息…');
});

test('user message stays visible through submit, task events and stale session snapshots', async ({ page }) => {
  await page.goto('/?preview=send');
  await page.getByRole('textbox', { name: '消息输入' }).fill('即时显示的测试消息');
  await page.getByRole('button', { name: '发送' }).click();

  await expect(page.getByText('即时显示的测试消息', { exact: true })).toBeVisible();
  await expect(page.locator('.message-delivery-pending')).toContainText('正在发送');
  await expect(page.getByRole('status')).toHaveText('正在等待模型响应', { timeout: 5_000 });
  await page.getByRole('button', { name: /1 步骤/ }).click();
  await expect(page.locator('.activity-tool-details summary')).toContainText('internal/desktop/service.go');
  await expect(page.getByText('已读取服务代码，正在整理结果。')).toBeVisible();
  await expect(page.getByText('检查结果')).toBeVisible();
  await expect(page.getByText('即时显示的测试消息', { exact: true })).toHaveCount(1);
});

test('failed submission stays in the transcript and retries without duplicating the bubble', async ({ page }) => {
  await page.goto('/?preview=send-failure');
  await page.getByRole('textbox', { name: '消息输入' }).fill('可以重试的消息');
  await page.getByRole('button', { name: '发送' }).click();
  await expect(page.getByText('可以重试的消息', { exact: true })).toBeVisible();
  await expect(page.locator('.message-delivery-error')).toContainText('模拟提交失败，请重试');
  await page.getByRole('button', { name: '重试' }).click();
  await expect(page.getByRole('status')).toHaveText('正在等待模型响应', { timeout: 5_000 });
  await expect(page.getByText('任务已完成')).toBeVisible({ timeout: 8_000 });
  await expect(page.getByText('可以重试的消息', { exact: true })).toHaveCount(1);
});

test('approval card supports three-way decisions, auto-review reason, and model failure details', async ({ page }) => {
  await page.goto('/?preview=pending-center');
  await page.getByRole('navigation', { name: '主导航' }).getByRole('button', { name: /待处理/ }).click();
  await page.getByRole('button', { name: /后台会话需要你处理，6 项待处理/ }).click();

  const approvalCard = page.locator('.approval-card').first();
  await expect(approvalCard).toContainText('自动审查建议由你确认工作区外或命令操作'.slice(0, 8));
  await expect(approvalCard).toContainText('fp-exec-git-status');
  await expect(approvalCard.getByRole('button', { name: '拒绝' })).toBeVisible();
  await expect(approvalCard.getByRole('button', { name: '本会话允许相同操作' })).toBeVisible();
  await expect(approvalCard.getByRole('button', { name: '允许本次操作' })).toBeVisible();

  const modelRecovery = page.locator('.recovery-card').filter({ hasText: '模型请求未完成' });
  await expect(modelRecovery).toContainText('触发限流 (rate_limit)');
  await expect(modelRecovery).toContainText('HTTP 429');
  await expect(modelRecovery).toContainText('rate_limit_exceeded');
  await expect(modelRecovery).toContainText('已尝试 2 次');
  await expect(modelRecovery).toContainText('ID: req-preview-429');

  const toolRecovery = page.locator('.recovery-card').filter({ hasText: '工具执行暂停' });
  await expect(toolRecovery).toContainText('docs/desktop.md');

  await approvalCard.getByRole('button', { name: '本会话允许相同操作' }).click();
  await expect(page.locator('.approval-card')).toHaveCount(0);
});

test('usage popover displays session metrics and performs manual compaction', async ({ page }) => {
  await page.goto('/?preview=1');
  const chip = page.getByRole('button', { name: '上下文与用量' });
  await expect(chip).toContainText('上下文');
  await chip.click();

  const popover = page.getByRole('dialog', { name: '上下文与用量' });
  await expect(popover).toBeVisible();
  await expect(popover).toContainText('请求次数');
  await expect(popover).toContainText('14.3k');

  await popover.getByRole('button', { name: '压缩上下文' }).click();
  await expect(popover.locator('.usage-compaction-note')).toContainText('已压缩上下文');
  await page.keyboard.press('Escape');
  await expect(popover).toHaveCount(0);
});

test('reasoning panel expands streamed thought, effort selector updates, and fork/export work', async ({ page }) => {
  await page.goto('/?preview=send');
  const effortSelect = page.locator('.composer').getByRole('combobox', { name: '思考强度' });
  await expect(effortSelect).toHaveValue('default');
  await effortSelect.selectOption('high');
  await expect(effortSelect).toHaveValue('high');

  await page.getByRole('textbox', { name: '消息输入' }).fill('测试思考过程展示');
  await page.getByRole('button', { name: '发送' }).click();
  const reasoningPanel = page.locator('.reasoning-panel');
  await expect(reasoningPanel).toBeVisible({ timeout: 5_000 });
  await expect(reasoningPanel).not.toHaveAttribute('open', '');
  await reasoningPanel.locator('summary').click();
  await expect(reasoningPanel).toHaveAttribute('open', '');
  await expect(reasoningPanel.locator('.reasoning-body')).toContainText('先梳理服务状态事件');

  await page.getByRole('button', { name: '更多工作区操作' }).click();
  await page.getByRole('menuitem', { name: '导出 Markdown (.md)' }).click();
  await expect(page.locator('.toast-info')).toContainText('MD');

  await page.getByRole('button', { name: '更多工作区操作' }).click();
  await page.getByRole('menuitem', { name: '创建分支会话' }).click();
  await expect(page.locator('.topbar-title')).toContainText('(分支)');
});
