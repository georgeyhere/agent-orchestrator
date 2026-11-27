import { describe, expect, it } from "vitest";
import {
	applyTeamHierarchy,
	applyTeamThreads,
	buildSidebarSessionOrder,
	buildTeamSessionMeta,
	densityForTier,
	pinnedFirst,
	teamOrchestratorSessionId,
} from "./sidebar-order";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

describe("pinnedFirst", () => {
	it("moves pinned items to the front and keeps each group's order", () => {
		expect(pinnedFirst(["a", "b", "c", "d"], (id) => id, ["d", "b"])).toEqual(["b", "d", "a", "c"]);
	});

	it("ignores pins for items that are not listed", () => {
		expect(pinnedFirst(["a", "b"], (id) => id, ["gone"])).toEqual(["a", "b"]);
	});
});

describe("buildTeamSessionMeta", () => {
	it("indexes attached agents by session with their supervisor link", () => {
		const meta = buildTeamSessionMeta([
			{ id: "orch", tier: "orchestrator", parent: null, session_id: "s-orch" },
			{ id: "chief", tier: "chief", parent: "orch", session_id: "s-chief" },
			{ id: "worker", tier: "worker", parent: "chief", session_id: "s-worker" },
			{ id: "detached", tier: "worker", parent: "chief", session_id: null },
		]);
		expect(meta.get("s-orch")).toEqual({ tier: "orchestrator", supervisorSessionId: null });
		expect(meta.get("s-chief")).toEqual({ tier: "chief", supervisorSessionId: "s-orch" });
		expect(meta.get("s-worker")).toEqual({ tier: "worker", supervisorSessionId: "s-chief" });
		expect([...meta.keys()]).toHaveLength(3);
		expect(teamOrchestratorSessionId(meta)).toBe("s-orch");
	});

	it("leaves the supervisor link empty when the supervisor is detached", () => {
		const meta = buildTeamSessionMeta([
			{ id: "chief", tier: "chief", parent: "orch", session_id: null },
			{ id: "worker", tier: "worker", parent: "chief", session_id: "s-worker" },
		]);
		expect(meta.get("s-worker")).toEqual({ tier: "worker", supervisorSessionId: null });
		expect(teamOrchestratorSessionId(meta)).toBeNull();
	});
});

describe("densityForTier", () => {
	it("maps tiers to row densities with a standard fallback", () => {
		expect(densityForTier("orchestrator")).toBe("standard");
		expect(densityForTier("planner")).toBe("standard");
		expect(densityForTier("chief")).toBe("chief");
		expect(densityForTier("worker")).toBe("worker");
		expect(densityForTier(undefined)).toBe("standard");
		expect(densityForTier("custom")).toBe("standard");
	});
});

describe("applyTeamHierarchy", () => {
	const agents = [
		{ id: "orch", tier: "orchestrator", parent: null, session_id: "s-orch" },
		{ id: "plan", tier: "planner", parent: null, session_id: "s-plan" },
		{ id: "chief-a", tier: "chief", parent: "orch", session_id: "s-chief-a" },
		{ id: "chief-b", tier: "chief", parent: "orch", session_id: "s-chief-b" },
		{ id: "w-orch", tier: "worker", parent: "orch", session_id: "s-w-orch" },
		{ id: "w-a1", tier: "worker", parent: "chief-a", session_id: "s-w-a1" },
		{ id: "w-a2", tier: "worker", parent: "chief-a", session_id: "s-w-a2" },
		{ id: "w-b1", tier: "worker", parent: "chief-b", session_id: "s-w-b1" },
		{ id: "w-lost", tier: "worker", parent: "chief-gone", session_id: "s-w-lost" },
	];
	const meta = () => buildTeamSessionMeta(agents);
	const item = (id: string) => ({ id });

	it("groups by reporting line with orch first, planner peer, then crews", () => {
		const scrambled = ["s-plain", "s-w-b1", "s-chief-b", "s-w-a2", "s-orch", "s-w-a1", "s-plan", "s-w-orch", "s-chief-a", "s-w-lost"].map(item);
		const { sessions, rowMeta } = applyTeamHierarchy(scrambled, meta());
		expect(sessions.map((s) => s.id)).toEqual([
			"s-orch", "s-plan", "s-w-orch",
			"s-chief-b", "s-w-b1", "s-chief-a", "s-w-a2", "s-w-a1",
			"s-w-lost", "s-plain",
		]);
		expect(rowMeta.get("s-orch")).toEqual({ density: "standard", depth: 0 });
		expect(rowMeta.get("s-plan")).toEqual({ density: "standard", depth: 0 });
		expect(rowMeta.get("s-w-orch")).toEqual({ density: "worker", depth: 1 });
		expect(rowMeta.get("s-chief-b")).toEqual({ density: "chief", depth: 0 });
		expect(rowMeta.get("s-w-b1")).toEqual({ density: "worker", depth: 2 });
		expect(rowMeta.get("s-w-lost")).toEqual({ density: "worker", depth: 0 });
		expect(rowMeta.get("s-plain")).toEqual({ density: "standard", depth: 0 });
	});

	it("keeps incoming relative order within every group", () => {
		const sessions = ["s-w-a2", "s-w-a1", "s-chief-b", "s-chief-a"].map(item);
		const { sessions: ordered } = applyTeamHierarchy(sessions, meta());
		expect(ordered.map((s) => s.id)).toEqual(["s-chief-b", "s-chief-a", "s-w-a2", "s-w-a1"]);
	});

	it("treats an orchestrator crew as supervisor-less when the orchestrator is not visible", () => {
		const sessions = ["s-w-orch", "s-plain"].map(item);
		const { sessions: ordered, rowMeta } = applyTeamHierarchy(sessions, meta());
		expect(ordered.map((s) => s.id)).toEqual(["s-w-orch", "s-plain"]);
		expect(rowMeta.get("s-w-orch")).toEqual({ density: "worker", depth: 0 });
	});

	it("passes ordinary sessions through when nothing matches", () => {
		const sessions = ["s-1", "s-2"].map(item);
		const { sessions: ordered, rowMeta } = applyTeamHierarchy(sessions, new Map());
		expect(ordered.map((s) => s.id)).toEqual(["s-1", "s-2"]);
		expect(rowMeta.get("s-1")).toEqual({ density: "standard", depth: 0 });
	});
});

