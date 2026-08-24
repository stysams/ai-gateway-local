import { expect, test } from "@playwright/test";

test("desktop preview renders both themes and stays usable", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "desktop-light", "The preview test controls its own desktop and mobile viewports.");
  const consoleErrors: string[] = [];
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });

  await page.goto("/preview.html");
  await expect(page.getByRole("heading", { name: "运行总览" })).toBeVisible();
  await expect(page.getByText("网关运行正常")).toBeVisible();

  const desktopOverflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(desktopOverflow).toBe(false);
  await page.screenshot({ path: "preview-artifacts/ai-gateway-desktop-light.png", fullPage: true });

  await page.getByRole("button", { name: "切换到深色主题" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await page.screenshot({ path: "preview-artifacts/ai-gateway-desktop-dark.png", fullPage: true });

  await page.getByRole("button", { name: "路由", exact: true }).click();
  await expect(page.getByRole("heading", { name: "路由", exact: true })).toBeVisible();
  await expect(page.getByRole("combobox", { name: "Codex 默认模型" })).toHaveValue("openrouter/gpt-5");

  await page.getByRole("button", { name: "总览", exact: true }).click();
  await page.setViewportSize({ width: 390, height: 844 });
  const mobileOverflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(mobileOverflow).toBe(false);
  await page.screenshot({ path: "preview-artifacts/ai-gateway-mobile-check.png", fullPage: true });

  expect(consoleErrors).toEqual([]);
});
