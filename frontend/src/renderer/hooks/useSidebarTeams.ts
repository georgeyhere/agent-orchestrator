import { useQueries } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import type { TeamState } from "../components/TeamView";

/**
 * Enabled team states for sidebar hierarchy, keyed by project id. Projects
 * without a team (or with a failed/pending query) are absent: the sidebar
 * falls back to its existing ordering there. Shares the ["project-team", id]
 * query cache with the board and teams views, so this adds no extra fetch.
 */
export function useSidebarTeams(projectIds: string[]): Map<string, TeamState> {
	const queries = useQueries({
		queries: projectIds.map((projectId) => ({
			queryKey: ["project-team", projectId],
			refetchInterval: 3000,
			queryFn: async () => {
				const { data, error } = await apiClient.GET("/api/v1/projects/{projectId}/team", { params: { path: { projectId } } });
				if (error) throw new Error(apiErrorMessage(error));
				return data?.result as TeamState;
			},
		})),
	});
	return new Map(
		queries.flatMap((query, index) =>
			query.data?.enabled ? [[projectIds[index], query.data] as const] : [],
		),
	);
}