describe("buildSidebarSessionOrder with team rows", () => {
	const ws = (id: string, kind: "worker" | "orchestrator", extra: Partial<WorkspaceSession> = {}): WorkspaceSession => ({
		id,
		workspaceId: "proj",
		workspaceName: "Project",
		title: id,
		provider: "codex",
		kind,
		branch: `session/${id}`,
		status: "idle",
		updatedAt: "2026-06-30T00:00:00Z",
		prs: [],
		...extra,
	});
	const workspace: WorkspaceSummary = {
		id: "proj",
		name: "Project",
		path: "/repo/project",
		sessions: [ws("s-orch", "orchestrator"), ws("s-a", "worker", { updatedAt: "2026-07-01T00:00:00Z" }), ws("s-b", "worker", { updatedAt: "2026-06-30T00:00:00Z" })],
	};

	it("walks stored team rows including the orchestrator", () => {
		const entries = buildSidebarSessionOrder({
			workspaces: [workspace],
			sessionOrders: { proj: ["s-orch", "s-b", "s-a"] },
		});
		expect(entries.map((entry) => entry.sessionId)).toEqual(["s-orch", "s-b", "s-a"]);
	});

	it("keeps the worker-only default without a stored order", () => {
		const entries = buildSidebarSessionOrder({ workspaces: [workspace] });
		expect(entries.map((entry) => entry.sessionId)).toEqual(["s-a", "s-b"]);
	});
});

describe("applyTeamThreads", () => {
	const agents = [
		{ id: "orch", tier: "orchestrator", parent: null, session_id: "s-orch" },
		{ id: "planner", tier: "planner", parent: null, session_id: "s-plan" },
		{ id: "chief", tier: "chief", parent: "orch", session_id: "s-chief" },
		{ id: "w1", tier: "worker", parent: "chief", session_id: "s-w1" },
		{ id: "w2", tier: "worker", parent: "orch", session_id: "s-w2" },
		{ id: "idle", tier: "worker", parent: "chief", session_id: "s-idle" },
	];
	const sessions = ["s-other", "s-idle", "s-w2", "s-w1", "s-plan", "s-chief", "s-orch"].map((id) => ({ id }));

	it("groups live missions with their chief and workers under a caption, then the plan", () => {
		const result = applyTeamThreads(sessions, agents, [
			{ id: "export", supervisor: "chief", workers: ["w1"], status: "active" },
			{ id: "docs", supervisor: "orch", workers: ["w2"], status: "review" },
			{ id: "old", supervisor: "orch", workers: ["idle"], status: "accepted" },
		]);
		expect(result.sessions.map((session) => session.id)).toEqual(["s-orch", "s-chief", "s-w1", "s-w2", "s-idle", "s-plan", "s-other"]);
		expect(result.rowMeta.get("s-orch")).toEqual({ density: "standard", depth: 0 });
		expect(result.rowMeta.get("s-chief")).toEqual({ density: "chief", depth: 0, label: "export" });
		expect(result.rowMeta.get("s-w1")).toEqual({ density: "worker", depth: 1 });
		expect(result.rowMeta.get("s-w2")).toEqual({ density: "worker", depth: 1, label: "docs" });
		expect(result.rowMeta.get("s-idle")?.label).toBe("Not on a mission");
		expect(result.rowMeta.get("s-plan")?.label).toBe("Plan");
		expect(result.rowMeta.get("s-other")?.label).toBe("Sessions");
	});

	it("falls back to the reporting-line hierarchy without a live mission", () => {
		const result = applyTeamThreads(sessions, agents, [{ id: "old", supervisor: "chief", workers: ["w1"], status: "accepted" }]);
		expect(result).toEqual(applyTeamHierarchy(sessions, buildTeamSessionMeta(agents)));
	});
});
