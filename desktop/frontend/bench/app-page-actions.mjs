/** Shared production page navigation used by behavior and memory fixtures. */
export async function chooseAppLayout(page, label, className) {
  await page.locator('button:has(svg.lucide-settings)').last().click();
  await page.locator('.settings-screen').waitFor();
  // Settings re-opens on the last visited tab; the layout seg lives on General.
  // Pin it so a previous round that ended on Hooks/Models cannot strand us.
  await page.getByRole('navigation', { name: 'Settings', exact: true }).getByRole('button', { name: 'General', exact: true }).click();
  await page.locator('.settings-screen .set-seg__btn').filter({ hasText: new RegExp(`^${label}$`) }).click();
  await page.locator(`.app.${className}`).waitFor();
  await page.locator('.settings-screen .management-screen__back').click();
  await page.locator('.settings-screen').waitFor({ state: 'detached' });
}
