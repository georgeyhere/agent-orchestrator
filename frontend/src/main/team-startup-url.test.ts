import { describe, expect, it } from "vitest";
import { teamStartupUrl } from "./team-startup-url";

describe("team desktop startup", () => {
	it("uses the existing hash router with the packaged renderer entry point", () => {
		expect(teamStartupUrl("app://renderer/index.html", true)).toBe("app://renderer/index.html#/teams");
	});
	it("uses the same team route when developing with Vite", () => {
		expect(teamStartupUrl("http://127.0.0.1:43992/", true)).toBe("http://127.0.0.1:43992/#/teams");
	});
	it("preserves AO's normal entry point outside the team launcher", () => {
		expect(teamStartupUrl("app://renderer/index.html", false)).toBe("app://renderer/index.html");
	});
});
