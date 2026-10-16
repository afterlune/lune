import { expect, test } from '@playwright/test';

test('desktop workspace preview matches the approved baseline', async ({ page }) => {
  await page.goto('/?preview=1');
  await expect(page.getByRole('main').getByText('重构桌面端提问流程')).toBeVisible();
  await expect(page.getByText('帮我检查 Question 面板的交互，并把截图测试流程接入桌面端。')).toBeVisible();
  await expect(page).toHaveScreenshot('desktop-workspace.png', { fullPage: true });
});

test('question panel preview matches its baseline', async ({ page }) => {
  await page.goto('/?preview=question');
  await expect(page.getByText('任务失败或取消后，Tab 标题怎么显示？')).toBeVisible();
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

test('execution status expands into a nested activity timeline', async ({ page }) => {
  await page.goto('/?preview=running');
  await expect(page.getByRole('status')).toHaveText('正在读取 internal/desktop/service.go');
  await expect(page.getByText('我会先检查消息的乐观更新和会话刷新流程。')).toBeVisible();
  await expect(page.locator('.activity-list')).toBeVisible();
  await expect(page).toHaveScreenshot('desktop-running.png', { fullPage: true });

  const toggle = page.getByRole('button', { name: /2 步骤/ });
  await toggle.click();
  await expect(page.locator('.activity-list')).toHaveCount(0);
  await toggle.click();
  await page.locator('.activity-tool-details summary').click();
  await expect(page.locator('.activity-tool-extra')).toContainText('internal/desktop/service.go');
});

test('user message stays visible through submit, task events and stale session snapshots', async ({ page }) => {
  await page.goto('/?preview=send');
  await page.getByRole('textbox', { name: '消息输入' }).fill('即时显示的测试消息');
  await page.getByRole('button', { name: '发送' }).click();

  await expect(page.getByText('即时显示的测试消息', { exact: true })).toBeVisible();
  await expect(page.getByRole('status')).toHaveText('正在提交请求');
  await expect(page.getByRole('status')).toHaveText('正在等待模型响应', { timeout: 5_000 });
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
