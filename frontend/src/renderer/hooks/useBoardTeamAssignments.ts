import { useQueries } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import type { TeamState } from "../components/TeamView";

export function boardTeamAssignments(teams: Array<{ projectId: string; state: TeamState }>) {
	const result = new Map<string, { parentSessionId?: string; missions: Array<{ id: string; objective: string; status: string }> }>();
	for (const { projectId, state } of teams) {
		if (!state.enabled) continue;
		const agents = new Map(state.agents.map((agent) => [agent.id, agent]));
		for (const agent of state.agents) {
			if (!agent.session_id) continue;
			const parent = agent.parent ? agents.get(agent.parent) : undefined;
			result.set(`${projectId}:${agent.session_id}`, {
				parentSessionId: parent?.session_id ?? undefined,
				missions: state.missions.filter((mission) => mission.status !== "approved" && (
					mission.chief === agent.id || mission.orchestrator === agent.id || mission.workers?.includes(agent.id)
				)).map(({ id, objective, status }) => ({ id, objective, status })),
			});
		}
	}
	return result;
}

export function useBoardTeamAssignments(projectIds: string[]) {
	const queries = useQueries({ queries: projectIds.map((projectId) => ({
		queryKey: ["project-team", projectId],
		refetchInterval: 3000,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/projects/{projectId}/team", { params: { path: { projectId } } });
			if (error) throw new Error(apiErrorMessage(error));
			return data?.result as TeamState;
		},
	})) });
	return boardTeamAssignments(queries.flatMap((query, index) => query.data ? [{ projectId: projectIds[index], state: query.data }] : []));
}
