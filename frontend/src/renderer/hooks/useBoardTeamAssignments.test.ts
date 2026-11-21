import { describe, expect, it } from "vitest";
import { boardTeamAssignments } from "./useBoardTeamAssignments";
import type { TeamState } from "../components/TeamView";

describe("board team assignments", () => {
	it("uses explicit supervisor and mission staffing rather than display names", () => {
		const state = { enabled: true, agents: [
			{ id: "chief-0", tier: "chief", parent: "orch", session_id: "chief-session" },
			{ id: "muse-0", tier: "worker", parent: "chief-0", session_id: "worker-session" },
			{ id: "other", tier: "worker", parent: "chief-0", session_id: "other-session" },
		], missions: [
			{ id: "delivery", chief: "chief-0", supervisor: "chief-0", workers: ["muse-0"], status: "active", objective: "Deliver the feature" },
			{ id: "finished", chief: "chief-0", supervisor: "chief-0", workers: ["muse-0"], status: "approved", objective: "Old work" },
		] } as TeamState;
		const result = boardTeamAssignments([{ projectId: "project", state }]);
		expect(result.get("project:worker-session")).toEqual({ parentSessionId: "chief-session", missions: [{ id: "delivery", objective: "Deliver the feature", status: "active" }] });
		expect(result.get("project:chief-session")?.missions.map((mission) => mission.id)).toEqual(["delivery"]);
		expect(result.get("project:other-session")?.missions).toEqual([]);
		expect(result.has("another-project:worker-session")).toBe(false);
		expect(boardTeamAssignments([{ projectId: "project", state: { ...state, enabled: false } }]).size).toBe(0);
	});

	it("attributes directly supervised missions to the orchestrator session", () => {
		const state = { enabled: true, agents: [
			{ id: "orch", tier: "orchestrator", parent: null, session_id: "orch-session" },
			{ id: "direct-0", tier: "worker", parent: "orch", session_id: "direct-session" },
		], missions: [
			{ id: "direct", chief: "orch", supervisor: "orch", orchestrator: "orch", workers: ["direct-0"], status: "active", objective: "Direct work" },
		] } as TeamState;
		const result = boardTeamAssignments([{ projectId: "project", state }]);
		expect(result.get("project:direct-session")).toEqual({ parentSessionId: "orch-session", missions: [{ id: "direct", objective: "Direct work", status: "active" }] });
		expect(result.get("project:orch-session")?.missions.map((mission) => mission.id)).toEqual(["direct"]);
	});
});
