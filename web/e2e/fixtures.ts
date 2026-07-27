import { expect, test as base } from "@playwright/test";

export const test = base.extend<{ consoleGuard: void }>({
  consoleGuard: [
    async ({ page }, use) => {
      const errors: string[] = [];
      page.on("console", (message) => {
        if (message.type() !== "error") return;
        const text = message.text();
        if (/Failed to load resource: the server responded with a status of (401|503)/.test(text)) return;
        errors.push(`console: ${text}`);
      });
      page.on("pageerror", (error) => errors.push(`pageerror: ${error.message}`));
      await use();
      expect(errors, errors.join("\n")).toEqual([]);
    },
    { auto: true },
  ],
});

export { expect };
