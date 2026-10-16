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
