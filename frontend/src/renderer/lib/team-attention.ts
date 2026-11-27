// What a team needs from the Principal, derived from the team snapshot. The
// sidebar lists these above a team project's threads; selecting one opens the
// Project teams page on the tab where the decision is made.

export type TeamAttentionTab = "missions" | "planning" | "agents";

export type TeamAttentionItem = {
	key: string;
	label: string;
	tab: TeamAttentionTab;
};

type AttentionTeam = {
	agents: ReadonlyArray<{ id: string; launch_error: string | null }>;
	missions: ReadonlyArray<{ id: string; status: string }>;
	tasks: ReadonlyArray<{ id: string; mission: string; status: string }>;
	drafts?: ReadonlyArray<{
		id: string;
		status: string;
		supervisor: string | null;
		workers?: string[];
		criteria: string;
		plan: string;
	}>;
};

/** Reviews, sign-offs, releasable drafts and failed launches, most urgent first. */
export function teamAttention(team: AttentionTeam): TeamAttentionItem[] {
	const items: TeamAttentionItem[] = [];
	for (const task of team.tasks) {
		if (task.status === "review") items.push({ key: `task:${task.id}`, label: `Review ${task.id}`, tab: "missions" });
	}
	for (const mission of team.missions) {
		if (mission.status === "review") {
			items.push({ key: `mission:${mission.id}`, label: `Sign off ${mission.id}`, tab: "missions" });
			continue;
		}
		const tasks = team.tasks.filter((task) => task.mission === mission.id);
		if (mission.status === "active" && tasks.length > 0 && tasks.every((task) => task.status === "accepted")) {
			items.push({ key: `mission:${mission.id}`, label: `Submit ${mission.id}`, tab: "missions" });
		}
	}
	for (const draft of team.drafts ?? []) {
		const complete = Boolean(draft.supervisor && draft.workers?.length && draft.criteria.trim() && draft.plan.trim());
		if (draft.status === "draft" && complete) items.push({ key: `draft:${draft.id}`, label: `Release ${draft.id}`, tab: "planning" });
	}
	for (const agent of team.agents) {
		if (agent.launch_error) items.push({ key: `agent:${agent.id}`, label: `${agent.id} failed to start`, tab: "agents" });
	}
	return items;
}
