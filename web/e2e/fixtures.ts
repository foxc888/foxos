import { expect, test as base } from "@playwright/test";

type ConsoleErrorAllowlist = {
	allowStatus: (status: number) => void;
	hasStatus: (status: number) => boolean;
};

export const test = base.extend<{ consoleGuard: void; consoleErrorAllowlist: ConsoleErrorAllowlist; expectedConsoleErrorStatuses: number[] }>({
	expectedConsoleErrorStatuses: [[], { option: true }],
	consoleErrorAllowlist: async ({}, use) => {
		const statuses = new Set<number>();
		await use({
			allowStatus: (status) => statuses.add(status),
			hasStatus: (status) => statuses.has(status),
		});
	},
  consoleGuard: [
		async ({ page, consoleErrorAllowlist, expectedConsoleErrorStatuses }, use) => {
			const consoleErrors: string[] = [];
			const pageErrors: string[] = [];
			const allowedStatuses = new Set([401, ...expectedConsoleErrorStatuses]);
      page.on("console", (message) => {
        if (message.type() !== "error") return;
				consoleErrors.push(message.text());
      });
			page.on("pageerror", (error) => pageErrors.push(error.message));
      await use();
			const errors = consoleErrors.flatMap((entry) => {
				const responseFailure = entry.match(/Failed to load resource: the server responded with a status of (\d+)/);
				if (responseFailure) {
					const status = Number(responseFailure[1]);
					if (allowedStatuses.has(status) || consoleErrorAllowlist.hasStatus(status)) return [];
				}
				return [`console: ${entry}`];
			});
			errors.push(...pageErrors.map((entry) => `pageerror: ${entry}`));
      expect(errors, errors.join("\n")).toEqual([]);
    },
    { auto: true },
  ],
});

export { expect };
