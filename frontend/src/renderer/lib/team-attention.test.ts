import { describe, expect, it } from "vitest";
import { teamAttention } from "./team-attention";

describe("teamAttention", () => {
	it("lists reviews, sign-offs, submissions, releasable drafts and failed launches", () => {
		const items = teamAttention({
			agents: [
				{ id: "orch", launch_error: null },
				{ id: "db-worker", launch_error: "codex exited" },
			],
			missions: [
				{ id: "export", status: "active" },
				{ id: "docs", status: "review" },
				{ id: "done", status: "active" },
				{ id: "empty", status: "active" },
			],
			tasks: [
				{ id: "endpoint", mission: "export", status: "review" },
				{ id: "writer", mission: "export", status: "accepted" },
				{ id: "final", mission: "done", status: "accepted" },
			],
			drafts: [
				{ id: "audit", status: "draft", supervisor: "orch", workers: ["w"], criteria: "c", plan: "p" },
				{ id: "rough", status: "draft", supervisor: null, workers: [], criteria: "", plan: "" },
				{ id: "shipped", status: "released", supervisor: "orch", workers: ["w"], criteria: "c", plan: "p" },
			],
		});
		expect(items.map((item) => [item.label, item.tab])).toEqual([
			["Review endpoint", "missions"],
			["Sign off docs", "missions"],
			["Submit done", "missions"],
			["Release audit", "planning"],
			["db-worker failed to start", "agents"],
		]);
	});

	it("is empty when nothing waits on the Principal", () => {
		expect(teamAttention({ agents: [], missions: [], tasks: [] })).toEqual([]);
	});
});
