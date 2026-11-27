import { describe, expect, it } from "vitest";
import { slugify, uniqueId } from "./team-ids";

describe("team ids", () => {
	it("derives a backend-valid slug from a name", () => {
		expect(slugify("CSV export: filters & paging")).toBe("csv-export-filters-paging");
		expect(slugify("  Café crème  ")).toBe("cafe-creme");
		expect(slugify("2nd pass", "task")).toBe("task-2nd-pass");
		expect(slugify("!!!")).toBe("");
		expect(slugify("a".repeat(80))).toHaveLength(64);
		expect(slugify("Review_queue")).toMatch(/^[a-z][a-z0-9_-]{0,63}$/);
	});

	it("avoids IDs already in use", () => {
		expect(uniqueId("Worker", ["worker"])).toBe("worker-2");
		expect(uniqueId("Worker", ["worker", "worker-2"])).toBe("worker-3");
		expect(uniqueId("Fresh", ["worker"])).toBe("fresh");
		expect(uniqueId("b".repeat(70), ["b".repeat(64)])).toHaveLength(64);
	});
});
